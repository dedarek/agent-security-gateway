package kgbridge

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestStartReusesHealthyExistingWorker(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","worker_token":"existing","graph_ready":true}`))
	}))
	defer worker.Close()

	_, portText, err := net.SplitHostPort(strings.TrimPrefix(worker.URL, "http://"))
	if err != nil {
		t.Fatalf("parse test worker address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test worker port: %v", err)
	}

	bridge := New("python-that-must-not-run", "missing-worker.py", "", port)
	if err := bridge.Start(); err != nil {
		t.Fatalf("healthy existing worker should be reused: %v", err)
	}
}
