// Package engine implements streaming response inspection: the data/network
// axis scans upstream output IN CHUNKS as it streams, so a mid-stream prompt
// injection or secret is caught before the full payload reaches the agent.
// It plugs into the engine.Stream interface (Peek).
package engine

import (
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// ChunkScanner wraps the compiled Pipelock rules for incremental scanning.
type ChunkScanner struct {
	injection []compiledRule
	dlp       []compiledRule
	buffer    strings.Builder
	carried   string // overlap between chunks so tokens split across reads are still caught
}

const carryLen = 256

// NewChunkScanner reuses the DataNetworkEngine's compiled rules.
func NewChunkScanner(dn *DataNetworkEngine) *ChunkScanner {
	return &ChunkScanner{injection: dn.injection, dlp: dn.dlp}
}

// Feed inspects one streamed chunk; returns a signal on first hit. The chunk
// is prepended with carried context from the previous chunk to catch patterns
// split across chunk boundaries.
func (cs *ChunkScanner) Feed(chunk string) *api.Signal {
	text := cs.carried + chunk
	if r, ok := scan(cs.injection, text); ok {
		return blockRuntime(r, "prompt-injection pattern detected in streamed tool result")
	}
	if r, ok := scan(cs.dlp, text); ok {
		return redactRuntime(r)
	}
	// keep tail as carry for boundary-spanning patterns
	if len(text) > carryLen {
		cs.carried = text[len(text)-carryLen:]
	} else {
		cs.carried = text
	}
	cs.buffer.WriteString(chunk)
	return nil
}

func blockRuntime(r compiledRule, reason string) *api.Signal {
	return &api.Signal{
		Axis:     api.AxisDataNetwork,
		Engine:   "datanetwork.runtime-stream",
		Score:    scoreFor(r.Severity),
		Verdict:  api.VerdictBlock,
		Reasons:  []string{reason + " [" + r.Name + "]"},
		Evidence: []api.Evidence{{Kind: "injection", Detail: r.ID + " (" + r.Severity + ")"}},
		FailMode: api.FailClosed,
	}
}

func redactRuntime(r compiledRule) *api.Signal {
	hits := map[string]bool{}
	_ = hits
	return &api.Signal{
		Axis:     api.AxisDataNetwork,
		Engine:   "datanetwork.runtime-stream",
		Score:    scoreFor(r.Severity),
		Verdict:  api.VerdictRedact,
		Reasons:  []string{"sensitive data in stream [" + r.Name + "] — post-pass will scrub"},
		Evidence: []api.Evidence{{Kind: "dlp", Detail: r.ID}},
		FailMode: api.FailClosed,
	}
}
