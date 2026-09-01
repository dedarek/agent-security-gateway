package agentregistry

import "testing"

func TestModeAllows(t *testing.T) {
	cases := []struct {
		name                           string
		mode                           ProtectionMode
		destructive, transmits, writes bool
		wantAllow                      bool
	}{
		{"normal allows all", ModeNormal, false, false, false, true},
		{"normal allows write", ModeNormal, false, false, true, true},
		{"quarantine blocks write", ModeQuarantine, false, false, true, false},
		{"quarantine blocks transmit", ModeQuarantine, false, true, false, false},
		{"quarantine blocks destructive", ModeQuarantine, true, false, false, false},
		{"quarantine allows read", ModeQuarantine, false, false, false, true},
		{"kill blocks read", ModeKill, false, false, false, false},
		{"kill blocks everything", ModeKill, true, true, true, false},
	}
	for _, c := range cases {
		allow, reason := c.mode.Allows(c.destructive, c.transmits, c.writes)
		if allow != c.wantAllow {
			t.Errorf("%s: Allows=%v want %v (reason=%q)", c.name, allow, c.wantAllow, reason)
		}
	}
}

func TestIsValidMode(t *testing.T) {
	if !IsValidMode("normal") || !IsValidMode("quarantine") || !IsValidMode("kill") {
		t.Fatal("valid modes not recognized")
	}
	if IsValidMode("banana") || IsValidMode("") {
		t.Fatal("invalid mode accepted")
	}
}

func TestRegistrySetModeRoundTrip(t *testing.T) {
	reg, err := Open(t.TempDir() + "/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	// upsert a base record
	rec := Record{AgentID: "ag1", SessionID: "s1", Status: "active", AgentType: "claude_code"}
	if err := reg.Upsert(rec); err != nil {
		t.Fatal(err)
	}
	// default mode normal
	if got := reg.ModeOf("ag1"); got != ModeNormal {
		t.Fatalf("default mode = %s, want normal", got)
	}
	// set quarantine
	updated, err := reg.SetMode("ag1", ModeQuarantine, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProtectionMode != ModeQuarantine {
		t.Fatalf("updated mode = %s", updated.ProtectionMode)
	}
	if got := reg.ModeOf("ag1"); got != ModeQuarantine {
		t.Fatalf("ModeOf = %s, want quarantine", got)
	}
	// unknown agent defaults normal
	if got := reg.ModeOf("nope"); got != ModeNormal {
		t.Fatalf("unknown agent mode = %s, want normal", got)
	}
}
