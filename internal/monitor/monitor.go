// Package monitor is the unified security monitor that wires together all
// detection engines (output safety, drift, cumulative risk, threat
// classification) and exposes their results to the console UI via /api/*.
package monitor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/drift"
	"github.com/dedarek/agent-security-gateway/internal/outputsafety"
	"github.com/dedarek/agent-security-gateway/internal/riskpattern"
	"github.com/dedarek/agent-security-gateway/internal/threatclass"
)

// Finding is a single security observation from any detection engine.
type Finding struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string   `json:"session_id"`
	TraceID   string   `json:"trace_id,omitempty"`
	Engine    string   `json:"engine"` // outputsafety | drift | riskpattern | threatclass
	Category  string   `json:"category"`
	Severity  string   `json:"severity"` // CRITICAL|HIGH|MEDIUM|LOW|INFO
	Action    string   `json:"action"`   // BLOCK|FLAG|AUDIT_ONLY
	Detail    string   `json:"detail"`
}

// Monitor aggregates all detection engines.
type Monitor struct {
	mu           sync.Mutex
	drift        *drift.Detector
	risk         *riskpattern.Detector
	findings     []Finding
	maxFindings  int
}

func New() *Monitor {
	return &Monitor{
		drift:       drift.NewDetector(),
		risk:        riskpattern.NewDetector(),
		maxFindings: 500,
	}
}

// ProcessUserMessage records the user's instruction for drift tracking.
func (m *Monitor) ProcessUserMessage(sessionID, message string) {
	m.drift.SetTask(sessionID, message)
}

// ProcessOutput scans LLM response content for harmful patterns (O1).
func (m *Monitor) ProcessOutput(sessionID, traceID, output string) []Finding {
	result := outputsafety.Scan(output)
	if result.IsClean {
		return nil
	}
	var findings []Finding
	for _, match := range result.Matched {
		f := Finding{
			Timestamp: time.Now(),
			SessionID: sessionID,
			TraceID:   traceID,
			Engine:    "outputsafety",
			Category:  match.Category,
			Severity:  match.Severity.String(),
			Action:    severityToAction(match.Severity),
			Detail:    match.Description,
		}
		findings = append(findings, f)
	}
	m.record(findings...)
	return findings
}

// ProcessToolCall checks drift (O2) and threat classification (O4) for one
// tool invocation. Returns findings + whether this call should be flagged.
func (m *Monitor) ProcessToolCall(sessionID, traceID, toolName string) ([]Finding, *drift.DriftResult) {
	var allFindings []Finding

	// Drift check (O2)
	dr := m.drift.Check(sessionID, toolName)

	// Threat classification (O4)
	cat := threatclass.Classify(toolName, actionFor(toolName), false, dr.DestructiveTool, dr.ShouldFlag)
	disp := threatclass.GetDisposition(cat)

	if dr.ShouldFlag || cat != threatclass.PolicyViolation {
		f := Finding{
			Timestamp: time.Now(),
			SessionID: sessionID,
			TraceID:   traceID,
			Engine:    "drift+threatclass",
			Category:  string(cat),
			Severity:  dispositionSeverity(disp.Action),
			Action:    disp.Action,
			Detail: fmt.Sprintf("tool=%s verdict=%s category=%s drift_score=%.2f disposition=%s",
				toolName, dr.Verdict, cat, dr.DriftScore, disp.Action),
		}
		allFindings = append(allFindings, f)
	}

	m.record(allFindings...)
	return allFindings, dr
}

// ProcessEvent feeds an event into the cumulative risk pattern detector (O3).
func (m *Monitor) ProcessEvent(traceID string, ev riskpattern.Event) []Finding {
	hits := m.risk.Add(traceID, ev)
	var findings []Finding
	for _, h := range hits {
		f := Finding{
			Timestamp: time.Now(),
			SessionID: traceID,
			TraceID:   traceID,
			Engine:    "riskpattern",
			Category:  "cumulative_risk",
			Severity:  "HIGH",
			Action:    "FLAG",
			Detail:    riskpattern.FormatHit(h),
		}
		findings = append(findings, f)
	}
	m.record(findings...)
	return findings
}

// Recent returns the most recent N findings for the UI.
func (m *Monitor) Recent(n int) []Finding {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > len(m.findings) {
		n = len(m.findings)
	}
	out := make([]Finding, n)
	copy(out, m.findings[len(m.findings)-n:])
	return out
}

func (m *Monitor) record(fs ...Finding) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.findings = append(m.findings, fs...)
	if len(m.findings) > m.maxFindings {
		m.findings = m.findings[len(m.findings)-m.maxFindings:]
	}
}

func severityToAction(s outputsafety.Severity) string {
	switch s {
	case outputsafety.Critical, outputsafety.High:
		return "BLOCK"
	case outputsafety.Medium:
		return "FLAG"
	default:
		return "AUDIT_ONLY"
	}
}

func dispositionSeverity(action string) string {
	switch action {
	case "BLOCK":
		return "CRITICAL"
	case "CONFIRM":
		return "HIGH"
	case "FLAG":
		return "MEDIUM"
	default:
		return "LOW"
	}
}

func actionFor(toolName string) string {
	t := strings.ToLower(toolName)
	if strings.Contains(t, "delete") || strings.Contains(t, "drop") ||
		strings.Contains(t, "rm") || strings.Contains(t, "truncate") {
		return "execute"
	}
	if strings.Contains(t, "send") || strings.Contains(t, "post") ||
		strings.Contains(t, "export") {
		return "network"
	}
	return "read"
}
