package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

func timeNow() time.Time { return time.Now() }

func idFor(session, what string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", session, what, hex.EncodeToString(b)[:6])
}

func mustJSONField(v any) []byte {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func decisionFromVerdict(v, reason string) api.Decision {
	var verdict api.Verdict
	switch v {
	case "BLOCK":
		verdict = api.VerdictBlock
	case "REDACT":
		verdict = api.VerdictRedact
	case "CONFIRM":
		verdict = api.VerdictConfirm
	default:
		verdict = api.VerdictAllow
	}
	return api.Decision{
		CallID:    "",
		Final:     verdict,
		Risk:      riskFor(verdict),
		Rationale: "probe: " + reason,
	}
}

func riskFor(v api.Verdict) int {
	switch v {
	case api.VerdictBlock:
		return 90
	case api.VerdictRedact:
		return 70
	case api.VerdictConfirm:
		return 50
	default:
		return 0
	}
}
