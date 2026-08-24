// Package escalation implements alert escalation chains: when LOW/MEDIUM
// severity FLAGs accumulate within a time window, they automatically escalate
// to HIGH alerts visible in the console and notification system.
package escalation

import (
	"fmt"
	"sync"
	"time"
)

type Flag struct {
	SessionID string
	Category  string // e.g. "data_exfiltration", "reconnaissance"
	Timestamp time.Time
}

type Alert struct {
	Level       string    `json:"level"` // "ESCALATED"
	Category    string    `json:"category"`
	Count       int       `json:"count"`
	Window      string    `json:"window"`
	Message     string    `json:"message"`
	Sessions    []string  `json:"sessions"`
	Timestamp   time.Time `json:"timestamp"`
}

type Chain struct {
	mu          sync.Mutex
	flags       []Flag
	window      time.Duration
	threshold   int
	alerts      []Alert
	onEscalate  func(Alert)
}

func New(window time.Duration, threshold int, onEscalate func(Alert)) *Chain {
	return &Chain{window: window, threshold: threshold, onEscalate: onEscalate}
}

// Add records a FLAG and checks if escalation threshold is reached.
func (c *Chain) Add(sessionID, category string) *Alert {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.flags = append(c.flags, Flag{SessionID: sessionID, Category: category, Timestamp: now})

	// Prune old flags outside window
	var active []Flag
	for _, f := range c.flags {
		if now.Sub(f.Timestamp) <= c.window { active = append(active, f) }
	}
	c.flags = active

	if len(active) < c.threshold { return nil }

	// Count by category
	catCount := map[string]int{}
	sessions := map[string]bool{}
	for _, f := range active {
		catCount[f.Category]++
		sessions[f.SessionID] = true
	}

	for cat, count := range catCount {
		if count >= c.threshold {
			alert := Alert{
				Level:     "ESCALATED",
				Category:  cat,
				Count:     count,
				Window:    c.window.String(),
				Message:   fmt.Sprintf("%d %s flags in %s — possible coordinated activity", count, cat, c.window),
				Sessions:  mapKeys(sessions),
				Timestamp: now,
			}
			c.alerts = append(c.alerts, alert)
			// Clear escalated flags to avoid re-triggering
			c.flags = nil
			if c.onEscalate != nil { go c.onEscalate(alert) }
			return &alert
		}
	}
	return nil
}

func (c *Chain) RecentAlerts(n int) []Alert {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > len(c.alerts) { n = len(c.alerts) }
	out := make([]Alert, n)
	copy(out, c.alerts[len(c.alerts)-n:])
	return out
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m { out = append(out, k) }
	return out
}
