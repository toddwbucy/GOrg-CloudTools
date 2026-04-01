package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := max(1, parseIntParam(q.Get("page"), 1))
	pageSize := min(100, max(1, parseIntParam(q.Get("page_size"), 20)))
	search := q.Get("search")
	scriptType := q.Get("script_type")
	isTemplateStr := q.Get("is_template")

	// Exclude ephemeral scripts (created from inline executions) from the catalog.
	tx := s.db.Model(&models.Script{}).Where("ephemeral = ?", false)
	if search != "" {
		like := "%" + search + "%"
		tx = tx.Where("name LIKE ? OR description LIKE ?", like, like)
	}
	if scriptType != "" {
		tx = tx.Where("script_type = ?", scriptType)
	}
	if isTemplateStr != "" {
		b, err := strconv.ParseBool(isTemplateStr)
		if err != nil {
			jsonError(w, "is_template must be a boolean (true/false/1/0)", http.StatusBadRequest)
			return
		}
		tx = tx.Where("is_template = ?", b)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	var scripts []models.Script
	if err := tx.Offset((page - 1) * pageSize).Limit(pageSize).Find(&scripts).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"items":     scripts,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	var script models.Script
	if err := s.db.Where("ephemeral = ?", false).First(&script, r.PathValue("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "script not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, script)
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	// Decode into a whitelist struct so clients cannot set internal fields
	// like change_id or tool_id on creation.
	var body struct {
		Name        string `json:"name"`
		Content     string `json:"content"`
		Description string `json:"description"`
		ScriptType  string `json:"script_type"`
		Interpreter string `json:"interpreter"`
		IsTemplate  bool   `json:"is_template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Content == "" || body.ScriptType == "" {
		jsonError(w, "name, content, and script_type are required", http.StatusBadRequest)
		return
	}
	if body.Interpreter == "" {
		body.Interpreter = "bash"
	}
	script := models.Script{
		Name:        body.Name,
		Content:     body.Content,
		Description: body.Description,
		ScriptType:  body.ScriptType,
		Interpreter: body.Interpreter,
		IsTemplate:  body.IsTemplate,
	}
	if err := s.db.Create(&script).Error; err != nil {
		jsonError(w, "failed to create script", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(script) //nolint:errcheck
}

func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	var existing models.Script
	if err := s.db.Where("ephemeral = ?", false).First(&existing, r.PathValue("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "script not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	// Pointer fields so callers can explicitly set zero values (e.g. is_template=false).
	var patch struct {
		Name        *string `json:"name"`
		Content     *string `json:"content"`
		Description *string `json:"description"`
		ScriptType  *string `json:"script_type"`
		Interpreter *string `json:"interpreter"`
		IsTemplate  *bool   `json:"is_template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	updates := map[string]any{}
	if patch.Name != nil {
		if *patch.Name == "" {
			jsonError(w, "name must not be empty", http.StatusBadRequest)
			return
		}
		updates["name"] = *patch.Name
	}
	if patch.Content != nil {
		if *patch.Content == "" {
			jsonError(w, "content must not be empty", http.StatusBadRequest)
			return
		}
		updates["content"] = *patch.Content
	}
	if patch.Description != nil {
		updates["description"] = *patch.Description // may be empty
	}
	if patch.ScriptType != nil {
		if *patch.ScriptType == "" {
			jsonError(w, "script_type must not be empty", http.StatusBadRequest)
			return
		}
		updates["script_type"] = *patch.ScriptType
	}
	if patch.Interpreter != nil {
		if *patch.Interpreter == "" {
			jsonError(w, "interpreter must not be empty", http.StatusBadRequest)
			return
		}
		updates["interpreter"] = *patch.Interpreter
	}
	if patch.IsTemplate != nil {
		updates["is_template"] = *patch.IsTemplate
	}
	if len(updates) > 0 {
		if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
			jsonError(w, "failed to update script", http.StatusInternalServerError)
			return
		}
	}
	// Reload to return the persisted state, including any DB-level defaults.
	if err := s.db.First(&existing, existing.ID).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, existing)
}

func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	res := s.db.Where("ephemeral = ?", false).Delete(&models.Script{}, r.PathValue("id"))
	if res.Error != nil {
		jsonError(w, "failed to delete script", http.StatusInternalServerError)
		return
	}
	if res.RowsAffected == 0 {
		jsonError(w, "script not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseIntParam(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}
