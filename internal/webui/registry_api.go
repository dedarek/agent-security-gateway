package webui

import (
	"encoding/json"
	"net/http"

	"github.com/dedarek/agent-security-gateway/internal/registry"
)

// RegisterRegistryAPI adds admin endpoints to curate the MCP registry and a
// probe-facing sync endpoint (GET /api/registry/sync?tenant=name) that returns
// the tenant's entries + content hash for change detection.
func (s *Server) RegisterRegistryAPI(mux *http.ServeMux, reg *registry.Registry, tenants *TenantNames) {
	mux.HandleFunc("/api/registry", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, reg.List())
		case http.MethodPost:
			var e registry.Entry
			if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := reg.Add(e); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		case http.MethodDelete:
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if err := reg.Remove(req.Name); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, "method", 405)
		}
	}))
	// /api/registry/sync stays open (probes authenticate with tenant keys via
	// the ingress, and the spool path is local-only).

	mux.HandleFunc("/api/registry/sync", func(w http.ResponseWriter, r *http.Request) {
		tenant := r.URL.Query().Get("tenant")
		if tenant == "" {
			http.Error(w, "missing tenant", 400)
			return
		}
		entries, hash := reg.ForTenant(tenant)
		writeJSON(w, map[string]any{"hash": hash, "entries": entries})
	})
}
