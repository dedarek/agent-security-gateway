package engine

import (
	"encoding/json"
	"regexp"
	"strings"
)

// This file implements the hook-path helpers for real (session-level) taint
// propagation when the gateway is wired via harness hooks instead of the MCP
// proxy. See TaintEngine.ObserveHook.

var (
	rePathLike = regexp.MustCompile(`(?:~|\.{0,2})/[A-Za-z0-9._\-/]+`)
	reRedirect = regexp.MustCompile(`>>?\s*("?)((?:~|\.{0,2})?/[A-Za-z0-9._\-/]+|[A-Za-z0-9._\-]+\.[A-Za-z0-9]+)`)
	reDashO    = regexp.MustCompile(`(?:^|\s)-o(?:utput)?[=\s]+("?)([^\s"']+)`)
	reTee      = regexp.MustCompile(`\btee\s+(?:-a\s+)?("?)([^\s"'|]+)`)
)

// sensitivePathMarkers are the file patterns whose mere READ means the session
// has touched a credential. Deliberately narrow: broad matching here would turn
// the causal axis into a path blacklist and blow up the false-positive rate.
var sensitivePathMarkers = []string{
	".aws/credentials", ".aws/config",
	".ssh/id_rsa", ".ssh/id_ed25519", ".ssh/id_dsa", ".ssh/id_ecdsa",
	".env", ".netrc", ".npmrc", ".pypirc", ".docker/config.json",
	".kube/config", ".gnupg/", "credentials.json", "service-account",
	"id_rsa", "secrets.yaml", "secrets.yml", ".pgpass",
	".config/gcloud/", ".azure/", "shadow", "private_key", "privatekey",
}

// egressCommands are the shell/network indicators that make a generic sink
// (Bash/Write) an actual egress channel.
var egressCommands = []string{
	"curl", "wget", "http://", "https://", "nc ", "netcat", "ncat",
	"scp ", "sftp", "rsync", "ftp ", "telnet", "mail ", "sendmail",
	"git push", "aws s3 cp", "gsutil cp", "az storage",
}

// alwaysEgressSinks egress by definition; no argument gate needed.
var alwaysEgressSinks = map[string]bool{
	"WebFetch": true, "WebSearch": true, "fetch": true,
	"http_post": true, "send_email": true, "export_all_users": true,
}

// isEgress reports whether this sink call actually ships data outward.
func isEgress(tool string, values []string) bool {
	if alwaysEgressSinks[tool] {
		return true
	}
	for _, v := range values {
		lv := strings.ToLower(v)
		for _, c := range egressCommands {
			if strings.Contains(lv, c) {
				return true
			}
		}
	}
	return false
}

// sensitiveTokens returns the provenance tokens for a sensitive file path. In
// addition to the literal path it emits the home-relative suffix, so a later
// reference through a different prefix (~/.aws/credentials vs
// /home/u/.aws/credentials vs $HOME/.aws/credentials) is still recognized as
// the SAME data object. Without this the causal chain silently breaks whenever
// the agent switches notation between steps.
func sensitiveTokens(p string) []string {
	toks := []string{p}
	lp := strings.ToLower(p)
	for _, m := range sensitivePathMarkers {
		if !strings.Contains(m, "/") {
			continue
		}
		if i := strings.Index(lp, m); i >= 0 {
			suffix := p[i:]
			if suffix != p && len(suffix) >= minTokenLen {
				toks = append(toks, suffix)
			}
			break
		}
	}
	return toks
}

// isSensitivePath reports whether a path denotes credential material.
func isSensitivePath(p string) bool {
	lp := strings.ToLower(p)
	for _, m := range sensitivePathMarkers {
		if strings.Contains(lp, m) {
			return true
		}
	}
	return false
}

// hookParts splits a raw hook payload into its tool_input object and the
// tool output text (tool_response), whichever the harness provided.
func hookParts(raw []byte) (map[string]any, string) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, ""
	}
	var input map[string]any
	for _, k := range []string{"tool_input", "toolInput", "input", "args", "arguments", "params"} {
		if v, ok := m[k]; ok {
			if json.Unmarshal(v, &input) == nil {
				break
			}
		}
	}
	var response string
	for _, k := range []string{"tool_response", "toolResponse", "output", "result", "response"} {
		v, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			response = s
		} else {
			response = string(v)
		}
		if strings.TrimSpace(response) != "" {
			break
		}
	}
	return input, response
}

// flattenValues returns every string leaf of a decoded JSON object.
func flattenValues(m map[string]any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(m)
	return out
}

// filePaths extracts path-like tokens from argument values.
func filePaths(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		for _, p := range rePathLike.FindAllString(v, -1) {
			p = strings.TrimRight(p, ".,;:'\"")
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// producedArtifacts returns the new files a call creates: shell redirects,
// -o/--output targets, tee targets, and Write/Edit file_path. These become
// derived taint marks when the call consumed tainted data.
func producedArtifacts(tool string, input map[string]any, values []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.Trim(strings.TrimSpace(s), `"'`)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	switch tool {
	case "Write", "Edit", "NotebookEdit", "write_file":
		for _, k := range []string{"file_path", "path", "filePath"} {
			if v, ok := input[k]; ok {
				if s, ok := v.(string); ok {
					add(s)
				}
			}
		}
	}
	for _, v := range values {
		for _, m := range reRedirect.FindAllStringSubmatch(v, -1) {
			add(m[2])
		}
		for _, m := range reDashO.FindAllStringSubmatch(v, -1) {
			add(m[2])
		}
		for _, m := range reTee.FindAllStringSubmatch(v, -1) {
			add(m[2])
		}
	}
	return out
}
