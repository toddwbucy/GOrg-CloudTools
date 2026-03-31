package exec

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ec2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ssm"
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

// Start discovers all accounts in the org (or OU), estimates the total
// instance count via DryRun, creates an ExecutionBatch, and begins async execution.
// Returns the batch ID for polling.
func (or *OrgRunner) Start(ctx context.Context, baseCfg aws.Config, req OrgRequest) (uint, error) {
	// Resolve the script first so we fail fast before touching AWS.
	runner := &Runner{db: or.db, maxConc: 10, timeoutSecs: or.timeoutSecs}
	script, err := runner.resolveScript(ScriptRequest{
		ScriptID:     req.ScriptID,
		InlineScript: req.InlineScript,
	})
	if err != nil {
		return 0, err
	}

	// DryRun to get scope without assuming any roles yet.
	accounts, _, err := or.visitor.DryRun(ctx, req.Env, req.ParentID)
	if err != nil {
		return 0, fmt.Errorf("org dry-run: %w", err)
	}

	// Use account count as a proxy for total_instances (updated as we discover instances).
	batch := &models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: len(accounts),
		Status:         models.BatchStatusPending,
		SessionID:      req.SessionID,
	}
	if err := or.db.Create(batch).Error; err != nil {
		return 0, fmt.Errorf("creating org batch: %w", err)
	}

	go or.run(context.Background(), baseCfg, batch.ID, script, req)
	return batch.ID, nil
}

func (or *OrgRunner) run(
	ctx context.Context,
	_ aws.Config, // base cfg is embedded in the visitor
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

	// Adjust TotalInstances now that we know the real count.
	or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
		UpdateColumn("total_instances", gorm.Expr("total_instances + ?", len(targets)-1))

	executor := ssm.New(cfg, or.timeoutSecs)
	for _, iid := range targets {
		now := time.Now()
		exec := &models.Execution{
			ScriptID:     script.ID,
			BatchID:      &batchID,
			InstanceID:   iid,
			AccountID:    accountID,
			Region:       region,
			Status:       models.ExecutionStatusRunning,
			StartTime:    now,
			ChangeNumber: req.ChangeNumber,
		}
		or.db.Create(exec)

		// Send first so commandID is persisted before we block.
		commandID, err := executor.Send(ctx, []string{iid}, script.Content, platform)
		if err != nil {
			exec.Status = models.ExecutionStatusFailed
			exec.Error = err.Error()
			or.db.Save(exec)
			or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
				UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
			continue
		}
		exec.CommandID = commandID
		or.db.Save(exec) // persist commandID before blocking

		result, err := executor.WaitForDone(ctx, commandID, iid)
		endTime := time.Now()
		exec.EndTime = &endTime

		if err != nil {
			exec.Status = models.ExecutionStatusFailed
			exec.Error = err.Error()
			or.db.Save(exec)
			or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
				UpdateColumn("failed_instances", gorm.Expr("failed_instances + 1"))
		} else {
			exitCode := result.ExitCode
			exec.Status = models.ExecutionStatus(result.Status)
			exec.Output = result.Output
			exec.Error = result.Error
			exec.ExitCode = &exitCode
			or.db.Save(exec)
			or.db.Model(&models.ExecutionBatch{}).Where("id = ?", batchID).
				UpdateColumn("completed_instances", gorm.Expr("completed_instances + 1"))
		}
	}
	return nil
}
