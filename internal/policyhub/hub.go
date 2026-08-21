// Package policyhub stores operator decisions on Intelligence suggestions and
// hot-reloads the permission engine's Cedar policies without restarting the
// gateway. Accepted suggestions are appended to the live policy file.
package policyhub

import (
	"fmt"
	"os"
	"sync"

	"github.com/dedarek/agent-security-gateway/internal/engine"
)

type Hub struct {
	mu         sync.Mutex
	policyPath string
	suggestions map[string]string // id -> cedar text (accepted)
}

func New(policyPath string) *Hub {
	return &Hub{policyPath: policyPath, suggestions: map[string]string{}}
}

// Accept records a suggestion and appends its Cedar policy to the live file,
// then triggers an engine reload.
func (h *Hub) Accept(id, cedar string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := os.OpenFile(h.policyPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + cedar + "\n"); err != nil {
		return err
	}
	h.suggestions[id] = cedar
	return h.reloadLocked()
}

func (h *Hub) Dismiss(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.suggestions, id)
}

// reloadLocked re-parses the policy file; on success swaps the PermissionEngine's
// policy set in place (engine exposes ReloadFromFile).
func (h *Hub) reloadLocked() error {
	return engine.ReloadPermissionPolicies(h.policyPath)
}

var _ = fmt.Sprintf
