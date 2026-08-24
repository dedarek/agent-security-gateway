package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func osOpenAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// serve runs the local proxy: an OpenAI/Anthropic-compatible transparent
// forwarder with full traffic capture, plus a periodic flusher.
func serve(cfgPath string) error {
	cfg, err := loadProbeConfig(cfgPath)
	if err != nil {
		return err
	}
	rep := newReporter(cfg.HubURL, cfg.TenantKey, cfg.EventSpoolPath)
	p := &llmProxy{cfg: cfg, rep: rep}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", p.handleLLM)
	mux.HandleFunc("/v1/messages", p.handleAnthropicBridge) // anthropic→openai bridge
	mux.HandleFunc("/v1/responses", p.handleResponses) // OpenAI Responses API (Codex)
	mux.HandleFunc("/responses", p.handleResponses)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	registerMCPShim(mux, cfg)
	mux.HandleFunc("/api/hook-check", hookHTTPHandler(cfg, rep))

	// Periodic flush + retry so offline work ships when hub returns.
	go func() {
		backoff := 10 * time.Second
		for range time.Tick(backoff) {
			if err := rep.Flush(); err != nil {
				log.Printf("[reporter] flush deferred: %v", err)
			}
		}
	}()

	// Registry sync: admin-curated MCP servers auto-mount to local agents.
	stop := make(chan struct{})
	go syncLoop(cfg, stop)

	log.Printf("[asg-connect] probe listening on %s (hub=%s tenant=%s)",
		cfg.Listen, cfg.HubURL, cfg.TenantName)
	srv := &http.Server{Addr: cfg.Listen, Handler: mux}
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// llmProxy forwards /v1/* to the provider matching the requested model,
// capturing both directions.
type llmProxy struct {
	cfg *ProbeConfig
	rep *reporter
}

func (p *llmProxy) handleLLM(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	prov, upstreamModel, routeErr := p.route(body)
	if routeErr != nil {
		// quota protection: deny non-free models before they hit the provider
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"type":"quota_protection","message":` + jsonQuote(routeErr.Error()) + `}}`))
		return
	}
	start := time.Now()

	// Rewrite the model field to the upstream model id the router chose.
	if upstreamModel != "" {
		var reqObj map[string]any
		if jsonUnmarshal(body, &reqObj) == nil && reqObj["model"] != upstreamModel {
			reqObj["model"] = upstreamModel
			if nb, err := json.Marshal(reqObj); err == nil {
				body = nb
			}
		}
	}

	// Anthropic dialect translation happens BEFORE building the upstream
	// request: the sanitized bytes are what actually goes on the wire.
	if strings.Contains(r.URL.Path, "/messages") {
		body = sanitizeAnthropicForZen(body)
		if p := os.Getenv("ASG_DUMP"); p != "" {
			_ = os.WriteFile(p, body, 0o644)
		}
	}

	upURL := strings.TrimSuffix(prov.BaseURL, "/") + strings.TrimPrefix(r.URL.Path, "")
	// Some clients post to /v1/chat/completions while provider base already
	// includes /v1 — normalize by avoiding double /v1.
	if strings.HasSuffix(prov.BaseURL, "/v1") && strings.HasPrefix(r.URL.Path, "/v1/") {
		upURL = strings.TrimSuffix(prov.BaseURL, "/") + strings.TrimPrefix(r.URL.Path, "/v1")
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upURL, strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+prov.APIKey)
	req.Header.Set("x-api-key", prov.APIKey) // anthropic-style providers
	if strings.Contains(r.URL.Path, "/messages") {
		// Anthropic wire format requires the version header.
		v := r.Header.Get("anthropic-version")
		if v == "" {
			v = "2023-06-01"
		}
		req.Header.Set("anthropic-version", v)
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	sessionID := r.Header.Get("x-asg-session")
	if sessionID == "" {
		sessionID = "probe-" + prov.Name
	}

	isStream := strings.Contains(resp.Header.Get("Content-Type"), "event-stream")

	// Streaming responses: pipe chunks to the agent as they arrive (true
	// pass-through, no buffering latency) while accumulating a full copy for
	// capture. Non-streaming: buffer then forward as before.
	var respBody []byte
	if isStream {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		buf := &bytes.Buffer{}
		chunk := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
				w.Write(chunk[:n])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if err != nil {
				break
			}
		}
		respBody = buf.Bytes()
		p.rep.ReportLLM(sessionID, upstreamModel, body, respBody, time.Since(start).Milliseconds())
		return
	}

	respBody, _ = io.ReadAll(resp.Body)
	if p := os.Getenv("ASG_DUMP_RESP"); p != "" {
		_ = os.WriteFile(p, []byte(fmt.Sprintf("STATUS %d\nURL %s\nBODY %s", resp.StatusCode, upURL, string(respBody))), 0o644)
	}

	p.rep.ReportLLM(sessionID, upstreamModel, body, respBody, time.Since(start).Milliseconds())

	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// route picks the provider by model name.
// allowed_models in config governs what may reach the provider. Names not on
// the list are silently remapped to the provider default — agents never break,
// and the operator fully controls which models may burn quota. An empty
// allowlist means "default only".
func (p *llmProxy) route(body []byte) (*Provider, string, error) {
	allowed := map[string]bool{}
	for _, prov := range p.cfg.Providers {
		for _, m := range prov.AllowedModels {
			allowed[strings.ToLower(m)] = true
		}
	}

	var req struct {
		Model string `json:"model"`
	}
	model := ""
	_ = jsonUnmarshal(body, &req)
	model = req.Model

	// Any model name the agent invents (claude-opus-5, gpt-4o, ...) is
	// silently remapped to this provider's default. The allowlist only
	// governs what may leave the box; agents never break, operators never
	// burn unintended quota.
	if model != "" && !allowed[strings.ToLower(model)] {
		for i := range p.cfg.Providers {
			if p.cfg.Providers[i].DefaultModel != "" {
				model = p.cfg.Providers[i].DefaultModel
				break
			}
		}
	}

	for i := range p.cfg.Providers {
		prov := &p.cfg.Providers[i]
		if prov.ModelMap != nil {
			if up, ok := prov.ModelMap[model]; ok {
				return prov, up, nil
			}
		}
		if model == "" {
			return prov, prov.DefaultModel, nil
		}
	}
	// An explicitly requested model name is passed through verbatim to the
	// first provider — the user chose it, we route it. Empty name falls back
	// to the provider default.
	if len(p.cfg.Providers) > 0 {
		prov := &p.cfg.Providers[0]
		up := model
		if up == "" && prov.DefaultModel != "" {
			up = prov.DefaultModel
		}
		return prov, up, nil
	}
	// fallback: first provider; unknown model names map to its default so
	// agents sending their own defaults (claude-opus-5 etc.) still work.
	if len(p.cfg.Providers) > 0 {
		prov := &p.cfg.Providers[0]
		up := model
		if prov.DefaultModel != "" {
			up = prov.DefaultModel
		}
		return prov, up, nil
	}
	return &Provider{Name: "none"}, model, fmt.Errorf("no providers configured")
}

// matchFamily lets agents send any model alias containing the provider tag.
func matchFamily(model, name string) bool {
	if name == "" {
		return false
	}
	return strings.Contains(strings.ToLower(model), strings.ToLower(name))
}

var _ = context.Background

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
