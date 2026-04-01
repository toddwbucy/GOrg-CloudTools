package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── crypto primitives ─────────────────────────────────────────────────────────

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	// Use a fixed non-zero key — no need for crypto/rand in a determinism test.
	for i := range key {
		key[i] = byte(i + 1)
	}
	plain := []byte("the quick brown fox")
	ct, err := encrypt(key, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := decrypt(key, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("roundtrip mismatch: want %q, got %q", plain, got)
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ct, err := encrypt(key, []byte("secret payload"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Flip a byte in the body (after the nonce) to simulate tampering.
	ct[len(ct)-1] ^= 0xFF
	_, err = decrypt(key, ct)
	if err == nil {
		t.Fatal("expected decryption error on tampered ciphertext, got nil")
	}
}

func TestDecryptRejectsTooShortInput(t *testing.T) {
	key := make([]byte, 32)
	_, err := decrypt(key, []byte("short"))
	if err == nil {
		t.Fatal("expected error for input shorter than nonce size, got nil")
	}
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	// GCM uses a random nonce so two encryptions of the same plaintext differ.
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xAB
	}
	ct1, _ := encrypt(key, []byte("same"))
	ct2, _ := encrypt(key, []byte("same"))
	if string(ct1) == string(ct2) {
		t.Error("two encryptions of the same plaintext must produce different ciphertexts")
	}
}

// ── full session cookie roundtrip ─────────────────────────────────────────────

func newTestSessionConfig() *SessionConfig {
	return NewSessionConfig("test-secret-key-32-bytes-minimum!!", 60, false)
}

func TestSaveAndGetSession(t *testing.T) {
	cfg := newTestSessionConfig()
	want := &Session{
		AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		AWSSessionToken:    "token123",
		AWSEnvironment:     "com",
		AWSAccountID:       "123456789012",
	}

	// Write the cookie via a ResponseRecorder.
	w := httptest.NewRecorder()
	if err := SaveSession(w, cfg, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Build a request with the cookie the recorder just set.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}

	got := loadSession(req, cfg)
	if got.AWSAccessKeyID != want.AWSAccessKeyID {
		t.Errorf("access key: want %q, got %q", want.AWSAccessKeyID, got.AWSAccessKeyID)
	}
	if got.AWSSecretAccessKey != want.AWSSecretAccessKey {
		t.Errorf("secret key mismatch")
	}
	if got.AWSEnvironment != want.AWSEnvironment {
		t.Errorf("env: want %q, got %q", want.AWSEnvironment, got.AWSEnvironment)
	}
	if got.AWSAccountID != want.AWSAccountID {
		t.Errorf("account id: want %q, got %q", want.AWSAccountID, got.AWSAccountID)
	}
}

func TestLoadSession_MissingCookieReturnsEmpty(t *testing.T) {
	cfg := newTestSessionConfig()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess := loadSession(req, cfg)
	if sess.AWSAccessKeyID != "" {
		t.Errorf("expected empty session, got credentials")
	}
}

func TestLoadSession_TamperedCookieReturnsEmpty(t *testing.T) {
	cfg := newTestSessionConfig()
	w := httptest.NewRecorder()
	_ = SaveSession(w, cfg, &Session{AWSAccessKeyID: "AKIA123", AWSEnvironment: "com"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		// Corrupt the value.
		c.Value = c.Value[:len(c.Value)-4] + "XXXX"
		req.AddCookie(c)
	}

	sess := loadSession(req, cfg)
	if sess.AWSAccessKeyID != "" {
		t.Errorf("expected empty session on tampered cookie, got credentials")
	}
}

func TestLoadSession_ExpiredSessionReturnsEmpty(t *testing.T) {
	// Lifetime of 0 minutes means 0 seconds — any session is immediately expired.
	cfg := NewSessionConfig("test-secret-key-32-bytes-minimum!!", 0, false)

	w := httptest.NewRecorder()
	_ = SaveSession(w, cfg, &Session{AWSAccessKeyID: "AKIA123", AWSEnvironment: "com"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}

	sess := loadSession(req, cfg)
	if sess.AWSAccessKeyID != "" {
		t.Errorf("expected empty session after expiry, got credentials")
	}
}

func TestClearSession_SetsMaxAgeNegative(t *testing.T) {
	cfg := newTestSessionConfig()
	w := httptest.NewRecorder()
	ClearSession(w, cfg)

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a Set-Cookie header from ClearSession")
	}
	c := cookies[0]
	if c.MaxAge >= 0 {
		t.Errorf("expected MaxAge < 0 to delete cookie, got %d", c.MaxAge)
	}
	if !c.Expires.Before(time.Now()) {
		t.Errorf("expected Expires in the past, got %v", c.Expires)
	}
}

func TestSessionMiddleware_InjectsEmptySessionWhenNoCookie(t *testing.T) {
	cfg := newTestSessionConfig()
	var gotSession *Session
	handler := SessionMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = GetSession(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotSession == nil {
		t.Fatal("expected non-nil session from context, got nil")
	}
	if gotSession.AWSAccessKeyID != "" {
		t.Errorf("expected empty session, got populated session")
	}
}

func TestHasAWSCredentials(t *testing.T) {
	tests := []struct {
		name   string
		sess   *Session
		env    string
		expect bool
	}{
		{"nil session", nil, "com", false},
		{"empty session", &Session{}, "com", false},
		{"wrong env", &Session{AWSAccessKeyID: "K", AWSSecretAccessKey: "S", AWSEnvironment: "gov"}, "com", false},
		{"missing key", &Session{AWSEnvironment: "com", AWSSecretAccessKey: "S"}, "com", false},
		{"valid", &Session{AWSAccessKeyID: "K", AWSSecretAccessKey: "S", AWSEnvironment: "com"}, "com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sess.HasAWSCredentials(tt.env); got != tt.expect {
				t.Errorf("HasAWSCredentials(%q) = %v, want %v", tt.env, got, tt.expect)
			}
		})
	}
}
