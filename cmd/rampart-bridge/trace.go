// Trace Correlator: unifies 8181 (Model Plane) and Rampart (Action Plane)
// events onto one trace per agent session.
//
// Key design (see .hermes/plans/2026-08-31-trace-correlator.md):
//   key   = (machine_id, agent_id, session_id)
//   value = trace_id   (first-seen session mints a new trace)
//   span  = {span_id, parent_id} — sequential within a trace
//
// Persistence: JSONL at ~/.config/asg/trace-map.jsonl so correlations survive
// bridge restarts. The map is append-only; compaction drops stale sessions.

package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type traceEntry struct {
	Key       string `json:"key"`
	TraceID   string `json:"trace_id"`
	FirstSeen string `json:"first_seen"`
	AgentID   string `json:"agent_id"`
	Session   string `json:"session"`
}

type traceCorrelator struct {
	mu    sync.Mutex
	m     map[string]traceEntry // key -> entry
	spans map[string]int        // trace_id -> next span seq
	path  string
}

func newTraceCorrelator() *traceCorrelator {
	tc := &traceCorrelator{
		m:     map[string]traceEntry{},
		spans: map[string]int{},
	}
	home, _ := os.UserHomeDir()
	tc.path = filepath.Join(home, ".config", "asg", "trace-map.jsonl")
	tc.load()
	return tc
}

func (tc *traceCorrelator) load() {
	f, err := os.Open(tc.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e traceEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		tc.m[e.Key] = e
	}
	log.Printf("trace-correlator: loaded %d session mappings from %s", len(tc.m), tc.path)
}

// traceFor returns the trace_id for a (agent, session) pair, minting one on
// first sight. Returns empty when both agent and session are blank.
func (tc *traceCorrelator) traceFor(agentID, session string) string {
	key := agentID + "|" + session
	if agentID == "" && session == "" {
		return ""
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if e, ok := tc.m[key]; ok {
		return e.TraceID
	}
	traceID := "tr_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000Z"), ":", "") + "_" + shortKey(key)
	e := traceEntry{
		Key: key, TraceID: traceID,
		FirstSeen: time.Now().UTC().Format(time.RFC3339),
		AgentID:   agentID, Session: session,
	}
	tc.m[key] = e
	tc.append(e)
	return traceID
}

// nextSpan mints the next span_id within a trace and returns (span, parent).
func (tc *traceCorrelator) nextSpan(traceID string) (span, parent string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	seq := tc.spans[traceID]
	tc.spans[traceID] = seq + 1
	span = traceID + "/s" + itoa(seq)
	if seq == 0 {
		parent = "" // root span
	} else {
		parent = traceID + "/s" + itoa(seq-1)
	}
	return span, parent
}

func (tc *traceCorrelator) append(e traceEntry) {
	f, err := os.OpenFile(tc.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

func shortKey(k string) string {
	s := strings.ReplaceAll(k, "|", "_")
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
