// Package egress is the last-line-of-defense network gate: an HTTP/HTTPS
// forward proxy that scans every outbound request from this machine —
// regardless of which agent, tool or process originated it. Domains not on
// the allowlist are blocked; payloads carrying credentials are redacted in
// logs (and blocked in strict mode).
package egress

import (
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	// AllowedDomains is a suffix allowlist ("corp.com" matches api.corp.com).
	// Empty list = allow all (monitor-only mode).
	AllowedDomains []string `yaml:"allowed_domains"`
	// Strict blocks credential-bearing requests; monitor mode only logs them.
	Strict bool `yaml:"strict"`
	Listen string `yaml:"listen"`
}

type Proxy struct {
	cfg      Config
	mu       sync.RWMutex
	client   *http.Client
	onEvent  func(kind, detail string)
	secretRe *regexp.Regexp
}

func New(cfg Config, onEvent func(kind, detail string)) *Proxy {
	return &Proxy{
		cfg:     cfg,
		client:  &http.Client{Timeout: 5 * time.Minute},
		onEvent: onEvent,
		secretRe: regexp.MustCompile(
			`sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{30,}|Bearer [A-Za-z0-9._\-]{25,}`),
	}
}

func (p *Proxy) domainAllowed(host string) bool {
	if len(p.cfg.AllowedDomains) == 0 {
		return true // monitor-only
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range p.cfg.AllowedDomains {
		d = strings.ToLower(strings.TrimSuffix(d, "."))
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// ServeHTTP handles absolute-form requests from HTTP clients and CONNECT.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net_SplitHostPort(host); err == nil {
		host = h
	}
	if !p.domainAllowed(host) {
		p.emit("egress_block", host+" "+r.URL.Path)
		http.Error(w, "blocked by ASG egress policy: "+host, http.StatusForbidden)
		return
	}

	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}

	// Plain HTTP proxying: scan body for credentials.
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
	}
	if len(body) > 0 && p.secretRe.Match(body) {
		p.emit("egress_secret", host+" payload carries credential-like token")
		if p.cfg.Strict {
			http.Error(w, "blocked: credential material in outbound payload", http.StatusForbidden)
			return
		}
	}

	resp, err := p.client.Do(r)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	p.copyHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleConnect tunnels TLS after a hostname-level policy check. Payloads of
// tunneled TLS are not inspected (would require CA interception — roadmap).
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT unsupported", 500)
		return
	}
	resp := &http.Response{StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1}
	_ = resp.Write(w) // 200 Connection Established

	serverConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer serverConn.Close()

	upstream := dialTCP(r.Host)
	if upstream == nil {
		return
	}
	defer upstream.Close()

	p.emit("egress_connect", r.Host)

	go io.Copy(upstream, serverConn)
	io.Copy(serverConn, upstream)
}

func (p *Proxy) copyHeaders(w http.ResponseWriter, h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

func (p *Proxy) emit(kind, detail string) {
	if p.onEvent != nil {
		p.onEvent(kind, sanitize(detail, p.secretRe))
	} else {
		log.Printf("[egress] %s %s", kind, sanitize(detail, p.secretRe))
	}
}

func sanitize(s string, re *regexp.Regexp) string {
	return re.ReplaceAllString(s, "***")
}
