// Package store persists every decision event (JSONL append-only) and serves
// queries for the Intelligence plane and the web UI. The file is the source of
// truth; the memory index is rebuilt on Start.
package store

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

type Store struct {
	mu        sync.RWMutex
	path      string
	f         *os.File
	events    []api.Event
	db        *sql.DB
	maxEvents int
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

// OpenWithDB opens a store backed by SQLite (primary) with optional JSONL audit.
func OpenWithDB(db *sql.DB, jsonlPath string, maxEvents int) (*Store, error) {
	s := &Store{db: db, maxEvents: maxEvents}
	if jsonlPath != "" && jsonlPath != "stdout" {
		s.path = jsonlPath
		if dir := filepath.Dir(jsonlPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create audit dir: %w", err)
			}
		}
		f, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		s.f = f
	}
	// If DB is empty, try to migrate from legacy JSONL (one-time)
	if db != nil && jsonlPath != "" {
		if cnt, _ := countEventsDB(db); cnt == 0 {
			if n, _ := migrateJSONLToDB(db, jsonlPath); n > 0 {
				// Log is handled by caller
				_ = n
			}
		}
		// Hydrate memory index from DB for fast queries
		if events, err := recentEventsDB(db, 10000); err == nil {
			// Store in chronological order for trajectory
			for i := len(events) - 1; i >= 0; i-- {
				s.events = append(s.events, events[i])
			}
		}
	}
	return s, nil
}

func countEventsDB(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	return n, err
}

func recentEventsDB(db *sql.DB, n int) ([]api.Event, error) {
	rows, err := db.Query(`SELECT payload FROM events ORDER BY ts DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Event
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		var ev api.Event
		if err := json.Unmarshal([]byte(j), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func migrateJSONLToDB(db *sql.DB, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	n := 0
	for sc.Scan() {
		var ev api.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Call.CallID == "" {
			continue
		}
		b, _ := json.Marshal(ev)
		ts := ev.Timestamp.UnixMilli()
		agentID := ev.Call.Principal.AgentID
		if agentID == "" {
			agentID = ev.SessionID
		}
		toolName := ev.Call.ToolID
		kind := "tool"
		if len(toolName) > 4 && toolName[:4] == "llm." {
			kind = "llm"
		}
		_, err := db.Exec(`INSERT INTO events(ts, agent_id, session_id, trace_id, parent_id, kind, tool_name, verdict, risk, payload) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			ts, agentID, ev.SessionID, ev.TraceID, ev.ParentID, kind, toolName, ev.Decision.Final.String(), ev.Decision.Risk, string(b))
		if err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
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
	if s.db != nil {
		// Write to SQLite (primary)
		ts := ev.Timestamp.UnixMilli()
		if ev.Timestamp.IsZero() {
			ts = bTimeNow().UnixMilli()
		}
		agentID := ev.Call.Principal.AgentID
		if agentID == "" {
			agentID = ev.SessionID
		}
		toolName := ev.Call.ToolID
		kind := "tool"
		if len(toolName) > 4 && toolName[:4] == "llm." {
			kind = "llm"
		}
		_, _ = s.db.Exec(`INSERT INTO events(ts, agent_id, session_id, trace_id, parent_id, kind, tool_name, verdict, risk, payload) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			ts, agentID, ev.SessionID, ev.TraceID, ev.ParentID, kind, toolName, ev.Decision.Final.String(), ev.Decision.Risk, string(b))
		// Trim if needed (async, best-effort)
		if s.maxEvents > 0 {
			go func() {
				_, _ = s.db.Exec(`DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY id DESC LIMIT -1 OFFSET ?)`, s.maxEvents)
			}()
		}
	}
	if s.f != nil {
		if _, err := s.f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

var bTimeNow = func() time.Time { return time.Now() }

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
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

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
