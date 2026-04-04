package api_test

import (
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// ── GET /api/exec/jobs/{id} ───────────────────────────────────────────────────

func TestGetJob_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/exec/jobs/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestGetJob_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	script := models.Script{Name: "s", Content: "echo x", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 1,
		Status:         models.BatchStatusRunning,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/exec/jobs/" + itoa(batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var got models.ExecutionBatch
	decodeJSON(t, res, &got)
	if got.TotalInstances != 1 {
		t.Errorf("total_instances: want 1, got %d", got.TotalInstances)
	}
}

func TestGetJob_PreloadsExecutions(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	script := models.Script{Name: "s", Content: "echo x", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 1,
		Status:         models.BatchStatusRunning,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	exec := models.Execution{
		ScriptID:   script.ID,
		InstanceID: "i-0abc123def456",
		Status:     models.ExecutionStatusPending,
		BatchID:    &batch.ID,
	}
	if err := db.Create(&exec).Error; err != nil {
		t.Fatalf("seed execution: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/exec/jobs/" + itoa(batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got models.ExecutionBatch
	decodeJSON(t, res, &got)
	if len(got.Executions) != 1 {
		t.Errorf("expected 1 execution preloaded, got %d", len(got.Executions))
	}
	if len(got.Executions) > 0 && got.Executions[0].InstanceID != "i-0abc123def456" {
		t.Errorf("instance_id: want i-0abc123def456, got %q", got.Executions[0].InstanceID)
	}
}

// ── POST /api/exec/script (validation paths only) ────────────────────────────

func TestExecScript_MissingScriptSource(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Neither script_id nor inline_script provided.
	res, err := http.Post(ts.URL+"/api/exec/script", "application/json",
		jsonBody(t, map[string]any{
			"instance_ids": []string{"i-0abc123def456"},
			"platform":     "linux",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecScript_MissingInstanceIDs(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/exec/script", "application/json",
		jsonBody(t, map[string]any{
			"inline_script": "echo hello",
			"platform":      "linux",
			"instance_ids":  []string{}, // empty
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecScript_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Validation passes but no session credentials → 401.
	res, err := http.Post(ts.URL+"/api/exec/script", "application/json",
		jsonBody(t, map[string]any{
			"inline_script": "echo hello",
			"platform":      "linux",
			"instance_ids":  []string{"i-0abc123def456"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

// ── POST /api/exec/org-script (validation paths only) ────────────────────────

func TestExecOrgScript_MissingScriptSource(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/exec/org-script", "application/json",
		jsonBody(t, map[string]any{"env": "com", "platform": "linux"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecOrgScript_InvalidEnv(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/exec/org-script", "application/json",
		jsonBody(t, map[string]any{
			"inline_script": "echo hello",
			"platform":      "linux",
			"env":           "bad-env",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad env, got %d", res.StatusCode)
	}
}

func TestExecOrgScript_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Valid params, valid env, but no session credentials.
	res, err := http.Post(ts.URL+"/api/exec/org-script", "application/json",
		jsonBody(t, map[string]any{
			"inline_script": "echo hello",
			"platform":      "linux",
			"env":           "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestExecOrgScript_NoOrgRunner_ReturnsServiceUnavailable(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db) // orgRunners is nil in both test server variants
	client := newTestClient(t)

	// Store session credentials via dev mode.
	postRes, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST creds: %v", err)
	}
	postRes.Body.Close()

	res, err := client.Post(ts.URL+"/api/exec/org-script", "application/json",
		jsonBody(t, map[string]any{
			"inline_script": "echo hello",
			"platform":      "linux",
			"env":           "com",
		}))
	if err != nil {
		t.Fatalf("POST org-script: %v", err)
	}
	res.Body.Close()
	// orgRunners[env] is nil → 503.
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", res.StatusCode)
	}
}

// ── GET /api/aws/ssm/commands/{command_id}/status (validation paths) ──────────

func TestGetCommandStatus_MissingParams(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []string{
		"/api/aws/ssm/commands/cmd-abc/status",                        // no account_id or region
		"/api/aws/ssm/commands/cmd-abc/status?account_id=123",         // missing region
		"/api/aws/ssm/commands/cmd-abc/status?region=us-east-1",       // missing account_id
	}
	for _, path := range cases {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("path %s: expected 400, got %d", path, res.StatusCode)
		}
	}
}

// ── POST /api/exec/jobs/{id}/resume ──────────────────────────────────────────

func TestResumeJob_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/exec/jobs/9999/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestResumeJob_NotInterrupted_ReturnsConflict(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	script := models.Script{Name: "s", Content: "echo x", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	// A completed batch cannot be resumed.
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 1,
		Status:         models.BatchStatusCompleted,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	res, err := http.Post(ts.URL+"/api/exec/jobs/"+itoa(batch.ID)+"/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", res.StatusCode)
	}
}

func TestResumeJob_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	script := models.Script{Name: "s", Content: "echo x", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 1,
		Status:         models.BatchStatusInterrupted,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	res, err := http.Post(ts.URL+"/api/exec/jobs/"+itoa(batch.ID)+"/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

// ── GET /api/aws/ssm/commands/{command_id}/status (validation paths) ──────────

func TestGetCommandStatus_NoExecutionsInDB_ReturnsNotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Params valid, DB has no executions for this command → 404 before auth check.
	res, err := http.Get(ts.URL + "/api/aws/ssm/commands/cmd-nonexistent/status?account_id=123456789012&region=us-east-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}
