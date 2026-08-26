// Package policyversion provides git-like version management for Cedar
// policies: every change is tracked with author, timestamp, diff, and can be
// rolled back to any previous version.
package policyversion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Version struct {
	Seq       int    `json:"seq"`
	Timestamp string `json:"timestamp"`
	Author    string `json:"author"`
	Hash      string `json:"hash"` // SHA-256 of policy content
	Content   string `json:"content"`
	Comment   string `json:"comment"`
}

type Manager struct {
	mu       sync.Mutex
	path     string // version history file (JSONL)
	policyPath string // current live policy file
	Versions []Version
}

func New(policyPath, historyPath string) *Manager {
	m := &Manager{policyPath: policyPath, path: historyPath}
	// Load existing history
	if b, err := os.ReadFile(historyPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" { continue }
			var v Version
			if json.Unmarshal([]byte(line), &v) == nil {
				m.Versions = append(m.Versions, v)
			}
		}
	}
	return m
}

// Save records a new version of the policy.
func (m *Manager) Save(author, comment string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, err := os.ReadFile(m.policyPath)
	if err != nil { return err }

	h := sha256.Sum256(b)
	v := Version{
		Seq:       len(m.Versions) + 1,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Author:    author,
		Hash:      hex.EncodeToString(h[:]),
		Content:   string(b),
		Comment:   comment,
	}
	m.Versions = append(m.Versions, v)

	// Append to history file
	line, _ := json.Marshal(v)
	f, err := os.OpenFile(m.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil { return err }
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Rollback restores a previous version and saves the rollback as a new version.
func (m *Manager) Rollback(seq int, author string) error {
	m.mu.Lock()
	var target *Version
	for i := range m.Versions {
		if m.Versions[i].Seq == seq { target = &m.Versions[i]; break }
	}
	m.mu.Unlock()
	if target == nil { return fmt.Errorf("version %d not found", seq) }

	if err := os.WriteFile(m.policyPath, []byte(target.Content), 0o644); err != nil { return err }
	return m.Save(author, fmt.Sprintf("rollback to v%d", seq))
}

// Diff returns changed lines between two versions.
func Diff(a, b string) []string {
	var changes []string
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")
	maxLen := len(aLines); if len(bLines) > maxLen { maxLen = len(bLines) }
	for i := 0; i < maxLen; i++ {
		al, bl := "", ""
		if i < len(aLines) { al = aLines[i] }
		if i < len(bLines) { bl = bLines[i] }
		if al != bl {
			if al != "" { changes = append(changes, "- "+al) }
			if bl != "" { changes = append(changes, "+ "+bl) }
		}
	}
	return changes
}

func (m *Manager) History() []Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Version, len(m.Versions))
	copy(out, m.Versions)
	return out
}
