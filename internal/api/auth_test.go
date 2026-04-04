package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
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
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
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
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
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

// ── credential input validation ───────────────────────────────────────────────

func TestCreateCredentials_InvalidAccessKeyFormat(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []string{
		"AKID",               // too short
		"akiaiosfodnn7exam",  // lowercase — must be uppercase
		"XXXX0000000000000000", // invalid prefix
		"AKIA00000000000000000", // too long (21 chars)
	}
	for _, key := range cases {
		res, err := http.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
			jsonBody(t, map[string]any{
				"access_key_id":     key,
				"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				"environment":       "com",
			}))
		if err != nil {
			t.Fatalf("POST (key=%q): %v", key, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("key %q: expected 400, got %d", key, res.StatusCode)
		}
	}
}

func TestCreateCredentials_XSSInSecretKey(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []string{
		"<script>alert(1)</script>",
		"javascript:alert(1)",
		"data:text/html,<h1>",
	}
	for _, secret := range cases {
		res, err := http.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
			jsonBody(t, map[string]any{
				"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"secret_access_key": secret,
				"environment":       "com",
			}))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("secret %q: expected 400, got %d", secret, res.StatusCode)
		}
	}
}

func TestCreateCredentials_XSSInSessionToken(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Post(ts.URL+"/api/auth/aws-credentials", "application/json",
		jsonBody(t, map[string]any{
			"access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"session_token":     "<iframe src=evil.com>",
			"environment":       "com",
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for XSS in session_token, got %d", res.StatusCode)
	}
}

// ── GET /api/auth/aws-check-credentials ──────────────────────────────────────

func TestCheckServerCredentials_NoServerCreds(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db) // default config has no server-side AWS creds

	res, err := http.Get(ts.URL + "/api/auth/aws-check-credentials")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, res, &body)
	if body["org_execution_enabled"] != false {
		t.Errorf("org_execution_enabled: want false, got %v", body["org_execution_enabled"])
	}
	envs, _ := body["available_environments"].([]any)
	if len(envs) != 0 {
		t.Errorf("available_environments: want empty, got %v", envs)
	}
}

func TestCheckServerCredentials_WithServerCreds(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServerWithConfig(t, db, &config.Config{
		SecretKey:              "test-secret-key-32-bytes-minimum!!",
		Environment:            "development",
		AWSAccessKeyIDCOM:      "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKeyCOM:  "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionLifetimeMinutes: 60,
		RateLimitAuth:          "1000/minute",
		RateLimitExecution:     "1000/minute",
		RateLimitRead:          "1000/minute",
		RateLimitWrite:         "1000/minute",
		StaticDir:              t.TempDir(),
	})

	res, err := http.Get(ts.URL + "/api/auth/aws-check-credentials")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, res, &body)
	if body["org_execution_enabled"] != true {
		t.Errorf("org_execution_enabled: want true, got %v", body["org_execution_enabled"])
	}
	envs, _ := body["available_environments"].([]any)
	if len(envs) != 1 {
		t.Errorf("available_environments: want [com], got %v", envs)
	}
}
