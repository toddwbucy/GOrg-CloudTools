package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// multipartCSV builds a multipart/form-data body containing a CSV file field.
func multipartCSV(t *testing.T, csvContent string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "test.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(csvContent)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

// ── load-change ───────────────────────────────────────────────────────────────

// TestLoadChange_NotFound expects 404 when the change does not exist.
func TestLoadChange_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	res, err := client.Post(ts.URL+"/aws/script-runner/load-change/99999", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

// TestLoadChange_InvalidID expects 400 for non-numeric IDs.
func TestLoadChange_InvalidID(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	for _, id := range []string{"abc", "0", "-1"} {
		res, err := client.Post(ts.URL+"/aws/script-runner/load-change/"+id, "application/json", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", id, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("id=%q: expected 400, got %d", id, res.StatusCode)
		}
	}
}

// TestLoadChange_Success verifies that loading a change stores it in the
// session cookie and the cookie persists to subsequent requests.
func TestLoadChange_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Seed a change.
	change := models.Change{ChangeNumber: "CHG0001", Status: models.ChangeStatusNew}
	if err := db.Create(&change).Error; err != nil {
		t.Fatalf("seed change: %v", err)
	}

	res, err := client.Post(
		fmt.Sprintf("%s/aws/script-runner/load-change/%d", ts.URL, change.ID),
		"application/json", nil,
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["change_id"] == nil {
		t.Error("response missing change_id")
	}

	// The same client (with cookie jar) can now fetch the current change.
	res2, err := client.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET current-change: %v", err)
	}
	var current models.Change
	decodeJSON(t, res2, &current)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on current-change, got %d", res2.StatusCode)
	}
	if current.ChangeNumber != "CHG0001" {
		t.Errorf("expected CHG0001, got %q", current.ChangeNumber)
	}
}

// TestLoadChange_AllToolPrefixes ensures the route is registered for every
// tool namespace that embeds change-management.js.
func TestLoadChange_AllToolPrefixes(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Seed a real change so we can verify a successful load on each prefix,
	// not just that the route is registered (a 404 from a missing route and a
	// 404 from a missing record are indistinguishable otherwise).
	change := models.Change{ChangeNumber: "CHG-PREFIX", Status: models.ChangeStatusNew}
	if err := db.Create(&change).Error; err != nil {
		t.Fatalf("seed change: %v", err)
	}

	prefixes := []string{
		"/aws/script-runner",
		"/aws/rhsa-compliance",
		"/aws/linux-qc-prep",
		"/aws/linux-qc-post",
	}
	for _, prefix := range prefixes {
		res, err := client.Post(
			fmt.Sprintf("%s%s/load-change/%d", ts.URL, prefix, change.ID),
			"application/json", nil,
		)
		if err != nil {
			t.Fatalf("%s: POST: %v", prefix, err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Errorf("%s: expected 200, got %d", prefix, res.StatusCode)
			continue
		}
		var body map[string]any
		decodeJSON(t, res, &body)
		if id, ok := body["change_id"].(float64); !ok || uint(id) != change.ID {
			t.Errorf("%s: expected change_id=%d, got %v", prefix, change.ID, body["change_id"])
		}
	}
}

// ── list-changes ──────────────────────────────────────────────────────────────

func TestListChangesAlias_Empty(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/list-changes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got []map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array, got %d items", len(got))
	}
}

func TestListChangesAlias_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Seed two changes with instances.
	c1 := models.Change{ChangeNumber: "CHG0001", Status: models.ChangeStatusNew}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatalf("seed c1: %v", err)
	}
	if err := db.Create(&models.ChangeInstance{ChangeID: c1.ID, InstanceID: "i-aaa", AccountID: "111", Region: "us-east-1", Platform: "linux"}).Error; err != nil {
		t.Fatalf("seed instance i-aaa: %v", err)
	}
	if err := db.Create(&models.ChangeInstance{ChangeID: c1.ID, InstanceID: "i-bbb", AccountID: "111", Region: "us-east-1", Platform: "linux"}).Error; err != nil {
		t.Fatalf("seed instance i-bbb: %v", err)
	}

	c2 := models.Change{ChangeNumber: "CHG0002", Status: models.ChangeStatusNew}
	if err := db.Create(&c2).Error; err != nil {
		t.Fatalf("seed c2: %v", err)
	}

	res, err := http.Get(ts.URL + "/aws/script-runner/list-changes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got []map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(got))
	}

	// Find CHG0001 in results and verify instance_count.
	foundCHG0001 := false
	for _, item := range got {
		if item["change_number"] == "CHG0001" {
			foundCHG0001 = true
			count, ok := item["instance_count"].(float64)
			if !ok {
				t.Fatalf("instance_count is not a number: %T", item["instance_count"])
			}
			if count != 2 {
				t.Errorf("CHG0001: expected instance_count=2, got %v", count)
			}
		}
	}
	if !foundCHG0001 {
		t.Fatalf("CHG0001 not found in list-changes response")
	}
}

// ── save-change-with-instances ────────────────────────────────────────────────

func TestSaveChangeWithInstances_MissingChangeNumber(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("description", "no change number")
	mw.Close()

	res, err := client.Post(ts.URL+"/aws/script-runner/save-change-with-instances",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestSaveChangeWithInstances_CreatesAndLoads(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	instances := []map[string]string{
		{"instance_id": "i-aaa", "account_id": "123456789012", "region": "us-east-1", "platform": "linux"},
		{"instance_id": "i-bbb", "account_id": "123456789012", "region": "us-east-1", "platform": "linux"},
	}
	instJSON, _ := json.Marshal(instances)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("change_number", "CHG0099")
	mw.WriteField("description", "test change")
	mw.WriteField("instances", string(instJSON))
	mw.Close()

	res, err := client.Post(ts.URL+"/aws/script-runner/save-change-with-instances",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	changeID, ok := got["change_id"].(float64)
	if !ok || changeID == 0 {
		t.Fatalf("expected non-zero change_id, got %v", got["change_id"])
	}

	// Verify instances were stored.
	var count int64
	if res := db.Model(&models.ChangeInstance{}).Where("change_id = ?", uint(changeID)).Count(&count); res.Error != nil {
		t.Fatalf("count query failed: %v", res.Error)
	}
	if count != 2 {
		t.Errorf("expected 2 instances, got %d", count)
	}

	// Session should have the change loaded (current-change returns 200).
	res2, err := client.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET current-change: %v", err)
	}
	var current models.Change
	decodeJSON(t, res2, &current)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on current-change, got %d", res2.StatusCode)
	}
	if current.ChangeNumber != "CHG0099" {
		t.Errorf("expected CHG0099, got %q", current.ChangeNumber)
	}
}

func TestSaveChangeWithInstances_ReplacesInstances(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// First save: 2 instances.
	inst1, _ := json.Marshal([]map[string]string{
		{"instance_id": "i-old", "account_id": "111", "region": "us-east-1", "platform": "linux"},
		{"instance_id": "i-also-old", "account_id": "111", "region": "us-east-1", "platform": "linux"},
	})
	var buf1 bytes.Buffer
	mw1 := multipart.NewWriter(&buf1)
	mw1.WriteField("change_number", "CHG0100")
	mw1.WriteField("instances", string(inst1))
	mw1.Close()
	res1, err := client.Post(ts.URL+"/aws/script-runner/save-change-with-instances",
		mw1.FormDataContentType(), &buf1)
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	if res1.StatusCode != http.StatusOK {
		res1.Body.Close()
		t.Fatalf("POST 1: expected 200, got %d", res1.StatusCode)
	}
	var r1 map[string]any
	decodeJSON(t, res1, &r1)
	rawID, ok := r1["change_id"].(float64)
	if !ok || rawID == 0 {
		t.Fatalf("POST 1: expected non-zero numeric change_id, got %v", r1["change_id"])
	}

	// Second save: 1 new instance.
	inst2, _ := json.Marshal([]map[string]string{
		{"instance_id": "i-new", "account_id": "222", "region": "us-west-2", "platform": "linux"},
	})
	var buf2 bytes.Buffer
	mw2 := multipart.NewWriter(&buf2)
	mw2.WriteField("change_number", "CHG0100")
	mw2.WriteField("instances", string(inst2))
	mw2.Close()
	res2, err := client.Post(ts.URL+"/aws/script-runner/save-change-with-instances",
		mw2.FormDataContentType(), &buf2)
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	if res2.StatusCode != http.StatusOK {
		res2.Body.Close()
		t.Fatalf("POST 2: expected 200, got %d", res2.StatusCode)
	}
	res2.Body.Close()

	changeID := uint(rawID)
	var count int64
	if res := db.Model(&models.ChangeInstance{}).Where("change_id = ?", changeID).Count(&count); res.Error != nil {
		t.Fatalf("count query failed: %v", res.Error)
	}
	if count != 1 {
		t.Errorf("expected 1 instance after replacement, got %d", count)
	}
}

// ── clear-change ──────────────────────────────────────────────────────────────

func TestClearChange_ClearsSession(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Load a change first.
	change := models.Change{ChangeNumber: "CHG-CLEAR", Status: models.ChangeStatusNew}
	if err := db.Create(&change).Error; err != nil {
		t.Fatalf("seed change: %v", err)
	}
	res, err := client.Post(
		fmt.Sprintf("%s/aws/script-runner/load-change/%d", ts.URL, change.ID),
		"application/json", nil,
	)
	if err != nil {
		t.Fatalf("POST load-change: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("POST load-change: expected 200, got %d", res.StatusCode)
	}
	res.Body.Close()

	// Verify the change is actually in the session before we clear it.
	resCheck, err := client.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET current-change before clear: %v", err)
	}
	if resCheck.StatusCode != http.StatusOK {
		resCheck.Body.Close()
		t.Fatalf("expected change loaded in session before clear, got %d", resCheck.StatusCode)
	}
	var loaded models.Change
	decodeJSON(t, resCheck, &loaded)
	if loaded.ID != change.ID {
		t.Fatalf("session contains change %d, expected %d", loaded.ID, change.ID)
	}

	// Clear it.
	res2, err := client.Post(ts.URL+"/aws/script-runner/clear-change", "application/json", nil)
	if err != nil {
		t.Fatalf("POST clear-change: %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res2.StatusCode)
	}

	// current-change should now return 404.
	res3, err := client.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET current-change: %v", err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after clear, got %d", res3.StatusCode)
	}
}

// ── upload-change-csv ─────────────────────────────────────────────────────────

func TestUploadChangeCSV_MissingFile(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.Close()

	res, err := client.Post(ts.URL+"/aws/script-runner/upload-change-csv",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestUploadChangeCSV_MissingColumns(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	// CSV with wrong column names.
	body, ct := multipartCSV(t, "foo,bar\nval1,val2\n")
	res, err := client.Post(ts.URL+"/aws/script-runner/upload-change-csv", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing columns, got %d", res.StatusCode)
	}
}

func TestUploadChangeCSV_MismatchedChangeNumber(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)
	client := newTestClient(t)

	csv := strings.Join([]string{
		"change_number,platform,region,account_id,instance_id",
		"CHG-A,linux,us-east-1,111111111111,i-aaa",
		"CHG-B,linux,us-east-1,111111111111,i-bbb", // different change_number
	}, "\n")

	body, ct := multipartCSV(t, csv)
	res, err := client.Post(ts.URL+"/aws/script-runner/upload-change-csv", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for mismatched change_number, got %d", res.StatusCode)
	}
}

func TestUploadChangeCSV_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	csv := strings.Join([]string{
		"change_number,platform,region,account_id,instance_id",
		"CHG-CSV,linux,us-east-1,123456789012,i-csv1",
		"CHG-CSV,linux,us-east-1,123456789012,i-csv2",
	}, "\n")

	body, ct := multipartCSV(t, csv)
	res, err := client.Post(ts.URL+"/aws/script-runner/upload-change-csv", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["change_id"] == nil {
		t.Error("response missing change_id")
	}
	instVal, ok := got["instances"].(float64)
	if !ok {
		t.Fatalf("instances is not a number: %T", got["instances"])
	}
	if instVal != 2 {
		t.Errorf("expected instances=2, got %v", instVal)
	}

	// current-change returns the uploaded change.
	res2, err := client.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET current-change: %v", err)
	}
	if res2.StatusCode != http.StatusOK {
		res2.Body.Close()
		t.Fatalf("GET current-change: expected 200, got %d", res2.StatusCode)
	}
	var current models.Change
	decodeJSON(t, res2, &current)
	if current.ChangeNumber != "CHG-CSV" {
		t.Errorf("expected CHG-CSV, got %q", current.ChangeNumber)
	}
	if len(current.Instances) != 2 {
		t.Errorf("expected 2 instances, got %d", len(current.Instances))
	}
}

// ── current-change ────────────────────────────────────────────────────────────

func TestGetCurrentChange_NoSession(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/script-runner/current-change")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 with no session, got %d", res.StatusCode)
	}
}
