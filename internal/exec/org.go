package exec

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ec2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	gorgaws "github.com/toddwbucy/gorg-aws"
	"gorm.io/gorm"
)

// OrgRequest describes an org-wide script execution.
type OrgRequest struct {
	ScriptID     *uint
	InlineScript string
	Platform     string // "linux" (default) or "windows"
	Env          string // "com" or "gov"
	ParentID     string // OU ID to scope traversal; "" = entire org
	SessionID    *uint
	ChangeNumber string
}

// OrgRunner runs a script across every matching instance in every account in the org.
// It uses gorg-aws to assume OrganizationAccountAccessRole in each account,
// discovers running instances, and fans out SSM execution concurrently.
type OrgRunner struct {
	db          *gorm.DB
	visitor     *gorgaws.OrgVisitor
	timeoutSecs int
}

// NewOrgRunner creates an OrgRunner. The visitor must be initialised with
// management-account credentials that can assume roles across the org.
func NewOrgRunner(db *gorm.DB, visitor *gorgaws.OrgVisitor, timeoutSecs int) *OrgRunner {
	return &OrgRunner{db: db, visitor: visitor, timeoutSecs: timeoutSecs}
}

// DryRun returns the accounts and regions in scope without assuming any roles.
// Delegates directly to the underlying gorg-aws OrgVisitor.
func (or *OrgRunner) DryRun(ctx context.Context, env, parentID string) ([]string, []string, error) {
	return or.visitor.DryRun(ctx, env, parentID)
}

// Start discovers all accounts in the org (or OU) via DryRun, creates an
// ExecutionBatch, and begins async execution. TotalInstances starts at 0 and
// increments per region as instances are discovered.
// Returns the batch ID for polling.
func (or *OrgRunner) Start(ctx context.Context, req OrgRequest) (uint, error) {
	// Resolve the script first so we fail fast before touching AWS.
	runner := &Runner{db: or.db, maxConc: 10, timeoutSecs: or.timeoutSecs}
	script, err := runner.resolveScript(ScriptRequest{
		ScriptID:     req.ScriptID,
		InlineScript: req.InlineScript,
	})
	if err != nil {
		return 0, err
	}

	// DryRun validates scope and fails fast without assuming any roles.
	if _, _, err := or.visitor.DryRun(ctx, req.Env, req.ParentID); err != nil {
		return 0, fmt.Errorf("org dry-run: %w", err)
	}

	// TotalInstances starts at 0 and is incremented as regions report real counts.
	batch := &models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 0,
		Status:         models.BatchStatusPending,
		SessionID:      req.SessionID,
	}
	if err := or.db.Create(batch).Error; err != nil {
		return 0, fmt.Errorf("creating org batch: %w", err)
	}

	go or.run(context.Background(), batch.ID, script, req)
	return batch.ID, nil
}

func (or *OrgRunner) run(
	ctx context.Context,
	batchID uint,
	script *models.Script,
	req OrgRequest,
) {
	or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		Update("status", models.BatchStatusRunning)

	platform := req.Platform
	if platform == "" {
		platform = "linux"
	}

	_, err := or.visitor.VisitOrganization(
		ctx,
		req.Env,
		nil, // no per-account visitor; work happens in the region visitor
		func(ctx context.Context, cfg aws.Config, accountID, region string) (any, error) {
			return nil, or.execInRegion(ctx, cfg, batchID, script, accountID, region, platform, req)
		},
		req.ParentID,
	)
	if err != nil {
		or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
			Update("status", models.BatchStatusFailed)
		return
	}

	var final models.ExecutionBatch
	or.db.First(&final, batchID)
	if final.FailedInstances == final.TotalInstances && final.TotalInstances > 0 {
		or.db.Model(&final).Update("status", models.BatchStatusFailed)
	} else {
		or.db.Model(&final).Update("status", models.BatchStatusCompleted)
	}
}

func (or *OrgRunner) execInRegion(
	ctx context.Context,
	cfg aws.Config,
	batchID uint,
	script *models.Script,
	accountID, region, platform string,
	req OrgRequest,
) error {
	instances, err := ec2.ListRunning(ctx, cfg, accountID)
	if err != nil {
		return err
	}

	// Filter by platform.
	var targets []string
	for _, inst := range instances {
		if inst.Platform == platform {
			targets = append(targets, inst.InstanceID)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// Increment TotalInstances by the actual number of targets in this region.
	or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		UpdateColumn("total_instances", gorm.Expr("total_instances + ?", len(targets)))

	executor := &ssmAdapter{e: ssm.New(cfg, or.timeoutSecs)}

	const maxConcPerRegion = 10
	sem := make(chan struct{}, maxConcPerRegion)
	var wg sync.WaitGroup

	for _, iid := range targets {
		wg.Add(1)
		go func(instanceID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			now := time.Now()
			ex := &models.Execution{
				ScriptID:     script.ID,
				BatchID:      &batchID,
				InstanceID:   instanceID,
				AccountID:    accountID,
				Region:       region,
				Status:       models.ExecutionStatusRunning,
				StartTime:    now,
				ChangeNumber: req.ChangeNumber,
			}
			if err := or.db.Create(ex).Error; err != nil {
				or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
					UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
				return
			}

			// Send first so commandID is persisted before we block.
			commandID, err := executor.Send(ctx, []string{instanceID}, script.Content, platform)
			if err != nil {
				ex.Status = models.ExecutionStatusFailed
				ex.Error = err.Error()
				or.db.Save(ex)
				or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
					UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
				return
			}
			ex.CommandID = commandID
			or.db.Save(ex) // persist commandID before blocking

			result, err := executor.WaitForDone(ctx, commandID, instanceID)
			endTime := time.Now()
			ex.EndTime = &endTime

			if err != nil {
				ex.Status = models.ExecutionStatusFailed
				ex.Error = err.Error()
				or.db.Save(ex)
				or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
					UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
			} else {
				exitCode := result.ExitCode
				ex.Status = models.ExecutionStatus(result.Status)
				ex.Output = result.Output
				ex.Error = result.Error
				ex.ExitCode = &exitCode
				or.db.Save(ex)
				or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
					UpdateColumn("completed_instances", gorm.Expr("completed_instances + 1"))
			}
		}(iid)
	}
	wg.Wait()
	return nil
}
