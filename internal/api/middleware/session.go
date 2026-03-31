// Package middleware provides HTTP middleware for the CloudOpsTools API server.
// Session management uses AES-256-GCM encrypted cookies — no Redis required.
package middleware

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type contextKey string

const sessionContextKey contextKey = "session"

// Session holds the data stored in the encrypted session cookie.
// Short JSON keys reduce cookie size.
type Session struct {
	AWSAccessKeyID     string    `json:"aki,omitempty"`
	AWSSecretAccessKey string    `json:"sak,omitempty"`
	AWSSessionToken    string    `json:"st,omitempty"`
	AWSEnvironment     string    `json:"env,omitempty"` // "com" or "gov"
	CreatedAt          time.Time `json:"ca"`
}

// HasAWSCredentials reports whether the session has credentials for the given environment.
func (s *Session) HasAWSCredentials(env string) bool {
	return s != nil && s.AWSEnvironment == env && s.AWSAccessKeyID != "" && s.AWSSecretAccessKey != ""
}

// SessionConfig holds the derived key and cookie parameters.
type SessionConfig struct {
	key             []byte
	lifetimeSeconds int
	secure          bool
}

// NewSessionConfig derives a 32-byte AES key from secretKey via SHA-256.
func NewSessionConfig(secretKey string, lifetimeMinutes int, secure bool) *SessionConfig {
	h := sha256.Sum256([]byte(secretKey))
	return &SessionConfig{
		key:             h[:],
		lifetimeSeconds: lifetimeMinutes * 60,
		secure:          secure,
	}
}

// SessionMiddleware decrypts the session cookie and injects a *Session into
// the request context. An empty *Session is always present after this runs.
func SessionMiddleware(cfg *SessionConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := loadSession(r, cfg)
			ctx := context.WithValue(r.Context(), sessionContextKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func loadSession(r *http.Request, cfg *SessionConfig) *Session {
	cookie, err := r.Cookie("cloudopstools_session")
	if err != nil {
		return &Session{}
	}
	data, err := base64.URLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return &Session{}
	}
	plain, err := decrypt(cfg.key, data)
	if err != nil {
		return &Session{}
	}
	var sess Session
	if err := json.Unmarshal(plain, &sess); err != nil {
		return &Session{}
	}
	if time.Since(sess.CreatedAt) > time.Duration(cfg.lifetimeSeconds)*time.Second {
		return &Session{}
	}
	return &sess
}

// GetSession retrieves the *Session from the request context.
// Always returns a non-nil pointer; the session may be empty.
func GetSession(r *http.Request) *Session {
	if sess, ok := r.Context().Value(sessionContextKey).(*Session); ok {
		return sess
	}
	return &Session{}
}

// SaveSession encrypts sess and writes it as an HttpOnly cookie on w.
func SaveSession(w http.ResponseWriter, cfg *SessionConfig, sess *Session) error {
	sess.CreatedAt = time.Now()
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	encrypted, err := encrypt(cfg.key, data)
	if err != nil {
		return err
	}
	sameSite := http.SameSiteLaxMode
	if cfg.secure {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cloudopstools_session",
		Value:    base64.URLEncoding.EncodeToString(encrypted),
		MaxAge:   cfg.lifetimeSeconds,
		HttpOnly: true,
		Secure:   cfg.secure,
		SameSite: sameSite,
		Path:     "/",
	})
	return nil
}

// ClearSession deletes the session cookie.
func ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    "cloudopstools_session",
		Value:   "",
		MaxAge:  -1,
		Path:    "/",
		Expires: time.Unix(0, 0),
	})
}

// ── AES-256-GCM helpers ───────────────────────────────────────────────────────

func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Seal appends ciphertext+tag to nonce, so the result is [nonce || ciphertext+tag].
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, io.ErrUnexpectedEOF
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}
