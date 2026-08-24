package drift

import "testing"

func TestOnTask(t *testing.T) {
	d := NewDetector()
	d.SetTask("s1", "帮我整理收件箱并总结邮件内容")
	r := d.Check("s1", "get_inbox")
	if r.Verdict != "ON_TASK" && r.Verdict != "PARTIAL_DRIFT" {
		t.Fatalf("reading inbox for '整理收件箱' should be on-task or partial, got %s", r.Verdict)
	}
}

func TestDriftDestructive(t *testing.T) {
	d := NewDetector()
	d.SetTask("s2", "帮我整理收件箱并总结邮件内容")
	r := d.Check("s2", "delete_user")
	if !r.ShouldFlag {
		t.Fatal("destructive tool with zero overlap must flag")
	}
	if r.DriftScore < 0.5 {
		t.Fatalf("drift score too low: %f", r.DriftScore)
	}
}

func TestNonDestructiveDriftOnlyFlags(t *testing.T) {
	d := NewDetector()
	d.SetTask("s3", "整理收件箱")
	r := d.Check("s3", "read_customer_db")
	if r.ShouldBlock {
		t.Fatal("drift alone should never BLOCK (usability guarantee)")
	}
	if !r.ShouldFlag {
		t.Fatal("drifted non-destructive call should still FLAG")
	}
}

func TestNoSessionNoDrift(t *testing.T) {
	d := NewDetector()
	r := d.Check("unknown-session", "delete_user")
	if r.ShouldFlag || r.ShouldBlock {
		t.Fatal("no session context = no drift assessment")
	}
}
