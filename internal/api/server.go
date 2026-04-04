// Package api implements the HTTP server and all REST API handlers.
//
// Route registration uses Go 1.22's enhanced ServeMux patterns:
//
//	"POST /api/exec/script"         — method + path
//	"GET  /api/exec/jobs/{id}"      — path variable
//
// Middleware chain (outermost last): CORS → Session → mux.
// Rate limiters are applied per-route group via RateLimiter.Wrap().
package api

import (
	"net/http"
	"strings"

	"github.com/toddwbucy/GOrg-CloudTools/internal/api/middleware"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"github.com/toddwbucy/GOrg-CloudTools/internal/exec"
	"gorm.io/gorm"
)

// Server is the root HTTP handler. It owns all shared resources and registers
// routes on construction.
type Server struct {
	cfg        *config.Config
	db         *gorm.DB
	mux        *http.ServeMux
	ses        *middleware.SessionConfig
	orgRunners map[string]*exec.OrgRunner // keyed by env ("com", "gov"); nil entry → 503
}

// NewServer builds the fully wired HTTP handler.
// orgRunners maps each AWS environment to its OrgRunner; a missing or nil entry
// causes org-scoped endpoints for that environment to return 503.
func NewServer(cfg *config.Config, db *gorm.DB, orgRunners map[string]*exec.OrgRunner) http.Handler {
	s := &Server{
		cfg:        cfg,
		db:         db,
		mux:        http.NewServeMux(),
		ses:        middleware.NewSessionConfig(cfg.SecretKey, cfg.SessionLifetimeMinutes, strings.EqualFold(cfg.Environment, "production")),
		orgRunners: orgRunners,
	}
	s.registerRoutes()

	var h http.Handler = s.mux
	h = middleware.SessionMiddleware(s.ses)(h)
	h = middleware.CORS(cfg.CORSOrigins)(h)
	return h
}

func (s *Server) registerRoutes() {
	authRL  := middleware.NewRateLimiter(s.cfg.RateLimitAuth)
	readRL  := middleware.NewRateLimiter(s.cfg.RateLimitRead)
	execRL  := middleware.NewRateLimiter(s.cfg.RateLimitExecution)
	writeRL := middleware.NewRateLimiter(s.cfg.RateLimitWrite)

	// ── Health ────────────────────────────────────────────────────────────────
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/health", s.handleAPIHealth)

	// ── Auth ──────────────────────────────────────────────────────────────────
	s.mux.Handle("POST /api/auth/aws-credentials",
		authRL.Wrap(http.HandlerFunc(s.handleCreateCredentials)))
	s.mux.Handle("GET /api/auth/aws-credentials/{environment}",
		authRL.Wrap(http.HandlerFunc(s.handleGetCredentials)))
	s.mux.Handle("DELETE /api/auth/aws-credentials/{environment}",
		authRL.Wrap(http.HandlerFunc(s.handleDeleteCredentials)))
	s.mux.HandleFunc("GET /api/auth/session-status", s.handleSessionStatus)
	s.mux.Handle("GET /api/auth/aws-check-credentials",
		readRL.Wrap(http.HandlerFunc(s.handleCheckServerCredentials)))

	// ── Script execution primitives ───────────────────────────────────────────
	s.mux.Handle("POST /api/exec/script",
		execRL.Wrap(http.HandlerFunc(s.handleExecScript)))
	s.mux.Handle("POST /api/exec/org-script",
		execRL.Wrap(http.HandlerFunc(s.handleExecOrgScript)))
	s.mux.Handle("GET /api/exec/jobs/{id}",
		readRL.Wrap(http.HandlerFunc(s.handleGetJob)))
	s.mux.Handle("POST /api/exec/jobs/{id}/resume",
		execRL.Wrap(http.HandlerFunc(s.handleResumeJob)))
	// Universal SSM command status primitive — used by every SSM-based workflow.
	s.mux.Handle("GET /api/aws/ssm/commands/{command_id}/status",
		readRL.Wrap(http.HandlerFunc(s.handleGetCommandStatus)))

	// ── AWS resource queries ──────────────────────────────────────────────────
	s.mux.Handle("GET /api/aws/instances",
		readRL.Wrap(http.HandlerFunc(s.handleListInstances)))
	s.mux.Handle("GET /api/aws/vpcs",
		readRL.Wrap(http.HandlerFunc(s.handleDescribeVPCs)))
	s.mux.Handle("GET /api/aws/org/accounts",
		readRL.Wrap(http.HandlerFunc(s.handleOrgAccounts)))

	// ── Execution sessions ────────────────────────────────────────────────────
	s.mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	s.mux.Handle("GET /api/sessions/{$}",
		readRL.Wrap(http.HandlerFunc(s.handleListSessions)))
	s.mux.Handle("GET /api/sessions/{id}",
		readRL.Wrap(http.HandlerFunc(s.handleGetSession)))
	s.mux.HandleFunc("PATCH /api/sessions/{id}/status", s.handleUpdateSessionStatus)

	// ── Script library ────────────────────────────────────────────────────────
	s.mux.Handle("GET /api/scripts/{$}",
		readRL.Wrap(http.HandlerFunc(s.handleListScripts)))
	s.mux.Handle("GET /api/scripts/{id}",
		readRL.Wrap(http.HandlerFunc(s.handleGetScript)))
	s.mux.HandleFunc("POST /api/scripts/{$}", s.handleCreateScript)
	s.mux.HandleFunc("PATCH /api/scripts/{id}", s.handleUpdateScript)
	s.mux.HandleFunc("DELETE /api/scripts/{id}", s.handleDeleteScript)

	// ── Change management ─────────────────────────────────────────────────────
	s.mux.Handle("GET /api/changes/{$}",
		readRL.Wrap(http.HandlerFunc(s.handleListChanges)))
	s.mux.Handle("GET /api/changes/{id}",
		readRL.Wrap(http.HandlerFunc(s.handleGetChange)))
	s.mux.Handle("POST /api/changes/{$}",
		writeRL.Wrap(http.HandlerFunc(s.handleCreateChange)))
	s.mux.Handle("PATCH /api/changes/{id}",
		writeRL.Wrap(http.HandlerFunc(s.handleUpdateChange)))

	// ── Tool library ──────────────────────────────────────────────────────────
	s.mux.Handle("GET /api/tools/{$}",
		readRL.Wrap(http.HandlerFunc(s.handleListTools)))
	s.mux.Handle("GET /api/tools/{id}",
		readRL.Wrap(http.HandlerFunc(s.handleGetTool)))
	s.mux.Handle("POST /api/exec/tool",
		execRL.Wrap(http.HandlerFunc(s.handleExecuteTool)))

	// ── Static files ──────────────────────────────────────────────────────────
	// STATIC_DIR is configurable so the server works correctly in containers and
	// systemd units where the working directory may differ from the binary location.
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.cfg.StaticDir))))
}
