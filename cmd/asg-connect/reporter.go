package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// reporter batches probe events and ships them to the central hub. Events are
// also spooled to disk so nothing is lost when the hub is unreachable.
type reporter struct {
	hubURL string
	key    string
	client *http.Client

	mu    sync.Mutex
	spool []byte // current JSONL buffer
	path  string
}

func newReporter(hubURL, key, spoolPath string) *reporter {
	return &reporter{
		hubURL: hubURL,
		key:    key,
		client: &http.Client{Timeout: 10 * time.Second},
		path:   spoolPath,
	}
}

// ReportLLM records one model call (prompt+response metadata + content).
func (r *reporter) ReportLLM(sessionID, model string, reqBody, respBody []byte, ms int64) {
	ev := map[string]any{
		"kind":      "llm_call",
		"session":   sessionID,
		"model":     model,
		"duration_ms": ms,
		// Store full content: the Intelligence plane replays thoughts.
		"request":  jsonRaw(reqBody),
		"response": jsonRaw(respBody),
	}
	r.enqueue(ev)
}

// ReportTool records one tool/command execution and its local verdict.
func (r *reporter) ReportTool(sessionID, toolID string, args []byte, verdict string, reason string) {
	ev := map[string]any{
		"kind":    "tool_call",
		"session": sessionID,
		"tool":    toolID,
		"args":    jsonRaw(args),
		"verdict": verdict,
		"reason":  reason,
	}
	r.enqueue(ev)
}

func (r *reporter) enqueue(ev map[string]any) {
	b, _ := json.Marshal(ev)
	b = append(b, '\n')
	r.mu.Lock()
	r.spool = append(r.spool, b...)
	_ = appendFile(r.path, b)
	n := len(r.spool)
	r.mu.Unlock()
	if n >= 32*1024 { // flush threshold
		go r.Flush()
	}
}

// Flush ships the spooled events to the hub /api/ingest.
func (r *reporter) Flush() error {
	r.mu.Lock()
	if len(r.spool) == 0 {
		r.mu.Unlock()
		return nil
	}
	payload := r.spool
	r.spool = nil
	r.mu.Unlock()

	if r.hubURL == "" {
		return nil // offline mode: spool file is the record
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(r.hubURL, "/")+"/api/ingest",
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Authorization", "Bearer "+r.key)
	resp, err := r.client.Do(req)
	if err != nil {
		// put it back at the front of the spool
		r.mu.Lock()
		r.spool = append(payload, r.spool...)
		r.mu.Unlock()
		return fmt.Errorf("hub ingest: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hub ingest status %d", resp.StatusCode)
	}
	return nil
}

// hubCheck asks the central gateway for a verdict on a sensitive action.
// Used by the hook path for CONFIRM-class decisions that need the operator UI.
func (r *reporter) hubCheck(ctx context.Context, sessionID, toolID string, args []byte) (string, error) {
	body, _ := json.Marshal(map[string]any{"session": sessionID, "tool": toolID, "arguments": jsonRaw(args)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(r.hubURL, "/")+"/api/hub-check", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.key)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Verdict + "|" + out.Reason, nil
}

func appendFile(path string, b []byte) error {
	if path == "" {
		return nil
	}
	f, err := osOpenAppend(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

var logFatalf = log.Fatalf

func jsonRaw(b []byte) json.RawMessage { return json.RawMessage(b) }

// ensure api import used even in offline builds
var _ = api.VerdictAllow
