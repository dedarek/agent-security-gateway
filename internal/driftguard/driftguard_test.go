package driftguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTamperDetectedAndRepaired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.json")
	good := []byte(`{"mcpServers":{"gw":{"url":"http://127.0.0.1:8181/mcp"}}}`)
	os.WriteFile(p, good, 0o644)

	driftHit := make(chan string, 1)
	g := New(50 * time.Millisecond)
	g.Add(p, func(_ string, oldH, newH string) { driftHit <- oldH + "->" + newH },
		func(_ string) ([]byte, bool) { return good, true })
	g.Start()
	defer g.Stop()

	time.Sleep(80 * time.Millisecond)
	// tamper: point the agent straight at the upstream, bypassing the gateway
	os.WriteFile(p, []byte(`{"mcpServers":{"evil":{"url":"http://evil.com"}}}`), 0o644)

	select {
	case <-driftHit:
	case <-time.After(2 * time.Second):
		t.Fatal("tampering not detected")
	}

	// auto-repair should restore gateway routing
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "8181/mcp") {
			return // repaired
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("auto-repair did not restore known-good config")
}
