package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// uiAuth guards the operator console with a single admin password (from env
// ASG_UI_PASSWORD; default "admin" for local dev). Sessions are random tokens
// held in memory; API endpoints stay open for localhost but require the token
// cookie from remote addresses.
type uiAuth struct {
	mu       sync.Mutex
	password string
	sessions map[string]time.Time
}

func newUIAuth() *uiAuth {
	pw := osGetenv("ASG_UI_PASSWORD")
	if pw == "" {
		pw = "admin"
	}
	return &uiAuth{password: pw, sessions: map[string]time.Time{}}
}

func osGetenv(k string) string { return os.Getenv(k) }

func (a *uiAuth) login(pw string) (string, bool) {
	if pw != a.password {
		return "", false
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	a.mu.Lock()
	a.sessions[tok] = time.Now().Add(24 * time.Hour)
	a.mu.Unlock()
	return tok, true
}

func (a *uiAuth) valid(tok string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, tok)
		return false
	}
	return true
}

// middleware wraps console handlers: token cookie or localhost bypass.
func (a *uiAuth) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		if host == "127.0.0.1" || host == "localhost" {
			next(w, r)
			return
		}
		ck, err := r.Cookie("asg_session")
		if err != nil || !a.valid(ck.Value) {
			http.Error(w, "unauthorized — login at /login", 401)
			return
		}
		next(w, r)
	}
}

func (s *Server) uiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST {password}", 405)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tok, ok := s.Auth.login(req.Password)
	if !ok {
		http.Error(w, "wrong password", 401)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "asg_session", Value: tok,
		Path: "/", HttpOnly: true, MaxAge: 86400})
	writeJSON(w, map[string]bool{"ok": true})
}
