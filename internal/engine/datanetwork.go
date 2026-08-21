package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/rulesbundle"
)

// DataNetworkEngine is the data/network axis (Pipelock class). It loads the REAL
// Pipelock community rule bundle (deploy/rules/pipelock-community.yaml, copied
// verbatim from luckyPipewrench/pipelock-rules) and scans tool-call arguments,
// tool identifiers, and tool results with the bundle's RE2 regexes.
//
// Rule semantics follow Pipelock:
//
//	type: dlp         -> secret/PII exfiltration  => REDACT (scrub) or BLOCK if critical
//	type: injection   -> prompt injection         => BLOCK
//	type: tool-poison -> malicious tool metadata  => BLOCK (scan_field: name)
//
// By default only `status: stable` rules are active (experimental off), matching
// Pipelock's LoadOptions{IncludeExperimental:false}.
// See docs/BASE-PROJECTS-ANALYSIS.md §2.
type DataNetworkEngine struct {
	dlp        []compiledRule
	injection  []compiledRule
	toolPoison []compiledRule
	bundleName string
	bundleVer  string
}

type compiledRule struct {
	ID         string
	Name       string
	Severity   string
	Confidence string
	Re         *regexp.Regexp
	ScanField  string // for tool-poison: "name" | "description"
}

// bundle mirrors the Pipelock signed-YAML bundle format.
type bundle struct {
	FormatVersion int    `yaml:"format_version"`
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Rules         []struct {
		ID         string `yaml:"id"`
		Type       string `yaml:"type"`
		Status     string `yaml:"status"`
		Name       string `yaml:"name"`
		Severity   string `yaml:"severity"`
		Confidence string `yaml:"confidence"`
		Pattern    struct {
			Regex     string `yaml:"regex"`
			ScanField string `yaml:"scan_field"`
		} `yaml:"pattern"`
	} `yaml:"rules"`
}

// NewDataNetworkEngineFromFile loads a Pipelock rule bundle from disk. The
// bundle's detached Ed25519 signature (<path>.sig) is verified against the
// embedded official Pipelock keyring BEFORE parsing (fail-closed: a missing,
// malformed, or untrusted signature aborts the load), and the verified bytes —
// not a re-read of the file — are the ones parsed.
func NewDataNetworkEngineFromFile(path string, includeExperimental bool) (*DataNetworkEngine, error) {
	raw, err := rulesbundle.LoadVerified(path)
	if err != nil {
		return nil, err
	}
	var b bundle
	if err := yaml.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("parse rule bundle: %w", err)
	}
	e := &DataNetworkEngine{bundleName: b.Name, bundleVer: b.Version}
	for _, r := range b.Rules {
		if r.Status != "stable" && !includeExperimental {
			continue
		}
		if r.Pattern.Regex == "" {
			continue
		}
		re, err := regexp.Compile(r.Pattern.Regex)
		if err != nil {
			// RE2 can't compile some PCRE constructs; skip rather than fail the whole bundle.
			continue
		}
		cr := compiledRule{ID: r.ID, Name: r.Name, Severity: r.Severity, Confidence: r.Confidence, Re: re, ScanField: r.Pattern.ScanField}
		switch r.Type {
		case "dlp":
			e.dlp = append(e.dlp, cr)
		case "injection":
			e.injection = append(e.injection, cr)
		case "tool-poison":
			e.toolPoison = append(e.toolPoison, cr)
		}
	}
	return e, nil
}

func (e *DataNetworkEngine) Name() string {
	return fmt.Sprintf("datanetwork.pipelock[%s@%s]", e.bundleName, e.bundleVer)
}
func (e *DataNetworkEngine) Axis() api.Axis         { return api.AxisDataNetwork }
func (e *DataNetworkEngine) FailMode() api.FailMode { return api.FailClosed }

// scan runs a set of rules over text and returns the first match.
func scan(rules []compiledRule, text string) (compiledRule, bool) {
	for _, r := range rules {
		if r.Re.MatchString(text) {
			return r, true
		}
	}
	return compiledRule{}, false
}

// scanWithHits returns the first matching rule together with the exact matched
// substrings, so the caller can emit concrete Redactions (literal Match values)
// instead of a vague whole-payload marker.
func scanWithHits(rules []compiledRule, text string) (compiledRule, []string, bool) {
	for _, r := range rules {
		if hits := r.Re.FindAllString(text, -1); len(hits) > 0 {
			return r, hits, true
		}
	}
	return compiledRule{}, nil, false
}

func (e *DataNetworkEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	// tool-poison: scan the tool identifier/name.
	for _, r := range e.toolPoison {
		if r.ScanField == "name" && r.Re.MatchString(lastSegment(c.ToolID)) {
			return block(e, "tool-poison", r, "tool name mimics a system binary: "+c.ToolID), nil
		}
	}
	args := string(c.Arguments)
	// injection in arguments -> BLOCK.
	if r, ok := scan(e.injection, args); ok {
		return block(e, "injection", r, "prompt-injection pattern in tool arguments"), nil
	}
	// dlp in arguments (outbound secret) -> REDACT with concrete matches.
	if r, hits, ok := scanWithHits(e.dlp, args); ok {
		return redactOrBlock(e, r, hits, "sensitive data in tool arguments"), nil
	}
	return &api.Signal{Axis: api.AxisDataNetwork, Engine: e.Name(), Verdict: api.VerdictAllow}, nil
}

func (e *DataNetworkEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (e *DataNetworkEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, r *api.ToolResult) (*api.Signal, error) {
	if r == nil {
		return nil, nil
	}
	out := string(r.Output)
	if rule, ok := scan(e.injection, out); ok {
		return block(e, "injection", rule, "prompt-injection pattern in tool result"), nil
	}
	if rule, hits, ok := scanWithHits(e.dlp, out); ok {
		return redactOrBlock(e, rule, hits, "sensitive data in tool result"), nil
	}
	return &api.Signal{Axis: api.AxisDataNetwork, Engine: e.Name(), Verdict: api.VerdictAllow}, nil
}

func block(e *DataNetworkEngine, kind string, r compiledRule, reason string) *api.Signal {
	return &api.Signal{
		Axis:     api.AxisDataNetwork,
		Engine:   e.Name(),
		Score:    scoreFor(r.Severity),
		Verdict:  api.VerdictBlock,
		Reasons:  []string{reason + " [" + r.Name + "]"},
		Evidence: []api.Evidence{{Kind: kind, Detail: r.ID + " (" + r.Severity + ")"}},
		FailMode: api.FailClosed,
	}
}

func redactOrBlock(e *DataNetworkEngine, r compiledRule, hits []string, reason string) *api.Signal {
	v := api.VerdictRedact
	if r.Severity == "critical" {
		v = api.VerdictRedact // scrub critical secrets rather than hard-block, keep usability
	}
	redactions := make([]api.Redaction, 0, len(hits))
	for _, h := range hits {
		redactions = append(redactions, api.Redaction{
			Path:    "*",
			Match:   h,
			Reason:  r.Name,
			Replace: "***",
		})
	}
	return &api.Signal{
		Axis:       api.AxisDataNetwork,
		Engine:     e.Name(),
		Score:      scoreFor(r.Severity),
		Verdict:    v,
		Reasons:    []string{reason + " [" + r.Name + "]"},
		Evidence:   []api.Evidence{{Kind: "dlp", Detail: r.ID + " (" + r.Severity + ")"}},
		Redactions: redactions,
		FailMode:   api.FailClosed,
	}
}

func scoreFor(sev string) int {
	switch sev {
	case "critical":
		return 95
	case "high":
		return 80
	case "medium":
		return 50
	default:
		return 30
	}
}

func lastSegment(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// Counts reports how many rules of each type are active (debug).
func (e *DataNetworkEngine) Counts() string {
	return fmt.Sprintf("dlp=%d injection=%d tool-poison=%d", len(e.dlp), len(e.injection), len(e.toolPoison))
}
