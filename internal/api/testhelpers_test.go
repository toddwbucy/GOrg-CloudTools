package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newTestDB opens an in-memory SQLite database with all application tables
// migrated. SetMaxOpenConns(1) prevents GORM from opening a second connection
// that would see a separate empty in-memory database.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.Account{},
		&models.Region{},
		&models.Instance{},
		&models.Change{},
		&models.ChangeInstance{},
		&models.Tool{},
		&models.Script{},
		&models.ExecutionSession{},
		&models.ExecutionBatch{},
		&models.Execution{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newTestServer builds a test HTTP server backed by db with default (non-dev-mode) config.
func newTestServer(t *testing.T, db *gorm.DB) *httptest.Server {
	t.Helper()
	return newTestServerWithConfig(t, db, &config.Config{
		SecretKey:               "test-secret-key-32-bytes-minimum!!",
		Environment:             "development",
		SessionLifetimeMinutes:  60,
		RateLimitAuth:           "1000/minute",
		RateLimitExecution:      "1000/minute",
		RateLimitRead:           "1000/minute",
		RateLimitWrite:          "1000/minute",
		StaticDir:               t.TempDir(),
	})
}

// newDevModeTestServer is like newTestServer but sets DevMode: true so that
// POST /api/auth/aws-credentials skips STS validation.
func newDevModeTestServer(t *testing.T, db *gorm.DB) *httptest.Server {
	t.Helper()
	return newTestServerWithConfig(t, db, &config.Config{
		SecretKey:               "test-secret-key-32-bytes-minimum!!",
		Environment:             "development",
		DevMode:                 true,
		SessionLifetimeMinutes:  60,
		RateLimitAuth:           "1000/minute",
		RateLimitExecution:      "1000/minute",
		RateLimitRead:           "1000/minute",
		RateLimitWrite:          "1000/minute",
		StaticDir:               t.TempDir(),
	})
}

func newTestServerWithConfig(t *testing.T, db *gorm.DB, cfg *config.Config) *httptest.Server {
	t.Helper()
	handler := api.NewServer(cfg, db, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// newTestClient returns an http.Client whose cookie jar persists session cookies
// across requests against the same host. Required for auth flows that depend on
// a session cookie set by a previous request.
func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// jsonBody serialises v and returns a *bytes.Buffer for use as an http.Request body.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewBuffer(b)
}

// decodeJSON decodes the HTTP response body into dst and closes the body.
func decodeJSON(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// itoa converts a uint ID to its decimal string for use in URL paths.
func itoa(id uint) string {
	return fmt.Sprintf("%d", id)
}
