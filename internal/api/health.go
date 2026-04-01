package api

import (
	"net/http"
	"runtime"
	"time"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"status": "healthy"})
}

func (s *Server) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	type serviceStatus struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	type healthResp struct {
		Status      string                   `json:"status"`
		Timestamp   time.Time                `json:"timestamp"`
		Version     string                   `json:"version"`
		Environment map[string]any           `json:"environment"`
		Services    map[string]serviceStatus `json:"services"`
	}

	resp := healthResp{
		Status:    "healthy",
		Timestamp: time.Now().UTC(),
		Version:   s.cfg.Version,
		Environment: map[string]any{
			"dev_mode":   s.cfg.DevMode,
			"go_version": runtime.Version(),
		},
		Services: map[string]serviceStatus{
			"rate_limiting": {Status: "enabled", Message: "in-memory per-IP token bucket"},
			"sessions":      {Status: "enabled", Message: "AES-256-GCM encrypted cookie"},
		},
	}

	// Database ping
	sqlDB, err := s.db.DB()
	if err != nil || sqlDB.PingContext(r.Context()) != nil {
		resp.Services["database"] = serviceStatus{Status: "unhealthy", Message: "ping failed"}
		resp.Status = "unhealthy"
	} else {
		resp.Services["database"] = serviceStatus{Status: "healthy"}
	}

	code := http.StatusOK
	if resp.Status == "unhealthy" {
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	jsonOK(w, resp)
}
