package agentregistry

import (
	"fmt"
	"time"
)

// ProtectionMode is the administrative protection state of an agent.
// NORMAL = full policy enforcement; QUARANTINE = read-only + local analysis
// only; KILL = all tool calls denied.
type ProtectionMode string

const (
	ModeNormal     ProtectionMode = "normal"
	ModeQuarantine ProtectionMode = "quarantine"
	ModeKill       ProtectionMode = "kill"
)

// Allows reports whether the mode permits a tool call classified by its
// side-effect profile. destructive = rm -rf / etc; transmits = external
// network egress; writes = file/system mutation.
func (m ProtectionMode) Allows(destructive, transmits, writes bool) (bool, string) {
	switch m {
	case ModeKill:
		return false, "Agent administratively suspended (KILL)"
	case ModeQuarantine:
		if destructive || transmits || writes {
			return false, "Agent quarantined: writes/network/destructive denied"
		}
	}
	return true, ""
}

// IsValid reports whether s is a valid protection mode.
func IsValidMode(s string) bool {
	switch ProtectionMode(s) {
	case ModeNormal, ModeQuarantine, ModeKill:
		return true
	}
	return false
}

// SetMode records the protection mode on an agent's durable record.
func (r *Registry) SetMode(agentID string, mode ProtectionMode, actor string) (Record, error) {
	rec, ok := r.Get(agentID)
	if !ok {
		return Record{}, fmt.Errorf("agent %s not found", agentID)
	}
	rec.ProtectionMode = mode
	rec.StateChangedAt = time.Now().UTC()
	rec.StateChangedBy = actor
	rec.Changes = append(rec.Changes, Change{
		At:     time.Now().UTC(),
		Field:  "protection_mode",
		From:   string(rec.ProtectionMode),
		To:     string(mode),
		Source: actor,
	})
	if err := r.Upsert(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// ModeOf returns the current protection mode of an agent (default normal).
func (r *Registry) ModeOf(agentID string) ProtectionMode {
	rec, ok := r.Get(agentID)
	if !ok {
		return ModeNormal
	}
	if !IsValidMode(string(rec.ProtectionMode)) {
		return ModeNormal
	}
	return rec.ProtectionMode
}
