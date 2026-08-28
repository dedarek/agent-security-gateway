// Package activity stores hook-driven work chains.
// Each harness event (PostToolUse / SessionStart / Stop) is normalized into a
// Step and kept per-agent. M1: in-memory, bounded, zero persistence beyond
// the process lifetime. M2 will spill to SQLite.
package activity

import (
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
}

// New returns a Store with defaults.
func New() *Store {
	return &Store{
		maxPerAgent: defaultMaxPerAgent,
		maxAgents:   defaultMaxAgents,
		data:        make(map[string][]Step),
	}
}

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
