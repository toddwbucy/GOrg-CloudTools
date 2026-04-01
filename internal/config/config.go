package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration, loaded from environment variables.
type Config struct {
	AppName     string
	Version     string
	Environment string
	Debug       bool
	DevMode     bool

	SecretKey string

	DatabaseURL string

	Host        string
	Port        int
	StaticDir   string
	CORSOrigins []string

	AWSDefaultRegion      string
	AWSAccessKeyIDCOM     string
	AWSSecretAccessKeyCOM string
	AWSSessionTokenCOM    string
	AWSAccessKeyIDGOV     string
	AWSSecretAccessKeyGOV string
	AWSSessionTokenGOV    string

	SessionLifetimeMinutes int
	SessionWarningMinutes  int

	MaxConcurrentExecutions int
	ExecutionTimeoutSecs    int

	// Rate limit specs, e.g. "10/minute"
	RateLimitAuth      string
	RateLimitExecution string
	RateLimitRead      string
}

// Load reads configuration from environment variables.
// In development, a random SECRET_KEY is generated if one is not provided.
// In production, SECRET_KEY must be set and at least 32 characters.
func Load() (*Config, error) {
	cfg := &Config{
		AppName:                 getEnv("APP_NAME", "CloudOpsTools"),
		Version:                 getEnv("VERSION", "2.0.0"),
		Environment:             getEnv("ENVIRONMENT", "development"),
		Debug:                   getBoolEnv("DEBUG", false),
		DevMode:                 getBoolEnv("DEV_MODE", false),
		SecretKey:               strings.TrimSpace(os.Getenv("SECRET_KEY")),
		DatabaseURL:             getEnv("DATABASE_URL", "./data/cloudopstools.db"),
		Host:                    getEnv("HOST", "0.0.0.0"),
		Port:                    getIntEnv("PORT", 8500),
		StaticDir:               getEnv("STATIC_DIR", "./static"),
		CORSOrigins:             getCORSOrigins(),
		AWSDefaultRegion:        getEnv("AWS_DEFAULT_REGION", "us-east-1"),
		AWSAccessKeyIDCOM:       os.Getenv("AWS_ACCESS_KEY_ID_COM"),
		AWSSecretAccessKeyCOM:   os.Getenv("AWS_SECRET_ACCESS_KEY_COM"),
		AWSSessionTokenCOM:      os.Getenv("AWS_SESSION_TOKEN_COM"),
		AWSAccessKeyIDGOV:       os.Getenv("AWS_ACCESS_KEY_ID_GOV"),
		AWSSecretAccessKeyGOV:   os.Getenv("AWS_SECRET_ACCESS_KEY_GOV"),
		AWSSessionTokenGOV:      os.Getenv("AWS_SESSION_TOKEN_GOV"),
		SessionLifetimeMinutes:  getIntEnv("SESSION_LIFETIME_MINUTES", 60),
		SessionWarningMinutes:   getIntEnv("SESSION_WARNING_MINUTES", 45),
		MaxConcurrentExecutions: getIntEnv("MAX_CONCURRENT_EXECUTIONS", 5),
		ExecutionTimeoutSecs:    getIntEnv("EXECUTION_TIMEOUT", 1800),
		RateLimitAuth:           getEnv("RATE_LIMIT_AUTH_ENDPOINTS", "10/minute"),
		RateLimitExecution:      getEnv("RATE_LIMIT_EXECUTION_ENDPOINTS", "5/minute"),
		RateLimitRead:           getEnv("RATE_LIMIT_READ_ENDPOINTS", "100/minute"),
	}

	isProd := strings.EqualFold(cfg.Environment, "production")

	switch {
	case isProd && cfg.SecretKey == "":
		return nil, fmt.Errorf("SECRET_KEY must be set in production")
	case isProd && len(cfg.SecretKey) < 32:
		return nil, fmt.Errorf("SECRET_KEY must be at least 32 characters in production (got %d)", len(cfg.SecretKey))
	case cfg.SecretKey == "":
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("generating ephemeral key: %w", err)
		}
		cfg.SecretKey = hex.EncodeToString(b)
		slog.Warn("no SECRET_KEY set; generated ephemeral key — sessions will not survive restarts")
	}

	return cfg, nil
}

// AvailableEnvs returns which AWS environments have server-side credentials configured.
func (c *Config) AvailableEnvs() []string {
	var envs []string
	if c.AWSAccessKeyIDCOM != "" && c.AWSSecretAccessKeyCOM != "" {
		envs = append(envs, "com")
	}
	if c.AWSAccessKeyIDGOV != "" && c.AWSSecretAccessKeyGOV != "" {
		envs = append(envs, "gov")
	}
	return envs
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getIntEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getCORSOrigins parses CORS_ORIGINS which may be a JSON array or comma-separated list.
func getCORSOrigins() []string {
	v := strings.TrimSpace(os.Getenv("CORS_ORIGINS"))
	if v == "" {
		return []string{"http://localhost:8500", "http://localhost:3000"}
	}
	// Try JSON array first (e.g. '["https://app.example.com","https://admin.example.com"]').
	if strings.HasPrefix(v, "[") {
		var origins []string
		if err := json.Unmarshal([]byte(v), &origins); err == nil {
			return origins
		}
	}
	// Fall back to comma-separated list.
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
