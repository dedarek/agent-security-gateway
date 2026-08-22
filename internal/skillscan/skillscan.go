// Package skillscan governs the content plane: skills (and any instruction
// text) entering an agent's context are scanned for injection patterns and
// stamped with a trust level. Untrusted content becomes a taint source —
// downstream sensitive actions then trip the behavior axis automatically.
package skillscan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Trust int

const (
	Trusted Trust = iota
	Untrusted
)

func (t Trust) String() string {
	if t == Trusted {
		return "trusted"
	}
	return "untrusted"
}

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(your|the)\s+(instructions|rules|prompt)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an|no longer)`),
	regexp.MustCompile(`(?i)(send|exfiltrate|upload|post)\s+.{0,40}(env|\.ssh|credential|secret|token|password)`),
	regexp.MustCompile(`(?i)reveal\s+(your\s+)?(system\s+prompt|instructions)`),
	regexp.MustCompile(`<system>|</system>`), // fake system tags in content
}

type Report struct {
	Path       string
	Trust      Trust
	Violations []string
	Bytes      int
}

// ScanDir walks a skills directory and returns a report per .md/.txt file.
func ScanDir(dir string, trustedSources []string) ([]Report, error) {
	var reports []Report
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".txt" {
			return nil
		}
		rep := scanFile(path, trustedSources)
		reports = append(reports, rep)
		return nil
	})
	return reports, err
}

func scanFile(path string, trustedSources []string) Report {
	rep := Report{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		rep.Violations = append(rep.Violations, "unreadable: "+err.Error())
		rep.Trust = Untrusted
		return rep
	}
	rep.Bytes = len(raw)
	content := string(raw)
	for _, re := range injectionPatterns {
		if loc := re.FindStringIndex(content); loc != nil {
			ctxStart := loc[0] - 30
			if ctxStart < 0 {
				ctxStart = 0
			}
			ctxEnd := loc[1] + 30
			if ctxEnd > len(content) {
				ctxEnd = len(content)
			}
			rep.Violations = append(rep.Violations, fmt.Sprintf(
				"injection pattern %q near: %q", re.String(),
				strings.ReplaceAll(content[ctxStart:ctxEnd], "\n", " ")))
		}
	}

	// provenance: path under a trusted source root => trusted when clean
	trustFromPath := Untrusted
	for _, src := range trustedSources {
		if src != "" && strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(src)) {
			trustFromPath = Trusted
			break
		}
	}
	if len(rep.Violations) > 0 {
		rep.Trust = Untrusted // violations always taint, regardless of origin
	} else {
		rep.Trust = trustFromPath
	}
	return rep
}

// TaintTokens extracts identifiers from untrusted content for the behavior
// axis: emails/URLs appearing in a malicious skill become tracked tokens.
func TaintTokens(content string) []string {
	re := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}|https?://[^\s"']+`)
	seen := map[string]bool{}
	var out []string
	for _, tok := range re.FindAllString(content, -1) {
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}
