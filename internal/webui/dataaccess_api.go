package webui

import (
	"net/http"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/db"
)

// apiDataAccess returns the data-flow hops for a trace (lineage graph edges),
// plus a derived textual lineage: source -> ... -> sink paths reconstructed
// from read/transform/write/transmit hops.
//
//	GET /api/data-access?trace_id=<id>   → hops + lineage paths
//	GET /api/data-access?agent_id=<id>   → recent hops for an agent
func (s *Server) apiDataAccess(w http.ResponseWriter, r *http.Request) {
	if s.InventoryDB == nil {
		http.Error(w, "data access store unavailable", http.StatusServiceUnavailable)
		return
	}
	var hops []api.DataAccess
	var err error
	if traceID := r.URL.Query().Get("trace_id"); traceID != "" {
		hops, err = db.QueryDataAccess(s.InventoryDB, traceID)
	} else if agentID := r.URL.Query().Get("agent_id"); agentID != "" {
		hops, err = db.QueryDataAccessByAgent(s.InventoryDB, agentID)
	} else {
		// no filter -> recent hops across all agents (Demo/lineage view)
		hops, err = db.RecentDataAccess(s.InventoryDB, 100)
	}
	if err != nil {
		http.Error(w, "data access read failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"hops":    hops,
		"lineage": buildLineagePaths(hops),
	})
}

// buildLineagePaths reconstructs source→…→sink paths from an ordered list of
// DataAccess hops. read hops open a path (source), transform hops carry it,
// write/transmit hops close it (destination). This is the human-readable
// data lineage ("secret read from .env flowed to external URL").
func buildLineagePaths(hops []api.DataAccess) [][]string {
	var paths [][]string
	open := []string{}
	for _, h := range hops {
		switch h.Operation {
		case "read":
			src := h.Source
			if src == "" {
				src = h.ToolID
			}
			open = append(open, src)
		case "transform":
			// carries all open sources forward; nothing new unless it names one
			if h.Source != "" && len(open) > 0 {
				open[len(open)-1] = h.Source
			}
		case "write", "transmit":
			dst := h.Destination
			if dst == "" {
				dst = h.ToolID
			}
			// close every open path into this sink
			for _, src := range open {
				paths = append(paths, []string{src, dst})
			}
			open = nil
		}
	}
	// dangling reads (no sink yet): still show as open lineage
	for _, src := range open {
		paths = append(paths, []string{src})
	}
	return paths
}
