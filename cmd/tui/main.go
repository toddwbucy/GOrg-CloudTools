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
	// Redirect slog to a file — bubbletea takes over stdout/stderr.
	// Any log output written to the terminal corrupts the TUI rendering.
	logPath := filepath.Join(os.TempDir(), "cloudtools-tui.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})))
		defer logFile.Close()
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database error: %v\n", err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(database); err != nil {
		fmt.Fprintf(os.Stderr, "migration error: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}
