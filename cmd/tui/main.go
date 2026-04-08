// Command cloudtools is the terminal user interface for GOrg CloudTools.
// It runs directly on a bastion server. Authentication is handled by the
// bastion access stack (VPN → SFT → SSH → system user); the TUI adds
// per-cloud-environment credential management on top.
//
// No HTTP server or listening port is started. All data is read from and
// written to the local SQLite database (shared with cloudtools-server if
// both are deployed).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"github.com/toddwbucy/GOrg-CloudTools/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Redirect slog to a file — bubbletea takes over stdout/stderr.
	// Any log output written to the terminal corrupts the TUI rendering.
	// If the file cannot be opened, send logs to io.Discard rather than
	// leaving slog's default handler writing to stderr.
	logPath := filepath.Join(os.TempDir(), "cloudtools-tui.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})))
		defer logFile.Close()
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}
	defer func() {
		if cerr := db.Close(database); cerr != nil {
			slog.Warn("db close failed", "err", cerr)
		}
	}()

	if err := db.AutoMigrate(database); err != nil {
		return fmt.Errorf("migration error: %w", err)
	}

	// Mark any jobs left in-flight by a previous run as interrupted.
	if n, err := exec.RecoverOrphanedJobs(context.Background(), database); err != nil {
		slog.Warn("startup recovery failed", "err", err)
	} else if n > 0 {
		slog.Info("marked orphaned jobs as interrupted on startup",
			"count", n,
			"action", "resume from Job History screen")
	}

	model := tui.New(cfg, database)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui error: %w", err)
	}
	return nil
}
