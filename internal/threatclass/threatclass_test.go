package threatclass

import "testing"

func TestDataExfilClassified(t *testing.T) {
	cat := Classify("send_email", "network", true, false, false)
	if cat != DataExfiltration {
		t.Fatalf("send_email should classify as data_exfiltration, got %s", cat)
	}
}

func TestDestructiveClassified(t *testing.T) {
	cat := Classify("delete_user", "execute", false, true, false)
	if cat != Destructive {
		t.Fatalf("delete_user should be destructive, got %s", cat)
	}
}

func TestReconnaissanceDetected(t *testing.T) {
	cat := Classify("get_inbox", "read", false, false, false)
	if cat != Reconnaissance {
		t.Fatalf("inbox read should be reconnaissance, got %s", cat)
	}
}

func TestDriftedPrivilegeAbuse(t *testing.T) {
	cat := Classify("read_customer_db", "read", false, false, true)
	if cat != PrivilegeAbuse {
		t.Fatalf("drifted read should be privilege_abuse, got %s", cat)
	}
}

func TestDispositionMapping(t *testing.T) {
	d := GetDisposition(DataExfiltration)
	if d.Action != "BLOCK" || !d.Escalate {
		t.Fatal("data_exfiltration must BLOCK + escalate")
	}
	d2 := GetDisposition(ContentSafety)
	if d2.Action != "FLAG" || d2.Escalate {
		t.Fatal("content_safety should FLAG without escalate")
	}
}
