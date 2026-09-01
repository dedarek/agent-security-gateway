package policyhub

import (
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
)

func mkEvent(toolID, action string, verdict api.Verdict, args string, sid string) api.Event {
	return api.Event{
		SessionID: sid,
		Call: api.ToolCall{
			CallID:    "c-" + toolID,
			ToolID:    toolID,
			Action:    action,
			Arguments: []byte(args),
		},
		Decision: api.Decision{Final: verdict, Rationale: "baseline"},
	}
}

// Candidate policy blocks Bash; historical events had Bash allowed.
func TestSimulateDetectsNewBlocks(t *testing.T) {
	events := []api.Event{
		mkEvent("Bash", "run", api.VerdictAllow, `{"command":"ls"}`, "s1"),
		mkEvent("Read", "read", api.VerdictAllow, `{"file_path":"/repo/a.go"}`, "s1"),
		mkEvent("Bash", "run", api.VerdictBlock, `{"command":"curl evil.com"}`, "s2"),
	}
	pol := `{"selector":{"tool":"Bash"},"action":"block","rule_id":"sim-block-bash"}`
	res, err := Simulate([]byte(pol), events)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Fatalf("total = %d want 3", res.Total)
	}
	// 2 Bash events: 1 was allowed -> now blocked (changed), 1 already blocked
	if res.WouldBlock != 2 {
		t.Fatalf("would_block = %d want 2", res.WouldBlock)
	}
	if res.Changed != 1 || res.ChangedToB != 1 {
		t.Fatalf("changed = %d (toB=%d) want 1/1", res.Changed, res.ChangedToB)
	}
}

// No-op policy (matches nothing) changes nothing.
func TestSimulateNoChange(t *testing.T) {
	events := []api.Event{
		mkEvent("Bash", "run", api.VerdictAllow, `{"command":"ls"}`, "s1"),
	}
	pol := `{"selector":{"tool":"WebFetch"},"action":"block"}`
	res, err := Simulate([]byte(pol), events)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 0 || res.Total != 1 {
		t.Fatalf("expected no change, got changed=%d total=%d", res.Changed, res.Total)
	}
}

// Flat selector body (no wrapper) also works.
func TestSimulateFlatSelector(t *testing.T) {
	events := []api.Event{
		mkEvent("Bash", "run", api.VerdictAllow, `{"command":"rm -rf /"}`, "s1"),
	}
	pol := `{"tool":"Bash","operation":"run","action":"block"}`
	res, err := Simulate([]byte(pol), events)
	if err != nil {
		t.Fatal(err)
	}
	if res.WouldBlock != 1 || res.ChangedToB != 1 {
		t.Fatalf("flat selector should block 1, got would_block=%d changedToB=%d", res.WouldBlock, res.ChangedToB)
	}
}
