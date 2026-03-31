package api

import (
	"errors"
	"net/http"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
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

func (s *Server) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	// TODO: implement SSM-based tool execution (Sprint 2)
	jsonError(w, "not yet implemented", http.StatusNotImplemented)
}
