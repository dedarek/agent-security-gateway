// Package approval implements the human-in-the-loop CONFIRM channel.
// Pending decisions are queued; the web UI (or CLI) lists them and approves or
// denies. Handle() blocks until a human answers or the timeout expires —
// fail-closed: timeout/deny => BLOCK.
package approval

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

type Request struct {
	ID        string
	ToolID    string
	Reason    string
	Risk      int
	CreatedAt time.Time
	decided   chan bool
}

type Manager struct {
	mu      sync.Mutex
	pending map[string]*Request
	seq     int
	timeout time.Duration
}

func NewManager(timeout time.Duration) *Manager {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Manager{pending: map[string]*Request{}, timeout: timeout}
}

// Enqueue adds a request without blocking (async approval flow). The caller
// returns an "ask" to the agent; the operator decides later in the console.
func (m *Manager) Enqueue(c *api.ToolCall, d api.Decision) *Request {
	return m.enqueue(c, d)
}

// Confirm implements proxy.Approver: enqueue, block for a human decision.
func (m *Manager) Confirm(ctx context.Context, c *api.ToolCall, d api.Decision) (bool, error) {
	req := m.enqueue(c, d)
	select {
	case ok := <-req.decided:
		return ok, nil
	case <-time.After(m.timeout):
		m.remove(req.ID)
		return false, fmt.Errorf("approval timeout after %s", m.timeout) // fail-closed
	case <-ctx.Done():
		m.remove(req.ID)
		return false, ctx.Err()
	}
}

func (m *Manager) enqueue(c *api.ToolCall, d api.Decision) *Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	req := &Request{
		ID:        fmt.Sprintf("apr-%d", m.seq),
		ToolID:    c.ToolID,
		Reason:    d.Rationale,
		Risk:      d.Risk,
		CreatedAt: time.Now(),
		decided:   make(chan bool, 1),
	}
	m.pending[req.ID] = req
	return req
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, id)
}

// Pending lists open requests (oldest first).
func (m *Manager) Pending() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Request{}
	for _, r := range m.pending {
		out = append(out, *r)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.Before(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Decide resolves a pending request; returns false if it no longer exists.
func (m *Manager) Decide(id string, approve bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.pending[id]
	if !ok {
		return false
	}
	delete(m.pending, id)
	r.decided <- approve
	return true
}
