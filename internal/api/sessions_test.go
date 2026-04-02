package api_test

import (
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// seedSession inserts an ExecutionSession and returns it.
func seedSession(t *testing.T, db *gorm.DB, workflowType, status string) models.ExecutionSession {
	t.Helper()
	s := models.ExecutionSession{
		WorkflowType: workflowType,
		Status:       status,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return s
}

// ── POST /api/sessions ────────────────────────────────────────────────────────

func TestCreateSession_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/sessions", "application/json",
		jsonBody(t, map[string]any{
			"workflow_type": "linux-qc",
			"description":  "quarterly QC run",
			"account_id":   "123456789012",
			"env":          "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.StatusCode)
	}

	var got models.ExecutionSession
	decodeJSON(t, res, &got)
	if got.WorkflowType != "linux-qc" {
		t.Errorf("workflow_type: want linux-qc, got %q", got.WorkflowType)
	}
	if got.Status != "in_progress" {
		t.Errorf("status: want in_progress, got %q", got.Status)
	}
	if got.ID == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestCreateSession_DefaultsToInProgress(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/sessions", "application/json",
		jsonBody(t, map[string]any{"workflow_type": "script-runner"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.StatusCode)
	}
	var got models.ExecutionSession
	decodeJSON(t, res, &got)
	if got.Status != "in_progress" {
		t.Errorf("status should default to in_progress, got %q", got.Status)
	}
}

// ── GET /api/sessions/{id} ────────────────────────────────────────────────────

func TestGetSession_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	sess := seedSession(t, db, "linux-qc", "in_progress")

	res, err := http.Get(ts.URL + "/api/sessions/" + itoa(sess.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var got models.ExecutionSession
	decodeJSON(t, res, &got)
	if got.WorkflowType != "linux-qc" {
		t.Errorf("workflow_type: want linux-qc, got %q", got.WorkflowType)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/sessions/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestGetSession_PreloadsBatches(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	sess := seedSession(t, db, "linux-qc", "in_progress")

	// Seed a script so the batch FK is valid.
	script := models.Script{Name: "s", Content: "echo x", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 2,
		Status:         models.BatchStatusRunning,
		SessionID:      &sess.ID,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/sessions/" + itoa(sess.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var got models.ExecutionSession
	decodeJSON(t, res, &got)
	if len(got.Batches) != 1 {
		t.Errorf("expected 1 batch preloaded, got %d", len(got.Batches))
	}
}

// ── GET /api/sessions/ ────────────────────────────────────────────────────────

func TestListSessions_ReturnsAll(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	seedSession(t, db, "linux-qc", "in_progress")
	seedSession(t, db, "script-runner", "completed")

	res, err := http.Get(ts.URL + "/api/sessions/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Items []models.ExecutionSession `json:"items"`
		Total int64                     `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 2 {
		t.Errorf("expected total=2, got %d", body.Total)
	}
	if len(body.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(body.Items))
	}
	found := map[string]bool{}
	for _, s := range body.Items {
		found[s.WorkflowType] = true
	}
	if !found["linux-qc"] {
		t.Errorf("expected linux-qc in results")
	}
	if !found["script-runner"] {
		t.Errorf("expected script-runner in results")
	}
}

func TestListSessions_FilterByWorkflowType(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	seedSession(t, db, "linux-qc", "in_progress")
	seedSession(t, db, "script-runner", "completed")

	res, err := http.Get(ts.URL + "/api/sessions/?workflow_type=linux-qc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var body struct {
		Items []models.ExecutionSession `json:"items"`
		Total int64                     `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected 1 session, got %d", body.Total)
	}
	if len(body.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Items))
	}
	if body.Items[0].WorkflowType != "linux-qc" {
		t.Errorf("wrong workflow_type: %q", body.Items[0].WorkflowType)
	}
	if body.Items[0].Status != "in_progress" {
		t.Errorf("wrong status: %q", body.Items[0].Status)
	}
}

func TestListSessions_FilterByStatus(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	seedSession(t, db, "linux-qc", "in_progress")
	seedSession(t, db, "script-runner", "completed")
	seedSession(t, db, "disk-recon", "completed")

	res, err := http.Get(ts.URL + "/api/sessions/?status=completed")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var body struct {
		Items []models.ExecutionSession `json:"items"`
		Total int64                     `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 2 {
		t.Errorf("expected 2 completed sessions, got %d", body.Total)
	}
	if len(body.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(body.Items))
	}
	for _, s := range body.Items {
		if s.Status != "completed" {
			t.Errorf("unexpected status %q in filtered result", s.Status)
		}
	}
}

// ── PATCH /api/sessions/{id}/status ──────────────────────────────────────────

func TestUpdateSessionStatus_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	sess := seedSession(t, db, "linux-qc", "in_progress")

	req, err := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/sessions/"+itoa(sess.ID)+"/status",
		jsonBody(t, map[string]any{"status": "completed"}))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.StatusCode)
	}

	// Verify via GET.
	getRes, err := http.Get(ts.URL + "/api/sessions/" + itoa(sess.ID))
	if err != nil {
		t.Fatalf("GET after update: %v", err)
	}
	if getRes.StatusCode != http.StatusOK {
		t.Fatalf("GET after update: expected 200, got %d", getRes.StatusCode)
	}
	var got models.ExecutionSession
	decodeJSON(t, getRes, &got)
	if got.Status != "completed" {
		t.Errorf("status after update: want completed, got %q", got.Status)
	}
}

func TestUpdateSessionStatus_InvalidStatus(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	sess := seedSession(t, db, "linux-qc", "in_progress")

	req, err := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/sessions/"+itoa(sess.ID)+"/status",
		jsonBody(t, map[string]any{"status": "banana"}))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestUpdateSessionStatus_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	req, err := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/sessions/9999/status",
		jsonBody(t, map[string]any{"status": "completed"}))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}
