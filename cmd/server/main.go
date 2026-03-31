package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/api"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	gorgaws "github.com/toddwbucy/gorg-aws"
	"gorm.io/gorm"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(database); err != nil {
		slog.Error("failed to migrate database", "err", err)
		os.Exit(1)
	}

	// OrgRunner is optional: only available when server-side management credentials
	// are configured. Per-account workflows use session credentials instead.
	orgRunner := buildOrgRunner(cfg, database)

	srv := api.NewServer(cfg, database, orgRunner)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("starting server", "addr", addr, "env", cfg.Environment, "version", cfg.Version)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("stopped")
}

// buildOrgRunner constructs an OrgRunner if server-side management credentials
// are present in the environment. Returns nil if not configured — org-scoped
// API endpoints will return 503 in that case.
func buildOrgRunner(cfg *config.Config, database *gorm.DB) *exec.OrgRunner {
	type envCreds struct {
		env, region, keyID, secret, token string
	}
	candidates := []envCreds{
		{"com", "us-east-1", cfg.AWSAccessKeyIDCOM, cfg.AWSSecretAccessKeyCOM, cfg.AWSSessionTokenCOM},
		{"gov", "us-gov-west-1", cfg.AWSAccessKeyIDGOV, cfg.AWSSecretAccessKeyGOV, cfg.AWSSessionTokenGOV},
	}

	for _, c := range candidates {
		if c.keyID == "" || c.secret == "" {
			continue
		}
		baseCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(c.region),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(c.keyID, c.secret, c.token),
			),
		)
		if err != nil {
			slog.Warn("skipping org runner for env", "env", c.env, "err", err)
			continue
		}
		visitor := gorgaws.New(baseCfg,
			gorgaws.WithConcurrency(20),
			gorgaws.WithLogger(slog.Default()),
		)
		slog.Info("org runner initialised", "env", c.env)
		return exec.NewOrgRunner(database, visitor, cfg.ExecutionTimeoutSecs)
	}

	slog.Info("no server-side management credentials configured; org execution disabled")
	return nil
}
