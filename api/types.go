// Package api defines the shared data model used across the Gateway data plane
// and the Intelligence/SOC analysis plane. In production this is generated from
// api/proto/*.proto so Go and Python share identical schemas.
package api

import "time"

// Axis is one of the three orthogonal security axes. Every Engine belongs to
// exactly one axis, but each axis spans all three lifecycle phases.
type Axis int

const (
	AxisPermission  Axis = iota // ToolHive-class: identity / tool / resource / arg scoping
	AxisDataNetwork             // Pipelock-class: egress / SSRF / DLP / secrets / poisoning
	AxisBehavior                // Invariant-class: trajectory / causal / indirect injection
)

func (a Axis) String() string {
	switch a {
	case AxisPermission:
		return "permission"
	case AxisDataNetwork:
		return "data_network"
	case AxisBehavior:
		return "behavior"
	default:
		return "unknown"
	}
}

// Phase is the lifecycle stage at which an evaluation happens.
type Phase int

const (
	PhasePre     Phase = iota // before the tool executes
	PhaseRuntime              // while the tool streams / executes
	PhasePost                 // after the tool returns a result
)

// Verdict is the outcome of an evaluation. The Risk Decision Engine aggregates
// per-engine verdicts into a single final verdict (see engine.Aggregate).
type Verdict int

const (
	VerdictAllow   Verdict = iota // pass through
	VerdictRedact                 // pass through but scrub sensitive fields
	VerdictConfirm                // human-in-the-loop approval required
	VerdictBlock                  // deny
)

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "ALLOW"
	case VerdictRedact:
		return "REDACT"
	case VerdictConfirm:
		return "CONFIRM"
	case VerdictBlock:
		return "BLOCK"
	default:
		return "UNKNOWN"
	}
}

// FailMode declares how the aggregator treats an engine error.
type FailMode int

const (
	FailOpen   FailMode = iota // engine error -> treat as ALLOW (low-sensitivity paths)
	FailClosed                 // engine error -> treat as BLOCK (high-sensitivity paths)
)

// Trust marks the provenance of data flowing through a session, used by the
// behavior/causal axis for taint propagation.
type Trust int

const (
	TrustTrusted Trust = iota
	TrustUntrusted
)

// Taint tags a piece of data with where it came from (e.g. external email,
// third-party MCP output) so the behavior axis can reason about causality.
type Taint struct {
	Source string // "email", "web", "mcp:github", "user"
	Trust  Trust
}

// Principal is who is making the call.
type Principal struct {
	UserID    string
	AgentID   string
	SessionID string
	Role      string
}

// ToolCall is a single tool/MCP invocation intercepted by the Gateway.
type ToolCall struct {
	CallID    string
	Principal Principal
	ToolID    string // e.g. "database.delete_user"
	Resource  string // e.g. "database.users"
	Action    string // read | write | delete | network ...
	Arguments []byte // JSON
	Taints    []Taint
	Timestamp time.Time
}

// ToolResult is what a tool returned.
type ToolResult struct {
	CallID       string
	Output       []byte
	Error        bool
	ResultTaints []Taint
}

// Evidence is a machine- and human-readable justification for a signal.
type Evidence struct {
	Kind   string // "policy_match", "dlp_hit", "ssrf", "trajectory"
	Detail string
}

// Redaction describes a scrub to apply before data crosses the boundary.
// Two flavors are supported today:
//   - Match != ""  -> replace every literal occurrence of Match with Replace
//     (used by regex-based DLP engines scanning raw payloads).
//   - Path != ""   -> future field-level scrubbing (JSON path into the payload).
//
// The Gateway MUST apply every redaction to arguments (before forwarding) and
// to results (before returning them to the agent / writing trajectories);
// emitting a REDACT verdict without rewriting bytes is a security bug.
type Redaction struct {
	Path    string // JSON path into arguments/output ("*" = whole payload)
	Match   string // exact substring to scrub (literal replace)
	Reason  string
	Replace string // e.g. "***"
}

// Signal is a single Engine's output for one call at one phase.
type Signal struct {
	Axis       Axis
	Engine     string
	Score      int // 0..100 risk score
	Verdict    Verdict
	Reasons    []string
	Evidence   []Evidence
	FailMode   FailMode
	Redactions []Redaction
}

// Decision is the aggregated outcome across all engines for one phase.
type Decision struct {
	CallID    string
	Phase     Phase
	Final     Verdict
	Signals   []Signal
	Rationale string
	Risk      int // max score, for alerting/sorting
}

// Event is the record persisted and streamed to the SOC analysis plane.
type Event struct {
	SessionID string
	Call      ToolCall
	Result    *ToolResult
	Decision  Decision
	Timestamp time.Time
}
