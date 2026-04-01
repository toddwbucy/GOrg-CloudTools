package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// handleListChanges returns paginated change records, optionally filtered by
// status or a full-text search across change_number and description.
func (s *Server) handleListChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	search := q.Get("search")
	page := max(1, parseIntParam(q.Get("page"), 1))
	pageSize := min(100, max(1, parseIntParam(q.Get("page_size"), 20)))

	tx := s.db.Model(&models.Change{})
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	if search != "" {
		// Escape LIKE metacharacters so user input is treated as a literal
		// substring. '!' is the escape character; it must be escaped first.
		esc := strings.ReplaceAll(search, "!", "!!")
		esc = strings.ReplaceAll(esc, "%", "!%")
		esc = strings.ReplaceAll(esc, "_", "!_")
		like := "%" + esc + "%"
		tx = tx.Where("change_number LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!'", like, like)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	var changes []models.Change
	if err := tx.Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&changes).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"items":     changes,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// handleGetChange returns a single change with its associated instances and
// non-ephemeral scripts.
func (s *Server) handleGetChange(w http.ResponseWriter, r *http.Request) {
	var change models.Change
	err := s.db.
		Preload("Instances").
		Preload("Scripts", "ephemeral = ?", false).
		First(&change, r.PathValue("id")).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "change not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, change)
}

// handleCreateChange creates a new change record.
// change_number must be unique; status defaults to "new" when omitted.
func (s *Server) handleCreateChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChangeNumber   string         `json:"change_number"`
		Description    string         `json:"description"`
		Status         string         `json:"status"`
		ChangeMetadata map[string]any `json:"change_metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	body.ChangeNumber = strings.TrimSpace(body.ChangeNumber)
	if body.ChangeNumber == "" {
		jsonError(w, "change_number is required", http.StatusBadRequest)
		return
	}
	status := models.ChangeStatusNew
	if body.Status != "" {
		status = models.ChangeStatus(body.Status)
		if !validChangeStatus(status) {
			jsonError(w, "status must be one of: new, approved, completed", http.StatusBadRequest)
			return
		}
	}

	change := models.Change{
		ChangeNumber:   body.ChangeNumber,
		Description:    body.Description,
		Status:         status,
		ChangeMetadata: body.ChangeMetadata,
	}
	if err := s.db.Create(&change).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			jsonError(w, "change_number already exists", http.StatusConflict)
			return
		}
		jsonError(w, "failed to create change", http.StatusInternalServerError)
		return
	}

	jsonCreated(w, change)
}

// handleUpdateChange applies a partial update to a change record.
// change_number is immutable after creation.
// change_metadata replaces the existing value when provided; omit the field
// to leave existing metadata unchanged.
func (s *Server) handleUpdateChange(w http.ResponseWriter, r *http.Request) {
	var existing models.Change
	if err := s.db.First(&existing, r.PathValue("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "change not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	// json.RawMessage distinguishes "field absent" (nil) from "field present
	// as null" so we do not accidentally clear metadata on a status-only PATCH.
	var patch struct {
		ChangeNumber   *string         `json:"change_number"`
		Description    *string         `json:"description"`
		Status         *string         `json:"status"`
		ChangeMetadata json.RawMessage `json:"change_metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if patch.ChangeNumber != nil {
		jsonError(w, "change_number is immutable", http.StatusBadRequest)
		return
	}

	updates := map[string]any{}
	if patch.Description != nil {
		updates["description"] = *patch.Description
	}
	if patch.Status != nil {
		newStatus := models.ChangeStatus(*patch.Status)
		if !validChangeStatus(newStatus) {
			jsonError(w, "status must be one of: new, approved, completed", http.StatusBadRequest)
			return
		}
		updates["status"] = newStatus
	}
	if len(patch.ChangeMetadata) > 0 {
		// json.RawMessage("null") has len=4, so the length check alone does not
		// distinguish an explicit null from an object. An explicit null means
		// "clear the metadata" → write SQL NULL rather than the string "null".
		if string(patch.ChangeMetadata) == "null" {
			updates["change_metadata"] = nil
		} else {
			var meta map[string]any
			if err := json.Unmarshal(patch.ChangeMetadata, &meta); err != nil {
				jsonError(w, "invalid change_metadata: must be a JSON object or null", http.StatusBadRequest)
				return
			}
			// Pre-serialize to a JSON string so GORM stores it correctly in the
			// serializer:json column — GORM does not apply struct-field serializers
			// when the value is passed through a map[string]any updates map.
			metaJSON, err := json.Marshal(meta)
			if err != nil {
				jsonError(w, "failed to serialize change_metadata", http.StatusInternalServerError)
				return
			}
			updates["change_metadata"] = string(metaJSON)
		}
	}

	if len(updates) > 0 {
		if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
			jsonError(w, "failed to update change", http.StatusInternalServerError)
			return
		}
	}

	if err := s.db.First(&existing, existing.ID).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, existing)
}

// validChangeStatus reports whether s is one of the defined ChangeStatus values.
func validChangeStatus(s models.ChangeStatus) bool {
	switch s {
	case models.ChangeStatusNew, models.ChangeStatusApproved, models.ChangeStatusCompleted:
		return true
	}
	return false
}
