// Package session keeps a per-session agent trajectory (OpenAI chat-message
// shape) so the behavior/causal axis can reason about multi-step attacks. This
// is the trajectory feed for the Invariant analyzer (LocalPolicy/Monitor).
package session

import "sync"

// Message is one event in a trajectory, in the OpenAI chat-message shape that
// Invariant's Input.parse_input consumes.
type Message struct {
	Role       string     `json:"role"` // user|assistant|tool|system
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string, per OpenAI schema
}

// Store holds trajectories keyed by session id.
type Store struct {
	mu     sync.RWMutex
	traces map[string][]Message
}

func NewStore() *Store { return &Store{traces: map[string][]Message{}} }

// AppendToolCall records an assistant message that issues a tool call.
func (s *Store) AppendToolCall(sessionID, callID, toolName, argsJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[sessionID] = append(s.traces[sessionID], Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID: callID, Type: "function",
			Function: Function{Name: toolName, Arguments: argsJSON},
		}},
	})
}

// AppendToolResult records the tool's output.
func (s *Store) AppendToolResult(sessionID, callID, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces[sessionID] = append(s.traces[sessionID], Message{
		Role: "tool", ToolCallID: callID, Content: content,
	})
}

// Trace returns the current trajectory for a session (copy).
func (s *Store) Trace(sessionID string) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.traces[sessionID]
	out := make([]Message, len(src))
	copy(out, src)
	return out
}
