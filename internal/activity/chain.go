// Package activity stores hook-driven work chains.
// Each harness event (PostToolUse / SessionStart / Stop) is normalized into a
// Step and kept per-agent. M1: in-memory, bounded, zero persistence beyond
// the process lifetime. M2 will spill to SQLite.
package activity

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

// Step is one normalized harness event.
type Step struct {
	At        time.Time `json:"at"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`      // tool_use | session_start | session_end | llm_request
	ToolName  string    `json:"tool_name"` // Read / Edit / Bash / WebFetch ...
	Summary   string    `json:"summary"`   // parameter summary, cropped by detail level
	Verdict   string    `json:"verdict"`   // ALLOW / BLOCK ... (filled by engine in M3)
	Reason    string    `json:"reason"`
	Taints    []string  `json:"taints,omitempty"`
	Raw       json.RawMessage `json:"-"` // original hook_payload for debugging, not exposed in list
}

const (
	defaultMaxPerAgent = 500
	defaultMaxAgents    = 1000
)

// Store keeps bounded per-agent step lists. Thread-safe.
type Store struct {
	mu          sync.RWMutex
	maxPerAgent int
	maxAgents   int
	data        map[string][]Step // agent_id -> steps (oldest first, capped)
	db          *sql.DB
}

// New returns a Store with defaults.
func New() *Store {
	return &Store{
		maxPerAgent: defaultMaxPerAgent,
		maxAgents:   defaultMaxAgents,
		data:        make(map[string][]Step),
	}
}

// NewWithDB returns a Store that also persists to SQLite when db != nil.
func NewWithDB(db *sql.DB) *Store {
	s := New()
	s.db = db
	if db != nil {
		// Hydrate recent steps into memory for fast reads
		rows, err := db.Query(`SELECT ts, agent_id, session_id, kind, tool_name, summary, verdict, payload FROM activity_steps ORDER BY ts ASC, id ASC LIMIT 10000`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ts int64
				var st Step
				var payload string
				if err := rows.Scan(&ts, &st.AgentID, &st.SessionID, &st.Kind, &st.ToolName, &st.Summary, &st.Verdict, &payload); err != nil {
					continue
				}
				st.At = time.UnixMilli(ts).UTC()
				if payload != "" {
					st.Raw = json.RawMessage(payload)
				}
				s.data[st.AgentID] = append(s.data[st.AgentID], st)
			}
			// Trim per-agent to max
			for k, v := range s.data {
				if len(v) > s.maxPerAgent {
					s.data[k] = v[len(v)-s.maxPerAgent:]
				}
			}
		}
	}
	return s
}

// SetDB attaches a DB handle after construction (used by gateway main).
func (s *Store) SetDB(db *sql.DB) { s.db = db }

// Add appends a step. If the per-agent list exceeds maxPerAgent, the oldest
// is dropped. If the number of agents exceeds maxAgents, the call is ignored
// (bounded memory).
func (s *Store) Add(step Step) {
	if step.AgentID == "" {
		return
	}
	if step.At.IsZero() {
		step.At = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) >= s.maxAgents {
		if _, ok := s.data[step.AgentID]; !ok {
			return
		}
	}
	list := s.data[step.AgentID]
	list = append(list, step)
	if len(list) > s.maxPerAgent {
		list = list[len(list)-s.maxPerAgent:]
	}
	s.data[step.AgentID] = list
	if s.db != nil {
		// Persist to SQLite (best-effort, no error propagation to hook path)
		ts := step.At.UnixMilli()
		var payload string
		if len(step.Raw) > 0 {
			payload = string(step.Raw)
		}
		_, _ = s.db.Exec(`INSERT INTO activity_steps(ts, agent_id, session_id, kind, tool_name, summary, verdict, payload) VALUES(?,?,?,?,?,?,?,?)`,
			ts, step.AgentID, step.SessionID, step.Kind, step.ToolName, step.Summary, step.Verdict, payload)
		// Trim per-agent cap in DB
		_, _ = s.db.Exec(`DELETE FROM activity_steps WHERE agent_id=? AND id IN (SELECT id FROM activity_steps WHERE agent_id=? ORDER BY id DESC LIMIT -1 OFFSET ?)`,
			step.AgentID, step.AgentID, s.maxPerAgent)
	}
}

// List returns a copy of steps for agentID, oldest first. Empty if none.
func (s *Store) List(agentID string) []Step {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.data[agentID]
	out := make([]Step, len(src))
	copy(out, src)
	return out
}

// Recent returns at most n newest steps for agentID, newest first.
func (s *Store) Recent(agentID string, n int) []Step {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.data[agentID]
	if n <= 0 || n > len(src) {
		n = len(src)
	}
	out := make([]Step, n)
	for i := 0; i < n; i++ {
		out[i] = src[len(src)-1-i]
	}
	return out
}

// Count returns the number of stored steps for agentID.
func (s *Store) Count(agentID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data[agentID])
}

// Clear removes all steps for agentID.
func (s *Store) Clear(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, agentID)
}

// AllAgents returns the set of agentIDs with any steps.
func (s *Store) AllAgents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		out = append(out, k)
	}
	return out
}
