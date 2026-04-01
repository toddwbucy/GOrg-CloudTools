package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ── test helpers ──────────────────────────────────────────────────────────────

func newChangeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.Change{},
		&models.ChangeInstance{},
		&models.Tool{},
		&models.Script{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newChangeTestServer(t *testing.T, db *gorm.DB) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		SecretKey:               "test-secret-key-32-bytes-minimum!!",
		Environment:             "development",
		SessionLifetimeMinutes:  60,
		RateLimitAuth:           "1000/minute",
		RateLimitExecution:      "1000/minute",
		RateLimitRead:           "1000/minute",
		RateLimitWrite:          "1000/minute",
		StaticDir:               t.TempDir(),
	}
	handler := api.NewServer(cfg, db, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// jsonBody serialises v and returns an *bytes.Buffer suitable for http.NewRequest.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewBuffer(b)
}

// decodeJSON decodes an HTTP response body into dst.
func decodeJSON(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// seedChange inserts a Change and returns it.
func seedChange(t *testing.T, db *gorm.DB, changeNumber string, status models.ChangeStatus) models.Change {
	t.Helper()
	c := models.Change{
		ChangeNumber: changeNumber,
		Description:  "seeded for tests",
		Status:       status,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed change %q: %v", changeNumber, err)
	}
	return c
}

// ── POST /api/changes/ ────────────────────────────────────────────────────────

func TestCreateChange_Success(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{
			"change_number": "CHG0001234",
			"description":   "quarterly patching",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", res.StatusCode)
	}

	var got models.Change
	decodeJSON(t, res, &got)
	if got.ChangeNumber != "CHG0001234" {
		t.Errorf("change_number: want CHG0001234, got %q", got.ChangeNumber)
	}
	if got.Status != models.ChangeStatusNew {
		t.Errorf("status: want new, got %q", got.Status)
	}
	if got.ID == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestCreateChange_ExplicitStatus(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{
			"change_number": "CHG0001235",
			"status":        "approved",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.Status != models.ChangeStatusApproved {
		t.Errorf("status: want approved, got %q", got.Status)
	}
}

func TestCreateChange_DuplicateChangeNumberReturns409(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{"change_number": "CHG0001"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", res.StatusCode)
	}
}

func TestCreateChange_MissingChangeNumber(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{"description": "no number"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestCreateChange_InvalidStatus(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{
			"change_number": "CHG9999",
			"status":        "banana",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestCreateChange_WithMetadata(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{
			"change_number":   "CHG0001236",
			"change_metadata": map[string]any{"ticket_url": "https://jira.example.com/CHG-1236", "priority": "high"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.ChangeMetadata == nil {
		t.Fatal("expected change_metadata to be populated")
	}
	if got.ChangeMetadata["priority"] != "high" {
		t.Errorf("metadata priority: want high, got %v", got.ChangeMetadata["priority"])
	}
}

// ── GET /api/changes/ ─────────────────────────────────────────────────────────

func TestListChanges_ReturnsAll(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	seedChange(t, db, "CHG0001", models.ChangeStatusNew)
	seedChange(t, db, "CHG0002", models.ChangeStatusApproved)

	res, err := http.Get(ts.URL + "/api/changes/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Items []models.Change `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 2 {
		t.Errorf("expected total=2, got %d", body.Total)
	}
	if len(body.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(body.Items))
	}
}

func TestListChanges_FilterByStatus(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	seedChange(t, db, "CHG0001", models.ChangeStatusNew)
	seedChange(t, db, "CHG0002", models.ChangeStatusApproved)
	seedChange(t, db, "CHG0003", models.ChangeStatusApproved)

	res, err := http.Get(ts.URL + "/api/changes/?status=approved")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Change `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 2 {
		t.Errorf("expected total=2 for status=approved, got %d", body.Total)
	}
	for _, c := range body.Items {
		if c.Status != models.ChangeStatusApproved {
			t.Errorf("unexpected status %q in filtered result", c.Status)
		}
	}
}

func TestListChanges_SearchByChangeNumber(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	seedChange(t, db, "CHG0001", models.ChangeStatusNew)
	seedChange(t, db, "INC9999", models.ChangeStatusNew)

	res, err := http.Get(ts.URL + "/api/changes/?search=CHG")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Change `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected 1 result for search=CHG, got %d", body.Total)
	}
}

func TestListChanges_Pagination(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	// Seed 25 changes so a second page exists with the default page_size of 20.
	for i := 1; i <= 25; i++ {
		seedChange(t, db, fmt.Sprintf("CHG%04d", i), models.ChangeStatusNew)
	}

	res, err := http.Get(ts.URL + "/api/changes/?page=2&page_size=20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Items    []models.Change `json:"items"`
		Total    int64           `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"page_size"`
	}
	decodeJSON(t, res, &body)

	if body.Total != 25 {
		t.Errorf("expected total=25, got %d", body.Total)
	}
	// Page 2 with page_size=20 should have the remaining 5 items.
	if len(body.Items) != 5 {
		t.Errorf("expected 5 items on page 2, got %d", len(body.Items))
	}
}

func TestListChanges_EmptyResult(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/changes/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Change `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 0 {
		t.Errorf("expected total=0, got %d", body.Total)
	}
	if len(body.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(body.Items))
	}
}

// ── GET /api/changes/{id} ─────────────────────────────────────────────────────

func TestGetChange_Success(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	res, err := http.Get(ts.URL + "/api/changes/" + itoa(c.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.ChangeNumber != "CHG0001" {
		t.Errorf("change_number: want CHG0001, got %q", got.ChangeNumber)
	}
}

func TestGetChange_NotFound(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/changes/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestGetChange_PreloadsInstances(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusApproved)

	// Seed an associated ChangeInstance.
	inst := models.ChangeInstance{
		ChangeID:   c.ID,
		InstanceID: "i-0abc123def456",
		AccountID:  "123456789012",
		Region:     "us-east-1",
		Platform:   "linux",
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/changes/" + itoa(c.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if len(got.Instances) != 1 {
		t.Errorf("expected 1 instance, got %d", len(got.Instances))
	}
	if got.Instances[0].InstanceID != "i-0abc123def456" {
		t.Errorf("unexpected instance_id: %q", got.Instances[0].InstanceID)
	}
}

func TestGetChange_PreloadsNonEphemeralScripts(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusApproved)

	// Non-ephemeral script — should appear in the response.
	visible := models.Script{
		Name:        "visible",
		Content:     "echo ok",
		ScriptType:  "bash",
		Interpreter: "bash",
		Ephemeral:   false,
		ChangeID:    &c.ID,
	}
	if err := db.Create(&visible).Error; err != nil {
		t.Fatalf("seed visible script: %v", err)
	}

	// Ephemeral script — must be filtered out.
	hidden := models.Script{
		Name:        "_inline",
		Content:     "echo hidden",
		ScriptType:  "bash",
		Interpreter: "bash",
		Ephemeral:   true,
		ChangeID:    &c.ID,
	}
	if err := db.Create(&hidden).Error; err != nil {
		t.Fatalf("seed hidden script: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/changes/" + itoa(c.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if len(got.Scripts) != 1 {
		t.Errorf("expected 1 non-ephemeral script, got %d", len(got.Scripts))
	}
	if len(got.Scripts) > 0 && got.Scripts[0].Name != "visible" {
		t.Errorf("expected script name %q, got %q", "visible", got.Scripts[0].Name)
	}
}

// ── PATCH /api/changes/{id} ───────────────────────────────────────────────────

func TestUpdateChange_Status(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(c.ID),
		jsonBody(t, map[string]any{"status": "approved"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.Status != models.ChangeStatusApproved {
		t.Errorf("status: want approved, got %q", got.Status)
	}
}

func TestUpdateChange_Description(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(c.ID),
		jsonBody(t, map[string]any{"description": "updated description"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.Description != "updated description" {
		t.Errorf("description: want %q, got %q", "updated description", got.Description)
	}
	// Status unchanged.
	if got.Status != models.ChangeStatusNew {
		t.Errorf("status should not have changed, got %q", got.Status)
	}
}

func TestUpdateChange_Metadata(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(c.ID),
		jsonBody(t, map[string]any{
			"change_metadata": map[string]any{"env": "production", "risk": "low"},
		}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.ChangeMetadata["risk"] != "low" {
		t.Errorf("metadata risk: want low, got %v", got.ChangeMetadata["risk"])
	}
}

func TestUpdateChange_OmittedMetadataPreservesExisting(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	// Create change with metadata.
	res, err := http.Post(ts.URL+"/api/changes/", "application/json",
		jsonBody(t, map[string]any{
			"change_number":   "CHG0001",
			"change_metadata": map[string]any{"priority": "critical"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var created models.Change
	decodeJSON(t, res, &created)

	// PATCH status only — metadata field absent from body.
	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(created.ID),
		jsonBody(t, map[string]any{"status": "approved"}))
	req.Header.Set("Content-Type", "application/json")
	patchRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	var got models.Change
	decodeJSON(t, patchRes, &got)
	if got.ChangeMetadata["priority"] != "critical" {
		t.Errorf("metadata should not have changed; got %v", got.ChangeMetadata)
	}
}

func TestUpdateChange_InvalidStatus(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(c.ID),
		jsonBody(t, map[string]any{"status": "banana"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestUpdateChange_NotFound(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/9999",
		jsonBody(t, map[string]any{"status": "approved"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestUpdateChange_EmptyBody(t *testing.T) {
	db := newChangeTestDB(t)
	ts := newChangeTestServer(t, db)
	c := seedChange(t, db, "CHG0001", models.ChangeStatusNew)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/changes/"+itoa(c.ID),
		jsonBody(t, map[string]any{}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	// Empty patch is a no-op — should succeed and return the unchanged record.
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for empty patch, got %d", res.StatusCode)
	}
	var got models.Change
	decodeJSON(t, res, &got)
	if got.Status != models.ChangeStatusNew {
		t.Errorf("status should be unchanged, got %q", got.Status)
	}
}

// itoa converts a uint to its decimal string for use in URL paths.
func itoa(id uint) string {
	return fmt.Sprintf("%d", id)
}
