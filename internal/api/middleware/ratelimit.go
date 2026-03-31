package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-IP token-bucket rate limiter backed by golang.org/x/time/rate.
// Visitor state is cleaned up automatically every 5 minutes.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitorState
	limit    rate.Limit
	burst    int
}

type visitorState struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter parses a spec string like "10/minute" or "5/second" and returns
// a RateLimiter. Falls back to 10/minute if the spec is invalid.
func NewRateLimiter(spec string) *RateLimiter {
	lim, burst := parseRateLimitSpec(spec)
	rl := &RateLimiter{
		visitors: make(map[string]*visitorState),
		limit:    lim,
		burst:    burst,
	}
	go rl.cleanup()
	return rl
}

// Wrap wraps an http.Handler with IP-based rate limiting.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(realIP(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded"}`)) //nolint:errcheck
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	v, ok := rl.visitors[ip]
	if !ok {
		v = &visitorState{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.visitors[ip] = v
	}
	v.lastSeen = time.Now()
	return v.limiter.Allow()
}

// cleanup removes visitor entries that have been idle for more than 10 minutes.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 10*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// parseRateLimitSpec parses "N/unit" → rate.Limit and burst size.
func parseRateLimitSpec(spec string) (rate.Limit, int) {
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 {
		return rate.Every(time.Minute / 10), 10
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n <= 0 {
		n = 10
	}
	var period time.Duration
	switch strings.TrimSpace(strings.ToLower(parts[1])) {
	case "second":
		period = time.Second
	case "hour":
		period = time.Hour
	default:
		period = time.Minute
	}
	return rate.Every(period / time.Duration(n)), n
}

// realIP extracts the client's real IP from X-Forwarded-For or RemoteAddr.
func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
