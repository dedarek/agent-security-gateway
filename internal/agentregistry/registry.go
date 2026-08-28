package agentregistry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is the minimal durable identity of one Agent process installation.
type Record struct {
	SessionID        string    `json:"session_id"`
	AgentID          string    `json:"agent_id"`
	ProbeID          string    `json:"probe_id"`
	MachineID        string    `json:"machine_id"`
	MachineName      string    `json:"machine_name"`
	Alias            string    `json:"alias"`
	AgentType        string    `json:"agent_type"`
	ProcessID        int       `json:"process_id"`
	OS               string    `json:"os"`
	User             string    `json:"user"`
	IP               string    `json:"ip"`
	DeclaredIPs      []string  `json:"declared_ips,omitempty"`
	ObservedIPs      []string  `json:"observed_ips,omitempty"`
	ConnectionIP     string    `json:"connection_ip,omitempty"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	DeclaredModel    string    `json:"declared_model,omitempty"`
	ObservedModel    string    `json:"observed_model,omitempty"`
	DeclaredProvider string    `json:"declared_provider,omitempty"`
	ObservedProvider string    `json:"observed_provider,omitempty"`
	Status           string    `json:"status"`
	Isolation        string    `json:"isolation"`
	SessionIDs       []string  `json:"session_ids,omitempty"`
	RegisteredAt     time.Time `json:"registered_at"`
	LastHeartbeat    time.Time `json:"last_heartbeat"`
	LastActivity     time.Time `json:"last_activity"`
	StateChangedAt   time.Time `json:"state_changed_at,omitempty"`
	StateChangedBy   string    `json:"state_changed_by,omitempty"`
	RestartCount     int       `json:"restart_count"`
	Changes          []Change  `json:"changes,omitempty"`
}

type Change struct {
	At     time.Time `json:"at"`
	Field  string    `json:"field"`
	From   string    `json:"from,omitempty"`
	To     string    `json:"to,omitempty"`
	Source string    `json:"source"`
}

var validIsolation = map[string]bool{
	"active": true, "paused": true, "restricted": true, "isolated": true,
}

const (
	activeWindow    = 5 * time.Minute
	heartbeatWindow = 2 * time.Minute
)

type Registry struct {
	mu      sync.RWMutex
	path    string
	records map[string]Record
	db      *sql.DB
}

func Open(path string) (*Registry, error) {
	r := &Registry{path: path, records: map[string]Record{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 0 {
		var loaded map[string]Record
		if err := json.Unmarshal(b, &loaded); err != nil {
			return nil, err
		}
		clean := make(map[string]Record, len(loaded))
		for _, v := range loaded {
			agentID := strings.TrimSpace(v.AgentID)
			if agentID == "" {
				continue
			}
			v.AgentID = agentID
			if v.Isolation == "" {
				v.Isolation = "active"
			}
			if len(v.ObservedIPs) == 0 && v.IP != "" {
				v.ObservedIPs = []string{v.IP}
			}
			if len(v.SessionIDs) == 0 && v.SessionID != "" {
				v.SessionIDs = []string{v.SessionID}
			}
			clean[agentID] = v
		}
		r.records = clean
	}
	return r, nil
}

// OpenWithDB opens a registry backed by SQLite. JSON path is used for one-time
// migration if the DB is empty, and kept as backup if configured.
func OpenWithDB(db *sql.DB, jsonPath string) (*Registry, error) {
	r := &Registry{path: jsonPath, records: map[string]Record{}, db: db}
	if db != nil {
		// Load from DB
		rows, err := db.Query(`SELECT record_json FROM agents`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var j string
			if err := rows.Scan(&j); err != nil {
				return nil, err
			}
			var rec Record
			if err := json.Unmarshal([]byte(j), &rec); err != nil {
				continue
			}
			if strings.TrimSpace(rec.AgentID) == "" {
				continue
			}
			r.records[rec.AgentID] = rec
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// If DB empty and JSON exists, migrate once
		if len(r.records) == 0 && jsonPath != "" {
			if b, err := os.ReadFile(jsonPath); err == nil && len(b) > 0 {
				var m map[string]Record
				if json.Unmarshal(b, &m) == nil {
					for _, rec := range m {
						if strings.TrimSpace(rec.AgentID) == "" {
							continue
						}
						r.records[rec.AgentID] = rec
						_ = upsertAgentDB(db, rec)
					}
				}
			}
		}
		return r, nil
	}
	// Fallback to JSON
	return Open(jsonPath)
}

func upsertAgentDB(db *sql.DB, rec Record) error {
	b, _ := json.Marshal(rec)
	status := rec.Status
	if strings.TrimSpace(status) == "" {
		status = "offline"
	}
	var la, lh int64
	if !rec.LastActivity.IsZero() {
		la = rec.LastActivity.UnixMilli()
	}
	if !rec.LastHeartbeat.IsZero() {
		lh = rec.LastHeartbeat.UnixMilli()
	}
	_, err := db.Exec(`INSERT INTO agents(agent_id, record_json, status, last_activity, last_heartbeat, updated_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(agent_id) DO UPDATE SET record_json=excluded.record_json, status=excluded.status, last_activity=excluded.last_activity, last_heartbeat=excluded.last_heartbeat, updated_at=excluded.updated_at`,
		rec.AgentID, string(b), status, la, lh, time.Now().UnixMilli())
	return err
}

func (r *Registry) Upsert(in Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	in.AgentID = strings.TrimSpace(in.AgentID)
	if in.AgentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if in.DeclaredModel == "" {
		in.DeclaredModel = in.Model
	}
	if in.DeclaredProvider == "" {
		in.DeclaredProvider = in.Provider
	}
	old, exists := r.records[in.AgentID]
	now := time.Now().UTC()
	if exists {
		in.RegisteredAt = old.RegisteredAt
		in.RestartCount = old.RestartCount
		in.Changes = old.Changes
		in.SessionIDs = appendUnique(append([]string{}, old.SessionIDs...), in.SessionID)
		if old.ProcessID != 0 && old.ProcessID != in.ProcessID {
			in.RestartCount++
		}
		if in.Isolation == "" {
			in.Isolation = old.Isolation
		}
		// Preserve last activity if incoming has none (heartbeat-only Upsert).
		if in.LastActivity.IsZero() {
			in.LastActivity = old.LastActivity
		}
		in.StateChangedAt = old.StateChangedAt
		in.StateChangedBy = old.StateChangedBy
		if in.ObservedModel == "" {
			in.ObservedModel = old.ObservedModel
		}
		if in.ObservedProvider == "" {
			in.ObservedProvider = old.ObservedProvider
		}
		if in.ObservedModel != "" {
			in.Model = in.ObservedModel
		}
		if in.ObservedProvider != "" {
			in.Provider = in.ObservedProvider
		}
		addChanges(&in, old, now, "probe")
	} else {
		in.SessionIDs = appendUnique(nil, in.SessionID)
		if in.Isolation == "" {
			in.Isolation = "active"
		}
	}
	if in.Isolation == "" {
		in.Isolation = "active"
	}
	if in.ObservedModel != "" {
		in.Model = in.ObservedModel
	}
	if in.ObservedProvider != "" {
		in.Provider = in.ObservedProvider
	}
	if in.RegisteredAt.IsZero() {
		in.RegisteredAt = now
	}
	in.DeclaredIPs = uniqueIPs(in.DeclaredIPs)
	in.ObservedIPs = uniqueIPs(in.ObservedIPs)
	if len(in.ObservedIPs) == 0 && in.IP != "" {
		in.ObservedIPs = []string{in.IP}
	}
	if in.IP == "" {
		if len(in.ObservedIPs) > 0 {
			in.IP = in.ObservedIPs[0]
		} else if len(in.DeclaredIPs) > 0 {
			in.IP = in.DeclaredIPs[0]
		}
	}
	in.LastHeartbeat = now
	// Status is computed via computeStatus (active/idle/offline), not stored.
	// Keep stored Status as last computed for persistence, but List/Get recompute.
	in.Status = computeStatus(in, now)
	r.records[in.AgentID] = in
	return r.saveLocked()
}

func (r *Registry) Heartbeat(agentID, ip string, observedIPs []string, model, provider, agentType, alias string, activity time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	v, ok := r.records[agentID]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	v.LastHeartbeat = now
	// Heartbeat only refreshes LastHeartbeat; LastActivity is driven by
	// real harness activity (hook/OTLP/LLM) via ObserveModel/ObserveSession
	// or Upsert. Idle probe heartbeats must not keep a closed harness "active".
	if ip != "" {
		if v.IP != ip {
			v.Changes = append(v.Changes, Change{At: now, Field: "ip", From: v.IP, To: ip, Source: "heartbeat"})
		}
		v.IP = ip
	}
	v.ObservedIPs = uniqueIPs(append(v.ObservedIPs, observedIPs...))
	if model != "" {
		v.DeclaredModel = model
		if v.ObservedModel == "" && v.Model != model {
			v.Changes = append(v.Changes, Change{At: now, Field: "model", From: v.Model, To: model, Source: "heartbeat"})
			v.Model = model
		}
	}
	if provider != "" {
		v.DeclaredProvider = provider
		if v.ObservedProvider == "" {
			v.Provider = provider
		}
	}
	if agentType != "" {
		v.AgentType = agentType
	}
	if strings.TrimSpace(alias) != "" {
		v.Alias = strings.TrimSpace(alias)
	}
	// Don't set Status here; List/Get compute it from LastActivity/LastHeartbeat
	// via computeStatus. Stored Status is stale otherwise.
	r.records[agentID] = v
	return r.saveLocked()
}

// ObserveModel records the model actually used by an incoming LLM event.
func (r *Registry) ObserveModel(agentID, model, provider string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.records[agentID]
	if !ok || strings.TrimSpace(model) == "" {
		return nil
	}
	from := v.Model
	if v.Model != model {
		v.Changes = append(v.Changes, Change{At: at.UTC(), Field: "model", From: v.Model, To: model, Source: "event"})
		v.Model = model
	}
	v.ObservedModel = model
	if provider != "" {
		v.ObservedProvider = provider
		v.Provider = provider
	}
	v.LastActivity = at.UTC()
	r.records[agentID] = v
	// Persist model history to DB if present
	if r.db != nil && from != model {
		source := "event"
		if v.ObservedProvider != "" {
			source = "gateway-observed"
		}
		_, _ = r.db.Exec(`INSERT INTO model_history(agent_id, ts, from_model, to_model, source) VALUES(?,?,?,?,?)`,
			agentID, at.UTC().UnixMilli(), from, model, source)
	}
	return r.saveLocked()
}

// ObserveSession attaches a session seen in telemetry to the stable runtime
// identity. A session is deliberately not a new Agent record.
func (r *Registry) ObserveSession(agentID, sessionID string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" {
		return nil
	}
	v, ok := r.records[agentID]
	if !ok {
		return nil
	}
	v.SessionIDs = appendUnique(v.SessionIDs, sessionID)
	if at.After(v.LastActivity) {
		v.LastActivity = at.UTC()
	}
	r.records[agentID] = v
	return r.saveLocked()
}

func (r *Registry) SetAlias(agentID, alias, actor string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.records[agentID]
	if !ok {
		return Record{}, fmt.Errorf("agent not found: %s", agentID)
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return Record{}, fmt.Errorf("alias is required")
	}
	if v.Alias != alias {
		v.Changes = append(v.Changes, Change{At: time.Now().UTC(), Field: "alias", From: v.Alias, To: alias, Source: actor})
		v.Alias = alias
	}
	r.records[agentID] = v
	if err := r.saveLocked(); err != nil {
		return Record{}, err
	}
	return v, nil
}

func (r *Registry) SetIsolation(agentID, level, actor string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !validIsolation[level] {
		return Record{}, fmt.Errorf("invalid isolation level %q", level)
	}
	v, ok := r.records[agentID]
	if !ok {
		return Record{}, fmt.Errorf("agent %q not found", agentID)
	}
	old := v.Isolation
	if old == "" {
		old = "active"
	}
	if old != level {
		v.Changes = append(v.Changes, Change{At: time.Now().UTC(), Field: "isolation", From: old, To: level, Source: actor})
	}
	v.Isolation = level
	v.StateChangedAt = time.Now().UTC()
	v.StateChangedBy = actor
	r.records[agentID] = v
	if err := r.saveLocked(); err != nil {
		return Record{}, err
	}
	return v, nil
}

func (r *Registry) List() []Record {
	return r.list(false)
}

// ListActive returns one row per stable runtime identity that has sent a
// heartbeat within the active window. Sessions and model changes remain
// attached to that row; they never create another Agent row.
func (r *Registry) ListActive() []Record {
	return r.list(true)
}

func (r *Registry) list(activeOnly bool) []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Record, 0, len(r.records))
	for _, v := range r.records {
		if strings.TrimSpace(v.AgentID) == "" {
			continue
		}
		v.Status = computeStatus(v, time.Now())
		if activeOnly && v.Status == "offline" {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

func (r *Registry) Get(agentID string) (Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.records[agentID]
	if ok {
		v.Status = computeStatus(v, time.Now())
	}
	return v, ok
}

func isActive(lastHeartbeat time.Time) bool {
	return !lastHeartbeat.IsZero() && time.Since(lastHeartbeat.UTC()) <= activeWindow
}

func isActiveRecord(v Record) bool {
	return computeStatus(v, time.Now()) != "offline"
}

// computeStatus implements the three-state model from DESIGN-V1 §2.8:
//   active  – real harness activity within 5m
//   idle    – no activity but probe heartbeat within 2m (process alive, harness idle)
//   offline – neither
func computeStatus(v Record, now time.Time) string {
	if !v.LastActivity.IsZero() && now.Sub(v.LastActivity.UTC()) <= activeWindow {
		return "active"
	}
	if !v.LastHeartbeat.IsZero() && now.Sub(v.LastHeartbeat.UTC()) <= heartbeatWindow {
		return "idle"
	}
	return "offline"
}

func (r *Registry) saveLocked() error {
	if r.db != nil {
		// Persist all records to DB (single writer, map is already locked)
		for _, rec := range r.records {
			if err := upsertAgentDB(r.db, rec); err != nil {
				return err
			}
		}
		// Also keep JSON as backup if path set (audit)
		if r.path == "" {
			return nil
		}
	}
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueIPs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUnique(out, value)
	}
	return out
}

func addChanges(in *Record, old Record, at time.Time, source string) {
	if old.Alias != in.Alias {
		in.Changes = append(in.Changes, Change{At: at, Field: "alias", From: old.Alias, To: in.Alias, Source: source})
	}
	if old.Model != in.Model {
		in.Changes = append(in.Changes, Change{At: at, Field: "model", From: old.Model, To: in.Model, Source: source})
	}
	if old.IP != in.IP {
		in.Changes = append(in.Changes, Change{At: at, Field: "ip", From: old.IP, To: in.IP, Source: source})
	}
}

// ModelHistory returns model change history for an agent, newest first.
// It reads from DB if available, falling back to the in-memory Changes slice.
func (r *Registry) ModelHistory(agentID string, limit int) ([]Change, error) {
	if r.db != nil {
		rows, err := r.db.Query(`SELECT ts, from_model, to_model, source FROM model_history WHERE agent_id=? ORDER BY ts DESC, id DESC LIMIT ?`, agentID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Change
		for rows.Next() {
			var ts int64
			var from, to, source string
			if err := rows.Scan(&ts, &from, &to, &source); err != nil {
				return nil, err
			}
			out = append(out, Change{At: time.UnixMilli(ts).UTC(), Field: "model", From: from, To: to, Source: source})
		}
		return out, rows.Err()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[agentID]
	if !ok {
		return nil, nil
	}
	var out []Change
	for i := len(rec.Changes) - 1; i >= 0; i-- {
		if rec.Changes[i].Field == "model" {
			out = append(out, rec.Changes[i])
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// Delete removes an agent manually (offline agents are only removed this way).
func (r *Registry) Delete(agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if _, ok := r.records[agentID]; !ok {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	delete(r.records, agentID)
	if r.db != nil {
		if _, err := r.db.Exec(`DELETE FROM agents WHERE agent_id=?`, agentID); err != nil {
			return err
		}
	}
	return r.saveLocked()
}

func RemoteIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remote)
}
