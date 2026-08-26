package webui

import (
	"fmt"
	"net/http"

	"github.com/dedarek/agent-security-gateway/internal/receipt"
)

// receiptProvider is satisfied by *receipt.Emitter.
type receiptProvider interface {
	Receipts() []receipt.Receipt
	SignerKey() string
}

var emitter receiptProvider

func (s *Server) SetEmitter(e receiptProvider) { emitter = e }

func (s *Server) RegisterReceiptAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/receipts", s.Auth.middleware(s.apiReceipts))
	mux.HandleFunc("/api/receipts/verify", s.Auth.middleware(s.apiReceiptVerify))
}

// verifyChainSegments verifies per-run segments: each signer key gets its own
// contiguous sub-chain, because a restart rotates the Ed25519 key. Within a
// segment we check signature + prev-hash link; global seq continuity is
// checked across segments separately.
func verifyChainSegments(rs []receipt.Receipt) error {
	if len(rs) == 0 {
		return nil
	}
	// global seq must be strictly increasing across the whole file
	for i := 1; i < len(rs); i++ {
		if rs[i].ActionRecord.ChainSeq != rs[i-1].ActionRecord.ChainSeq+1 {
			return fmt.Errorf("receipt %d: seq discontinuity (%d -> %d)", i,
				rs[i-1].ActionRecord.ChainSeq, rs[i].ActionRecord.ChainSeq)
		}
	}
	start := 0
	for i := 1; i <= len(rs); i++ {
		if i == len(rs) || rs[i].SignerKey != rs[start].SignerKey {
			// verify signatures within this segment (same key)
			for _, r := range rs[start:i] {
				if err := receipt.VerifyWithKey(r, rs[start].SignerKey); err != nil {
					return err
				}
			}
			// prev-hash continuity across the segment boundary
			if start > 0 {
				prevHash, err := receipt.ReceiptHash(rs[start-1])
				if err != nil {
					return err
				}
				if rs[start].ActionRecord.ChainPrevHash != prevHash {
					return fmt.Errorf("receipt %d: broken chain link at segment boundary", start)
				}
			}
			// prev-hash within segment
			for k := start + 1; k < i; k++ {
				prevHash, err := receipt.ReceiptHash(rs[k-1])
				if err != nil {
					return err
				}
				if rs[k].ActionRecord.ChainPrevHash != prevHash {
					return fmt.Errorf("receipt %d: broken chain link", k)
				}
			}
			start = i
		}
	}
	return nil
}

func (s *Server) apiReceipts(w http.ResponseWriter, _ *http.Request) {
	if emitter == nil {
		writeJSON(w, map[string]any{"receipts": []receipt.Receipt{}, "signer_key": "", "verified": false})
		return
	}
	rs := emitter.Receipts()
	verified := verifyChainSegments(rs) == nil
	writeJSON(w, map[string]any{
		"receipts":   rs,
		"signer_key": emitter.SignerKey(),
		"verified":   verified,
		"count":      len(rs),
	})
}

func (s *Server) apiReceiptVerify(w http.ResponseWriter, _ *http.Request) {
	if emitter == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "no emitter"})
		return
	}
	rs := emitter.Receipts()
	if err := verifyChainSegments(rs); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "count": len(rs)})
}
