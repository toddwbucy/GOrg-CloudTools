package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// ── linux-qc-prep ─────────────────────────────────────────────────────────────

func TestQCStep_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/linux-qc-prep/execute-qc-step", "application/json",
		jsonBody(t, map[string]any{"step": "step1_initial_qc"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without session, got %d", res.StatusCode)
	}
}

func TestQCStep_InvalidStep(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	// Valid session but unknown step name.
	res, err := client.Post(ts.URL+"/aws/linux-qc-prep/execute-qc-step", "application/json",
		jsonBody(t, map[string]any{"step": "step99_nope"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown step, got %d", res.StatusCode)
	}
}

func TestQCStep_Step2MissingKernel(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Post(ts.URL+"/aws/linux-qc-prep/execute-qc-step", "application/json",
		jsonBody(t, map[string]any{"step": "step2_kernel_staging", "kernel_version": ""}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when kernel_version is empty for step2, got %d", res.StatusCode)
	}
}

func TestQCStep_UnsafeKernelVersion(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	// Seed a change with one instance so we get past the "no change" gate.
	ch := models.Change{ChangeNumber: "CHG-KERN", Status: models.ChangeStatusNew}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("seed change: %v", err)
	}
	if err := db.Create(&models.ChangeInstance{
		ChangeID: ch.ID, InstanceID: "i-safe", AccountID: "123456789012",
		Region: "us-east-1", Platform: "linux",
	}).Error; err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	// Load the change into session.
	loadRes, err := client.Post(
		fmt.Sprintf("%s/aws/linux-qc-prep/load-change/%d", ts.URL, ch.ID),
		"application/json", nil,
	)
	if err != nil {
		t.Fatalf("load-change: %v", err)
	}
	loadRes.Body.Close()

	// kernel_version with shell metacharacters must be rejected.
	for _, bad := range []string{"5.14; rm -rf /", "$(evil)", "`whoami`", "kernel version"} {
		res, err := client.Post(ts.URL+"/aws/linux-qc-prep/execute-qc-step", "application/json",
			jsonBody(t, map[string]any{"step": "step2_kernel_staging", "kernel_version": bad}))
		if err != nil {
			t.Fatalf("POST (kernel=%q): %v", bad, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("kernel_version=%q: expected 400, got %d", bad, res.StatusCode)
		}
	}
}

func TestQCStep_NoChange(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)
	// No change loaded in session → 400.

	res, err := client.Post(ts.URL+"/aws/linux-qc-prep/execute-qc-step", "application/json",
		jsonBody(t, map[string]any{"step": "step1_initial_qc"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when no change in session, got %d", res.StatusCode)
	}
}

func TestQCResults_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/linux-qc-prep/qc-results/1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestQCResults_NotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/linux-qc-prep/qc-results/99999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestQCResults_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "qc", Content: "echo qc", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{
		ScriptID: script.ID, TotalInstances: 1, Status: models.BatchStatusRunning,
		CallerKey: testCallerKey,
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-qc1", AccountID: "111", Region: "us-east-1",
		Status: models.ExecutionStatusRunning,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/linux-qc-prep/qc-results/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["status"] != "success" {
		t.Errorf("expected status=success, got %v", got["status"])
	}
	if got["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", got["total"])
	}
	if _, ok := got["kernel_groups"]; !ok {
		t.Error("kernel_groups field missing from response")
	}
}

func TestQCLatestStep1Results_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/linux-qc-prep/latest-step1-results")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestQCLatestStep1Results_NoChange(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Get(ts.URL + "/aws/linux-qc-prep/latest-step1-results")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	// No change in session → no_change status, not an error.
	if got["status"] != "no_change" {
		t.Errorf("expected status=no_change, got %v", got["status"])
	}
}

func TestQCDownload_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	for _, path := range []string{"/aws/linux-qc-prep/download-reports", "/aws/linux-qc-prep/download-final-report"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d", path, res.StatusCode)
		}
	}
}

func TestQCDownload_Stub(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	for _, path := range []string{"/aws/linux-qc-prep/download-reports", "/aws/linux-qc-prep/download-final-report"} {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Errorf("%s: expected 501, got %d", path, res.StatusCode)
		}
	}
}

// ── linux-qc-post ─────────────────────────────────────────────────────────────

func TestLinuxQCPostExec_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/linux-qc-post/execute-post-validation", "application/json",
		jsonBody(t, map[string]any{"instance_ids": []string{"i-aaa"}}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestLinuxQCPostExec_MissingFields(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	cases := []map[string]any{
		{},                                      // no instance_ids
		{"instance_ids": []string{}},            // empty instance_ids
	}
	for _, body := range cases {
		res, err := client.Post(ts.URL+"/aws/linux-qc-post/execute-post-validation", "application/json",
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

func TestLinuxQCPostResults_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/linux-qc-post/validation-results/1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestLinuxQCPostResults_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "qcpost", Content: "echo post", ScriptType: "bash", Interpreter: "bash"}
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
	ec := 0
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-post1", AccountID: "222", Region: "us-east-1",
		Status:   models.ExecutionStatusCompleted,
		Output:   "myhostname\n✓ PASS: Kernel matches target\n  Current: 4.18.0-553.el8.x86_64\n  Target:  4.18.0-553.el8.x86_64\n",
		ExitCode: &ec,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/linux-qc-post/validation-results/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["status"] != "success" {
		t.Errorf("expected status=success, got %v", got["status"])
	}
	if got["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", got["total"])
	}
	if got["completed"].(float64) != 1 {
		t.Errorf("expected completed=1, got %v", got["completed"])
	}
	if got["passed_count"].(float64) != 1 {
		t.Errorf("expected passed_count=1, got %v", got["passed_count"])
	}
}

// ── sft-fixer ─────────────────────────────────────────────────────────────────

func TestSFTValidateInstance_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/sft-fixer/validate-instance", "application/json",
		jsonBody(t, map[string]any{"instance_id": "i-aaa", "account_number": "123456789012", "region": "us-east-1"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestSFTValidateInstance_MissingFields(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	cases := []map[string]any{
		{},
		{"instance_id": "i-aaa"},
		{"instance_id": "i-aaa", "account_number": "123456789012"},
	}
	for _, body := range cases {
		res, err := client.Post(ts.URL+"/aws/sft-fixer/validate-instance", "application/json",
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

func TestSFTExecScript_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/sft-fixer/execute-script", "application/json",
		jsonBody(t, map[string]any{
			"instance_config": map[string]any{"instance_id": "i-aaa", "account_number": "123456789012", "region": "us-east-1"},
			"script_type":     "detect",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestSFTExecScript_InvalidScriptType(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Post(ts.URL+"/aws/sft-fixer/execute-script", "application/json",
		jsonBody(t, map[string]any{
			"instance_config": map[string]any{
				"instance_id": "i-aaa", "account_number": "123456789012", "region": "us-east-1",
			},
			"script_type": "nonexistent_type",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown script_type, got %d", res.StatusCode)
	}
}

func TestSFTExecScript_MissingRegion(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Post(ts.URL+"/aws/sft-fixer/execute-script", "application/json",
		jsonBody(t, map[string]any{
			"instance_config": map[string]any{
				"instance_id": "i-aaa", "account_number": "123456789012",
				// region omitted
			},
			"script_type": "detect",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing region, got %d", res.StatusCode)
	}
}

func TestSFTExecScript_MissingAccountNumber(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	res, err := client.Post(ts.URL+"/aws/sft-fixer/execute-script", "application/json",
		jsonBody(t, map[string]any{
			"instance_config": map[string]any{
				"instance_id": "i-aaa", "region": "us-east-1",
				// account_number omitted
			},
			"script_type": "detect",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing account_number, got %d", res.StatusCode)
	}
}

func TestSFTBatchStatus_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/sft-fixer/batch-status/1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestSFTBatchStatus_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "sft-detect", Content: "echo detect", ScriptType: "bash", Interpreter: "bash"}
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
	ec := 0
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-sft1", AccountID: "333", Region: "us-east-1",
		Status: models.ExecutionStatusCompleted, Output: "SFT_INSTALLED=true", ExitCode: &ec,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/sft-fixer/batch-status/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if got["status"] != "success" {
		t.Errorf("expected status=success, got %v", got["status"])
	}
	bs, ok := got["batch_status"].(map[string]any)
	if !ok {
		t.Fatalf("batch_status missing or wrong type: %T", got["batch_status"])
	}
	if bs["completed_count"].(float64) != 1 {
		t.Errorf("expected completed_count=1, got %v", bs["completed_count"])
	}
	results, ok := bs["results"].([]any)
	if !ok {
		t.Fatalf("batch_status.results missing or wrong type")
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// ── disk-recon ────────────────────────────────────────────────────────────────

func TestDiskReconRun_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/disk-recon/run", "application/json",
		jsonBody(t, map[string]any{
			"environment": "com", "account_id": "123456789012",
			"region": "us-east-1", "instance_id": "i-aaa", "os_type": "linux",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestDiskReconRun_ValidationErrors(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	cases := []struct {
		body map[string]any
		desc string
	}{
		{map[string]any{"account_id": "123", "region": "us-east-1", "instance_id": "i-aaa"}, "short account_id"},
		{map[string]any{"account_id": "123456789012", "region": "", "instance_id": "i-aaa"}, "empty region"},
		{map[string]any{"account_id": "123456789012", "region": "us-east-1", "instance_id": "notvalid"}, "bad instance_id"},
		{map[string]any{"account_id": "123456789012", "region": "us-east-1", "instance_id": "i-aaa", "os_type": "plan9"}, "bad os_type"},
	}
	for _, tc := range cases {
		res, err := client.Post(ts.URL+"/aws/disk-recon/run", "application/json",
			jsonBody(t, tc.body))
		if err != nil {
			t.Fatalf("%s: POST: %v", tc.desc, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", tc.desc, res.StatusCode)
		}
	}
}

func TestDiskReconPoll_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/disk-recon/poll/fake-command-id?instance_id=i-aaa&region=us-east-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestDiskReconPoll_MissingParams(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	// Missing instance_id and region.
	res, err := client.Get(ts.URL + "/aws/disk-recon/poll/some-command-id")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing query params, got %d", res.StatusCode)
	}
}

// ── rhsa-compliance ───────────────────────────────────────────────────────────

func TestRHSAExecute_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/aws/rhsa-compliance/execute", "application/json",
		jsonBody(t, map[string]any{
			"check_type": "rhsa", "advisory_ids": []string{"RHSA-2024:1234"},
			"instance_ids": []string{"i-aaa"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestRHSAExecute_ValidationErrors(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	cases := []struct {
		body map[string]any
		desc string
	}{
		{map[string]any{"check_type": "rhsa", "advisory_ids": []string{}, "instance_ids": []string{"i-aaa"}}, "empty advisory_ids"},
		{map[string]any{"check_type": "badtype", "advisory_ids": []string{"RHSA-2024:1234"}, "instance_ids": []string{"i-aaa"}}, "bad check_type"},
		{map[string]any{"check_type": "rhsa", "advisory_ids": []string{"INVALID-ID"}, "instance_ids": []string{"i-aaa"}}, "invalid advisory ID format"},
		{map[string]any{"check_type": "rhsa", "advisory_ids": []string{"RHSA-2024:1234"}, "instance_ids": []string{}}, "empty instance_ids"},
	}
	for _, tc := range cases {
		res, err := client.Post(ts.URL+"/aws/rhsa-compliance/execute", "application/json",
			jsonBody(t, tc.body))
		if err != nil {
			t.Fatalf("%s: POST: %v", tc.desc, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", tc.desc, res.StatusCode)
		}
	}
}

func TestRHSAResults_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/rhsa-compliance/results/1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestRHSAResults_Shape(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "rhsa-check", Content: "echo check", ScriptType: "bash", Interpreter: "bash"}
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
	ec := 0
	// Provide valid JSON output matching what the RHSA script produces.
	output := `{"hostname":"host1","date":"2024-01-01T00:00:00Z","pkg_mgr":"yum","results":[{"advisory":"RHSA-2024:1234","status":"APPLIED"}]}`
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-rhsa1", AccountID: "444", Region: "us-east-1",
		Status: models.ExecutionStatusCompleted, Output: output, ExitCode: &ec,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/rhsa-compliance/results/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	// Must have status_counts and results.
	sc, ok := got["status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("status_counts missing or wrong type: %T", got["status_counts"])
	}
	if sc["completed"].(float64) != 1 {
		t.Errorf("expected completed=1, got %v", sc["completed"])
	}

	results, ok := got["results"].([]any)
	if !ok {
		t.Fatalf("results missing or wrong type: %T", got["results"])
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	row := results[0].(map[string]any)
	// compliance field must be present and contain compliance_status.
	compliance, ok := row["compliance"].(map[string]any)
	if !ok {
		t.Fatalf("compliance field missing or wrong type: %T", row["compliance"])
	}
	if compliance["compliance_status"] != "COMPLIANT" {
		t.Errorf("expected COMPLIANT (RHSA was APPLIED), got %v", compliance["compliance_status"])
	}
}

func TestRHSADownload_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/aws/rhsa-compliance/download-results/1?format=csv")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

func TestRHSADownload_Formats(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "rhsa-dl", Content: "echo dl", ScriptType: "bash", Interpreter: "bash"}
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
	ec := 0
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-dl1", AccountID: "555", Region: "us-east-1",
		Status: models.ExecutionStatusCompleted,
		Output: `{"hostname":"h","date":"d","pkg_mgr":"yum","results":[]}`,
		ExitCode: &ec,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	for _, format := range []string{"csv", "json", "text"} {
		url := fmt.Sprintf("%s/aws/rhsa-compliance/download-results/%d?format=%s", ts.URL, batch.ID, format)
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

func TestRHSADownload_BadFormat(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "rhsa-dl2", Content: "echo dl2", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	batch := models.ExecutionBatch{ScriptID: script.ID, TotalInstances: 1, Status: models.BatchStatusCompleted, CallerKey: testCallerKey}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	url := fmt.Sprintf("%s/aws/rhsa-compliance/download-results/%d?format=xml", ts.URL, batch.ID)
	res, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported format, got %d", res.StatusCode)
	}
}

// ── decom-survey ──────────────────────────────────────────────────────────────

func TestDecomSurvey_NoAuth(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	paths := []struct{ method, path string }{
		{"POST", "/aws/decom-survey/scan"},
		{"GET", "/aws/decom-survey/results/1"},
		{"GET", "/aws/decom-survey/download"},
	}
	for _, p := range paths {
		var res *http.Response
		var err error
		if p.method == "POST" {
			res, err = http.Post(ts.URL+p.path, "application/json", jsonBody(t, map[string]any{}))
		} else {
			req, _ := http.NewRequest(p.method, ts.URL+p.path, nil)
			res, err = http.DefaultClient.Do(req)
		}
		if err != nil {
			t.Fatalf("%s %s: %v", p.method, p.path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401, got %d", p.method, p.path, res.StatusCode)
		}
	}
}

func TestDecomSurvey_Stubs(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	paths := []struct{ method, path string }{
		{"POST", "/aws/decom-survey/scan"},
		{"GET", "/aws/decom-survey/results/1"},
		{"GET", "/aws/decom-survey/download"},
	}
	for _, p := range paths {
		var res *http.Response
		var err error
		if p.method == "POST" {
			res, err = client.Post(ts.URL+p.path, "application/json", jsonBody(t, map[string]any{}))
		} else {
			req, _ := http.NewRequest(p.method, ts.URL+p.path, nil)
			res, err = client.Do(req)
		}
		if err != nil {
			t.Fatalf("%s %s: %v", p.method, p.path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Errorf("%s %s: expected 501, got %d", p.method, p.path, res.StatusCode)
		}
	}
}

// ── Output parser unit tests ──────────────────────────────────────────────────

func TestParseComplianceOutput_ValidJSON(t *testing.T) {
	// The compliance parser is exercised by TestRHSAResults_Shape. This test
	// directly verifies the COMPLIANT vs NON_COMPLIANT path via the HTTP result
	// shape.
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)
	authAndStore(t, client, ts.URL)

	script := models.Script{Name: "rhsa-noncompliant", Content: "echo c", ScriptType: "bash", Interpreter: "bash"}
	if err := db.Create(&script).Error; err != nil {
		t.Fatalf("seed script: %v", err)
	}
	batch := models.ExecutionBatch{ScriptID: script.ID, TotalInstances: 1, Status: models.BatchStatusCompleted, CallerKey: testCallerKey}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	ec := 0
	// Advisory is MISSING → should report NON_COMPLIANT.
	output := `{"hostname":"h2","date":"d2","pkg_mgr":"dnf","results":[{"advisory":"RHSA-2024:9999","status":"MISSING"}]}`
	ex := models.Execution{
		ScriptID: script.ID, BatchID: &batch.ID,
		InstanceID: "i-nc1", AccountID: "666", Region: "us-east-1",
		Status: models.ExecutionStatusCompleted, Output: output, ExitCode: &ec,
	}
	if err := db.Create(&ex).Error; err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	res, err := client.Get(fmt.Sprintf("%s/aws/rhsa-compliance/results/%d", ts.URL, batch.ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var got map[string]any
	decodeJSON(t, res, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	results := got["results"].([]any)
	row := results[0].(map[string]any)
	compliance := row["compliance"].(map[string]any)
	if compliance["compliance_status"] != "NON_COMPLIANT" {
		t.Errorf("expected NON_COMPLIANT for missing advisory, got %v", compliance["compliance_status"])
	}
	if compliance["missing"].(float64) != 1 {
		t.Errorf("expected missing=1, got %v", compliance["missing"])
	}
}
