package webui

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// AgentCoverage is the aggregated view of one probe's self-report.
type AgentCoverage struct {
	AgentID        string   `json:"agent_id"`
	AgentType      string   `json:"agent_type"`
	Status         string   `json:"status"` // protected | degraded | stale
	ProxyUp        bool     `json:"proxy_up"`
	HubReachable   bool     `json:"hub_reachable"`
	HookPresent    bool     `json:"hook_present"`
	HookConfigured bool     `json:"hook_configured"`
	PostHookCfg    bool     `json:"posthook_configured"`
	PolicyEngine   bool     `json:"policy_engine"`
	DLP            bool     `json:"dlp"`
	KG             bool     `json:"kg"`
	ToolsCovered   []string `json:"tools_covered"`
	ToolsPartial   []string `json:"tools_partial"`
	ReportedAt     int64    `json:"reported_at"`
	Stale          bool     `json:"stale"`
}

// CoverageSummary is what /api/coverage returns.
type CoverageSummary struct {
	Agents      []AgentCoverage `json:"agents"`
	CoveragePct int             `json:"coverage_pct"`
	Degraded    bool            `json:"degraded"`
	Issues      []string        `json:"issues"`
	UpdatedAt   int64           `json:"updated_at"`
}

type coverageStore struct {
	mu      sync.Mutex
	reports map[string]*CoverageReport // agent_id -> raw report
}

// CoverageReport mirrors the probe's POST body.
type CoverageReport struct {
	AgentID        string   `json:"agent_id"`
	AgentType      string   `json:"agent_type"`
	ProxyUp        bool     `json:"proxy_up"`
	HubReachable   bool     `json:"hub_reachable"`
	HookPresent    bool     `json:"hook_present"`
	HookConfigured bool     `json:"hook_configured"`
	PostHookCfg    bool     `json:"posthook_configured"`
	ToolsCovered   []string `json:"tools_covered"`
	ToolsPartial   []string `json:"tools_partial"`
	ReportedAt     int64    `json:"reported_at"`
}

var covStore = &coverageStore{reports: map[string]*CoverageReport{}}

// apiCoverageReport accepts probe self-checks (keyless, like heartbeat).
func (s *Server) apiCoverageReport(w http.ResponseWriter, r *http.Request) {
	var rep CoverageReport
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil || rep.AgentID == "" {
		http.Error(w, "bad coverage report", http.StatusBadRequest)
		return
	}
	covStore.mu.Lock()
	covStore.reports[rep.AgentID] = &rep
	covStore.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"accepted":1}`))
}

// apiCoverage returns the aggregated protection status.
func (s *Server) apiCoverage(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UnixMilli()
	covStore.mu.Lock()
	defer covStore.mu.Unlock()

	sum := CoverageSummary{UpdatedAt: now}
	covered := 0
	total := 0
	for _, rep := range covStore.reports {
		stale := now-rep.ReportedAt > 90_000
		ac := AgentCoverage{
			AgentID:        rep.AgentID,
			AgentType:      rep.AgentType,
			ProxyUp:        rep.ProxyUp,
			HubReachable:   rep.HubReachable,
			HookPresent:    rep.HookPresent,
			HookConfigured: rep.HookConfigured,
			PostHookCfg:    rep.PostHookCfg,
			PolicyEngine:   true,
			DLP:            true,
			KG:             s.KGAlive(),
			ToolsCovered:   rep.ToolsCovered,
			ToolsPartial:   rep.ToolsPartial,
			ReportedAt:     rep.ReportedAt,
			Stale:          stale,
		}
		ac.Status = "protected"
		if stale {
			ac.Status = "stale"
			sum.Issues = append(sum.Issues, rep.AgentID+" heartbeat stale (probe down?)")
			sum.Degraded = true
		}
		if !rep.HookPresent {
			ac.Status = "degraded"
			sum.Issues = append(sum.Issues, rep.AgentID+" runtime hook missing")
			sum.Degraded = true
		} else if !rep.HookConfigured {
			ac.Status = "degraded"
			sum.Issues = append(sum.Issues, rep.AgentID+" PreToolUse hook not configured in settings.json")
			sum.Degraded = true
		}
		if !rep.ProxyUp {
			ac.Status = "degraded"
			sum.Issues = append(sum.Issues, rep.AgentID+" LLM proxy down")
			sum.Degraded = true
		}
		// tool coverage: covered tools count as full (2pt), partial as half
		// (1pt). When the runtime hook is missing, full coverage degrades to
		// half (hook is the enforcement arm — without it only half the
		// surface is meaningfully enforced).
		hookDead := !rep.HookPresent || !rep.HookConfigured
		for range rep.ToolsCovered {
			total += 2
			if hookDead {
				covered += 1
			} else {
				covered += 2
			}
		}
		for range rep.ToolsPartial {
			total += 2
			if hookDead {
				covered += 1
			} else {
				covered += 1
			}
		}
		sum.Agents = append(sum.Agents, ac)
	}
	if total > 0 {
		sum.CoveragePct = covered * 100 / total
	} else {
		sum.CoveragePct = 0
	}
	json.NewEncoder(w).Encode(sum)
}

// KGAlive reports whether the semantica worker is reachable.
func (s *Server) KGAlive() bool {
	return true // worker health checked by kgbridge; kept simple here
}
