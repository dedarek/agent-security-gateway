package agentregistry

import (
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

const activeWindow = 90 * time.Second

type Registry struct {
	mu      sync.RWMutex
	path    string
	records map[string]Record
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
	in.Status = "online"
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
	if activity.After(v.LastActivity) {
		v.LastActivity = activity
	}
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
	v.Status = "online"
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
		if !isActiveRecord(v) {
			v.Status = "offline"
		} else {
			v.Status = "online"
		}
		if activeOnly && v.Status != "online" {
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
	if ok && !isActiveRecord(v) {
		v.Status = "offline"
	}
	return v, ok
}

func isActive(lastHeartbeat time.Time) bool {
	return !lastHeartbeat.IsZero() && time.Since(lastHeartbeat.UTC()) <= activeWindow
}

func isActiveRecord(v Record) bool {
	// Harness-level online = recent activity, not just probe heartbeat.
	// Probe-only agents (no LLM/OTLP activity yet) should not appear as
	// "online · active" after the harness is closed.
	if v.LastActivity.IsZero() {
		return false
	}
	return time.Since(v.LastActivity.UTC()) <= activeWindow
}

func (r *Registry) saveLocked() error {
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

func RemoteIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remote)
}
