package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
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

// readOnlyPaths are safe for anonymous public viewers (console reads, graphs,
// activity). Everything else — especially anything that mutates — still needs
// the admin session. This powers the "免密只读公网" mode.
var readOnlyPaths = []string{
	"/api/agents", "/api/status", "/api/stats/", "/api/onto/", "/api/kg/",
	"/api/events", "/api/sessions", "/api/trajectory", "/api/judge/findings",
	"/api/monitor/findings", "/api/clusters", "/api/stream",
}

func isReadOnlyPath(p string) bool {
	for _, pre := range readOnlyPaths {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// middleware wraps console handlers. Public (tunneled) requests get read-only
// access for GET on safe paths — enough for a demo viewer to browse the
// console — while any mutation or non-GET requires the admin session. Local
// loopback (no tunnel) stays fully open as before.
func (a *uiAuth) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isLocalOnly(r) {
			// public/remote: static assets (they carry a dot) + read-only GETs
			// are open so the console shell loads and read views work for an
			// anonymous viewer. Data writes and non-GET still need the session.
			isStatic := strings.Contains(r.URL.Path, ".") && !strings.HasPrefix(r.URL.Path, "/api/")
			if r.Method == http.MethodGet && (isStatic || isReadOnlyPath(r.URL.Path)) {
				next(w, r)
				return
			}
			ck, err := r.Cookie("asg_session")
			if err != nil || !a.valid(ck.Value) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				} else {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(401)
					w.Write([]byte(loginPageHTML))
				}
				return
			}
			next(w, r)
			return
		}
		// local-only mode: still allow, but honor explicit public flag
		if os.Getenv("ASG_PUBLIC") == "1" {
			ck, err := r.Cookie("asg_session")
			if err != nil || !a.valid(ck.Value) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(401)
				w.Write([]byte(loginPageHTML))
				return
			}
		}
		next(w, r)
	}
}

// isLocalOnly reports whether this request genuinely originated from the
// loopback interface. It trusts RemoteAddr (set by the network stack), NOT
// the client-controlled Host header. Tunnel forwarding headers force auth.
func (a *uiAuth) isLocalOnly(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false // tunneled/proxied: require auth regardless of apparent host
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
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

const loginPageHTML = `<!DOCTYPE html>
<html lang="zh"><head><meta charset="utf-8"><title>ASG 登录</title>
<style>
body{background:#0d1117;color:#e6edf3;font-family:system-ui,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0}
.box{background:#161b22;border:1px solid #30363d;border-radius:10px;padding:32px;width:320px}
h1{font-size:16px;margin:0 0 16px}
input{width:100%;box-sizing:border-box;background:#0a0e14;border:1px solid #30363d;color:#e6edf3;border-radius:6px;padding:8px 12px;font-size:14px}
button{width:100%;margin-top:12px;background:#238636;border:none;color:#fff;border-radius:6px;padding:8px;font-size:14px;font-weight:600;cursor:pointer}
.err{color:#f85149;font-size:12px;margin-top:8px;min-height:16px}
</style></head><body><div class="box">
<h1>🛡 Agent Security Gateway</h1>
<form onsubmit="return doLogin()">
<input type="password" id="pw" placeholder="管理员密码" autofocus>
<div class="err" id="err"></div>
<button type="submit">登 录</button>
</form></div>
<script>
async function doLogin(){
  const pw=document.getElementById('pw').value;
  const r=await fetch('/api/ui-login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:pw})});
  if(r.ok){location.reload();return false}
  document.getElementById('err').textContent='密码错误';
  return false;
}
document.getElementById('pw').focus();
</script></body></html>`
