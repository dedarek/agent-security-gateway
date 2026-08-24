// Package shellcontrol gates command execution through the three-axis engine.
// Agents' "bash" tool is the biggest lateral-movement risk in office settings;
// this wraps every command with DLP scanning, role policy, and audit receipts
// before anything executes.
package shellcontrol

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// Engine is the minimal decision surface the shell gate needs.
type Engine interface {
	EvaluatePre(ctx context.Context, c *api.ToolCall) api.Decision
}

// Gate decides and (optionally) runs a shell command.
type Gate struct {
	Engine    Engine
	Principal func() api.Principal // identity provider for the current user
	WorkDir   string
}

// dangerous patterns — deterministic local rules, always BLOCK.
var dangerous = []*regexp.Regexp{
	// --- Linux/macOS ---
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\b`),
	regexp.MustCompile(`(?i)\bdrop\s+(table|database)\b`),
	regexp.MustCompile(`(?i)\bshutdown\b`),
	regexp.MustCompile(`:\(\)\{.*\};:`), // fork bomb
	regexp.MustCompile(`(?i)\bmkfs\b`),
	regexp.MustCompile(`(?i)\bformat\s+[a-z]:`),
	regexp.MustCompile(`(?i)\bchmod\s+777\s+/`),
	// --- Windows / PowerShell ---
	regexp.MustCompile(`(?i)Remove-Item\s+.*(-Recurse|-Force)`),
	regexp.MustCompile(`(?i)\brd\s+/s\s+/q`),
	regexp.MustCompile(`(?i)\brmdir\s+/s\s+/q`),
	regexp.MustCompile(`(?i)\bformat\s+[a-zA-Z]:`),
	// PowerShell-specific dangerous operations
	regexp.MustCompile(`(?i)Set-ExecutionPolicy\s+(Bypass|Unrestricted)`),
	regexp.MustCompile(`(?i)Invoke-Expression\b`),
	regexp.MustCompile(`(?i)\biex\b`), // iex alias for Invoke-Expression
	regexp.MustCompile(`(?i)Start-Process\s+.*-Verb\s+RunAs`),
	regexp.MustCompile(`(?i)Invoke-WebRequest\s+.*-OutFile`), // download payload
	regexp.MustCompile(`(?i)DownloadString\s*\(`),            // common in PS attacks
	regexp.MustCompile(`(?i)Net-WebClient`),                  // WebClient download
	regexp.MustCompile(`(?i)\bnet\s+user\s+\w+\s+/add`),      // add user
	regexp.MustCompile(`(?i)\bnet\s+localgroup\s+administrators`), // escalate to admin
}

// secret patterns — REDACT candidates when a command would ship them out.
var secrets = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// Verdict carries what happened to a command.
type Verdict struct {
	Decision   api.Decision
	Sanitized  string // possibly rewritten command
	Executed   bool
	Output     string
	Err        error
}

// Run gates then executes `cmdline` via the system shell.
func (g *Gate) Run(ctx context.Context, cmdline string) *Verdict {
	v := &Verdict{Sanitized: cmdline}

	// --- axis: data/network (local deterministic rules first, cheap + strict)
	for _, re := range dangerous {
		if re.MatchString(cmdline) {
			v.Decision = blockDec("shell: dangerous pattern matched: " + truncate(re.String(), 60))
			return v
		}
	}
	for _, re := range secrets {
		if re.MatchString(cmdline) && egressy(cmdline) {
			v.Decision = redactDec("shell: outbound payload contains credential-like token")
			return v
		}
	}

	// --- full three-axis evaluation through the engine registry
	c := &api.ToolCall{
		CallID:    fmt.Sprintf("sh-%d", nowUnixNano()),
		Principal: g.Principal(),
		ToolID:    "shell.bash",
		Resource:  "host.shell",
		Action:    "execute",
		Arguments: []byte(mustJSON(map[string]string{"command": cmdline})),
	}
	dec := g.Engine.EvaluatePre(ctx, c)
	v.Decision = dec

	switch dec.Final {
	case api.VerdictBlock:
		return v
	case api.VerdictConfirm:
		// callers without an approver treat CONFIRM as deny (fail-closed)
		return v
	}

	// --- execute
	cmd := exec.CommandContext(ctx, shellBin(), shellFlag(), cmdline)
	cmd.Dir = g.WorkDir
	out, err := cmd.CombinedOutput()
	v.Executed = true
	v.Output = string(out)
	v.Err = err

	// --- post-scan output: secrets in results get scrubbed before returning
	o := v.Output
	for _, re := range secrets {
		o = re.ReplaceAllString(o, "***")
	}
	v.Output = o
	return v
}

// egressy reports whether the command line looks like it ships data off-box.
func egressy(cmdline string) bool {
	s := strings.ToLower(cmdline)
	return strings.Contains(s, "curl") || strings.Contains(s, "wget") ||
		strings.Contains(s, "http") || strings.Contains(s, "scp") ||
		strings.Contains(s, "send-mail") || strings.Contains(s, "| mail")
}

func blockDec(reason string) api.Decision {
	return api.Decision{Final: api.VerdictBlock, Risk: 95, Rationale: reason}
}

func redactDec(reason string) api.Decision {
	return api.Decision{Final: api.VerdictRedact, Risk: 75, Rationale: reason}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func mustJSON(v any) []byte {
	b, _ := jsonMarshal(v)
	return b
}
