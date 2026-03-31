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

	tx := s.db.Model(&models.Script{})
	if search != "" {
		like := "%" + search + "%"
		tx = tx.Where("name LIKE ? OR description LIKE ?", like, like)
	}
	if scriptType != "" {
		tx = tx.Where("script_type = ?", scriptType)
	}
	if isTemplateStr != "" {
		b, _ := strconv.ParseBool(isTemplateStr)
		tx = tx.Where("is_template = ?", b)
	}

	var total int64
	tx.Count(&total)

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
	if err := s.db.First(&script, r.PathValue("id")).Error; err != nil {
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
	var script models.Script
	if err := json.NewDecoder(r.Body).Decode(&script); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if script.Name == "" || script.Content == "" || script.ScriptType == "" {
		jsonError(w, "name, content, and script_type are required", http.StatusBadRequest)
		return
	}
	if script.Interpreter == "" {
		script.Interpreter = "bash"
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
	if err := s.db.First(&existing, r.PathValue("id")).Error; err != nil {
		jsonError(w, "script not found", http.StatusNotFound)
		return
	}
	var updates models.Script
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
		jsonError(w, "failed to update script", http.StatusInternalServerError)
		return
	}
	jsonOK(w, existing)
}

func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Delete(&models.Script{}, r.PathValue("id")).Error; err != nil {
		jsonError(w, "failed to delete script", http.StatusInternalServerError)
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
