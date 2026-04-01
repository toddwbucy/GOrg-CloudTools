package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// ── POST /api/auth/aws-credentials ───────────────────────────────────────────

func TestCreateCredentials_MissingFields(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []map[string]any{
		{"secret_access_key": "secret", "environment": "com"}, // no access_key_id
		{"access_key_id": "AKID", "environment": "com"},       // no secret_access_key
		{"access_key_id": "AKID", "secret_access_key": "s"},   // no environment
	}
	for _, body := range cases {
		res, err := http.Post(ts.URL+"/api/auth/aws-credentials", "application/json", jsonBody(t, body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for body %v, got %d", body, res.StatusCode)
		}
	}
}

func TestCreateCredentials_InvalidEnvironment(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKID",
			"secret_access_key": "secret",
			"environment":       "us-east-1", // not "com" or "gov"
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestCreateCredentials_DevMode_Success(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	res, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body map[string]string
	decodeJSON(t, res, &body)
	if body["account_id"] != "dev-mode" {
		t.Errorf("account_id: want dev-mode, got %q", body["account_id"])
	}
}

// ── GET /api/auth/aws-credentials/{environment} ───────────────────────────────

func TestGetCredentials_NoSession_ReturnsNotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/auth/aws-credentials/com")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 with no session, got %d", res.StatusCode)
	}
}

func TestGetCredentials_DevMode_Found(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	// Use a client with cookie jar so the session cookie survives to the GET.
	client := newTestClient(t)

	postRes, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST credentials: %v", err)
	}
	postRes.Body.Close()

	res, err := client.Get(ts.URL + "/api/auth/aws-credentials/com")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var body map[string]string
	decodeJSON(t, res, &body)
	if body["environment"] != "com" {
		t.Errorf("environment: want com, got %q", body["environment"])
	}
}

func TestGetCredentials_WrongEnv_ReturnsNotFound(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Store credentials for "com".
	postRes, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKID",
			"secret_access_key": "secret",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	postRes.Body.Close()

	// Ask for "gov" credentials — should be 404.
	res, err := client.Get(ts.URL + "/api/auth/aws-credentials/gov")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for wrong env, got %d", res.StatusCode)
	}
}

// ── DELETE /api/auth/aws-credentials/{environment} ───────────────────────────

func TestDeleteCredentials_ClearsSession(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	// Store credentials.
	postRes, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKID",
			"secret_access_key": "secret",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	postRes.Body.Close()

	// Confirm credentials are present.
	getRes, err := client.Get(ts.URL + "/api/auth/aws-credentials/com")
	if err != nil {
		t.Fatalf("GET before delete: %v", err)
	}
	getRes.Body.Close()
	if getRes.StatusCode != http.StatusOK {
		t.Fatalf("expected credentials before delete, got %d", getRes.StatusCode)
	}

	// Delete.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/auth/aws-credentials/com", nil)
	delRes, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", delRes.StatusCode)
	}

	// After delete, credentials should be gone.
	getRes2, err := client.Get(ts.URL + "/api/auth/aws-credentials/com")
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	getRes2.Body.Close()
	if getRes2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getRes2.StatusCode)
	}
}

// ── GET /api/auth/session-status ─────────────────────────────────────────────

func TestSessionStatus_Unauthenticated(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/auth/session-status")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, res, &body)
	if body["authenticated"] != false {
		t.Errorf("authenticated: want false, got %v", body["authenticated"])
	}
}

func TestSessionStatus_AuthenticatedDevMode(t *testing.T) {
	db := newTestDB(t)
	ts := newDevModeTestServer(t, db)
	client := newTestClient(t)

	postRes, err := client.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	postRes.Body.Close()

	res, err := client.Get(ts.URL + "/api/auth/session-status")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body map[string]any
	decodeJSON(t, res, &body)
	if body["authenticated"] != true {
		t.Errorf("authenticated: want true, got %v", body["authenticated"])
	}
	env, _ := body["environment"].(string)
	if !strings.EqualFold(env, "com") {
		t.Errorf("environment: want com, got %q", env)
	}
}
