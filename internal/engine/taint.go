package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

// TaintEngine is the behavior/causal axis implemented as REAL content-based taint
// propagation. It replaces Invariant's positional-reachability model (which flags
// any sink after any untrusted event regardless of content). Here a sink call is
// only blocked when its argument values are provably derived from untrusted
// source content — genuine data-flow provenance.
//
//	source tool output  ->  extract tokens (emails/URLs/secrets)  ->  taint marks
//	sink tool call      ->  if an argument value flows from a taint mark => BLOCK
//
// This yields precision: a send_email to a TRUSTED internal address after
// get_inbox is ALLOWED (positional reachability would wrongly block it), while a
// send_email whose recipient came from the untrusted inbox is BLOCKED with a
// causal explanation. See docs/BASE-PROJECTS-ANALYSIS.md §4 (must self-build taint).
type TaintEngine struct {
	store    *session.Store
	sources  map[string]bool // tools whose OUTPUT is untrusted (e.g. get_inbox)
	sinks    map[string]bool // tools that egress data (e.g. send_email, http_post)
	failMode api.FailMode
}

var (
	reEmail = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	reURL   = regexp.MustCompile(`https?://[^\s"']+`)
	reHost  = regexp.MustCompile(`\b[a-zA-Z0-9.\-]+\.(?:com|net|org|io|ru|cn|xyz)\b`)
)

// NewTaintEngine builds the taint engine. sources/sinks are unqualified tool
// names (last segment of the tool id).
func NewTaintEngine(store *session.Store, sources, sinks []string, failMode api.FailMode) *TaintEngine {
	e := &TaintEngine{store: store, sources: map[string]bool{}, sinks: map[string]bool{}, failMode: failMode}
	for _, s := range sources {
		e.sources[s] = true
	}
	for _, s := range sinks {
		e.sinks[s] = true
	}
	return e
}

func (t *TaintEngine) Name() string           { return "behavior.taint" }
func (t *TaintEngine) Axis() api.Axis         { return api.AxisBehavior }
func (t *TaintEngine) FailMode() api.FailMode { return t.failMode }

// IsUntrustedSource reports whether a tool's output should be marked untrusted.
func (t *TaintEngine) IsUntrustedSource(toolID string) bool { return t.sources[lastSegment(toolID)] }

// SessionTaints returns the accumulated untrusted marks for a session
// (V0 session-level taint: any sensitive read taints the whole session).
func (t *TaintEngine) SessionTaints(sessionID string) []session.TaintMark {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Taints(sessionID)
}

// ObserveResult is called by the proxy after a tool returns. If the tool is an
// untrusted source, its output content is recorded as a taint mark with the
// extracted tokens. This is the propagation step.
func (t *TaintEngine) ObserveResult(sessionID, toolID string, output []byte) {
	name := lastSegment(toolID)
	if !t.sources[name] {
		return
	}
	content := string(output)
	t.store.MarkUntrusted(sessionID, name, content, extractTokens(content))
}

// ObserveHook is the hook-path (PreToolUse/PostToolUse) counterpart of
// ObserveResult. Harness hooks are config-only: there is no proxy in the data
// path, so the gateway never sees raw tool output for most events. This method
// implements session-level data-flow propagation from the hook payload alone:
//
//  1. SOURCE ingest — if the tool is a taint source and the payload carries
//     tool_response (PostToolUse), the real output content is the taint mark.
//     If there is no output yet (PreToolUse), a *sensitive path* in tool_input
//     (.aws/credentials, .ssh/id_rsa, .env, ...) marks the session as having
//     touched a secret, with that path as the provenance token.
//  2. DERIVATION — for ANY tool, if the call's arguments reference an existing
//     taint token AND the call produces a new artifact (shell redirect, -o,
//     tee, Write file_path), that artifact becomes a derived taint mark. This
//     is the actual propagation step: secret path -> /tmp/x -> egress.
//
// Together these give a causal chain, not a path-matching heuristic: step 3 is
// blocked because /tmp/x is provably derived from the credentials file read in
// step 1, and the block reason names that lineage.
func (t *TaintEngine) ObserveHook(sessionID, toolID string, payload []byte) {
	if sessionID == "" || len(payload) == 0 {
		return
	}
	name := lastSegment(toolID)
	input, response := hookParts(payload)
	values := flattenValues(input)

	// (1) source ingest
	if t.sources[name] {
		if strings.TrimSpace(response) != "" {
			toks := extractTokens(response)
			for _, p := range filePaths(values) {
				toks = append(toks, sensitiveTokens(p)...)
			}
			t.store.MarkUntrusted(sessionID, name, response, toks)
		} else {
			for _, p := range filePaths(values) {
				if isSensitivePath(p) {
					t.store.MarkUntrusted(sessionID, name+":"+p, p, sensitiveTokens(p))
				}
			}
		}
	}

	// (2) derivation: tainted input -> new artifact
	marks := t.store.Taints(sessionID)
	if len(marks) == 0 {
		return
	}
	var origin string
	for _, v := range values {
		for _, m := range marks {
			if _, ok := tokenFlow(v, m); ok {
				origin = m.Source
				break
			}
		}
		if origin != "" {
			break
		}
	}
	if origin == "" {
		return
	}
	existing := map[string]bool{}
	for _, m := range marks {
		existing[m.Content] = true
	}
	for _, art := range producedArtifacts(name, input, values) {
		if art == "" || existing[art] {
			continue
		}
		t.store.MarkUntrusted(sessionID, "derived("+origin+")", art, []string{art})
		existing[art] = true
	}
}

// EvaluatePre blocks a sink call whose arguments flow from untrusted content.
func (t *TaintEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	name := lastSegment(c.ToolID)
	if !t.sinks[name] {
		return &api.Signal{Axis: api.AxisBehavior, Engine: t.Name(), Verdict: api.VerdictAllow}, nil
	}
	marks := t.store.Taints(c.Principal.SessionID)
	if len(marks) == 0 {
		return &api.Signal{Axis: api.AxisBehavior, Engine: t.Name(), Verdict: api.VerdictAllow}, nil
	}

	values := argValues(c.Arguments)
	// Generic sinks (Bash/Write) only count as an exfil channel when the call
	// actually egresses. Without this gate, adding Bash to taint_sinks would
	// block every local command in a session that once read a secret.
	if !isEgress(name, values) {
		return &api.Signal{Axis: api.AxisBehavior, Engine: t.Name(), Verdict: api.VerdictAllow}, nil
	}
	// Pass 1: high-signal token match (emails/URLs/hosts) — the headline exfil channel.
	for _, v := range values {
		for _, m := range marks {
			if tok, ok := tokenFlow(v, m); ok {
				return t.block(c, m.Source, tok), nil
			}
		}
	}
	// Pass 2: substring provenance for larger copied payloads.
	for _, v := range values {
		if len(strings.TrimSpace(v)) < 12 {
			continue
		}
		for _, m := range marks {
			if strings.Contains(strings.ToLower(m.Content), strings.ToLower(strings.TrimSpace(v))) {
				return t.block(c, m.Source, v), nil
			}
		}
	}
	return &api.Signal{Axis: api.AxisBehavior, Engine: t.Name(), Verdict: api.VerdictAllow}, nil
}

func (t *TaintEngine) block(c *api.ToolCall, source, tok string) *api.Signal {
	return &api.Signal{
		Axis:    api.AxisBehavior,
		Engine:  t.Name(),
		Score:   93,
		Verdict: api.VerdictBlock,
		Reasons: []string{fmt.Sprintf(
			"value '%s' in %s originated from untrusted source %s (data-flow taint)", tok, c.ToolID, source)},
		Evidence: []api.Evidence{{
			Kind:   "taint",
			Detail: fmt.Sprintf("untrusted %s -> %s argument ('%s')", source, lastSegment(c.ToolID), tok),
		}},
		FailMode: t.failMode,
	}
}

func (t *TaintEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (t *TaintEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

// tokenFlow reports whether value v matches a high-signal token (email/URL/host)
// from taint mark m, returning the matching token. Short or generic values are
// rejected to avoid false positives: a tiny argument like "com" must not match
// every host token that happens to contain it.
func tokenFlow(v string, m session.TaintMark) (string, bool) {
	lv := strings.ToLower(strings.TrimSpace(v))
	if len(lv) < minTokenLen {
		return "", false
	}
	for _, tok := range m.Tokens {
		lt := strings.ToLower(tok)
		if lt == "" {
			continue
		}
		if strings.Contains(lv, lt) || strings.Contains(lt, lv) {
			return tok, true
		}
	}
	return "", false
}

// minTokenLen is the shortest value eligible for token-flow matching. Real
// emails/URLs/hosts are longer; anything shorter is noise (e.g. "com", "io").
const minTokenLen = 6

// extractTokens pulls high-signal identifiers (emails, URLs, hostnames) from text.
func extractTokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ms []string) {
		for _, s := range ms {
			s = strings.TrimRight(s, ".,);\"'")
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	add(reEmail.FindAllString(text, -1))
	add(reURL.FindAllString(text, -1))
	add(reHost.FindAllString(text, -1))
	return out
}

// argValues returns the string leaf values of a JSON arguments blob. Hook
// payloads nest the real arguments under tool_input, so this walks recursively.
func argValues(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		// Fall back to scanning the raw text for tokens.
		return extractTokens(string(raw))
	}
	return flattenValues(m)
}

// isExternalDestination reports whether a URL points outside the local trust
// zone: public hosts, non-private IPs, anything not on loopback/private/
// link-local ranges. Internal corporate domains (.internal/.corp/.local) and
// private IPs are NOT external.
func isExternalDestination(raw string) bool {
	u := strings.TrimSpace(strings.ToLower(raw))
	if u == "" {
		return false
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, ":"); i >= 0 && !strings.Contains(u[i:], "]") {
		u = u[:i]
	}
	if u == "" {
		return false
	}
	if ip := net.ParseIP(u); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
	}
	if strings.HasSuffix(u, ".local") || strings.HasSuffix(u, ".internal") || strings.HasSuffix(u, ".corp") ||
		strings.HasSuffix(u, ".lan") || strings.HasSuffix(u, ".localhost") ||
		strings.Contains(u, ".internal.") || u == "localhost" {
		return false
	}
	return strings.Contains(u, ".")
}
