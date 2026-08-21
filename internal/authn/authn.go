// Package authn provides multi-tenant identity for the gateway ingress.
// Tenants are declared in YAML (or env bootstrap); each carries an API key,
// a role, and a session namespace. The MCP ingress maps the transport identity
// to api.Principal so Cedar policies and taint state are per-tenant.
package authn

import (
	"crypto/subtle"
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Tenant struct {
	Name    string `yaml:"name"`
	APIKey  string `yaml:"api_key"`
	Role    string `yaml:"role"` // employee | admin | service
	UserID  string `yaml:"user_id"`
	Enabled bool   `yaml:"enabled"`
}

type Registry struct {
	mu      sync.RWMutex
	byKey   map[string]Tenant
	filePath string
}

// Load reads tenants from a YAML file with shape:
//
//	tenants:
//	  - name: alice
//	    api_key: sk-...
//	    role: employee
//	    user_id: alice@corp
//	    enabled: true
func Load(path string) (*Registry, error) {
	r := &Registry{byKey: map[string]Tenant{}, filePath: path}
	if path == "" {
		return r, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tenants: %w", err)
	}
	var doc struct {
		Tenants []Tenant `yaml:"tenants"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse tenants: %w", err)
	}
	for _, t := range doc.Tenants {
		if t.APIKey != "" && t.Enabled {
			r.byKey[t.APIKey] = t
		}
	}
	return r, nil
}

// Bootstrap creates a single-tenant registry for local dev (env override).
func Bootstrap() *Registry {
	key := os.Getenv("ASG_DEV_TENANT_KEY")
	if key == "" {
		key = "dev-key"
	}
	return &Registry{byKey: map[string]Tenant{key: {
		Name: "local", APIKey: key, Role: "employee", UserID: "local-user", Enabled: true,
	}}}
}

// BootstrapTenant is the fallback identity when no auth registry is wired.
func BootstrapTenant() Tenant {
	return Tenant{Name: "local", Role: "employee", UserID: "local-user", Enabled: true}
}

// Authenticate resolves an API key (from Authorization: Bearer or X-API-Key)
// to a Tenant. Constant-time compare against each stored key is avoided by
// map lookup + subtle compare on hit; unknown keys fail closed.
func (r *Registry) Authenticate(header string) (Tenant, bool) {
	key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(header), "Bearer "))
	r.mu.RLock()
	t, ok := r.byKey[key]
	r.mu.RUnlock()
	if !ok {
		return Tenant{}, false
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(t.APIKey)) != 1 {
		return Tenant{}, false
	}
	return t, true
}

// Count returns the number of active tenants (debug/UI).
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byKey)
}
