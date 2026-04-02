package api_test

import (
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
)

// seedScript inserts a non-ephemeral Script and returns it.
func seedScript(t *testing.T, db *gorm.DB, name string) models.Script {
	t.Helper()
	s := models.Script{
		Name:        name,
		Content:     "echo " + name,
		ScriptType:  "bash",
		Interpreter: "bash",
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed script %q: %v", name, err)
	}
	return s
}

// ── GET /api/scripts/ ─────────────────────────────────────────────────────────

func TestListScripts_ExcludesEphemeral(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	seedScript(t, db, "visible")
	// Insert an ephemeral script directly — the API never creates these, runner does.
	ephemeral := models.Script{
		Name: "_inline", Content: "echo hidden",
		ScriptType: "bash", Interpreter: "bash", Ephemeral: true,
	}
	if err := db.Create(&ephemeral).Error; err != nil {
		t.Fatalf("seed ephemeral: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/scripts/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Items []models.Script `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected total=1 (ephemeral excluded), got %d", body.Total)
	}
	for _, s := range body.Items {
		if s.Name == "_inline" {
			t.Error("ephemeral script must not appear in listing")
		}
	}
}

func TestListScripts_SearchFilter(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	seedScript(t, db, "rhsa-check")
	seedScript(t, db, "disk-recon")

	res, err := http.Get(ts.URL + "/api/scripts/?search=rhsa")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Script `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected 1 result for search=rhsa, got %d", body.Total)
	}
	if len(body.Items) > 0 && body.Items[0].Name != "rhsa-check" {
		t.Errorf("wrong script returned: %q", body.Items[0].Name)
	}
}

func TestListScripts_ScriptTypeFilter(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	seedScript(t, db, "bash-script") // script_type defaults to "bash" via seedScript
	if err := db.Create(&models.Script{Name: "ps-script", Content: "echo ps", ScriptType: "powershell", Interpreter: "powershell"}).Error; err != nil {
		t.Fatalf("seed ps-script: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/scripts/?script_type=powershell")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Script `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected 1 powershell script, got %d", body.Total)
	}
}

func TestListScripts_IsTemplateFilter(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	if err := db.Create(&models.Script{Name: "template", Content: "echo t", ScriptType: "bash", Interpreter: "bash", IsTemplate: true}).Error; err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if err := db.Create(&models.Script{Name: "non-template", Content: "echo n", ScriptType: "bash", Interpreter: "bash", IsTemplate: false}).Error; err != nil {
		t.Fatalf("seed non-template: %v", err)
	}

	res, err := http.Get(ts.URL + "/api/scripts/?is_template=true")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Items []models.Script `json:"items"`
		Total int64           `json:"total"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 1 {
		t.Errorf("expected 1 template, got %d", body.Total)
	}
	if len(body.Items) == 0 {
		t.Fatal("expected at least 1 item in response")
	}
	if body.Items[0].Name != "template" {
		t.Errorf("expected template, got %q", body.Items[0].Name)
	}
}

func TestListScripts_IsTemplateInvalidValue(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/scripts/?is_template=banana")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestListScripts_Pagination(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	for i := 1; i <= 25; i++ {
		if err := db.Create(&models.Script{
			Name:        itoa(uint(i)),
			Content:     "echo " + itoa(uint(i)),
			ScriptType:  "bash",
			Interpreter: "bash",
		}).Error; err != nil {
			t.Fatalf("seed script %d: %v", i, err)
		}
	}

	res, err := http.Get(ts.URL + "/api/scripts/?page=2&page_size=20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Items    []models.Script `json:"items"`
		Total    int64           `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"page_size"`
	}
	decodeJSON(t, res, &body)
	if body.Total != 25 {
		t.Errorf("expected total=25, got %d", body.Total)
	}
	if len(body.Items) != 5 {
		t.Errorf("expected 5 items on page 2, got %d", len(body.Items))
	}
}

// ── GET /api/scripts/{id} ─────────────────────────────────────────────────────

func TestGetScript_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	s := seedScript(t, db, "rhsa-check")

	res, err := http.Get(ts.URL + "/api/scripts/" + itoa(s.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Script
	decodeJSON(t, res, &got)
	if got.Name != "rhsa-check" {
		t.Errorf("name: want rhsa-check, got %q", got.Name)
	}
}

func TestGetScript_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/scripts/9999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestGetScript_EphemeralReturns404(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	ep := models.Script{Name: "_inline", Content: "echo x", ScriptType: "bash", Interpreter: "bash", Ephemeral: true}
	if err := db.Create(&ep).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Ephemeral scripts must not be accessible via the public API even by ID.
	res, err := http.Get(ts.URL + "/api/scripts/" + itoa(ep.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for ephemeral script, got %d", res.StatusCode)
	}
}

// ── POST /api/scripts/ ────────────────────────────────────────────────────────

func TestCreateScript_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/scripts/", "application/json",
		jsonBody(t, map[string]any{
			"name":        "my-script",
			"content":     "echo hello",
			"script_type": "bash",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", res.StatusCode)
	}

	var got models.Script
	decodeJSON(t, res, &got)
	if got.Name != "my-script" {
		t.Errorf("name: want my-script, got %q", got.Name)
	}
	if got.ID == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestCreateScript_DefaultsInterpreterToBash(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/scripts/", "application/json",
		jsonBody(t, map[string]any{
			"name":        "no-interp",
			"content":     "echo hi",
			"script_type": "bash",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var got models.Script
	decodeJSON(t, res, &got)
	if got.Interpreter != "bash" {
		t.Errorf("interpreter: want bash, got %q", got.Interpreter)
	}
}

func TestCreateScript_MissingRequiredFields(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []map[string]any{
		{"content": "echo x", "script_type": "bash"},           // no name
		{"name": "x", "script_type": "bash"},                   // no content
		{"name": "x", "content": "echo x"},                     // no script_type
	}
	for _, body := range cases {
		res, err := http.Post(ts.URL+"/api/scripts/", "application/json", jsonBody(t, body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for body %v, got %d", body, res.StatusCode)
		}
	}
}

// ── PATCH /api/scripts/{id} ───────────────────────────────────────────────────

func TestUpdateScript_Name(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	s := seedScript(t, db, "old-name")

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/scripts/"+itoa(s.ID),
		jsonBody(t, map[string]any{"name": "new-name"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Script
	decodeJSON(t, res, &got)
	if got.Name != "new-name" {
		t.Errorf("name: want new-name, got %q", got.Name)
	}
}

func TestUpdateScript_EmptyNameRejected(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	s := seedScript(t, db, "my-script")

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/scripts/"+itoa(s.ID),
		jsonBody(t, map[string]any{"name": ""}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestUpdateScript_IsTemplateFalse(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Create a template script.
	tmpl := models.Script{Name: "tmpl", Content: "echo t", ScriptType: "bash", Interpreter: "bash", IsTemplate: true}
	if err := db.Create(&tmpl).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// PATCH is_template to false — zero value must be honoured via pointer field.
	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/scripts/"+itoa(tmpl.ID),
		jsonBody(t, map[string]any{"is_template": false}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var got models.Script
	decodeJSON(t, res, &got)
	if got.IsTemplate {
		t.Error("is_template should have been set to false")
	}
}

func TestUpdateScript_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/scripts/9999",
		jsonBody(t, map[string]any{"name": "x"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestUpdateScript_EphemeralReturns404(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	ep := models.Script{Name: "_inline", Content: "echo x", ScriptType: "bash", Interpreter: "bash", Ephemeral: true}
	if err := db.Create(&ep).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/scripts/"+itoa(ep.ID),
		jsonBody(t, map[string]any{"name": "hacked"}))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for ephemeral script update, got %d", res.StatusCode)
	}
}

// ── DELETE /api/scripts/{id} ──────────────────────────────────────────────────

func TestDeleteScript_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	s := seedScript(t, db, "to-delete")

	req, _ := http.NewRequest(http.MethodDelete,
		ts.URL+"/api/scripts/"+itoa(s.ID), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", res.StatusCode)
	}

	// Confirm it is gone.
	res2, _ := http.Get(ts.URL + "/api/scripts/" + itoa(s.ID))
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", res2.StatusCode)
	}
}

func TestDeleteScript_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/scripts/9999", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestDeleteScript_EphemeralReturns404(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	ep := models.Script{Name: "_inline", Content: "echo x", ScriptType: "bash", Interpreter: "bash", Ephemeral: true}
	if err := db.Create(&ep).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete,
		ts.URL+"/api/scripts/"+itoa(ep.ID), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	// ephemeral = false WHERE clause means RowsAffected == 0 → 404.
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for ephemeral delete, got %d", res.StatusCode)
	}
}
