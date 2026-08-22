// Package driftguard detects agent config tampering: it hashes the MCP/agent
// config files it manages and periodically re-verifies them. A file whose hash
// changed without a corresponding registry sync is reported (and optionally
// auto-restored) — this closes the "user edits config to bypass the gateway"
// hole.
package driftguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"time"
)

type Watch struct {
	Path       string
	LastHash   string
	OnDrift    func(path, oldHash, newHash string)
	AutoRepair func(path string) ([]byte, bool) // returns known-good content if available
}

type Guard struct {
	mu     sync.Mutex
	watch  map[string]*Watch
	stop   chan struct{}
	interval time.Duration
	drifts []string
}

func New(interval time.Duration) *Guard {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Guard{watch: map[string]*Watch{}, stop: make(chan struct{}), interval: interval}
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Add starts watching a file. current content becomes the trusted baseline.
func (g *Guard) Add(path string, onDrift func(path, old, new string), repair func(path string) ([]byte, bool)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, _ := hashFile(path)
	g.watch[path] = &Watch{Path: path, LastHash: h, OnDrift: onDrift, AutoRepair: repair}
}

func (g *Guard) Start() {
	go func() {
		for {
			select {
			case <-g.stop:
				return
			case <-time.After(g.interval):
				g.check()
			}
		}
	}()
}

func (g *Guard) Stop() { close(g.stop) }

func (g *Guard) check() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, w := range g.watch {
		h, err := hashFile(w.Path)
		if err != nil {
			continue // file may legitimately not exist yet
		}
		if h != w.LastHash {
			old := w.LastHash
			w.LastHash = h
			g.drifts = append(g.drifts, time.Now().Format(time.RFC3339)+" "+w.Path)
			if w.OnDrift != nil {
				w.OnDrift(w.Path, old, h)
			}
			// auto-restore when we know the good content
			if w.AutoRepair != nil {
				if good, ok := w.AutoRepair(w.Path); ok {
					_ = os.WriteFile(w.Path, good, 0o644)
					if h2, err := hashFile(w.Path); err == nil {
						w.LastHash = h2
					}
				}
			}
		}
	}
}

// Drifts returns recorded tamper events.
func (g *Guard) Drifts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.drifts))
	copy(out, g.drifts)
	return out
}
