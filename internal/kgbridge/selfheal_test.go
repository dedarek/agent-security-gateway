package kgbridge

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bridgeForServer(t *testing.T, url string) *Bridge {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatalf("parse addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return New("python-that-must-not-run", "missing-worker.py", "", port)
}

// A worker that reports an EMPTY graph must trigger a re-ingest.
func TestEnsureGraphReingestsWhenWorkerGraphEmpty(t *testing.T) {
	var mu sync.Mutex
	nodeCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok", "graph_ready": nodeCount > 0,
				"node_count": nodeCount, "edge_count": 0,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	b := bridgeForServer(t, srv.URL)
	var calls int32
	replay := func() error {
		atomic.AddInt32(&calls, 1)
		mu.Lock()
		nodeCount = 5
		mu.Unlock()
		return nil
	}
	if err := b.EnsureGraph(replay, func() int { return 12 }); err != nil {
		t.Fatalf("EnsureGraph: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 replay, got %d", calls)
	}
	// Second call: graph now non-empty → must NOT replay again (idempotent).
	if err := b.EnsureGraph(replay, func() int { return 12 }); err != nil {
		t.Fatalf("EnsureGraph 2: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected no second replay, got %d", calls)
	}
}

// No local events → nothing to re-ingest, must not call replay.
func TestEnsureGraphSkipsWhenNoLocalEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","graph_ready":false,"node_count":0}`))
	}))
	defer srv.Close()
	b := bridgeForServer(t, srv.URL)
	called := false
	if err := b.EnsureGraph(func() error { called = true; return nil }, func() int { return 0 }); err != nil {
		t.Fatalf("EnsureGraph: %v", err)
	}
	if called {
		t.Fatal("replay must not run when there are no local events")
	}
}

// The background watcher must self-heal without a gateway restart.
func TestStartSelfHealLoopRecoversEmptyGraph(t *testing.T) {
	var mu sync.Mutex
	nodeCount := 7
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "graph_ready": nodeCount > 0, "node_count": nodeCount,
		})
	}))
	defer srv.Close()

	b := bridgeForServer(t, srv.URL)
	var calls int32
	stop := b.StartSelfHeal(50*time.Millisecond, func() error {
		atomic.AddInt32(&calls, 1)
		mu.Lock()
		nodeCount = 7
		mu.Unlock()
		return nil
	}, func() int { return 3 })
	defer stop()

	// simulate worker restart: memory wiped
	mu.Lock()
	nodeCount = 0
	mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := nodeCount
		mu.Unlock()
		if n > 0 && atomic.LoadInt32(&calls) > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("self-heal loop did not re-ingest (calls=%d)", atomic.LoadInt32(&calls))
}

// Health must expose the honest counters for the status API.
func TestGraphStatsReadsHonestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","graph_ready":true,"node_count":167,"edge_count":198,"ingested_at":1700000000}`))
	}))
	defer srv.Close()
	b := bridgeForServer(t, srv.URL)
	st, err := b.GraphStats()
	if err != nil {
		t.Fatalf("GraphStats: %v", err)
	}
	if st.NodeCount != 167 || st.EdgeCount != 198 || !st.GraphReady || st.IngestedAt != 1700000000 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}
