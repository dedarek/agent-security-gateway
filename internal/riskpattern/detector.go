// Package riskpattern implements cumulative risk detection: patterns that
// only emerge across multiple events within a single trace, where each
// individual event may look benign.
//
// Uses a sliding-window approach over the session's ordered event list to
// detect sequences like: read_sensitive → write_external, or repeated
// failed attempts (reconnaissance), or privilege escalation chains.
package riskpattern

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Event is a minimal view of a gateway event for pattern matching.
type Event struct {
	ToolID   string
	Action   string // read | write | execute | network
	Verdict  string // ALLOW | BLOCK | REDACT | CONFIRM
	Risk     int
	Category string // e.g. "sensitive_data", "external_target", "destructive"
	Time     time.Time
}

// Pattern defines a multi-event sequence that indicates elevated risk.
type Pattern struct {
	Name        string
	Description string
	MinEvents   int
	Window      time.Duration // max time span for the sequence
	// Match checks if the last N events in this session match this pattern
	Match func(events []Event) bool
}

var patterns = []Pattern{
	{
		Name:        "read_then_egress",
		Description: "Sensitive data was read, followed by external network activity",
		MinEvents:   2,
		Window:      5 * time.Minute,
		Match: func(evs []Event) bool {
			var hasRead, hasEgress bool
			readIdx, egressIdx := -1, -1
			for i, e := range evs {
				if strings.Contains(strings.ToLower(e.ToolID), "inbox") ||
					strings.Contains(strings.ToLower(e.ToolID), "customer") ||
					strings.Contains(strings.ToLower(e.Category), "sensitive") {
					hasRead = true
					readIdx = i
				}
				if strings.Contains(strings.ToLower(e.ToolID), "send_email") ||
					strings.Contains(strings.ToLower(e.ToolID), "http_post") ||
					e.Action == "network" {
					hasEgress = true
					egressIdx = i
				}
			}
			return hasRead && hasEgress && readIdx < egressIdx
		},
	},
	{
		Name:        "repeated_denials",
		Description: "Agent repeatedly attempting blocked operations — possible reconnaissance or persistence",
		MinEvents:   3,
		Window:      10 * time.Minute,
		Match: func(evs []Event) bool {
			blocks := 0
			for _, e := range evs {
				if e.Verdict == "BLOCK" {
					blocks++
				}
			}
			return blocks >= 2
		},
	},
	{
		Name:        "privilege_escalation_chain",
		Description: "Read secret → write operation → external send in one trace",
		MinEvents:   3,
		Window:      10 * time.Minute,
		Match: func(evs []Event) bool {
			hasSecret := false
			for _, e := range evs {
				if strings.Contains(strings.ToLower(e.ToolID), "secret") ||
					strings.Contains(strings.ToLower(e.ToolID), "credential") {
					hasSecret = true
				}
				if hasSecret && (strings.Contains(strings.ToLower(e.ToolID), "send") ||
					strings.Contains(strings.ToLower(e.ToolID), "post")) {
					return true
				}
			}
			return false
		},
	},
	{
		Name:        "bulk_reconnaissance",
		Description: "Multiple read operations across different data types in short succession",
		MinEvents:   4,
		Window:      5 * time.Minute,
		Match: func(evs []Event) bool {
			types := map[string]bool{}
			for _, e := range evs {
				if e.Action == "read" {
					types[strings.ToLower(e.ToolID)] = true
				}
			}
			return len(types) >= 3
		},
	},
}

// Detector tracks per-trace event sequences and matches them against known
// cumulative risk patterns.
type Detector struct {
	mu        sync.Mutex
	traces    map[string][]Event // trace_id → ordered events
	maxKeep   int                // max events kept per trace (memory guard)
	hits      []Hit
}

type Hit struct {
	TraceID string
	Pattern string
	Desc    string
	Time    time.Time
	Tools   []string
}

func NewDetector() *Detector {
	return &Detector{
		traces:  map[string][]Event{},
		maxKeep: 100,
	}
}

// Add records an event into its trace's window and re-evaluates all patterns.
// Returns any newly matched pattern hits.
func (d *Detector) Add(traceID string, ev Event) []Hit {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.traces[traceID] = append(d.traces[traceID], ev)

	// Trim old events outside the widest window (10 min)
	cutoff := time.Now().Add(-10 * time.Minute)
	evs := d.traces[traceID]
	filtered := evs[:0]
	for _, e := range evs {
		if e.Time.After(cutoff) {
			filtered = append(filtered, e)
		}
	}
	d.traces[traceID] = filtered

	// Re-evaluate all patterns against current trace
	var newHits []Hit
	for _, p := range patterns {
		if len(filtered) >= p.MinEvents && p.Match(filtered) {
			hit := Hit{
				TraceID: traceID,
				Pattern: p.Name,
				Desc:    p.Description,
				Time:    time.Now(),
			}
			for _, e := range filtered {
				hit.Tools = append(hit.Tools, e.ToolID)
			}
			newHits = append(newHits, hit)
		}
	}

	for _, h := range newHits {
		d.hits = append(d.hits, h)
	}
	return newHits
}

// RecentHits returns the most recent N pattern hits.
func (d *Detector) RecentHits(n int) []Hit {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n > len(d.hits) {
		n = len(d.hits)
	}
	out := make([]Hit, n)
	copy(out, d.hits[len(d.hits)-n:])
	return out
}

// FormatHit renders a hit as human-readable text.
func FormatHit(h Hit) string {
	return fmt.Sprintf("[%s] %s — %s (tools involved: %s)",
		h.TraceID, h.Pattern, h.Desc, strings.Join(h.Tools, " → "))
}

var _ = fmt.Sprintf
