// Package store persists every decision event (JSONL append-only) and serves
// queries for the Intelligence plane and the web UI. The file is the source of
// truth; the memory index is rebuilt on Start.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dedarek/agent-security-gateway/api"
)

type Store struct {
	mu     sync.RWMutex
	path   string
	f      *os.File
	events []api.Event
}

// Open opens (creating if needed) the JSONL event log and replays it.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" || path == "stdout" {
		return s, nil // memory-only
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create event log dir: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s.f = f
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var ev api.Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Call.CallID != "" {
			s.events = append(s.events, ev)
		}
	}
	return s, nil
}

// Write implements audit.Sink: append + persist.
func (s *Store) Write(ev api.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	if s.f != nil {
		if _, err := s.f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// QueryFilter holds optional filter criteria for event queries.
type QueryFilter struct {
	SessionID string
	ToolID    string // substring match
	Verdict   string // exact match: ALLOW|BLOCK|REDACT|CONFIRM
	Offset    int
	Limit     int
}

// Query filters events with pagination. Returns matching events + total count.
func (s *Store) Query(f QueryFilter) ([]api.Event, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []int // indices into s.events
	for i := len(s.events) - 1; i >= 0; i-- { // newest first
		e := &s.events[i]
		if f.SessionID != "" && e.SessionID != f.SessionID {
			continue
		}
		if f.ToolID != "" && !strings.Contains(strings.ToLower(e.Call.ToolID), strings.ToLower(f.ToolID)) {
			continue
		}
		if f.Verdict != "" && e.Decision.Final.String() != f.Verdict {
			continue
		}
		matched = append(matched, i)
	}

	total := len(matched)
	offset := f.Offset
	limit := f.Limit
	if limit <= 0 { limit = 50 }

	var out []api.Event
	for idx, i := range matched {
		if idx < offset { continue }
		if len(out) >= limit { break }
		out = append(out, s.events[i])
	}
	return out, total
}

// Recent returns up to n newest events (newest first).
func (s *Store) Recent(n int) []api.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n > len(s.events) {
		n = len(s.events)
	}
	out := make([]api.Event, n)
	for i := 0; i < n; i++ {
		out[i] = s.events[len(s.events)-1-i]
	}
	return out
}

// Sessions lists distinct session ids with event counts (newest first).
func (s *Store) Sessions() []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	order := []string{}
	count := map[string]int{}
	lastVerdict := map[string]api.Verdict{}
	for _, ev := range s.events {
		id := ev.SessionID
		if _, ok := count[id]; !ok {
			order = append(order, id)
		}
		count[id]++
		lastVerdict[id] = ev.Decision.Final // events are appended chronologically
	}
	out := make([]SessionSummary, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		out = append(out, SessionSummary{SessionID: id, Events: count[id], LastVerdict: lastVerdict[id].String()})
	}
	return out
}

// Trajectory returns all events of one session in chronological order.
func (s *Store) Trajectory(sessionID string) []api.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []api.Event{}
	for _, ev := range s.events {
		if ev.SessionID == sessionID {
			out = append(out, ev)
		}
	}
	return out
}

type SessionSummary struct {
	SessionID   string `json:"session_id"`
	Events      int    `json:"events"`
	LastVerdict string `json:"last_verdict"`
}
