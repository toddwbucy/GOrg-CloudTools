package api

import (
	"encoding/json"
	"errors"
	"net/http"

	awscreds "github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"gorm.io/gorm"
)

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	var tools []models.Tool
	if err := s.db.Find(&tools).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, tools)
}

func (s *Server) handleGetTool(w http.ResponseWriter, r *http.Request) {
	var tool models.Tool
	if err := s.db.Preload("Scripts").First(&tool, r.PathValue("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "tool not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, tool)
}

type execToolRequest struct {
	ToolID       uint     `json:"tool_id"`
	InstanceIDs  []string `json:"instance_ids"`
	AccountID    string   `json:"account_id"`
	Region       string   `json:"region"`
	SessionID    *uint    `json:"session_id"`
	ChangeNumber string   `json:"change_number"`
}

// handleExecuteTool runs every script associated with a tool against the given
// instances. One ExecutionBatch is created per script; all job IDs are returned
// immediately for polling via GET /api/exec/jobs/{id}.
func (s *Server) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	var req execToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ToolID == 0 {
		jsonError(w, "tool_id is required", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}

	var tool models.Tool
	if err := s.db.Preload("Scripts").First(&tool, req.ToolID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "tool not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	if len(tool.Scripts) == 0 {
		jsonError(w, "tool has no scripts", http.StatusBadRequest)
		return
	}

	sess := middleware.GetSession(r)
	cfg, _, err := awscreds.FromSession(r.Context(), sess)
	if err != nil {
		jsonError(w, "no valid AWS credentials in session", http.StatusUnauthorized)
		return
	}

	runner := exec.New(s.db, s.cfg.MaxConcurrentExecutions, s.cfg.ExecutionTimeoutSecs)
	jobIDs := make([]uint, 0, len(tool.Scripts))
	for i := range tool.Scripts {
		scriptID := tool.Scripts[i].ID
		jobID, err := runner.Start(r.Context(), cfg, exec.ScriptRequest{
			ScriptID:     &scriptID,
			Platform:     tool.Platform,
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
		jobIDs = append(jobIDs, jobID)
	}

	w.WriteHeader(http.StatusAccepted)
	jsonOK(w, map[string]any{"tool_id": req.ToolID, "job_ids": jobIDs})
}
