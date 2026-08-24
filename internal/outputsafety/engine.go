// Package outputsafety scans agent-generated content (LLM responses) for
// harmful patterns. Unlike the data axis (which protects tool I/O), this
// engine guards what the agent *produced* — code it wrote, documents it
// composed, decisions it made.
//
// Disposition philosophy: usability-first.
//   - CRITICAL (malware/backdoor/exfiltration logic)  → BLOCK
//   - HIGH (credential harvesting, phishing content)  → BLOCK
//   - MEDIUM (suspicious pattern, needs human review) → FLAG (audit only, pass through)
//   - LOW (minor policy deviation)                    → AUDIT_ONLY (log + move on)
package outputsafety

import (
	"regexp"
	"strings"
)

type Severity int

const (
	Low      Severity = iota // AUDIT_ONLY: log and continue silently
	Medium                   // FLAG: audit-only, visible in console but passes through
	High                     // BLOCK: credential/phishing risk
	Critical                 // BLOCK: irreversible harm possible
)

func (s Severity) String() string {
	switch s {
	case Critical:
		return "CRITICAL"
	case High:
		return "HIGH"
	case Medium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

type Action int

const (
	Block      Action = iota // deny the output
	Flag                     // pass through but mark in audit trail
	AuditOnly                // log only, no marking visible to agent
)

type Rule struct {
	Name        string
	Category    string // "harmful_code" | "data_fabrication" | "misinformation" | "scope_creep" | ...
	Pattern     *regexp.Regexp
	Severity    Severity
	Action      Action
	Description string
}

var rules = []*Rule{
	// --- harmful_code: CRITICAL ---
	{Name: "reverse_shell", Category: "harmful_code", Severity: Critical, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(nc\s+-[le]|/dev/tcp/|bash\s+-i\s*[>&]+\s*/dev/tcp|mkfifo.*netcat)`),
		Description: "Agent generated reverse shell payload"},
	{Name: "credential_stealer", Category: "harmful_code", Severity: Critical, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(\.ssh/id_rsa|\.aws/credentials|\.env\b).*(curl|wget|send|upload|post|exfil)`),
		Description: "Agent wrote code to exfiltrate credentials"},
	{Name: "ransomware_pattern", Category: "harmful_code", Severity: Critical, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(encrypt.*all.*files|AES.*CBC.*key.*delete|ransom_note|bitcoin_address)`),
		Description: "Ransomware-style file encryption pattern"},

	// --- harmful_code: HIGH ---
	{Name: "data_collector", Category: "harmful_code", Severity: High, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(collect|harvest|gather).{0,30}(email|password|token|credit.?card|ssn)\b`),
		Description: "Code harvests user PII or credentials"},
	{Name: "backdoor_implant", Category: "harmful_code", Severity: High, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(hidden\s+(api|endpoint|route)|bypass\s+auth|hardcoded\s+(api_)?key\s*=)`),
		Description: "Potential backdoor: hidden auth bypass or hardcoded key"},
	{Name: "supply_chain_poison", Category: "harmful_code", Severity: High, Action: Block,
		Pattern:     regexp.MustCompile(`(?i)(postinstall|preinstall).{0,50}(curl|wget|eval|exec)\b`),
		Description: "Package install script downloads/executes remote code"},

	// --- misinformation / fabrication: MEDIUM (flag, don't block — may be false positive) ---
	{Name: "fabricated_data", Category: "data_fabrication", Severity: Medium, Action: Flag,
		Pattern:     regexp.MustCompile(`(?i)(according to my (research|analysis)).{0,80}\d{4}[-/]\d{2}`),
		Description: "Agent cites specific dated sources that may be hallucinated"},
	{Name: "fake_citation", Category: "misinformation", Severity: Medium, Action: Flag,
		Pattern:     regexp.MustCompile(`(?i)(doi[:\s]+10\.\d{4}/|arxiv:\d{4}\.\d{4,5})`),
		Description: "Contains citation-like identifiers that should be verified"},

	// --- scope_creep: MEDIUM (agent drifting from original task) ---
	{Name: "unrequested_destructive", Category: "scope_creep", Severity: Medium, Action: Flag,
		Pattern:     regexp.MustCompile(`(?i)(delete|drop|truncate|purge)\s+(table|database|index|collection)\b`),
		Description: "Agent performing destructive DB operations without explicit request"},

	// --- LOW: audit only ---
	{Name: "pii_in_output", Category: "content_safety", Severity: Low, Action: AuditOnly,
		Pattern:     regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), // SSN format
		Description: "Output contains potential PII (SSN format)"},
}

// Result carries what was found in a scan.
type ScanResult struct {
	Matched   []*Rule
	MaxSev    Severity
	Summary   string
	HasBlock  bool
	HasFlag   bool
	IsClean   bool
}

// Scan runs all rules over agent output text.
func Scan(output string) *ScanResult {
	result := &ScanResult{}
	if strings.TrimSpace(output) == "" {
		result.IsClean = true
		return result
	}
	for _, r := range rules {
		if r.Pattern.MatchString(output) {
			result.Matched = append(result.Matched, r)
			if r.Severity > result.MaxSev && r.Action == Block {
				result.MaxSev = r.Severity
				result.HasBlock = true
			}
			if r.Action == Flag {
				result.HasFlag = true
			}
		}
	}
	if len(result.Matched) == 0 {
		result.IsClean = true
		return result
	}
	// Summary: highest severity rule name + category list
	cats := map[string]bool{}
	for _, m := range result.Matched {
		cats[m.Category] = true
	}
	var catList []string
	for c := range cats {
		catList = append(catList, c)
	}
	result.Summary = result.Matched[0].Name + " [" + result.Matched[0].Severity.String() +
		"] categories: " + strings.Join(catList, ",")
	return result
}

// ShouldBlock returns true if any matched rule requires blocking.
func (r *ScanResult) ShouldBlock() bool { return r.HasBlock }

// Categories returns unique threat categories found.
func (r *ScanResult) Categories() []string {
	set := map[string]bool{}
	var out []string
	for _, m := range r.Matched {
		if !set[m.Category] {
			set[m.Category] = true
			out = append(out, m.Category)
		}
	}
	return out
}
