package api_test

import (
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

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
