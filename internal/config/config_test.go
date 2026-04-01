package config

import (
	"strings"
	"testing"
)

// ── Load() validation ─────────────────────────────────────────────────────────

func TestLoad_DevelopmentDefaults(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("SECRET_KEY", "")
	t.Setenv("MAX_CONCURRENT_EXECUTIONS", "")
	t.Setenv("EXECUTION_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error for valid development config: %v", err)
	}
	if cfg.MaxConcurrentExecutions != 5 {
		t.Errorf("expected MaxConcurrentExecutions=5, got %d", cfg.MaxConcurrentExecutions)
	}
	if cfg.ExecutionTimeoutSecs != 1800 {
		t.Errorf("expected ExecutionTimeoutSecs=1800, got %d", cfg.ExecutionTimeoutSecs)
	}
	if cfg.SecretKey == "" {
		t.Error("expected ephemeral SecretKey to be generated, got empty string")
	}
	if cfg.Environment != "development" {
		t.Errorf("expected Environment=development, got %q", cfg.Environment)
	}
}

func TestLoad_EnvironmentValueIsTrimmed(t *testing.T) {
	t.Setenv("ENVIRONMENT", "  production  ")
	t.Setenv("SECRET_KEY", "this-is-a-32-char-secret-key!!!!")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Environment != "production" {
		t.Errorf("expected trimmed Environment=production, got %q", cfg.Environment)
	}
}

func TestLoad_InvalidMaxConcurrentExecutionsReturnsError(t *testing.T) {
	t.Setenv("MAX_CONCURRENT_EXECUTIONS", "0")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for MAX_CONCURRENT_EXECUTIONS=0, got nil")
	}
	if !strings.Contains(err.Error(), "MAX_CONCURRENT_EXECUTIONS") {
		t.Errorf("error should mention MAX_CONCURRENT_EXECUTIONS, got: %v", err)
	}
}

func TestLoad_NegativeMaxConcurrentExecutionsReturnsError(t *testing.T) {
	t.Setenv("MAX_CONCURRENT_EXECUTIONS", "-1")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for negative MAX_CONCURRENT_EXECUTIONS, got nil")
	}
}

func TestLoad_InvalidExecutionTimeoutReturnsError(t *testing.T) {
	t.Setenv("EXECUTION_TIMEOUT", "0")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for EXECUTION_TIMEOUT=0, got nil")
	}
	if !strings.Contains(err.Error(), "EXECUTION_TIMEOUT") {
		t.Errorf("error should mention EXECUTION_TIMEOUT, got: %v", err)
	}
}

func TestLoad_ProductionRequiresSecretKey(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SECRET_KEY", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for production without SECRET_KEY, got nil")
	}
}

func TestLoad_ProductionRequires32CharSecretKey(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SECRET_KEY", "tooshort")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short SECRET_KEY in production, got nil")
	}
}

func TestLoad_ProductionAcceptsAtLeast32CharSecretKey(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SECRET_KEY", "exactly-32-chars-long-secret-key")
	_, err := Load()
	if err != nil {
		t.Fatalf("expected 32-char SECRET_KEY to succeed, got: %v", err)
	}
}

func TestLoad_ProductionAcceptsLongerThan32CharSecretKey(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SECRET_KEY", "this-is-longer-than-32-characters-secret-key-value")
	_, err := Load()
	if err != nil {
		t.Fatalf("expected SECRET_KEY longer than 32 chars to succeed, got: %v", err)
	}
}

// ── getCORSOrigins ────────────────────────────────────────────────────────────

func TestGetCORSOrigins_DevDefaultsWhenUnset(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "")
	origins := getCORSOrigins("development")
	if len(origins) == 0 {
		t.Fatal("expected localhost defaults for development, got empty slice")
	}
	found := false
	for _, o := range origins {
		if strings.Contains(o, "localhost") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one localhost origin in dev defaults, got %v", origins)
	}
}

func TestGetCORSOrigins_ProductionEmptyWhenUnset(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "")
	origins := getCORSOrigins("production")
	if len(origins) != 0 {
		t.Errorf("expected empty origins for production with no CORS_ORIGINS set, got %v", origins)
	}
}

func TestGetCORSOrigins_JSONArray(t *testing.T) {
	t.Setenv("CORS_ORIGINS", `["https://app.example.com","https://admin.example.com"]`)
	origins := getCORSOrigins("production")
	if len(origins) != 2 {
		t.Fatalf("expected 2 origins from JSON array, got %d: %v", len(origins), origins)
	}
	if origins[0] != "https://app.example.com" {
		t.Errorf("unexpected first origin: %q", origins[0])
	}
}

func TestGetCORSOrigins_CommaSeparated(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "https://app.example.com, https://admin.example.com")
	origins := getCORSOrigins("production")
	if len(origins) != 2 {
		t.Fatalf("expected 2 origins from comma-separated value, got %d: %v", len(origins), origins)
	}
	if origins[1] != "https://admin.example.com" {
		t.Errorf("unexpected second origin: %q", origins[1])
	}
}

func TestGetCORSOrigins_EmptyEnvTreatedAsDevDefaults(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "")
	// An empty ENVIRONMENT string should behave like development.
	origins := getCORSOrigins("")
	if len(origins) == 0 {
		t.Fatal("expected localhost defaults for empty environment string, got empty slice")
	}
}
