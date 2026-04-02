package api_test

import (
	"net/http"
	"testing"
)

func TestHealth_ReturnsOK(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	var body map[string]string
	decodeJSON(t, res, &body)
	if body["status"] != "healthy" {
		t.Errorf("status: want healthy, got %q", body["status"])
	}
}

func TestAPIHealth_ReturnsOK(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	decodeJSON(t, res, &body)
	if body.Status != "healthy" {
		t.Errorf("status: want healthy, got %q", body.Status)
	}
}

func TestAPIHealth_IncludesDatabaseStatus(t *testing.T) {
	db := newTestDB(t)
	ts := newTestServer(t, db)

	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}

	var body struct {
		Services map[string]struct {
			Status string `json:"status"`
		} `json:"services"`
	}
	decodeJSON(t, res, &body)

	dbSvc, ok := body.Services["database"]
	if !ok {
		t.Fatal("expected 'database' key in services")
	}
	if dbSvc.Status != "healthy" {
		t.Errorf("database service status: want healthy, got %q", dbSvc.Status)
	}
}
