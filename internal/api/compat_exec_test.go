package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// testCallerKey is the AWS access-key ID posted by authAndStore. Batches seeded
// directly via db.Create must set CallerKey to this value so the strict
// caller_key = ? ownership filter in the results/download handlers matches them.
const testCallerKey = "AKIAIOSFODNN7EXAMPLE"

// ── validate-script ───────────────────────────────────────────────────────────

func TestValidateScript_NoWarnings(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/script-runner/validate-script", "application/json",
		jsonBody(t, map[string]any{
			"content":     "df -h\necho 'disk check done'",
			"interpreter": "bash",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	warnings, ok := got["warnings"].([]any)
	if !ok {
		t.Fatalf("warnings field missing or wrong type: %T", got["warnings"])
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateScript_DangerousPattern(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []struct {
		content string
		wantMin int // minimum expected warning count
	}{
		{"rm -rf /", 1},
		{"dd if=/dev/zero of=/dev/sda", 1},
		{"curl https://example.com/script.sh | bash", 1},
	}
	for _, tc := range cases {
		res, err := http.Post(ts.URL+"/aws/script-runner/validate-script", "application/json",
			jsonBody(t, map[string]any{
				"content":     tc.content,
				"interpreter": "bash",
			}))
		if err != nil {
			t.Fatalf("POST %q: %v", tc.content, err)
		}
		var got map[string]any
		decodeJSON(t, res, &got)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%q: expected 200, got %d", tc.content, res.StatusCode)
			continue
		}
		warnings, ok := got["warnings"].([]any)
		if !ok {
			t.Fatalf("%q: warnings field wrong type: %T", tc.content, got["warnings"])
		}
		if len(warnings) < tc.wantMin {
			t.Errorf("%q: expected ≥%d warnings, got %d", tc.content, tc.wantMin, len(warnings))
		}
	}
}

func TestValidateScript_BadBody(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/script-runner/validate-script",
		"application/json", jsonBody(t, "not an object"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

// ── execute ───────────────────────────────────────────────────────────────────

func TestScriptRunnerExec_NoCredentials(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db) // no dev mode, no session
	client := newTestClient(t)

	res, err := client.Post(ts.URL+"/aws/script-runner/execute", "application/json",
		jsonBody(t, map[string]any{
			"content":      "echo hi",
			"instance_ids": []string{"i-aaa"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", res.StatusCode)
	}
}

func TestScriptRunnerExec_MissingFields(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Authenticate in dev mode.
	authAndStore(t, client, ts.URL)

	cases := []map[string]any{
		{"content": "", "instance_ids": []string{"i-aaa"}},                  // empty content
		{"content": "echo hi", "instance_ids": []string{}},                  // no instances
		{"content": "echo hi"},                                              // missing instance_ids
	}
	for _, body := range cases {
		res, err := client.Post(ts.URL+"/aws/script-runner/execute", "application/json",
			jsonBody(t, body))
		if err != nil {
			t.Fatalf("POST %v: %v", body, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("body %v: expected 400, got %d", body, res.StatusCode)
		}
	}
}

// ── results ───────────────────────────────────────────────────────────────────

func TestScriptRunnerResults_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/results/99999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", res.StatusCode)
	}
}

func TestScriptRunnerResults_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/script-runner/results/99999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestScriptRunnerResults_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	// Seed a batch with two executions.
	script := models.Script{Name: "test", Content: "echo hi", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID:       script.ID,
		TotalInstances: 2,
		Status:         models.BatchStatusRunning,
		CallerKey:      testCallerKey,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	exitCode := 0
	execs := []models.Execution{
		{
			ScriptID: script.ID, BatchID: &batch.ID,
			InstanceID: "i-aaa", AccountID: "111", Region: "us-east-1",
			Status: models.ExecutionStatusCompleted, Output: "hello", ExitCode: &exitCode,
		},
		{
			ScriptID: script.ID, BatchID: &batch.ID,
			InstanceID: "i-bbb", AccountID: "111", Region: "us-east-1",
			Status: models.ExecutionStatusRunning,
		},
	}
	for i := range execs {
		if err := db.Create(&execs[i]).Error; err != nil {
			t.Fatalf("seed exec %d: %v", i, err)
		}
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/script-runner/results/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	// Verify status_counts.
	sc, ok := got["status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("status_counts missing or wrong type: %T", got["status_counts"])
	}
	if sc["completed"].(float64) != 1 {
		t.Errorf("expected completed=1, got %v", sc["completed"])
	}
	if sc["running"].(float64) != 1 {
		t.Errorf("expected running=1, got %v", sc["running"])
	}

	// Verify results array shape.
	results, ok := got["results"].([]any)
	if !ok {
		t.Fatalf("results missing or wrong type: %T", got["results"])
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Find completed instance and check field mapping.
	for _, r := range results {
		row := r.(map[string]any)
		if row["instance_id"] == "i-aaa" {
			if row["stdout"] != "hello" {
				t.Errorf("expected stdout=hello, got %v", row["stdout"])
			}
			if row["status"] != "completed" {
				t.Errorf("expected status=completed, got %v", row["status"])
			}
		}
	}
}

// ── download-results ──────────────────────────────────────────────────────────

func TestDownloadResults_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/download-results/99999?format=csv")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", res.StatusCode)
	}
}

func TestDownloadResults_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/script-runner/download-results/99999?format=csv")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestDownloadResults_Formats(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "dl", Content: "echo dl", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID: script.ID, TotalInstances: 1, Status: models.BatchStatusCompleted,
		CallerKey: testCallerKey,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	exitCode := 0
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-ccc", AccountID: "111", Region: "us-east-1",
		Status: models.ExecutionStatusCompleted, Output: "output-text", ExitCode: &exitCode,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	for _, format := range []string{"csv", "json", "text"} {
		url := fmt.Sprintf("%s/aws/script-runner/download-results/%d?format=%s", ts.URL, batch.ID, format)
		res, err := client.Get(url)
		if err != nil {
			t.Fatalf("%s: GET: %v", format, err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Errorf("%s: expected 200, got %d", format, res.StatusCode)
			continue
		}
		cd := res.Header.Get("Content-Disposition")
		if !containsStr(cd, "attachment") {
			t.Errorf("%s: expected Content-Disposition attachment, got %q", format, cd)
		}
		res.Body.Close()
	}
}

// ── library ───────────────────────────────────────────────────────────────────

func TestScriptLibrary_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/library")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", res.StatusCode)
	}
}

func TestScriptLibrary_Empty(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/script-runner/library")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	scripts, ok := got["scripts"].([]any)
	if !ok {
		t.Fatalf("scripts field wrong type: %T", got["scripts"])
	}
	if len(scripts) != 0 {
		t.Errorf("expected empty library, got %d scripts", len(scripts))
	}
}

func TestScriptLibrary_ExcludesEphemeral(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	visible := models.Script{Name: "visible", Content: "echo v", ScriptType: "bash", Interpreter: "bash", Ephemeral: false}
	hidden := models.Script{Name: "hidden", Content: "echo h", ScriptType: "bash", Interpreter: "bash", Ephemeral: true}
	for _, s := range []*models.Script{&visible, &hidden} {
		if err := db.Create(s).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	res, err := client.Get(ts.URL + "/aws/script-runner/library")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	scripts, ok := got["scripts"].([]any)
	if !ok {
		t.Fatalf("scripts field wrong type: %T", got["scripts"])
	}
	if len(scripts) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scripts))
	}
	sc := scripts[0].(map[string]any)
	if sc["name"] != "visible" {
		t.Errorf("expected name=visible, got %q", sc["name"])
	}
}

func TestScriptLibraryGet_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/library/99999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", res.StatusCode)
	}
}

func TestScriptLibraryGet_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/script-runner/library/99999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestScriptLibraryGet_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{
		Name:        "my-script",
		Content:     "echo hello",
		Description: "a test script",
		ScriptType:  "bash",
		Interpreter: "bash",
	}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/script-runner/library/%d", ts.URL, script.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["name"] != "my-script" {
		t.Errorf("expected name=my-script, got %q", got["name"])
	}
	if got["content"] != "echo hello" {
		t.Errorf("expected content=echo hello, got %q", got["content"])
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// authAndStore posts dev-mode credentials and stores the resulting session
// cookie in the client's jar. Required before calling authenticated endpoints.
func authAndStore(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	res, err := client.Post(baseURL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("auth: expected 200, got %d", res.StatusCode)
	}
}

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
