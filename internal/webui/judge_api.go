package webui

import (
	"net/http"
	"strconv"

	"github.com/dedarek/agent-security-gateway/internal/judge"
)

var judgeInst judgeProvider

type judgeProvider interface {
	Recent(n int) []judge.JudgeFinding
}

func (s *Server) SetJudge(j judgeProvider) { judgeInst = j }

func (s *Server) RegisterJudgeAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/judge/findings", s.Auth.middleware(s.apiJudgeFindings))
}

func (s *Server) apiJudgeFindings(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if judgeInst == nil {
		writeJSON(w, []judge.JudgeFinding{})
		return
	}
	writeJSON(w, judgeInst.Recent(limit))
}
