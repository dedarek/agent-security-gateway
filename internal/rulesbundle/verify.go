// Package rulesbundle verifies Pipelock rule bundles before they are loaded.
//
// A bundle ships as YAML plus a detached Ed25519 signature (<file>.sig,
// base64-encoded, signed over the exact file bytes) published in the
// luckyPipewrench/pipelock-rules repo. The Gateway embeds the OFFICIAL
// Pipelock rules-signing public key as its trust root and refuses to start on
// any verification failure: a tampered or unsigned rule bundle is a supply-
// chain attack on every downstream decision, so this is fail-closed by design.
package rulesbundle

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// defaultKeyringHex is the published official Pipelock rules signing public key
// ("pipelock-official-rules"). Public information, not a secret; mirrors
// pipelock's internal/rules/keyring.go DefaultKeyringHex.
var defaultKeyringHex = "7051d8082f3a369886d25e847e3827b4b4263f9d28cb070104b606c9fb07ae82"

// extraTrustedKeysHex holds additional operator-configured third-party keys
// (comma-separated hex). Empty by default.
var extraTrustedKeysHex string

// SetExtraTrustedKeys registers additional trusted third-party signing keys
// (comma-separated hex Ed25519 public keys).
func SetExtraTrustedKeys(hexKeys string) { extraTrustedKeysHex = hexKeys }

// Keyring returns the deduplicated set of trusted Ed25519 public keys:
// the embedded official key first, then operator extras.
func Keyring() []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	seen := map[string]struct{}{}
	for _, ring := range []string{defaultKeyringHex, extraTrustedKeysHex} {
		for _, part := range strings.Split(ring, ",") {
			h := strings.TrimSpace(part)
			if h == "" {
				continue
			}
			raw, err := hex.DecodeString(h)
			if err != nil || len(raw) != ed25519.PublicKeySize {
				continue // invalid entries are skipped, matching upstream behavior
			}
			if _, dup := seen[string(raw)]; dup {
				continue
			}
			seen[string(raw)] = struct{}{}
			keys = append(keys, ed25519.PublicKey(raw))
		}
	}
	return keys
}

// LoadVerified reads bundlePath, verifies its detached signature against the
// trusted keyring, and returns the exact verified bytes. Callers must parse
// THESE bytes (not re-read from disk) to avoid a TOCTOU window where the file
// is swapped between verification and parsing.
//
// Fail-closed: missing signature file, malformed signature, unknown signer, or
// a signature that does not verify all return an error.
func LoadVerified(bundlePath string) ([]byte, error) {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("read rule bundle: %w", err)
	}
	sigRaw, err := os.ReadFile(bundlePath + ".sig")
	if err != nil {
		return nil, fmt.Errorf("rule bundle signature missing (%s.sig): %w", bundlePath, err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return nil, fmt.Errorf("malformed rule bundle signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("rule bundle signature: bad length %d", len(sig))
	}
	for _, key := range Keyring() {
		if ed25519.Verify(key, data, sig) {
			return data, nil
		}
	}
	return nil, fmt.Errorf("rule bundle signature verification FAILED against all %d trusted keys — refusing to load", len(Keyring()))
}
