// Package receipt implements mediator-signed action receipts, reusing the design
// of Pipelock's internal/receipt (luckyPipewrench/pipelock): an Ed25519 signature
// over SHA-256 of a canonical ActionRecord, chained into a tamper-evident
// sequence via chain_prev_hash / chain_seq, bound to one process run by run_nonce.
//
// Field names and the action taxonomy (action_type / side_effect_class /
// reversibility) mirror Pipelock so receipts are schema-compatible with its
// documented format. Signing digest = SHA-256(canonicalJSON(ActionRecord)),
// signature string = "ed25519:" + hex. See docs/BASE-PROJECTS-ANALYSIS.md §2.4
// and docs/ARCHITECTURE.md §9.
package receipt

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	receiptVersion  = 1
	signaturePrefix = "ed25519:"
	genesis         = "genesis"
)

// ActionRecord is the signed payload. Field order/tags mirror Pipelock's
// canonical v1 projection (the subset relevant to the gateway MVP).
type ActionRecord struct {
	Version         int       `json:"version"`
	ActionID        string    `json:"action_id"`
	ParentActionID  string    `json:"parent_action_id,omitempty"`
	ActionType      string    `json:"action_type"` // read|derive|write|delegate|authorize|spend|commit|actuate
	Timestamp       time.Time `json:"timestamp"`
	Principal       string    `json:"principal"`
	Actor           string    `json:"actor"`
	DelegationChain []string  `json:"delegation_chain"`
	Target          string    `json:"target"`
	Intent          string    `json:"intent,omitempty"`
	DataClassesIn   []string  `json:"data_classes_in,omitempty"`
	DataClassesOut  []string  `json:"data_classes_out,omitempty"`
	SideEffectClass string    `json:"side_effect_class"` // none|local|external|irreversible
	Reversibility   string    `json:"reversibility"`     // reversible|partial|irreversible
	PolicyHash      string    `json:"policy_hash"`
	Verdict         string    `json:"verdict"`
	SessionID       string    `json:"session_id,omitempty"`
	Transport       string    `json:"transport"`
	Method          string    `json:"method,omitempty"`
	ChainPrevHash   string    `json:"chain_prev_hash"`
	ChainSeq        uint64    `json:"chain_seq"`
	RunNonce        string    `json:"run_nonce,omitempty"`
}

// Receipt is the signed envelope.
type Receipt struct {
	Version      int             `json:"version"`
	ActionRecord ActionRecord    `json:"action_record"`
	Signature    string          `json:"signature"`
	SignerKey    string          `json:"signer_key"`
	Ext          json.RawMessage `json:"ext,omitempty"`
}

// canonicalActionRecord is the frozen signing projection (json.Marshal in field
// declaration order), matching Pipelock's canonicalActionRecordV1 approach.
func canonicalActionRecord(ar ActionRecord) ([]byte, error) {
	return json.Marshal(ar)
}

// Sign produces a signed Receipt for an ActionRecord.
func Sign(ar ActionRecord, priv ed25519.PrivateKey) (Receipt, error) {
	ar.Version = receiptVersion
	data, err := canonicalActionRecord(ar)
	if err != nil {
		return Receipt{}, err
	}
	sum := sha256.Sum256(data)
	sig := ed25519.Sign(priv, sum[:])
	pub := priv.Public().(ed25519.PublicKey)
	return Receipt{
		Version:      receiptVersion,
		ActionRecord: ar,
		Signature:    signaturePrefix + hex.EncodeToString(sig),
		SignerKey:    hex.EncodeToString(pub),
	}, nil
}

// ReceiptHash = SHA-256(json.Marshal(receipt)), used to chain the next receipt.
func ReceiptHash(r Receipt) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyWithKey checks a receipt's signature against an expected signer key (hex).
func VerifyWithKey(r Receipt, expectedKeyHex string) error {
	if r.SignerKey != expectedKeyHex {
		return fmt.Errorf("signer key mismatch")
	}
	if len(r.Signature) <= len(signaturePrefix) || r.Signature[:len(signaturePrefix)] != signaturePrefix {
		return fmt.Errorf("bad signature prefix")
	}
	sigHex := r.Signature[len(signaturePrefix):]
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("bad signature hex: %w", err)
	}
	pub, err := hex.DecodeString(r.SignerKey)
	if err != nil {
		return fmt.Errorf("bad signer key hex: %w", err)
	}
	data, err := canonicalActionRecord(r.ActionRecord)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if !ed25519.Verify(pub, sum[:], sig) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

// Emitter signs receipts and maintains the tamper-evident chain. Concurrency-safe:
// the lock spans stamp -> sign -> hash -> advance so the chain stays monotonic.
// When filePath != "" every receipt is appended to a JSONL file and replayed on
// Open, so the chain survives restarts.
type Emitter struct {
	mu       sync.Mutex
	priv     ed25519.PrivateKey
	pubHex   string
	prevHash string
	seq      uint64
	runNonce string
	receipts []Receipt
	filePath string
	file     *os.File
}

// NewEmitter generates a fresh Ed25519 key and run nonce (memory only).
func NewEmitter() (*Emitter, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Emitter{
		priv:     priv,
		pubHex:   hex.EncodeToString(pub),
		prevHash: genesis,
		runNonce: uuid.NewString(),
	}, nil
}

// OpenEmitter creates an emitter backed by a JSONL file at path. The file is
// replayed to restore prevHash/seq so the hash chain is continuous across
// restarts. Pass "" for memory-only (demo) mode.
func OpenEmitter(path string) (*Emitter, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	e := &Emitter{
		priv:     priv,
		pubHex:   hex.EncodeToString(pub),
		prevHash: genesis,
		runNonce: uuid.NewString(),
		filePath: path,
	}
	if path == "" {
		return e, nil
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("receipt dir: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	e.file = f
	// replay
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	var last *Receipt
	for sc.Scan() {
		var r Receipt
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		e.receipts = append(e.receipts, r)
		cp := r
		last = &cp
	}
	if last != nil {
		h, err := ReceiptHash(*last)
		if err == nil {
			e.prevHash = h
			e.seq = last.ActionRecord.ChainSeq + 1
		}
	}
	return e, nil
}

// SignerKey returns the emitter's public key (hex) — the trust anchor for verification.
func (e *Emitter) SignerKey() string { return e.pubHex }

// Emit stamps chain fields, signs, records the receipt, and advances the chain.
func (e *Emitter) Emit(ar ActionRecord) (Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ar.ChainPrevHash = e.prevHash
	ar.ChainSeq = e.seq
	ar.RunNonce = e.runNonce
	rec, err := Sign(ar, e.priv)
	if err != nil {
		return Receipt{}, err
	}
	h, err := ReceiptHash(rec)
	if err != nil {
		return Receipt{}, err
	}
	e.prevHash = h
	e.seq++
	e.receipts = append(e.receipts, rec)
	if e.file != nil {
		b, _ := json.Marshal(rec)
		_, _ = e.file.Write(append(b, '\n'))
	}
	return rec, nil
}

// Receipts returns the emitted chain (copy).
func (e *Emitter) Receipts() []Receipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Receipt, len(e.receipts))
	copy(out, e.receipts)
	return out
}

// VerifyChain walks a receipt chain: signatures, monotonic seq, prev-hash links.
func VerifyChain(receipts []Receipt, trustedKeyHex string) error {
	prev := genesis
	for i, r := range receipts {
		if err := VerifyWithKey(r, trustedKeyHex); err != nil {
			return fmt.Errorf("receipt %d: %w", i, err)
		}
		if r.ActionRecord.ChainSeq != uint64(i) {
			return fmt.Errorf("receipt %d: seq mismatch got %d", i, r.ActionRecord.ChainSeq)
		}
		if r.ActionRecord.ChainPrevHash != prev {
			return fmt.Errorf("receipt %d: broken chain link", i)
		}
		h, err := ReceiptHash(r)
		if err != nil {
			return err
		}
		prev = h
	}
	return nil
}
