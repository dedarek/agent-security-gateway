package webui

import (
	"net/http"
	"os"
	"strconv"

	"github.com/dedarek/agent-security-gateway/internal/policyversion"
)

var policyMgr *policyversion.Manager

func (s *Server) RegisterPolicyAPI(mux *http.ServeMux) {
	// lazy init: history file siblings the live policy file
	policyMgr = policyversion.New("./deploy/policies/permission.cedar", "./data/policy-history.jsonl")
	mux.HandleFunc("/api/policies/current", s.Auth.middleware(s.apiPolicyCurrent))
	mux.HandleFunc("/api/policies/history", s.Auth.middleware(s.apiPolicyHistory))
	mux.HandleFunc("/api/policies/rollback", s.Auth.middleware(s.apiPolicyRollback))
}

func (s *Server) apiPolicyCurrent(w http.ResponseWriter, _ *http.Request) {
	b, err := os.ReadFile("./deploy/policies/permission.cedar")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"content": string(b)})
}

func (s *Server) apiPolicyHistory(w http.ResponseWriter, _ *http.Request) {
	if policyMgr == nil {
		writeJSON(w, []policyversion.Version{})
		return
	}
	writeJSON(w, policyMgr.History())
}

func (s *Server) apiPolicyRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	seqStr := r.URL.Query().Get("seq")
	if seqStr == "" {
		http.Error(w, "missing seq", 400)
		return
	}
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		http.Error(w, "bad seq", 400)
		return
	}
	if policyMgr == nil {
		http.Error(w, "no manager", 500)
		return
	}
	if err := policyMgr.Rollback(seq, "operator"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
