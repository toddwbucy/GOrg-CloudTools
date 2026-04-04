package api_test

import (
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// seedToolWithScript inserts a Tool with one associated Script and returns both.
func seedToolWithScript(t *testing.T, db *gorm.DB) (models.Tool, models.Script) {
	t.Helper()
	tool := models.Tool{Name: "disk-recon", ToolType: "operational", Platform: "linux"}
	if err := db.Create(&tool).Error; err != nil {
		t.Fatalf("seed tool: %v", err)
	}
	script := models.Script{
		Name:        "disk-check.sh",
		Content:     "df -h",
		ScriptType:  "bash",
		Interpreter: "bash",
		ToolID:      &tool.ID,
	}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	return tool, script
}

// seedTool inserts a Tool (without scripts) and returns it.
func seedTool(t *testing.T, db *gorm.DB, name string) models.Tool {
	t.Helper()
	tool := models.Tool{
		Name:     name,
		ToolType: "operational",
		Platform: "linux",
	}
	if err := db.Create(&tool).Error; err != nil {
		t.Fatalf("seed tool %q: %v", name, err)
	}
	return tool
}

// ── GET /api/tools/ ───────────────────────────────────────────────────────────

func TestListTools_Empty(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/tools/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var tools []models.Tool
	decodeJSON(t, res, &tools)
	if len(tools) != 0 {
		t.Errorf("expected empty list, got %d tools", len(tools))
	}
}

func TestListTools_ReturnsSeededTools(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	seedTool(t, db, "disk-recon")
	seedTool(t, db, "rhsa-check")

	res, err := http.Get(ts.URL + "/api/tools/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var tools []models.Tool
	decodeJSON(t, res, &tools)
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

// ── GET /api/tools/{id} ───────────────────────────────────────────────────────

func TestGetTool_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	tool := models.Tool{Name: "disk-recon", ToolType: "operational", Platform: "linux"}
	if err := db.Create(&tool).Error; err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/tools/" + itoa(tool.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var got models.Tool
	decodeJSON(t, res, &got)
	if got.Name != "disk-recon" {
		t.Errorf("name: want disk-recon, got %q", got.Name)
	}
}

func TestGetTool_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/tools/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestGetTool_PreloadsScripts(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	tool := models.Tool{Name: "disk-recon", ToolType: "operational", Platform: "linux"}
	if err := db.Create(&tool).Error; err != nil {
		t.Fatalf("seed tool: %v", err)
	}
	script := models.Script{
		Name:        "disk-check.sh",
		Content:     "df -h",
		ScriptType:  "bash",
		Interpreter: "bash",
		ToolID:      &tool.ID,
	}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/tools/" + itoa(tool.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var got models.Tool
	decodeJSON(t, res, &got)
	if len(got.Scripts) != 1 {
		t.Errorf("expected 1 script preloaded, got %d", len(got.Scripts))
	}
	if len(got.Scripts) > 0 && got.Scripts[0].Name != "disk-check.sh" {
		t.Errorf("script name: want disk-check.sh, got %q", got.Scripts[0].Name)
	}
}

// ── POST /api/exec/tool ───────────────────────────────────────────────────────

func TestExecuteTool_MissingToolID_ReturnsBadRequest(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// tool_id omitted → defaults to 0, which fails validation.
	body := jsonBody(t, map[string]any{
		"instance_ids": []string{"i-abc123"},
		"account_id":   "123456789012",
		"region":       "us-east-1",
	})
	res, err := http.Post(ts.URL+"/api/exec/tool", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecuteTool_MissingInstanceIDs_ReturnsBadRequest(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	body := jsonBody(t, map[string]any{
		"tool_id":    1,
		"account_id": "123456789012",
		"region":     "us-east-1",
	})
	res, err := http.Post(ts.URL+"/api/exec/tool", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecuteTool_ToolNotFound_ReturnsNotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	body := jsonBody(t, map[string]any{
		"tool_id":      9999,
		"instance_ids": []string{"i-abc123"},
		"account_id":   "123456789012",
		"region":       "us-east-1",
	})
	res, err := http.Post(ts.URL+"/api/exec/tool", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestExecuteTool_NoScripts_ReturnsBadRequest(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Seed a tool with no associated scripts.
	tool := seedTool(t, db, "empty-tool")
	body := jsonBody(t, map[string]any{
		"tool_id":      tool.ID,
		"instance_ids": []string{"i-abc123"},
		"account_id":   "123456789012",
		"region":       "us-east-1",
	})
	res, err := http.Post(ts.URL+"/api/exec/tool", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestExecuteTool_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	// Default test server: DevMode=false, so credentials are checked.
	ts := newTestServer(t, db)

	tool, _ := seedToolWithScript(t, db)
	body := jsonBody(t, map[string]any{
		"tool_id":      tool.ID,
		"instance_ids": []string{"i-abc123"},
		"account_id":   "123456789012",
		"region":       "us-east-1",
	})
	res, err := http.Post(ts.URL+"/api/exec/tool", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}
