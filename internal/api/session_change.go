package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// handleLoadChange stores the given change ID in the encrypted session cookie,
// making it the "active" change for subsequent tool operations.
//
// Route: POST {tool-prefix}/load-change/{id}
func (s *Server) handleLoadChange(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		jsonError(w, "invalid change id", http.StatusBadRequest)
		return
	}

	var change models.Change
	if err := s.db.First(&change, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			jsonError(w, "change not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}

	sess := middleware.GetSession(r)
	sess.CurrentChangeID = change.ID
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"status": "ok", "change_id": change.ID})
}

// handleListChangesAlias returns the flat array shape expected by change-management.js:
//
//	[{id, change_number, instance_count}, ...]
//
// This differs from handleListChanges which returns a paginated envelope.
// Route: GET {tool-prefix}/list-changes
func (s *Server) handleListChangesAlias(w http.ResponseWriter, r *http.Request) {
	type row struct {
		ID           uint   `json:"id"`
		ChangeNumber string `json:"change_number"`
		InstanceCount int64 `json:"instance_count"`
	}

	var changes []models.Change
	if err := s.db.Order("created_at DESC").Find(&changes).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	// Single aggregated query instead of N separate Count calls.
	type countRow struct {
		ChangeID uint  `gorm:"column:change_id"`
		Count    int64 `gorm:"column:count"`
	}
	var counts []countRow
	if err := s.db.Model(&models.ChangeInstance{}).
		Select("change_id, COUNT(*) as count").
		Group("change_id").
		Scan(&counts).Error; err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}
	countByID := make(map[uint]int64, len(counts))
	for _, cr := range counts {
		countByID[cr.ChangeID] = cr.Count
	}

	result := make([]row, 0, len(changes))
	for _, c := range changes {
		result = append(result, row{
			ID:            c.ID,
			ChangeNumber:  c.ChangeNumber,
			InstanceCount: countByID[c.ID],
		})
	}
	jsonOK(w, result)
}

// handleSaveChangeWithInstances creates (or replaces) a Change and its
// ChangeInstance records from a multipart/form-data body, then loads the
// change into the session.
//
// FormData fields:
//
//	change_number  — string, required
//	description    — string, optional
//	instances      — JSON array of {instance_id, account_id, region, platform}
//
// Route: POST {tool-prefix}/save-change-with-instances
func (s *Server) handleSaveChangeWithInstances(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if ferr := r.ParseForm(); ferr != nil {
			jsonError(w, "failed to parse form", http.StatusBadRequest)
			return
		}
	}

	changeNumber := strings.TrimSpace(r.PostFormValue("change_number"))
	if changeNumber == "" {
		jsonError(w, "change_number is required", http.StatusBadRequest)
		return
	}
	description := strings.TrimSpace(r.PostFormValue("description"))

	type instanceInput struct {
		InstanceID string `json:"instance_id"`
		AccountID  string `json:"account_id"`
		Region     string `json:"region"`
		Platform   string `json:"platform"`
	}
	var instances []instanceInput
	if raw := r.PostFormValue("instances"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &instances); err != nil {
			jsonError(w, "invalid instances JSON", http.StatusBadRequest)
			return
		}
	}

	// Find or create the Change, then replace ChangeInstances — all in one transaction.
	var change models.Change
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("change_number = ?", changeNumber).First(&change).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			change = models.Change{
				ChangeNumber: changeNumber,
				Description:  description,
				Status:       models.ChangeStatusNew,
			}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
		} else if description != "" {
			if err := tx.Model(&change).Update("description", description).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("change_id = ?", change.ID).Delete(&models.ChangeInstance{}).Error; err != nil {
			return err
		}
		for _, inst := range instances {
			ci := models.ChangeInstance{
				ChangeID:   change.ID,
				InstanceID: inst.InstanceID,
				AccountID:  inst.AccountID,
				Region:     inst.Region,
				Platform:   inst.Platform,
			}
			if err := tx.Create(&ci).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	// Load into session.
	sess := middleware.GetSession(r)
	sess.CurrentChangeID = change.ID
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"status": "ok", "change_id": change.ID})
}

// handleSaveManualChange creates (or finds) a Change from a textarea list of
// instance IDs and loads it into the session. Unlike save-change-with-instances,
// the caller provides plain instance IDs only; account_id and region are taken
// from the request body (required) rather than session credentials because the
// session does not store region.
//
// JSON body fields:
//
//	change_number  — string, required
//	instance_ids   — []string, required (one EC2 instance ID per element)
//	account_id     — string, required
//	region         — string, required
//	platform       — string, optional (defaults to "linux")
//
// Route: POST /aws/script-runner/save-manual-change
func (s *Server) handleSaveManualChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChangeNumber string   `json:"change_number"`
		InstanceIDs  []string `json:"instance_ids"`
		AccountID    string   `json:"account_id"`
		Region       string   `json:"region"`
		Platform     string   `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.ChangeNumber = strings.TrimSpace(req.ChangeNumber)
	if req.ChangeNumber == "" {
		jsonError(w, "change_number is required", http.StatusBadRequest)
		return
	}
	if len(req.InstanceIDs) == 0 {
		jsonError(w, "instance_ids must not be empty", http.StatusBadRequest)
		return
	}
	if req.AccountID == "" {
		jsonError(w, "account_id is required", http.StatusBadRequest)
		return
	}
	if req.Region == "" {
		jsonError(w, "region is required", http.StatusBadRequest)
		return
	}
	if req.Platform == "" {
		req.Platform = "linux"
	}

	// Deduplicate instance IDs while preserving order.
	seen := make(map[string]struct{}, len(req.InstanceIDs))
	unique := req.InstanceIDs[:0]
	for _, id := range req.InstanceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		jsonError(w, "no valid instance_ids after deduplication", http.StatusBadRequest)
		return
	}

	var change models.Change
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("change_number = ?", req.ChangeNumber).First(&change).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			change = models.Change{
				ChangeNumber: req.ChangeNumber,
				Status:       models.ChangeStatusNew,
			}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
		}
		// Replace all instances for this change.
		if err := tx.Where("change_id = ?", change.ID).Delete(&models.ChangeInstance{}).Error; err != nil {
			return err
		}
		for _, instID := range unique {
			ci := models.ChangeInstance{
				ChangeID:   change.ID,
				InstanceID: instID,
				AccountID:  req.AccountID,
				Region:     req.Region,
				Platform:   req.Platform,
			}
			if err := tx.Create(&ci).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	sess := middleware.GetSession(r)
	sess.CurrentChangeID = change.ID
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"status": "ok", "change_id": change.ID, "instances": len(unique)})
}

// handleClearChange removes the current change from the session.
//
// Route: POST {tool-prefix}/clear-change
func (s *Server) handleClearChange(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	sess.CurrentChangeID = 0
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// handleUploadChangeCSV parses a CSV file and creates a Change with its
// ChangeInstances, then loads it into the session.
//
// CSV columns (header row required): change_number, platform, region, account_id, instance_id
// All rows must share the same change_number; extra columns are ignored.
//
// Route: POST {tool-prefix}/upload-change-csv
func (s *Server) handleUploadChangeCSV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		jsonError(w, "failed to parse multipart form", http.StatusBadRequest)
		return
	}

	f, _, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file field is required", http.StatusBadRequest)
		return
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		jsonError(w, "failed to parse CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(records) < 2 {
		jsonError(w, "CSV must have a header row and at least one data row", http.StatusBadRequest)
		return
	}

	// Build column index from header.
	header := records[0]
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	required := []string{"change_number", "platform", "region", "account_id", "instance_id"}
	for _, req := range required {
		if _, ok := col[req]; !ok {
			jsonError(w, fmt.Sprintf("CSV missing required column: %s", req), http.StatusBadRequest)
			return
		}
	}

	// Compute the maximum column index among all required fields so a single
	// length check per row covers all of them.
	maxColIdx := 0
	for _, req := range required {
		if col[req] > maxColIdx {
			maxColIdx = col[req]
		}
	}

	type csvRow struct {
		changeNumber string
		platform     string
		region       string
		accountID    string
		instanceID   string
	}
	rows := make([]csvRow, 0, len(records)-1)
	for i, rec := range records[1:] {
		if len(rec) <= maxColIdx {
			jsonError(w, fmt.Sprintf("CSV row %d: not enough columns", i+2), http.StatusBadRequest)
			return
		}
		rows = append(rows, csvRow{
			changeNumber: strings.TrimSpace(rec[col["change_number"]]),
			platform:     strings.TrimSpace(rec[col["platform"]]),
			region:       strings.TrimSpace(rec[col["region"]]),
			accountID:    strings.TrimSpace(rec[col["account_id"]]),
			instanceID:   strings.TrimSpace(rec[col["instance_id"]]),
		})
	}
	if len(rows) == 0 {
		jsonError(w, "CSV contains no data rows", http.StatusBadRequest)
		return
	}

	changeNumber := rows[0].changeNumber
	if changeNumber == "" {
		jsonError(w, "change_number must not be empty", http.StatusBadRequest)
		return
	}
	// All rows must belong to the same change to prevent silently attaching
	// instances to the wrong record.
	for i, row := range rows[1:] {
		if row.changeNumber != changeNumber {
			jsonError(w, fmt.Sprintf("CSV row %d: change_number %q does not match first row %q", i+3, row.changeNumber, changeNumber), http.StatusBadRequest)
			return
		}
	}

	// Find or create the Change, then replace ChangeInstances — all in one transaction.
	var change models.Change
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("change_number = ?", changeNumber).First(&change).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			change = models.Change{
				ChangeNumber: changeNumber,
				Status:       models.ChangeStatusNew,
			}
			if err := tx.Create(&change).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("change_id = ?", change.ID).Delete(&models.ChangeInstance{}).Error; err != nil {
			return err
		}
		for _, row := range rows {
			ci := models.ChangeInstance{
				ChangeID:   change.ID,
				InstanceID: row.instanceID,
				AccountID:  row.accountID,
				Region:     row.region,
				Platform:   row.platform,
			}
			if err := tx.Create(&ci).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "database error", http.StatusInternalServerError)
		return
	}

	// Load into session.
	sess := middleware.GetSession(r)
	sess.CurrentChangeID = change.ID
	if err := middleware.SaveSession(w, s.ses, sess); err != nil {
		jsonError(w, "failed to save session", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{
		"status":    "ok",
		"change_id": change.ID,
		"instances": len(rows),
	})
}

// handleGetCurrentChange returns the change currently loaded in the session,
// including its ChangeInstance records.
//
// Route: GET {tool-prefix}/current-change
func (s *Server) handleGetCurrentChange(w http.ResponseWriter, r *http.Request) {
	sess := middleware.GetSession(r)
	if sess.CurrentChangeID == 0 {
		jsonError(w, "no change loaded", http.StatusNotFound)
		return
	}
	var change models.Change
	err := s.db.Preload("Instances").First(&change, sess.CurrentChangeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Change was deleted after session was written; clean up stale reference.
			sess.CurrentChangeID = 0
			middleware.SaveSession(w, s.ses, sess) //nolint:errcheck
			jsonError(w, "change not found", http.StatusNotFound)
		} else {
			jsonError(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, change)
}

// registerChangeManagementRoutes registers the 5 change-management alias routes
// under the given tool prefix (e.g. "/aws/script-runner").
func (s *Server) registerChangeManagementRoutes(prefix string, readRL, writeRL rateLimiterWrapper) {
	s.mux.Handle("POST "+prefix+"/load-change/{id}",
		writeRL.Wrap(http.HandlerFunc(s.handleLoadChange)))
	s.mux.Handle("GET "+prefix+"/list-changes",
		readRL.Wrap(http.HandlerFunc(s.handleListChangesAlias)))
	s.mux.Handle("POST "+prefix+"/save-change-with-instances",
		writeRL.Wrap(http.HandlerFunc(s.handleSaveChangeWithInstances)))
	s.mux.Handle("POST "+prefix+"/clear-change",
		writeRL.Wrap(http.HandlerFunc(s.handleClearChange)))
	s.mux.Handle("POST "+prefix+"/upload-change-csv",
		writeRL.Wrap(http.HandlerFunc(s.handleUploadChangeCSV)))
	s.mux.Handle("GET "+prefix+"/current-change",
		readRL.Wrap(http.HandlerFunc(s.handleGetCurrentChange)))
}

// rateLimiterWrapper is a minimal interface so registerChangeManagementRoutes
// can accept the concrete *middleware.RateLimiter without importing the package directly.
type rateLimiterWrapper interface {
	Wrap(http.Handler) http.Handler
}
