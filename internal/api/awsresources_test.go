package api_test

import (
	"net/http"
	"testing"
)

// These tests cover validation and auth-guard paths only. The AWS-calling paths
// (ec2.ListRunning, vpc.Describe, runner.DryRun) require live AWS and are out
// of scope for unit tests.

// ── GET /api/aws/instances ────────────────────────────────────────────────────

func TestListInstances_MissingAccountID(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/aws/instances?region=us-east-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestListInstances_MissingRegion(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/aws/instances?account_id=123456789012")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestListInstances_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	// Params valid, no session credentials.
	res, err := http.Get(ts.URL + "/api/aws/instances?account_id=123456789012&region=us-east-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

// ── GET /api/aws/vpcs ─────────────────────────────────────────────────────────

func TestDescribeVPCs_MissingParams(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	cases := []string{
		"/api/aws/vpcs?region=us-east-1",           // missing account_id
		"/api/aws/vpcs?account_id=123456789012",    // missing region
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

func TestDescribeVPCs_NoCredentials_ReturnsUnauthorized(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/aws/vpcs?account_id=123456789012&region=us-east-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", res.StatusCode)
	}
}

// ── GET /api/aws/org/accounts ─────────────────────────────────────────────────

func TestOrgAccounts_InvalidEnv(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/aws/org/accounts?env=bad")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", res.StatusCode)
	}
}

func TestOrgAccounts_NoRunner_ReturnsServiceUnavailable(t *testing.T) {
	db := newTestDB(t)
	// newTestServer passes nil for orgRunners.
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/aws/org/accounts?env=com")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", res.StatusCode)
	}
}
