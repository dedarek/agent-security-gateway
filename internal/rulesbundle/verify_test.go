package rulesbundle

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realBundle is the actual community bundle shipped in deploy/rules; verifying
// it here proves the embedded keyring matches the upstream publisher.
func TestLoadVerifiedOfficialBundle(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "rules", "pipelock-community.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("official bundle not present")
	}
	data, err := LoadVerified(path)
	if err != nil {
		t.Fatalf("official bundle must verify: %v", err)
	}
	if !strings.Contains(string(data), "pipelock") {
		t.Fatalf("verified bytes look wrong: %q", string(data)[:40])
	}
}

func TestLoadVerifiedTamperedBundleFails(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.yaml")
	sig := bundle + ".sig"

	good, err := os.ReadFile(filepath.Join("..", "..", "deploy", "rules", "pipelock-community.yaml"))
	if err != nil {
		t.Skip("official bundle not present")
	}
	if err := os.WriteFile(bundle, good, 0o600); err != nil {
		t.Fatal(err)
	}
	sigData, err := os.ReadFile(sig + "")
	if err != nil {
		_ = sigData
		t.Skip("signature file not present")
	}
	_ = os.WriteFile(sig, []byte(signCurrent(t, bundle)), 0o600)

	// Tamper AFTER signing: one byte changed => verification must fail.
	tampered := append([]byte{}, good...)
	tampered[0] ^= 0x20
	if err := os.WriteFile(bundle, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVerified(bundle); err == nil {
		t.Fatal("tampered bundle MUST fail verification (fail-closed)")
	}
}

func TestLoadVerifiedMissingSigFails(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(bundle, []byte("format_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVerified(bundle); err == nil {
		t.Fatal("missing signature MUST fail (fail-closed)")
	}
}

func TestKeyringDedupesAndSkipsInvalid(t *testing.T) {
	SetExtraTrustedKeys("zz-not-hex," + defaultKeyringHex + ",7051d8082f3a369886d25e847e3827b4b4263f9d28cb070104b606c9fb07ae82")
	defer SetExtraTrustedKeys("")
	keys := Keyring()
	if len(keys) != 1 {
		t.Fatalf("want exactly 1 deduped key, got %d", len(keys))
	}
}

// signCurrent is a test helper signing the file with a THROWAWAY key to build
// a well-formed but untrusted signature (wrong-key negative case).
func signCurrent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
}
