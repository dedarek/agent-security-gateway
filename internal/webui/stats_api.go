package webui

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// apiStatsSummary aggregates recent events into chart-ready buckets so the
// console doesn't pull the full event list and reduce client-side.
//
//	GET /api/stats/summary?n=200
//	→ { verdict:{block,confirm,allow,redact,other}, tools:[{name,count}],
//	    by_hour:[{hour,block,confirm,allow}], engines:[{engine,verdict,count}],
//	    per_agent:[{agent_id,block,confirm,total}], total }
func (s *Server) RegisterStatsAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/stats/summary", s.Auth.middleware(s.apiStatsSummary))
}

func (s *Server) apiStatsSummary(w http.ResponseWriter, r *http.Request) {
	n := 200
	if q := r.URL.Query().Get("n"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 2000 {
			n = v
		}
	}
	var events []api.Event
	if s.Store != nil {
		events = s.Store.Recent(n)
	}

	verdict := map[string]int{"block": 0, "confirm": 0, "allow": 0, "redact": 0, "other": 0}
	tools := map[string]int{}
	engines := map[string]map[string]int{} // engine -> verdict -> count
	perAgent := map[string]*[3]int{}       // agent -> [block, confirm, total]
	hours := map[string]map[string]int{}   // hour bucket -> verdict -> count
	risks := map[string]int{}              // risk key -> count

	for _, ev := range events {
		v := ev.Decision.Final.String()
		vk := "other"
		switch v {
		case "BLOCK":
			vk = "block"
		case "CONFIRM":
			vk = "confirm"
		case "ALLOW":
			vk = "allow"
		case "REDACT":
			vk = "redact"
		}
		verdict[vk]++

		tool := ev.Call.ToolID
		if tool == "" {
			tool = "unknown"
		}
		tools[tool]++

		// risk classification (dynamic, from real events)
		if vk == "block" || vk == "confirm" {
			if rk := classifyRisk(tool, string(ev.Call.Arguments)); rk != "" {
				risks[rk]++
			}
		}

		for _, sig := range ev.Decision.Signals {
			if sig.Engine == "" {
				continue
			}
			m := engines[sig.Engine]
			if m == nil {
				m = map[string]int{}
				engines[sig.Engine] = m
			}
			m[sig.Verdict.String()]++
		}

		ag := ev.Call.Principal.AgentID
		if ag == "" {
			ag = ev.Call.Principal.UserID
		}
		if ag != "" {
			rec := perAgent[ag]
			if rec == nil {
				rec = &[3]int{}
				perAgent[ag] = rec
			}
			rec[2]++
			if vk == "block" {
				rec[0]++
			} else if vk == "confirm" {
				rec[1]++
			}
		}

		ts := ev.Timestamp
		if ts.IsZero() {
			ts = ev.Call.Timestamp
		}
		if !ts.IsZero() {
			h := ts.UTC().Truncate(time.Hour).Format("15:04") // HH:00
			hm := hours[h]
			if hm == nil {
				hm = map[string]int{}
				hours[h] = hm
			}
			hm[vk]++
		}
	}

	toolList := make([]map[string]any, 0, len(tools))
	for name, c := range tools {
		toolList = append(toolList, map[string]any{"name": name, "count": c})
	}
	sort.Slice(toolList, func(i, j int) bool { return toolList[i]["count"].(int) > toolList[j]["count"].(int) })

	engineList := make([]map[string]any, 0, len(engines))
	for eng, m := range engines {
		for v, c := range m {
			engineList = append(engineList, map[string]any{"engine": eng, "verdict": v, "count": c})
		}
	}
	sort.Slice(engineList, func(i, j int) bool { return engineList[i]["count"].(int) > engineList[j]["count"].(int) })

	agentList := make([]map[string]any, 0, len(perAgent))
	for ag, rec := range perAgent {
		agentList = append(agentList, map[string]any{
			"agent_id": ag, "block": rec[0], "confirm": rec[1], "total": rec[2],
			"score": rec[0]*10 + rec[1]*2,
		})
	}
	sort.Slice(agentList, func(i, j int) bool { return agentList[i]["score"].(int) > agentList[j]["score"].(int) })

	hourList := make([]map[string]any, 0, len(hours))
	for h, m := range hours {
		hourList = append(hourList, map[string]any{
			"hour": h, "block": m["block"], "confirm": m["confirm"], "allow": m["allow"],
		})
	}
	sort.Slice(hourList, func(i, j int) bool { return hourList[i]["hour"].(string) < hourList[j]["hour"].(string) })

	// risk types: dynamic aggregation, Chinese labels, sorted desc, drop zeros
	riskList := make([]map[string]any, 0, len(risks))
	for name, c := range risks {
		riskList = append(riskList, map[string]any{"name": name, "count": c})
	}
	sort.Slice(riskList, func(i, j int) bool { return riskList[i]["count"].(int) > riskList[j]["count"].(int) })
	if len(riskList) > 5 {
		riskList = riskList[:5]
	}

	writeJSON(w, map[string]any{
		"verdict":   verdict,
		"tools":     toolList,
		"by_hour":   hourList,
		"engines":   engineList,
		"per_agent": agentList,
		"risks":     riskList,
		"total":     len(events),
	})
}

// classifyRisk maps a BLOCK/CONFIRM event to a Chinese risk type label.
// Dynamic: derived from the actual tool + arguments, not a static preset.
func classifyRisk(tool string, argsRaw string) string {
	t := strings.ToLower(tool)
	s := strings.ToLower(argsRaw)
	// decode base64 arguments (probe wraps tool_input in base64)
	if dec, err := base64.StdEncoding.DecodeString(s); err == nil {
		s = strings.ToLower(string(dec))
	}
	// 1. sensitive credential access (Read/Write/Edit of secret files)
	if strings.Contains(t, "read") || strings.Contains(t, "write") || strings.Contains(t, "edit") {
		for _, k := range []string{".env", "token", "secret", "password", "credential", "api_key", "private_key", "ssh"} {
			if strings.Contains(s, k) {
				return "敏感凭据与文件越权访问"
			}
		}
	}
	// 2. command injection / destructive shell
	if strings.Contains(t, "bash") || strings.Contains(t, "shell") || strings.Contains(t, "exec") {
		for _, k := range []string{"rm -rf", "drop table", "shutdown", "mkfs", ":(){", "curl", "wget", "chmod", "chown", "sudo"} {
			if strings.Contains(s, k) {
				return "高危命令与脚本注入"
			}
		}
	}
	// 3. exfiltration (network tools)
	if strings.Contains(t, "webfetch") || strings.Contains(t, "websearch") || strings.Contains(t, "http") || strings.Contains(t, "network") {
		return "外部不可信网络外联"
	}
	// 4. prompt hijack (task delegation / agentic tools)
	if strings.Contains(t, "task") || strings.Contains(t, "agent") || strings.Contains(t, "delegate") || strings.Contains(t, "prompt") {
		return "提示词劫持与意图伪造"
	}
	// 5. non-standard model (llm.* events)
	if strings.Contains(t, "llm") {
		return "非合规模型调用"
	}
	return "敏感操作"
}
