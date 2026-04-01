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

	// OrgRunners are optional: one per AWS environment that has server-side
	// management credentials configured. Per-account workflows always use
	// session credentials; org-scoped endpoints return 503 for unconfigured envs.
	orgRunners := buildOrgRunners(cfg, database)

	srv := api.NewServer(cfg, database, orgRunners)
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

// buildOrgRunners constructs an OrgRunner for each AWS environment that has
// server-side management credentials configured. Environments without credentials
// are omitted — org-scoped API endpoints return 503 for those envs.
func buildOrgRunners(cfg *config.Config, database *gorm.DB) map[string]*exec.OrgRunner {
	type envCreds struct {
		env, region, keyID, secret, token string
	}
	candidates := []envCreds{
		{"com", "us-east-1", cfg.AWSAccessKeyIDCOM, cfg.AWSSecretAccessKeyCOM, cfg.AWSSessionTokenCOM},
		{"gov", "us-gov-west-1", cfg.AWSAccessKeyIDGOV, cfg.AWSSecretAccessKeyGOV, cfg.AWSSessionTokenGOV},
	}

	runners := make(map[string]*exec.OrgRunner)
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
		runners[c.env] = exec.NewOrgRunner(database, visitor, cfg.ExecutionTimeoutSecs)
		slog.Info("org runner initialised", "env", c.env)
	}

	if len(runners) == 0 {
		slog.Info("no server-side management credentials configured; org execution disabled")
	}
	return runners
}
