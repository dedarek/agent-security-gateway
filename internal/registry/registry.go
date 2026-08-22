// Central MCP registry: admins curate the tool servers each tenant may use.
// Probes sync this on heartbeat and auto-mount the entries into local agent
// configs — users never edit MCP config by hand.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

// Entry is one curated MCP server in the central registry.
type Entry struct {
	Name    string   `json:"name" yaml:"name"`
	Command []string `json:"command,omitempty" yaml:"command,omitempty"` // stdio server argv
	URL     string   `json:"url,omitempty" yaml:"url,omitempty"`         // remote http server
	Tools   []string `json:"tools,omitempty" yaml:"tools,omitempty"`     // empty = all
	Tenants []string `json:"tenants" yaml:"tenants"`                     // which tenant names may see it
}

// Registry is the persisted central registry (JSON file, admin-edited or via UI API).
type Registry struct {
	mu      sync.RWMutex
	path    string
	entries []Entry
}

func Open(path string) (*Registry, error) {
	r := &Registry{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil // empty registry
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &r.entries); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

func (r *Registry) Add(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return r.saveLocked()
}

func (r *Registry) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.entries[:0]
	for _, e := range r.entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	r.entries = out
	return r.saveLocked()
}

// ForTenant returns entries visible to a tenant and a hash of the resulting
// set so probes can cheaply detect changes (config drift detection reuses it).
func (r *Registry) ForTenant(tenantName string) ([]Entry, string) {
	list := r.List()
	var out []Entry
	for _, e := range list {
		for _, t := range e.Tenants {
			if t == tenantName {
				out = append(out, e)
				break
			}
		}
	}
	h := sha256.Sum256([]byte(mustJSON(out)))
	return out, hex.EncodeToString(h[:])[:16]
}

func (r *Registry) saveLocked() error {
	b, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o644)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
