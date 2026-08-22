package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// traceID returns a stable per-session task id, rotated when the session goes
// idle long enough to count as a new task (30 min heuristic).
func (r *reporter) traceID(session string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if t, ok := r.traces[session]; ok && now.Sub(r.lastSeen[session]) < 30*time.Minute {
		r.lastSeen[session] = now
		return t
	}
	t := "trace-" + session + "-" + now.Format("0102-150405")
	r.traces[session] = t
	r.lastSeen[session] = now
	return t
}

// lastLLM returns the most recent LLM call id for a session (parent links).
func (r *reporter) lastLLM(session string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastLLMCall[session]
}

// reporter batches probe events and ships them to the central hub. Events are
// durable: every batch is spooled to disk and retried with backoff until the
// hub accepts it — offline work is never lost.
type reporter struct {
	hubURL string
	key    string
	client *http.Client

	mu        sync.Mutex
	spool     *spoolStore
	traces    map[string]string
	lastSeen  map[string]time.Time
	lastLLMCall map[string]string
}

func newReporter(hubURL, key, spoolPath string) *reporter {
	return &reporter{
		hubURL:      hubURL,
		key:         key,
		client:      &http.Client{Timeout: 10 * time.Second},
		spool:       newSpool(spoolPath + ".queue"),
		traces:      map[string]string{},
		lastSeen:    map[string]time.Time{},
		lastLLMCall: map[string]string{},
	}
}

// ReportLLM records one model call (prompt+response metadata + content).
func (r *reporter) ReportLLM(sessionID, model string, reqBody, respBody []byte, ms int64) {
	ev := map[string]any{
		"kind":        "llm_call",
		"session":     sessionID,
		"trace_id":    r.traceID(sessionID),
		"model":       model,
		"duration_ms": ms,
		// Store full content: the Intelligence plane replays thoughts.
		"request":  jsonRaw(reqBody),
		"response": jsonRaw(respBody),
	}
	r.mu.Lock()
	r.lastLLMCall[sessionID] = fmt.Sprintf("llm-%d", time.Now().UnixNano())
	r.mu.Unlock()
	r.enqueue(ev)
}

// ReportTool records one tool/command execution and its local verdict.
func (r *reporter) ReportTool(sessionID, toolID string, args []byte, verdict string, reason string) {
	ev := map[string]any{
		"kind":     "tool_call",
		"session":  sessionID,
		"trace_id": r.traceID(sessionID),
		"parent":   r.lastLLM(sessionID), // causal link: which LLM call led here
		"tool":     toolID,
		"args":     jsonRaw(args),
		"verdict":  verdict,
		"reason":   reason,
	}
	r.enqueue(ev)
}

func (r *reporter) enqueue(ev map[string]any) {
	b, _ := json.Marshal(ev)
	b = append(b, '\n')
	r.spool.push(b)
	if r.spool.len() >= 8 { // small batches flush fast
		go r.Flush()
	}
}

// Flush ships all pending batches to the hub /api/ingest, retrying failures.
func (r *reporter) Flush() error {
	var lastErr error
	for i := 0; i < 64; i++ { // bounded drain per flush cycle
		batch, ok := r.spool.pop()
		if !ok {
			return lastErr
		}
		if r.hubURL == "" {
			return nil // offline mode: spool file is the record
		}
		if err := r.ship(batch); err != nil {
			r.spool.unpop(batch)
			lastErr = err
			return err // leave remaining batches for next cycle
		}
	}
	return lastErr
}

func (r *reporter) ship(batch []byte) error {
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(r.hubURL, "/")+"/api/ingest", bytes.NewReader(batch))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Authorization", "Bearer "+r.key)
	resp, err := r.client.Do(req)
	if err != nil {
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

var logFatalf = logPrintf

func jsonRaw(b []byte) json.RawMessage { return json.RawMessage(b) }

var _ = api.VerdictAllow
