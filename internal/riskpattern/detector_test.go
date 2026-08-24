package riskpattern

import (
	"testing"
	"time"
)

func TestReadThenEgress(t *testing.T) {
	d := NewDetector()
	base := time.Now()

	// simulate: read inbox (ALLOW), then send email to attacker
	hits := d.Add("t1", Event{ToolID: "get_inbox", Action: "read", Verdict: "ALLOW", Time: base})
	if len(hits) > 0 {
		t.Fatal("single read should not trigger")
	}
	hits = d.Add("t1", Event{ToolID: "send_email", Action: "network", Verdict: "BLOCK", Time: base.Add(time.Minute)})
	found := false
	for _, h := range hits {
		if h.Pattern == "read_then_egress" {
			found = true
		}
	}
	if !found {
		t.Fatal("read→egress pattern should trigger")
	}
}

func TestRepeatedDenials(t *testing.T) {
	d := NewDetector()
	base := time.Now()
	d.Add("t2", Event{ToolID: "get_inbox", Action: "read", Verdict: "ALLOW", Time: base})
	d.Add("t2", Event{ToolID: "delete_user", Verdict: "BLOCK", Time: base.Add(time.Second)})
	hits := d.Add("t2", Event{ToolID: "delete_user", Verdict: "BLOCK", Time: base.Add(2 * time.Second)})
	found := false
	for _, h := range hits {
		if h.Pattern == "repeated_denials" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("2+ BLOCKs should trigger repeated_denials")
	}
}

func TestSecretThenSend(t *testing.T) {
	d := NewDetector()
	base := time.Now()
	d.Add("t3", Event{ToolID: "get_inbox", Action: "read", Verdict: "ALLOW", Time: base})
	d.Add("t3", Event{ToolID: "read_secret", Action: "read", Verdict: "REDACT", Time: base.Add(time.Second)})
	hits := d.Add("t3", Event{ToolID: "http_post", Action: "network", Verdict: "ALLOW", Time: base.Add(30 * time.Second)})
	found := false
	for _, h := range hits {
		if h.Pattern == "privilege_escalation_chain" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("secret→send chain should trigger privilege_escalation_chain")
	}
}

func TestCleanSessionNoHit(t *testing.T) {
	d := NewDetector()
	base := time.Now()
	h1 := d.Add("t4", Event{ToolID: "get_inbox", Action: "read", Verdict: "ALLOW", Time: base})
	h2 := d.Add("t4", Event{ToolID: "read_customer_db", Action: "read", Verdict: "ALLOW", Time: base.Add(time.Second)})
	if len(h1)+len(h2) > 0 {
		t.Fatal("normal reads should not trigger patterns")
	}
}
