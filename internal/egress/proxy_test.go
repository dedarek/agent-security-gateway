package egress

import (
	"net/http"
	"net/url"
	"testing"
)

func TestDomainAllowlist(t *testing.T) {
	p := New(Config{AllowedDomains: []string{"corp.com", "api.github.com"}}, nil)
	cases := map[string]bool{
		"api.corp.com":     true,
		"corp.com":         true,
		"evil-corp.com":    false, // suffix spoof must not match
		"github.com":       false,
		"api.github.com":   true,
		"evil.com:8080":    false,
	}
	for host, want := range cases {
		if got := p.domainAllowed(host); got != want {
			t.Errorf("domainAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestMonitorModeAllowsAll(t *testing.T) {
	p := New(Config{}, nil)
	if !p.domainAllowed("anything.example") {
		t.Fatal("empty allowlist = monitor-only, all allowed")
	}
}

func TestProxyBlocksNonWhitelisted(t *testing.T) {
	p := New(Config{AllowedDomains: []string{"corp.com"}}, nil)
	srvURL, _ := url.Parse("http://evil.com/steal")
	r := (&http.Request{Method: "GET", URL: srvURL, Host: "evil.com"})
	w := &recorder{}
	p.ServeHTTP(w, r)
	if w.code != http.StatusForbidden {
		t.Fatalf("non-whitelisted domain must 403, got %d", w.code)
	}
}

type recorder struct {
	header http.Header
	code   int
	body   string
}

func (r *recorder) Header() http.Header { return http.Header{} }
func (r *recorder) Write(b []byte) (int, error) { r.body += string(b); return len(b), nil }
func (r *recorder) WriteHeader(code int) { r.code = code }
