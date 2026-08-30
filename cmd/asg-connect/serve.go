package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/outputsafety"
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
	startAgentRegistration(*cfg)
	rep := newReporter(cfg.HubURL, cfg.TenantKey, cfg.EventSpoolPath, cfg.TenantName, cfg.AgentID)
	p := &llmProxy{cfg: cfg, rep: rep}

	// Initialize semantic scanner (LLM-powered output analysis)
	if len(cfg.Providers) > 0 {
		prov := cfg.Providers[0]
		outputsafety.InitSemantic(prov.BaseURL, prov.APIKey, 100)
		log.Printf("[outputsafety] semantic scanner initialized")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", p.handleLLM)
	mux.HandleFunc("/v1/messages", p.handleAnthropicBridge) // anthropic→openai bridge
	mux.HandleFunc("/v1/responses", p.handleResponses)      // OpenAI Responses API (Codex)
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
	body, err := copyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	prov, upstreamModel, routeErr := p.route(body)
	if routeErr == nil {
		setObservedProvider(prov.Name)
		if upstreamModel != "" {
			observedModel.Store(upstreamModel)
		}
	}
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
		// Honor client-supplied conversation/session hints from body
		// (claude-code and opencode both pass one), else fall back to a
		// per-probe-process bucket so concurrent tasks on one agent still
		// share one session while multiple agents stay separate.
		var hint struct {
			ConversationID string `json:"conversation_id"`
			SessionID      string `json:"session_id"`
			ThreadID       string `json:"thread_id"`
		}
		_ = jsonUnmarshal(body, &hint)
		switch {
		case hint.ConversationID != "":
			sessionID = hint.ConversationID
		case hint.SessionID != "":
			sessionID = hint.SessionID
		case hint.ThreadID != "":
			sessionID = hint.ThreadID
		default:
			sessionID = fmt.Sprintf("probe-%s-%d", prov.Name, os.Getpid())
		}
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

	// Semantic output safety scan (O1): scan LLM response for harmful content.
	// Only for non-streaming; streaming responses are scanned post-hoc by the Intelligence plane.
	if resp.StatusCode == 200 {
		go func() {
			defer func() { recover() }()
			// Extract text content from OpenAI-format response
			var cc struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if json.Unmarshal(respBody, &cc) == nil && len(cc.Choices) > 0 {
				outputText := cc.Choices[0].Message.Content
				if outputText != "" {
					// Extract user task from request
					var reqObj struct {
						Messages []struct {
							Role    string `json:"role"`
							Content any    `json:"content"`
						} `json:"messages"`
					}
					json.Unmarshal(body, &reqObj)
					userTask := ""
					for _, m := range reqObj.Messages {
						if m.Role == "user" {
							if s, ok := m.Content.(string); ok {
								userTask = s
							}
							break
						}
					}
					sr := outputsafety.ScanSemantic(outputText, userTask, "http://127.0.0.1:8902")
					if sr.Suspicious {
						log.Printf("[outputsafety] SEMANTIC %s: %s", sr.FinalVerdict, sr.Detail)
						p.rep.ReportTool(sessionID, "semantic_scan",
							[]byte(fmt.Sprintf(`{"verdict":"%s","detail":"%s"}`, sr.FinalVerdict, sr.Detail)),
							sr.FinalVerdict, sr.Detail)
					}
				}
			}
		}()
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// route picks the provider by model name. Empty model falls back to the
// first provider's default; otherwise the requested model is passed through
// verbatim so the console shows the real model the harness chose.
func (p *llmProxy) route(body []byte) (*Provider, string, error) {
	var req struct {
		Model string `json:"model"`
	}
	model := ""
	_ = jsonUnmarshal(body, &req)
	model = req.Model

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
	// No map hit: pass through to the FIRST provider that can host the
	// requested model. We deliberately do NOT silently rewrite the model
	// name — if no provider can serve it, return a clear error so the
	// caller sees the misconfiguration instead of a confusing 401/404.
	if model != "" {
		for i := range p.cfg.Providers {
			prov := &p.cfg.Providers[i]
			// Heuristic: provider can host a model when its name appears
			// in the provider name or when no allowed_models list is set
			// (explicit allowlists are honored elsewhere; empty means
			// open).
			if len(prov.AllowedModels) == 0 {
				return prov, model, nil
			}
			for _, am := range prov.AllowedModels {
				if strings.EqualFold(am, model) {
					return prov, model, nil
				}
			}
		}
		return nil, "", fmt.Errorf("no provider can serve model %q", model)
	}
	return &Provider{Name: "none"}, model, fmt.Errorf("no providers configured")
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
