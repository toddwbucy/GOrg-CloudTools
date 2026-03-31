package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

type createSessionRequest struct {
	WorkflowType string `json:"workflow_type"` // e.g. "linux-qc", "script-runner"
	Description  string `json:"description"`
	AccountID    string `json:"account_id"`
	Env          string `json:"env"`
}

// handleCreateSession creates a new ExecutionSession and returns its ID.
// The frontend uses the session ID to group related job batches.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	accountID := req.AccountID
	if accountID == "" {
		accountID = sess.AWSEnvironment // best effort
	}

	session := &models.ExecutionSession{
		WorkflowType: req.WorkflowType,
		Description:  req.Description,
		Status:       "in_progress",
		AccountID:    accountID,
		Env:          req.Env,
	}
	if err := s.db.Create(session).Error; err != nil {
		jsonError(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, session)
}

// handleGetSession returns a session with all its batches and per-instance executions.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	var session models.ExecutionSession
	err := s.db.
		Preload("Batches").
		Preload("Batches.Executions").
		First(&session, r.PathValue("id")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "session not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, session)
}

// handleUpdateSessionStatus allows the frontend to mark a session complete or failed.
func (s *Server) handleUpdateSessionStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	allowed := map[string]bool{"in_progress": true, "completed": true, "failed": true, "cancelled": true}
	if !allowed[body.Status] {
		jsonError(w, "status must be one of: in_progress, completed, failed, cancelled", http.StatusBadRequest)
		return
	}
	if err := s.db.Model(&models.ExecutionSession{}).
		Where("id = ?", r.PathValue("id")).
		Update("status", body.Status).Error; err != nil {
		jsonError(w, "failed to update session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListSessions returns recent sessions with summary info (no batch/execution details).
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	workflowType := q.Get("workflow_type")
	status := q.Get("status")
	page := max(1, parseIntParam(q.Get("page"), 1))
	pageSize := min(100, max(1, parseIntParam(q.Get("page_size"), 20)))

	tx := s.db.Model(&models.ExecutionSession{})
	if workflowType != "" {
		tx = tx.Where("workflow_type = ?", workflowType)
	}
	if status != "" {
		tx = tx.Where("status = ?", status)
	}

	var total int64
	tx.Count(&total)

	var sessions []models.ExecutionSession
	if err := tx.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&sessions).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"items":     sessions,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
