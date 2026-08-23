package kg

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha8(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}
