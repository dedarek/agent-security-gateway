package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReporterOmitsAuthorizationWhenTenantKeyIsEmpty(t *testing.T) {
	var gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"decision":"allow","reason":"ok"}`))
	}))
	defer srv.Close()

	r := newReporter(srv.URL, "", t.TempDir()+"/events", "public", "public-agent-1")
	if err := r.ship([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "" {
		t.Fatalf("expected no authorization header without tenant key, got %q", gotAuthorization)
	}
	result, _, err := r.hubCheck(context.Background(), "session", "ag", "tool", []byte(`{}`))
	if err != nil || result != "ALLOW" {
		t.Fatalf("hub-check failed: result=%q err=%v", result, err)
	}
	if gotAuthorization != "" {
		t.Fatalf("expected no authorization header on hub-check without tenant key, got %q", gotAuthorization)
	}
}
