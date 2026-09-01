package webui

import (
	"encoding/json"
	"net/http"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
)

// apiPolicySimulate replays a candidate policy against recent historical
// events and reports what would change (impact analysis before deploy).
//
//	POST /api/policies/simulate
//	{ "selector": {"tool":"Bash","operation":"run"}, "action":"block", "rule_id":"x" }
//	→ { total, would_allow, would_block, changed, changed_to_block, changed_samples:[...] }
func (s *Server) apiPolicySimulate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Selector json.RawMessage `json:"selector"`
		Action   string          `json:"action"`
		RuleID   string          `json:"rule_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	pol, _ := json.Marshal(req)

	var evs []api.Event
	if s.Store != nil {
		evs = s.Store.Recent(2000)
	}
	res, err := policyhub.Simulate(pol, evs)
	if err != nil {
		http.Error(w, "simulate failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, res)
}
