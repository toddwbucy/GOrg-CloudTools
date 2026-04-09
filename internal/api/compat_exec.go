package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	awscreds "github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"gorm.io/gorm"
)

// errCrossRegion is returned by resolveUniformMeta when instance IDs resolve to
// more than one (account, region) pair. The caller uses errors.Is to distinguish
// this validation failure (400) from DB errors (500).
var errCrossRegion = errors.New("instances span multiple account/region pairs; submit separate requests per region")

// ── test-connectivity ─────────────────────────────────────────────────────────

type connectivityRequest struct {
	InstanceIDs []string `json:"instance_ids"`
}

type connectivityResult struct {
	InstanceID string  `json:"instance_id"`
	Accessible bool    `json:"accessible"`
	Error      *string `json:"error"`
}

// handleTestConnectivity uses SSM DescribeInstanceInformation to check whether
// the SSM agent is online for each requested instance.
//
// Route: POST /aws/script-runner/test-connectivity
func (s *Server) handleTestConnectivity(w http.ResponseWriter, r *http.Request) {
	var req connectivityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)

	// Validate region uniformity before building the SSM client — consistent
	// with handleScriptRunnerExec. Cross-region IDs would silently appear
	// unreachable otherwise.
	if _, _, err := s.resolveUniformMeta(req.InstanceIDs, sess); err != nil {
		if errors.Is(err, errCrossRegion) {
			jsonError(w, err.Error(), http.StatusBadRequest)
		} else {
			jsonError(w, "failed to resolve instance metadata", http.StatusInternalServerError)
		}
		return
	}

	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}

	online, err := ssmOnlineSet(r.Context(), cfg, req.InstanceIDs)
	if err != nil {
		jsonError(w, "connectivity check failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]connectivityResult, 0, len(req.InstanceIDs))
	for _, id := range req.InstanceIDs {
		if online[id] {
			results = append(results, connectivityResult{InstanceID: id, Accessible: true})
		} else {
			msg := "SSM agent not reachable"
			results = append(results, connectivityResult{InstanceID: id, Accessible: false, Error: &msg})
		}
	}
	jsonOK(w, map[string]any{"results": results})
}

// ssmOnlineSet calls SSM DescribeInstanceInformation (paginated) and returns
// the set of instance IDs whose agent reports PingStatus Online.
// Any paginator error is returned so the caller can surface it as a 5xx.
func ssmOnlineSet(ctx context.Context, cfg aws.Config, ids []string) (map[string]bool, error) {
	client := awsssm.NewFromConfig(cfg)
	online := make(map[string]bool, len(ids))

	paginator := awsssm.NewDescribeInstanceInformationPaginator(client,
		&awsssm.DescribeInstanceInformationInput{
			Filters: []ssmtypes.InstanceInformationStringFilter{
				{Key: aws.String("InstanceIds"), Values: ids},
			},
		},
	)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, info := range page.InstanceInformationList {
			if info.InstanceId != nil && info.PingStatus == ssmtypes.PingStatusOnline {
				online[*info.InstanceId] = true
			}
		}
	}
	return online, nil
}

// ── validate-script ───────────────────────────────────────────────────────────

type validateScriptRequest struct {
	Content     string `json:"content"`
	Interpreter string `json:"interpreter"`
}

// dangerousPatterns lists patterns that produce a validation warning.
// Keys are the pattern (substring match) and values are the warning message.
var dangerousPatterns = []struct {
	pattern string
	message string
}{
	{"rm -rf /", "Unconditional recursive deletion of root filesystem (rm -rf /)"},
	{"rm -rf /*", "Unconditional recursive deletion of all root paths (rm -rf /*)"},
	{"dd if=", "Disk duplication/destruction with dd"},
	{"mkfs", "Filesystem formatting command (mkfs)"},
	{"> /dev/sd", "Direct write to a block device"},
	{"> /dev/hd", "Direct write to a block device"},
	{":(){ :|:& };:", "Fork bomb detected"},
	{"chmod -R 777 /", "World-writable permissions on root filesystem"},
	{"chown -R", "Recursive ownership change — verify target path"},
	{"| bash", "Piped execution into bash (potential remote code execution)"},
	{"| sh", "Piped execution into sh (potential remote code execution)"},
}

// handleValidateScript performs static analysis on a script and returns
// any detected dangerous patterns as warnings.
//
// Route: POST /aws/script-runner/validate-script
func (s *Server) handleValidateScript(w http.ResponseWriter, r *http.Request) {
	var req validateScriptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	lower := strings.ToLower(req.Content)
	var warnings []string
	for _, dp := range dangerousPatterns {
		if strings.Contains(lower, dp.pattern) {
			warnings = append(warnings, dp.message)
		}
	}
	if warnings == nil {
		warnings = []string{} // return [] not null
	}
	jsonOK(w, map[string]any{"warnings": warnings})
}

// ── execute ───────────────────────────────────────────────────────────────────

type scriptRunnerExecRequest struct {
	Name          string   `json:"name"`
	Content       string   `json:"content"`
	Interpreter   string   `json:"interpreter"`
	Description   string   `json:"description"`
	SaveToLibrary bool     `json:"save_to_library"`
	InstanceIDs   []string `json:"instance_ids"`
}

// handleScriptRunnerExec translates the script-runner frontend's execute call
// into the canonical exec.Runner.Start() primitive.
//
// Route: POST /aws/script-runner/execute
func (s *Server) handleScriptRunnerExec(w http.ResponseWriter, r *http.Request) {
	var req scriptRunnerExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		jsonError(w, "content is required", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	interpreter := strings.ToLower(strings.TrimSpace(req.Interpreter))
	if interpreter == "" {
		interpreter = "bash"
	}
	var platform string
	switch interpreter {
	case "bash":
		platform = "linux"
	case "powershell":
		platform = "windows"
	default:
		jsonError(w, fmt.Sprintf("unsupported interpreter %q: must be bash or powershell", interpreter), http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}

	// Resolve and validate that all instances share the same account/region.
	// SSM is region-scoped: a single SendCommand can only target instances in
	// the caller's region. Return 400 for cross-region requests, 500 for DB errors.
	accountID, region, err := s.resolveUniformMeta(req.InstanceIDs, sess)
	if err != nil {
		if errors.Is(err, errCrossRegion) {
			jsonError(w, err.Error(), http.StatusBadRequest)
		} else {
			jsonError(w, "failed to resolve instance metadata", http.StatusInternalServerError)
		}
		return
	}

	runner := exec.New(s.db, s.cfg.MaxConcurrentExecutions, s.cfg.ExecutionTimeoutSecs)
	execReq := exec.ScriptRequest{
		InlineScript: req.Content,
		Platform:     platform,
		InstanceIDs:  req.InstanceIDs,
		AccountID:    accountID,
		Region:       region,
		CallerKey:    sess.AWSAccessKeyID,
	}

	// If requested, persist the script as a library entry (non-ephemeral) and
	// reference it by ID so the execution record points to the saved script.
	if req.SaveToLibrary {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonError(w, "name is required when save_to_library is true", http.StatusBadRequest)
			return
		}
		scriptType := "bash"
		if interpreter == "powershell" {
			scriptType = "powershell"
		}
		script := models.Script{
			Name:        name,
			Content:     req.Content,
			Description: req.Description,
			ScriptType:  scriptType,
			Interpreter: interpreter,
		}
		if err := s.db.Create(&script).Error; err != nil {
			jsonError(w, "failed to save script to library", http.StatusInternalServerError)
			return
		}
		execReq.InlineScript = ""
		execReq.ScriptID = &script.ID
	}

	jobID, err := runner.Start(r.Context(), cfg, execReq)
	if err != nil {
		// All client-facing validation (content, instance_ids, interpreter,
		// region uniformity, save_to_library name) is done before this call.
		// Any error from Start is an internal failure (DB write, SSM setup).
		slog.Error("runner.Start failed", "err", err)
		jsonError(w, "failed to start execution", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"batch_id":        jobID,
		"execution_count": len(req.InstanceIDs),
	})
}

// resolveUniformMeta resolves account/region for every instance in one bulk DB
// query and validates they all land in the same (account, region) pair. SSM
// SendCommand is region-scoped, so cross-region batches must be split by the
// caller. Returns a 400-worthy error if instances span multiple pairs.
// Instances not found in the DB fall back to the session's account/region.
func (s *Server) resolveUniformMeta(instanceIDs []string, sess *middleware.Session) (accountID, region string, err error) {
	if len(instanceIDs) == 0 {
		return sess.AWSAccountID, homeRegion(sess.AWSEnvironment), nil
	}

	// One query for all IDs.
	type dbRow struct {
		InstanceID string
		AccountID  string
		RegionName string
	}
	var rows []dbRow
	if err := s.db.Model(&models.Instance{}).
		Select("instances.instance_id, accounts.account_id as account_id, regions.name as region_name").
		Joins("JOIN regions ON instances.region_id = regions.id").
		Joins("JOIN accounts ON regions.account_id = accounts.id").
		Where("instances.instance_id IN ?", instanceIDs).
		Scan(&rows).Error; err != nil {
		return "", "", fmt.Errorf("resolving instance metadata: %w", err)
	}

	// Build a lookup table.
	byID := make(map[string]dbRow, len(rows))
	for _, r := range rows {
		byID[r.InstanceID] = r
	}

	fallbackAccount := sess.AWSAccountID
	fallbackRegion := homeRegion(sess.AWSEnvironment)

	type pair struct{ account, region string }
	seen := make(map[pair]struct{})
	var resolved pair

	for _, id := range instanceIDs {
		var p pair
		if r, ok := byID[id]; ok {
			p = pair{r.AccountID, r.RegionName}
		} else {
			p = pair{fallbackAccount, fallbackRegion}
		}
		seen[p] = struct{}{}
		resolved = p // last wins; only used when len(seen)==1
	}

	if len(seen) > 1 {
		return "", "", errCrossRegion
	}
	return resolved.account, resolved.region, nil
}

// homeRegion returns the default home region for the given AWS environment.
func homeRegion(env string) string {
	if strings.EqualFold(env, "gov") {
		return "us-gov-west-1"
	}
	return "us-east-1"
}

// ── results ───────────────────────────────────────────────────────────────────

// handleScriptRunnerResults translates the internal ExecutionBatch shape into
// the {status_counts, results} shape expected by the script-runner JS.
//
// Route: GET /aws/script-runner/results/{batch_id}
func (s *Server) handleScriptRunnerResults(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	var batch models.ExecutionBatch
	err := s.db.Preload("Executions").
		Where("id = ? AND caller_key = ?", r.PathValue("batch_id"), sess.AWSAccessKeyID).
		First(&batch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "batch not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	counts := map[string]int{
		"pending":     0,
		"running":     0,
		"completed":   0,
		"failed":      0,
		"interrupted": 0,
	}
	type resultRow struct {
		InstanceID string  `json:"instance_id"`
		AccountID  string  `json:"account_id"`
		Region     string  `json:"region"`
		Status     string  `json:"status"`
		ExitCode   *int    `json:"exit_code"`
		Stdout     string  `json:"stdout"`
		Stderr     string  `json:"stderr"`
	}
	results := make([]resultRow, 0, len(batch.Executions))

	for _, ex := range batch.Executions {
		st := string(ex.Status)
		if _, ok := counts[st]; ok {
			counts[st]++
		}
		results = append(results, resultRow{
			InstanceID: ex.InstanceID,
			AccountID:  ex.AccountID,
			Region:     ex.Region,
			Status:     st,
			ExitCode:   ex.ExitCode,
			Stdout:     ex.Output,
			Stderr:     ex.Error,
		})
	}

	jsonOK(w, map[string]any{
		"status_counts": counts,
		"results":       results,
	})
}

// ── download-results ──────────────────────────────────────────────────────────

// handleDownloadResults formats batch results as CSV, JSON, or plain text and
// returns them as a file download.
//
// Route: GET /aws/script-runner/download-results/{batch_id}?format=csv|json|text
func (s *Server) handleDownloadResults(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	var batch models.ExecutionBatch
	err := s.db.Preload("Executions").
		Where("id = ? AND caller_key = ?", r.PathValue("batch_id"), sess.AWSAccessKeyID).
		First(&batch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "batch not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "csv"
	}

	batchIDStr := fmt.Sprintf("%d", batch.ID)

	switch format {
	case "json":
		type row struct {
			InstanceID string `json:"instance_id"`
			AccountID  string `json:"account_id"`
			Region     string `json:"region"`
			Status     string `json:"status"`
			ExitCode   *int   `json:"exit_code"`
			Stdout     string `json:"stdout"`
			Stderr     string `json:"stderr"`
		}
		rows := make([]row, 0, len(batch.Executions))
		for _, ex := range batch.Executions {
			rows = append(rows, row{
				InstanceID: ex.InstanceID,
				AccountID:  ex.AccountID,
				Region:     ex.Region,
				Status:     string(ex.Status),
				ExitCode:   ex.ExitCode,
				Stdout:     ex.Output,
				Stderr:     ex.Error,
			})
		}
		b, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			jsonError(w, "failed to marshal results", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="results-`+batchIDStr+`.json"`)
		w.Write(b) //nolint:errcheck

	case "text":
		var buf bytes.Buffer
		for _, ex := range batch.Executions {
			exitStr := "n/a"
			if ex.ExitCode != nil {
				exitStr = fmt.Sprintf("%d", *ex.ExitCode)
			}
			fmt.Fprintf(&buf, "=== Instance: %s | Status: %s | Exit: %s ===\n",
				ex.InstanceID, ex.Status, exitStr)
			if ex.Output != "" {
				fmt.Fprintf(&buf, "--- stdout ---\n%s\n", ex.Output)
			}
			if ex.Error != "" {
				fmt.Fprintf(&buf, "--- stderr ---\n%s\n", ex.Error)
			}
			fmt.Fprintln(&buf)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="results-`+batchIDStr+`.txt"`)
		w.Write(buf.Bytes()) //nolint:errcheck

	case "csv":
		var buf bytes.Buffer
		cw := csv.NewWriter(&buf)
		cw.Write([]string{"instance_id", "account_id", "region", "status", "exit_code", "stdout", "stderr"}) //nolint:errcheck
		for _, ex := range batch.Executions {
			exitStr := ""
			if ex.ExitCode != nil {
				exitStr = fmt.Sprintf("%d", *ex.ExitCode)
			}
			cw.Write([]string{ //nolint:errcheck
				ex.InstanceID,
				ex.AccountID,
				ex.Region,
				string(ex.Status),
				exitStr,
				ex.Output,
				ex.Error,
			})
		}
		cw.Flush()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="results-`+batchIDStr+`.csv"`)
		w.Write(buf.Bytes()) //nolint:errcheck

	default:
		jsonError(w, fmt.Sprintf("unsupported format %q: must be csv, json, or text", format), http.StatusBadRequest)
	}
}

// ── library ───────────────────────────────────────────────────────────────────

// handleScriptLibrary returns the non-ephemeral script catalog in the flat
// array shape expected by the script-runner JS.
//
// Route: GET /aws/script-runner/library
func (s *Server) handleScriptLibrary(w http.ResponseWriter, r *http.Request) {
	type libScript struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		Interpreter string `json:"interpreter"`
		Description string `json:"description"`
	}
	var scripts []models.Script
	if err := s.db.Where("ephemeral = ?", false).
		Order("name ASC").
		Find(&scripts).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	result := make([]libScript, 0, len(scripts))
	for _, sc := range scripts {
		result = append(result, libScript{
			ID:          sc.ID,
			Name:        sc.Name,
			Interpreter: sc.Interpreter,
			Description: sc.Description,
		})
	}
	jsonOK(w, map[string]any{"scripts": result})
}

// handleScriptLibraryGet returns a single library script including its content.
//
// Route: GET /aws/script-runner/library/{id}
func (s *Server) handleScriptLibraryGet(w http.ResponseWriter, r *http.Request) {
	var script models.Script
	err := s.db.Where("ephemeral = ?", false).First(&script, r.PathValue("id")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "script not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, map[string]any{
		"id":          script.ID,
		"name":        script.Name,
		"content":     script.Content,
		"interpreter": script.Interpreter,
		"description": script.Description,
	})
}

// requireAWSSession is a middleware that gates a handler behind a session check:
// the request must carry an encrypted session cookie with AWS credentials.
// Returns 401 for anonymous or credentialless requests.
func (s *Server) requireAWSSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := middleware.GetSession(r)
		if sess.AWSAccessKeyID == "" || sess.AWSSecretAccessKey == "" {
			jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── route registration ────────────────────────────────────────────────────────

// registerScriptRunnerCompatRoutes wires the script-runner frontend compat
// endpoints. Called from registerRoutes().
func (s *Server) registerScriptRunnerCompatRoutes(execRL, readRL rateLimiterWrapper) {
	s.mux.Handle("POST /aws/script-runner/test-connectivity",
		execRL.Wrap(http.HandlerFunc(s.handleTestConnectivity)))
	// validate-script is pure static analysis — no credentials required.
	s.mux.Handle("POST /aws/script-runner/validate-script",
		readRL.Wrap(http.HandlerFunc(s.handleValidateScript)))
	s.mux.Handle("POST /aws/script-runner/execute",
		execRL.Wrap(http.HandlerFunc(s.handleScriptRunnerExec)))
	// Results and library endpoints are gated: callers must have an active session.
	s.mux.Handle("GET /aws/script-runner/results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleScriptRunnerResults))))
	s.mux.Handle("GET /aws/script-runner/download-results/{batch_id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleDownloadResults))))
	s.mux.Handle("GET /aws/script-runner/library",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleScriptLibrary))))
	s.mux.Handle("GET /aws/script-runner/library/{id}",
		readRL.Wrap(s.requireAWSSession(http.HandlerFunc(s.handleScriptLibraryGet))))
}
