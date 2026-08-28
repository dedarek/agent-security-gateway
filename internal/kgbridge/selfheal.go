package kgbridge

import (
	"log"
	"time"
)

// GraphStats is the honest view of what the worker actually holds.
// A live process is NOT the same thing as a populated graph.
type GraphStats struct {
	Status     string `json:"status"`
	GraphReady bool   `json:"graph_ready"`
	NodeCount  int    `json:"node_count"`
	EdgeCount  int    `json:"edge_count"`
	IngestedAt int64  `json:"ingested_at"`
}

// GraphStats queries the worker /health and decodes the honest counters.
func (b *Bridge) GraphStats() (GraphStats, error) {
	var st GraphStats
	h, err := b.Health()
	if err != nil {
		return st, err
	}
	if v, ok := h["status"].(string); ok {
		st.Status = v
	}
	if v, ok := h["graph_ready"].(bool); ok {
		st.GraphReady = v
	}
	st.NodeCount = asInt(h["node_count"])
	st.EdgeCount = asInt(h["edge_count"])
	st.IngestedAt = int64(asInt(h["ingested_at"]))
	return st, nil
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// LastStats returns the most recent stats observed by the self-heal loop.
func (b *Bridge) LastStats() (GraphStats, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastStats, b.haveStats
}

// EnsureGraph performs one self-heal probe: if the worker reports an empty
// graph while the local store still has events, replayFn re-ingests them.
// Idempotent — a non-empty graph is left alone.
func (b *Bridge) EnsureGraph(replayFn func() error, localEventCount func() int) error {
	if replayFn == nil {
		return nil
	}
	st, err := b.GraphStats()
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.lastStats, b.haveStats = st, true
	b.mu.Unlock()

	if st.GraphReady && st.NodeCount > 0 {
		return nil
	}
	n := 0
	if localEventCount != nil {
		n = localEventCount()
	}
	if n == 0 {
		return nil
	}
	log.Printf("[kg] worker graph empty (node_count=%d), re-ingesting %d events", st.NodeCount, n)
	if err := replayFn(); err != nil {
		log.Printf("[kg] self-heal re-ingest failed: %v", err)
		return err
	}
	if after, err := b.GraphStats(); err == nil {
		b.mu.Lock()
		b.lastStats, b.haveStats = after, true
		b.mu.Unlock()
		log.Printf("[kg] self-heal complete: node_count=%d edge_count=%d", after.NodeCount, after.EdgeCount)
	}
	return nil
}

// StartSelfHeal runs EnsureGraph on a ticker until the returned stop func is
// called. This is what survives a worker restart without a gateway restart.
func (b *Bridge) StartSelfHeal(interval time.Duration, replayFn func() error, localEventCount func() int) func() {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = b.EnsureGraph(replayFn, localEventCount)
			}
		}
	}()
	return func() { close(stop) }
}
