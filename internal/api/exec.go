package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	awscreds "github.com/toddwbucy/GOrg-CloudTools/internal/aws/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/aws/ssm"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"gorm.io/gorm"
)

type execScriptRequest struct {
	ScriptID     *uint    `json:"script_id"`
	InlineScript string   `json:"inline_script"`
	Platform     string   `json:"platform"`
	InstanceIDs  []string `json:"instance_ids"`
	AccountID    string   `json:"account_id"`
	Region       string   `json:"region"`
	SessionID    *uint    `json:"session_id"`
	ChangeNumber string   `json:"change_number"`
}

type execOrgRequest struct {
	ScriptID     *uint  `json:"script_id"`
	InlineScript string `json:"inline_script"`
	Platform     string `json:"platform"`
	Env          string `json:"env"`      // "com" or "gov"
	ParentID     string `json:"parent_id"` // OU ID; "" = full org
	SessionID    *uint  `json:"session_id"`
	ChangeNumber string `json:"change_number"`
}

// handleExecScript runs a script against a list of instances and returns a job ID immediately.
func (s *Server) handleExecScript(w http.ResponseWriter, r *http.Request) {
	var req execScriptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ScriptID == nil && req.InlineScript == "" {
		jsonError(w, "one of script_id or inline_script is required", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}

	runner := exec.New(s.db, s.cfg.MaxConcurrentExecutions, s.cfg.ExecutionTimeoutSecs)
	jobID, err := runner.Start(r.Context(), cfg, exec.ScriptRequest{
		ScriptID:     req.ScriptID,
		InlineScript: req.InlineScript,
		Platform:     req.Platform,
		InstanceIDs:  req.InstanceIDs,
		AccountID:    req.AccountID,
		Region:       req.Region,
		SessionID:    req.SessionID,
		ChangeNumber: req.ChangeNumber,
	})
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	jsonOK(w, map[string]any{"job_id": jobID})
}

// handleExecOrgScript runs a script across every matching instance in the org.
// The OrgRunner uses its own server-side management credentials; the user session
// is checked only for authentication (not forwarded to the runner).
func (s *Server) handleExecOrgScript(w http.ResponseWriter, r *http.Request) {
	var req execOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ScriptID == nil && req.InlineScript == "" {
		jsonError(w, "one of script_id or inline_script is required", http.StatusBadRequest)
		return
	}
	if req.Env != "com" && req.Env != "gov" {
		jsonError(w, "env must be 'com' or 'gov'", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	if !sess.HasAWSCredentials(req.Env) {
		jsonError(w, "no valid AWS credentials in session for env "+req.Env, http.StatusUnauthorized)
		return
	}

	runner := s.orgRunners[req.Env]
	if runner == nil {
		jsonError(w, "org execution is not configured for env "+req.Env+" (management credentials required)", http.StatusServiceUnavailable)
		return
	}

	jobID, err := runner.Start(r.Context(), exec.OrgRequest{
		ScriptID:     req.ScriptID,
		InlineScript: req.InlineScript,
		Platform:     req.Platform,
		Env:          req.Env,
		ParentID:     req.ParentID,
		SessionID:    req.SessionID,
		ChangeNumber: req.ChangeNumber,
	})
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	jsonOK(w, map[string]any{"job_id": jobID})
}

// handleGetCommandStatus is the universal SSM command status primitive.
//
// It loads every Execution record for the given commandID, calls
// SSM GetCommandInvocation once per instance using the credentials in the
// session (which must be able to reach the target account), updates the DB
// records, and returns the live status for all instances.
//
// This is the single reusable polling primitive — Linux QC, RHSA checks,
// disk recon, and every other SSM workflow use this same endpoint rather
// than duplicating polling logic.
//
// GET /api/aws/ssm/commands/{command_id}/status
// Query params: account_id, region (required — identifies which assumed-role
// config to use when the session holds management credentials)
func (s *Server) handleGetCommandStatus(w http.ResponseWriter, r *http.Request) {
	commandID := r.PathValue("command_id")
	q := r.URL.Query()
	accountID := q.Get("account_id")
	region := q.Get("region")

	if accountID == "" || region == "" {
		jsonError(w, "account_id and region are required", http.StatusBadRequest)
		return
	}

	// Load all Execution records for this commandID.
	var executions []models.Execution
	if err := s.db.Where("command_id = ? AND account_id = ? AND region = ?",
		commandID, accountID, region).Find(&executions).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	if len(executions) == 0 {
		jsonError(w, "no executions found for command_id", http.StatusNotFound)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}
	cfg.Region = region

	executor := ssm.New(cfg, s.cfg.ExecutionTimeoutSecs)
	results := make([]ssm.InvocationStatus, 0, len(executions))

	for i := range executions {
		ex := &executions[i]
		if ex.CommandID == "" {
			continue
		}

		status, err := executor.GetStatus(r.Context(), commandID, ex.InstanceID)
		if err != nil {
			// Non-fatal: record the error but continue checking other instances.
			results = append(results, ssm.InvocationStatus{
				CommandID:  commandID,
				InstanceID: ex.InstanceID,
				Status:     "error",
				Error:      err.Error(),
			})
			continue
		}

		// Update the DB record if status changed or output arrived.
		// Use a targeted update to avoid clobbering EndTime or other fields
		// that may be written concurrently by the background runOne goroutine.
		if string(ex.Status) != status.Status || ex.Output != status.Output {
			fields := map[string]any{
				"status": models.ExecutionStatus(status.Status),
				"output": status.Output,
				"error":  status.Error,
			}
			if status.Done {
				exitCode := status.ExitCode
				ex.ExitCode = &exitCode
				fields["exit_code"] = &exitCode
			}
			if err := s.db.Model(ex).Updates(fields).Error; err != nil {
				slog.Error("failed to persist execution status update", "execution_id", ex.ID, "err", err)
			}
			// Reflect locally so the response contains the updated values.
			ex.Status = models.ExecutionStatus(status.Status)
			ex.Output = status.Output
			ex.Error = status.Error
		}

		results = append(results, *status)
	}

	jsonOK(w, map[string]any{
		"command_id": commandID,
		"account_id": accountID,
		"region":     region,
		"instances":  results,
	})
}

// handleResumeJob re-attaches polling to an interrupted batch using the
// session credentials provided by the user. Returns 202 immediately;
// the client should poll GET /api/exec/jobs/{id} for final status.
func (s *Server) handleResumeJob(w http.ResponseWriter, r *http.Request) {
	var batch models.ExecutionBatch
	if err := s.db.First(&batch, r.PathValue("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "job not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	if batch.Status != models.BatchStatusInterrupted {
		jsonError(w, "job is not in interrupted state", http.StatusConflict)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}

	runner := exec.New(s.db, s.cfg.MaxConcurrentExecutions, s.cfg.ExecutionTimeoutSecs)
	if err := runner.Resume(r.Context(), cfg, batch.ID); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	jsonOK(w, map[string]any{"job_id": batch.ID})
}

// handleGetJob returns the current status of an ExecutionBatch and its per-instance results.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	var batch models.ExecutionBatch
	err := s.db.Preload("Executions").First(&batch, r.PathValue("id")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "job not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, batch)
}
