// Package threatclass adds a harm-category dimension to every decision,
// enabling operators to write policies like "data_exfiltration always BLOCK"
// vs "content_safety FLAG only" instead of a single flat risk threshold.
package threatclass

import "strings"

// Category classifies the type of harm an event could cause.
type Category string

const (
	DataExfiltration  Category = "data_exfiltration"
	Destructive       Category = "destructive_operation"
	ContentSafety     Category = "content_safety"
	PrivilegeAbuse    Category = "privilege_abuse"
	SupplyChain       Category = "supply_chain"
	Misinformation    Category = "misinformation"
	Reconnaissance    Category = "reconnaissance"
	PolicyViolation   Category = "policy_violation"
	Unknown           Category = ""
)

// Disposition determines what happens when this category is detected.
type Disposition struct {
	Action      string // "BLOCK" | "CONFIRM" | "FLAG" | "AUDIT_ONLY"
	Escalate    bool   // notify operator immediately
	Retention   string // audit log retention period
	Description string
}

// DefaultPolicies maps categories to their default dispositions.
// These are the built-in fallbacks; operators can override via config.
var DefaultPolicies = map[Category]Disposition{
	DataExfiltration: {
		Action: "BLOCK", Escalate: true, Retention: "365d",
		Description: "Irreversible data loss — highest priority, always block and alert",
	},
	Destructive: {
		Action: "CONFIRM", Escalate: true, Retention: "180d",
		Description: "Potentially irreversible — human must approve before execution",
	},
	ContentSafety: {
		Action: "FLAG", Escalate: false, Retention: "90d",
		Description: "Pass through but mark in audit trail for review",
	},
	PrivilegeAbuse: {
		Action: "CONFIRM", Escalate: true, Retention: "180d",
		Description: "Legal permission but unusual pattern — verify intent",
	},
	SupplyChain: {
		Action: "BLOCK", Escalate: true, Retention: "365d",
		Description: "Package/install script compromise — always block",
	},
	Misinformation: {
		Action: "FLAG", Escalate: false, Retention: "90d",
		Description: "Possible hallucination — flag for fact-check",
	},
	Reconnaissance: {
		Action: "FLAG", Escalate: false, Retention: "90d",
		Description: "Multiple reads may indicate pre-attack recon — track",
	},
	PolicyViolation: {
		Action: "BLOCK", Escalate: true, Retention: "180d",
		Description: "Explicit policy breach — enforce",
	},
}

// Classify maps a tool call + context to a threat category.
// This is the "brain" that decides which policy applies.
func Classify(toolID, action string, outputContainsSecret, isDestructiveTool, isDrifted bool) Category {
	toolLower := strings.ToLower(toolID)
	actionLower := strings.ToLower(action)

	if outputContainsSecret && (actionLower == "network" || actionLower == "write") {
		return DataExfiltration
	}
	if strings.Contains(toolLower, "send_email") || strings.Contains(toolLower, "http_post") ||
		strings.Contains(toolLower, "exfil") {
		return DataExfiltration
	}
	if strings.Contains(toolLower, "delete") || strings.Contains(toolLower, "drop") ||
		strings.Contains(toolLower, "truncate") || strings.Contains(toolLower, "purge") ||
		isDestructiveTool {
		return Destructive
	}
	if strings.Contains(toolLower, "postinstall") || strings.Contains(toolLower, "preinstall") ||
		strings.Contains(toolLower, "npm_install") || strings.Contains(toolLower, "pip_install") {
		return SupplyChain
	}
	if isDrifted {
		return PrivilegeAbuse
	}
	if actionLower == "read" && (strings.Contains(toolLower, "inbox") ||
		strings.Contains(toolLower, "customer") || strings.Contains(toolLower, "secret")) {
		return Reconnaissance
	}
	return PolicyViolation
}

// GetDisposition returns the disposition for a category.
func GetDisposition(cat Category) Disposition {
	if d, ok := DefaultPolicies[cat]; ok {
		return d
	}
	return Disposition{Action: "AUDIT_ONLY", Description: "unknown category"}
}

// AllCategories lists all registered categories with their dispositions (for UI).
func AllCategories() map[Category]Disposition {
	return DefaultPolicies
}
