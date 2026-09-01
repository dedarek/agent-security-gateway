package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// InventoryItem is a harness-agnostic observed capability. It describes what
// was found, not what is trusted or allowed to execute.
type InventoryItem struct {
	ID               int64    `json:"id"`
	StableKey        string   `json:"stable_key"`
	AgentID          string   `json:"agent_id"`
	ParentID         string   `json:"parent_id,omitempty"`
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Source           string   `json:"source,omitempty"`
	Origin           string   `json:"origin,omitempty"`
	Version          string   `json:"version,omitempty"`
	ManifestHash     string   `json:"manifest_hash,omitempty"`
	SchemaHash       string   `json:"schema_hash,omitempty"`
	InstallPath      string   `json:"install_path,omitempty"`
	Status           string   `json:"status"`
	RiskLevel        string   `json:"risk_level,omitempty"`
	RiskLabels       []string `json:"risk_labels,omitempty"`
	RiskReasons      []string `json:"risk_reasons,omitempty"`
	DeclaredCaps     []string `json:"declared_capabilities,omitempty"`
	ObservedCaps     []string `json:"observed_capabilities,omitempty"`
	AIAnalysisStatus string   `json:"ai_analysis_status"`
	PolicySuggestion any      `json:"policy_suggestion,omitempty"`
	FirstSeen        int64    `json:"first_seen"`
	LastSeen         int64    `json:"last_seen"`
	UpdatedAt        int64    `json:"updated_at"`
}

// StableInventoryKey identifies a logical capability independently of its
// current schema or risk analysis. Hashes changing therefore update the same
// row and requeue analysis instead of creating duplicate assets.
func StableInventoryKey(agentID, kind, origin, name string) string {
	s := strings.Join([]string{agentID, kind, origin, name}, "\x00")
	h := sha256.Sum256([]byte(s))
	return "inv-" + hex.EncodeToString(h[:])
}

// UpsertInventory records an observation. New or changed capabilities remain
// pending review; discovery never grants execution permission.
func UpsertInventory(database *sql.DB, item InventoryItem) (InventoryItem, error) {
	if database == nil {
		return InventoryItem{}, fmt.Errorf("nil inventory database")
	}
	if strings.TrimSpace(item.AgentID) == "" || strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Name) == "" {
		return InventoryItem{}, fmt.Errorf("inventory agent_id, kind and name are required")
	}
	if item.StableKey == "" {
		item.StableKey = StableInventoryKey(item.AgentID, item.Kind, item.Origin, item.Name)
	}
	now := time.Now().UnixMilli()
	if item.FirstSeen == 0 {
		item.FirstSeen = now
	}
	if item.LastSeen == 0 {
		item.LastSeen = now
	}
	item.UpdatedAt = now
	if item.Status == "" {
		item.Status = "pending_review"
	}
	if item.AIAnalysisStatus == "" {
		item.AIAnalysisStatus = "pending"
	}

	labels, err := json.Marshal(nonNilStrings(item.RiskLabels))
	if err != nil {
		return InventoryItem{}, fmt.Errorf("marshal risk labels: %w", err)
	}
	reasons, err := json.Marshal(nonNilStrings(item.RiskReasons))
	if err != nil {
		return InventoryItem{}, fmt.Errorf("marshal risk reasons: %w", err)
	}
	declared, err := json.Marshal(nonNilStrings(item.DeclaredCaps))
	if err != nil {
		return InventoryItem{}, fmt.Errorf("marshal declared capabilities: %w", err)
	}
	observed, err := json.Marshal(nonNilStrings(item.ObservedCaps))
	if err != nil {
		return InventoryItem{}, fmt.Errorf("marshal observed capabilities: %w", err)
	}
	policy := []byte("{}")
	if item.PolicySuggestion != nil {
		policy, err = json.Marshal(item.PolicySuggestion)
		if err != nil {
			return InventoryItem{}, fmt.Errorf("marshal policy suggestion: %w", err)
		}
	}

	var existing InventoryItem
	err = scanInventory(database.QueryRow(`SELECT id, stable_key, agent_id, parent_id, kind, name, source, origin, version, manifest_hash, schema_hash, install_path, status, risk_level, risk_labels_json, risk_reasons_json, declared_caps_json, observed_caps_json, ai_status, policy_json, first_seen, last_seen, updated_at FROM inventory_items WHERE stable_key=?`, item.StableKey), &existing)
	if err != nil && err != sql.ErrNoRows {
		return InventoryItem{}, err
	}
	changed := err == nil && (existing.ManifestHash != item.ManifestHash || existing.SchemaHash != item.SchemaHash)
	if changed {
		item.Status = "pending_review"
		item.AIAnalysisStatus = "pending"
		item.FirstSeen = existing.FirstSeen
	}
	if err == sql.ErrNoRows {
		_, err = database.Exec(`INSERT INTO inventory_items(stable_key, agent_id, parent_id, kind, name, source, origin, version, manifest_hash, schema_hash, install_path, status, risk_level, risk_labels_json, risk_reasons_json, declared_caps_json, observed_caps_json, ai_status, policy_json, first_seen, last_seen, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.StableKey, item.AgentID, item.ParentID, item.Kind, item.Name, item.Source, item.Origin, item.Version, item.ManifestHash, item.SchemaHash, item.InstallPath, item.Status, item.RiskLevel, string(labels), string(reasons), string(declared), string(observed), item.AIAnalysisStatus, string(policy), item.FirstSeen, item.LastSeen, item.UpdatedAt)
	} else {
		_, err = database.Exec(`UPDATE inventory_items SET agent_id=?, parent_id=?, kind=?, name=?, source=?, origin=?, version=?, manifest_hash=?, schema_hash=?, install_path=?, status=?, risk_level=?, risk_labels_json=?, risk_reasons_json=?, declared_caps_json=?, observed_caps_json=?, ai_status=?, policy_json=?, first_seen=?, last_seen=?, updated_at=? WHERE stable_key=?`, item.AgentID, item.ParentID, item.Kind, item.Name, item.Source, item.Origin, item.Version, item.ManifestHash, item.SchemaHash, item.InstallPath, item.Status, item.RiskLevel, string(labels), string(reasons), string(declared), string(observed), item.AIAnalysisStatus, string(policy), item.FirstSeen, item.LastSeen, item.UpdatedAt, item.StableKey)
	}
	if err != nil {
		return InventoryItem{}, fmt.Errorf("upsert inventory: %w", err)
	}
	return getInventoryByKey(database, item.StableKey)
}

// ListInventory returns the latest observed capabilities for an agent.
func ListInventory(database *sql.DB, agentID string) ([]InventoryItem, error) {
	rows, err := database.Query(`SELECT id, stable_key, agent_id, parent_id, kind, name, source, origin, version, manifest_hash, schema_hash, install_path, status, risk_level, risk_labels_json, risk_reasons_json, declared_caps_json, observed_caps_json, ai_status, policy_json, first_seen, last_seen, updated_at FROM inventory_items WHERE agent_id=? ORDER BY last_seen DESC, id DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InventoryItem
	for rows.Next() {
		var item InventoryItem
		if err := scanInventory(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type inventoryScanner interface {
	Scan(dest ...any) error
}

func scanInventory(s inventoryScanner, item *InventoryItem) error {
	var labels, reasons, declared, observed, policy string
	if err := s.Scan(&item.ID, &item.StableKey, &item.AgentID, &item.ParentID, &item.Kind, &item.Name, &item.Source, &item.Origin, &item.Version, &item.ManifestHash, &item.SchemaHash, &item.InstallPath, &item.Status, &item.RiskLevel, &labels, &reasons, &declared, &observed, &item.AIAnalysisStatus, &policy, &item.FirstSeen, &item.LastSeen, &item.UpdatedAt); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(labels), &item.RiskLabels); err != nil {
		return fmt.Errorf("decode risk labels: %w", err)
	}
	if err := json.Unmarshal([]byte(reasons), &item.RiskReasons); err != nil {
		return fmt.Errorf("decode risk reasons: %w", err)
	}
	if err := json.Unmarshal([]byte(declared), &item.DeclaredCaps); err != nil {
		return fmt.Errorf("decode declared capabilities: %w", err)
	}
	if err := json.Unmarshal([]byte(observed), &item.ObservedCaps); err != nil {
		return fmt.Errorf("decode observed capabilities: %w", err)
	}
	if policy != "" && policy != "{}" {
		if err := json.Unmarshal([]byte(policy), &item.PolicySuggestion); err != nil {
			return fmt.Errorf("decode policy suggestion: %w", err)
		}
	}
	return nil
}

func getInventoryByKey(database *sql.DB, key string) (InventoryItem, error) {
	var item InventoryItem
	err := scanInventory(database.QueryRow(`SELECT id, stable_key, agent_id, parent_id, kind, name, source, origin, version, manifest_hash, schema_hash, install_path, status, risk_level, risk_labels_json, risk_reasons_json, declared_caps_json, observed_caps_json, ai_status, policy_json, first_seen, last_seen, updated_at FROM inventory_items WHERE stable_key=?`, key), &item)
	return item, err
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
