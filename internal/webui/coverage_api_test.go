package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postCoverage(s *Server, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/coverage/report", s.apiCoverageReport)
	mux.HandleFunc("/api/coverage", s.apiCoverage)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/coverage/report", strings.NewReader(body)))
	return rec
}

func getCoverage(s *Server) (*CoverageSummary, *httptest.ResponseRecorder) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/coverage/report", s.apiCoverageReport)
	mux.HandleFunc("/api/coverage", s.apiCoverage)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coverage", nil))
	var sum CoverageSummary
	json.Unmarshal(rec.Body.Bytes(), &sum)
	return &sum, rec
}

func resetCovStore() {
	covStore.mu.Lock()
	covStore.reports = map[string]*CoverageReport{}
	covStore.mu.Unlock()
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// Healthy probe -> protected, not degraded.
func TestCoverageHealthy(t *testing.T) {
	resetCovStore()
	s := New(nil, nil, nil)
	covStore.mu.Lock()
	covStore.reports["a1"] = &CoverageReport{AgentID: "a1", AgentType: "claude_code", ProxyUp: true, HubReachable: true, HookPresent: true, HookConfigured: true, PostHookCfg: false, ToolsCovered: []string{"Bash", "Read", "Write"}, ToolsPartial: []string{"MCP"}, ReportedAt: nowMillis()}
	covStore.mu.Unlock()
	sum, _ := getCoverage(s)
	if sum.Degraded {
		t.Fatalf("healthy probe should not be degraded: %v", sum.Issues)
	}
	if len(sum.Agents) != 1 || sum.Agents[0].Status != "protected" {
		t.Fatalf("expected protected agent, got %+v", sum.Agents)
	}
	if sum.CoveragePct != 87 {
		t.Fatalf("3 covered + 1 partial = 7/8 = 87, got %d", sum.CoveragePct)
	}
}

// Hook missing -> degraded + coverage drops.
func TestCoverageDegradedOnHookMissing(t *testing.T) {
	resetCovStore()
	s := New(nil, nil, nil)
	postCoverage(s, `{"agent_id":"a1","agent_type":"claude_code","proxy_up":true,"hub_reachable":true,"hook_present":false,"hook_configured":false,"posthook_configured":false,"tools_covered":["Bash","Read","Write"],"tools_partial":[]}`)
	sum, _ := getCoverage(s)
	if !sum.Degraded {
		t.Fatal("missing hook must degrade")
	}
	found := false
	for _, iss := range sum.Issues {
		if strings.Contains(iss, "hook missing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues should mention hook missing, got %v", sum.Issues)
	}
	if sum.Agents[0].Status != "degraded" {
		t.Fatalf("agent status should be degraded, got %s", sum.Agents[0].Status)
	}
	if sum.CoveragePct == 100 {
		t.Fatal("coverage should drop below 100 when hook missing")
	}
}

// Hook present but not configured in settings -> degraded.
func TestCoverageDegradedHookNotConfigured(t *testing.T) {
	resetCovStore()
	s := New(nil, nil, nil)
	postCoverage(s, `{"agent_id":"a1","agent_type":"claude_code","proxy_up":true,"hub_reachable":true,"hook_present":true,"hook_configured":false,"posthook_configured":false,"tools_covered":["Bash"],"tools_partial":[]}`)
	sum, _ := getCoverage(s)
	if !sum.Degraded {
		t.Fatal("hook not configured must degrade")
	}
}

// Partial tools (MCP) count half: 1 covered + 1 partial -> 75%.
func TestCoveragePartialTools(t *testing.T) {
	resetCovStore()
	s := New(nil, nil, nil)
	postCoverage(s, `{"agent_id":"a1","agent_type":"claude_code","proxy_up":true,"hub_reachable":true,"hook_present":true,"hook_configured":true,"posthook_configured":false,"tools_covered":["Bash","Read","Write"],"tools_partial":["MCP"]}`)
	sum, _ := getCoverage(s)
	if sum.CoveragePct != 87 {
		t.Fatalf("3 covered (6pts) + 1 partial (1pt) = 7/8 = 87, got %d", sum.CoveragePct)
	}
}

// Stale report (>90s) -> degraded.
func TestCoverageStale(t *testing.T) {
	resetCovStore()
	s := New(nil, nil, nil)
	covStore.mu.Lock()
	covStore.reports["a1"] = &CoverageReport{AgentID: "a1", ReportedAt: 0, HookPresent: true, HookConfigured: true, ProxyUp: true, ToolsCovered: []string{"Bash"}}
	covStore.mu.Unlock()
	sum, _ := getCoverage(s)
	if !sum.Degraded {
		t.Fatal("stale report must degrade")
	}
	if sum.Agents[0].Status != "stale" {
		t.Fatalf("expected stale status, got %s", sum.Agents[0].Status)
	}
}
