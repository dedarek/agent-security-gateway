// Package drift implements task-drift detection: compares the agent's original
// user instruction against its current tool call to detect when the agent has
// wandered away from the assigned task — even if every individual step looks
// legitimate.
//
// Uses lightweight keyword-overlap scoring (no external LLM call needed for
// real-time enforcement). Higher-threshold LLM-based review is done async by
// the Intelligence plane.
package drift

import (
	"strings"
	"sync"
	"time"
)

// SessionState tracks what the user originally asked vs what the agent is
// currently doing, per session.
type SessionState struct {
	OriginalTask string    `json:"original_task"`
	Tasks        []string  `json:"tasks"` // all user messages in this trace
	StartedAt    time.Time `json:"started_at"`
}

// Detector holds session states and computes drift.
type Detector struct {
	mu        sync.Mutex
	sessions  map[string]*SessionState
	stopWords map[string]bool // common words that don't indicate intent
}

func NewDetector() *Detector {
	return &Detector{
		sessions: map[string]*SessionState{},
		stopWords: stopWordSet([]string{
			"the", "a", "an", "is", "are", "was", "were", "to", "for", "of", "in",
			"on", "at", "and", "or", "but", "with", "by", "from", "up", "about",
			"into", "over", "after", "this", "that", "it", "as", "be", "has", "had",
			"do", "does", "did", "will", "would", "can", "could", "should", "may",
			"me", "my", "i", "we", "you", "your", "he", "she", "they", "them",
			"please", "help", "need", "want", "get", "make", "let", "us",
			"的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都",
			"一个", "上", "也", "很", "到", "说", "要", "去", "你", "会", "着",
			"没有", "看", "好", "自己", "这", "他", "她", "它", "们", "那", "被",
			"请", "帮我", "然后", "可以", "什么", "怎么", "如何", "一下",
		}),
	}
}

// SetTask records the user's instruction for a session (called on each user message).
func (d *Detector) SetTask(sessionID, userMessage string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ss, ok := d.sessions[sessionID]; ok {
		ss.Tasks = append(ss.Tasks, userMessage)
		// Update original if this is a significant new instruction (>10 chars of keywords)
		if len(extractKeywords(userMessage, d.stopWords)) > 2 {
			ss.OriginalTask = userMessage
			ss.StartedAt = time.Now()
		}
	} else {
		d.sessions[sessionID] = &SessionState{
			OriginalTask: userMessage,
			Tasks:        []string{userMessage},
			StartedAt:    time.Now(),
		}
	}
}

// DriftResult carries the drift assessment for one tool call.
type DriftResult struct {
	SessionID     string
	DriftScore    float64 // 0.0 = on-task, 1.0 = completely off-task
	TaskKeywords  []string
	CallKeywords  []string
	OverlapRatio  float64
	Verdict       string // ON_TASK | PARTIAL_DRIFT | DRIFTED | OFF_TASK
	ShouldFlag    bool   // true → mark in audit trail but don't block
	ShouldBlock   bool   // true → extreme drift with destructive tool
	DestructiveTool bool
}

// destructiveTools are tools where drift is more dangerous.
var destructiveTools = map[string]bool{
	"delete_user": true, "delete": true, "drop": true, "truncate": true,
	"export_all_users": true, "send_email": true, "http_post": true,
	"rm": true, "bash": true,
}

// Check evaluates one tool call against the session's original task.
// Returns drift assessment; never blocks by itself (drift is FLAG-only),
// except when combined with destructive tools at extreme drift levels.
func (d *Detector) Check(sessionID, toolName string) *DriftResult {
	d.mu.Lock()
	ss, ok := d.sessions[sessionID]
	if !ok {
		d.mu.Unlock()
		return &DriftResult{Verdict: "ON_TASK", ShouldFlag: false}
	}
	task := ss.OriginalTask
	d.mu.Unlock()

	taskKw := extractKeywords(task, d.stopWords)
	if len(taskKw) == 0 {
		return &DriftResult{SessionID: sessionID, Verdict: "ON_TASK"}
	}

	// Build call context from tool name + any semantic hints
	callText := strings.ReplaceAll(toolName, "_", " ")
	callKw := extractKeywords(callText, d.stopWords)

	overlap := countOverlap(taskKw, callKw)
	ratio := 0.0
	if len(callKw) > 0 {
		ratio = float64(overlap) / float64(len(callKw))
	}

	isDestructive := destructiveTools[strings.ToLower(toolName)]

	result := &DriftResult{
		SessionID:       sessionID,
		DriftScore:      1.0 - ratio,
		TaskKeywords:    taskKw,
		CallKeywords:    callKw,
		OverlapRatio:    ratio,
		DestructiveTool: isDestructive,
	}

	switch {
	case ratio >= 0.3:
		result.Verdict = "ON_TASK"
	case ratio > 0:
		result.Verdict = "PARTIAL_DRIFT"
		result.ShouldFlag = true
	default:
		if isDestructive {
			result.Verdict = "OFF_TASK"
			result.ShouldFlag = true
			result.ShouldBlock = false // still FLAG not BLOCK — needs human review
		} else {
			result.Verdict = "DRIFTED"
			result.ShouldFlag = true
		}
	}
	return result
}

// extractKeywords splits text into meaningful words (lowercase, deduplicated).
func extractKeywords(text string, stops map[string]bool) []string {
	lower := strings.ToLower(text)

	// Domain-specific concept mapping: Chinese/colloquial terms → canonical concepts
	concepts := map[string]string{
		"收件箱": "inbox", "邮件": "email", "发邮件": "send_email",
		"发送": "send", "读取": "read", "删除": "delete",
		"导出": "export", "用户": "user", "客户": "customer",
		"密钥": "secret", "密码": "password", "文件": "file",
		"数据": "data", "数据库": "database", "搜索": "search",
		"分析": "analyze", "报告": "report", "总结": "summarize",
	}
	for cn, en := range concepts {
		if strings.Contains(lower, cn) {
			lower += " " + en
		}
	}

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if len(w) < 2 || stops[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func countOverlap(a, b []string) int {
	setA := map[string]bool{}
	for _, x := range a {
		setA[x] = true
	}
	n := 0
	for _, x := range b {
		if setA[x] {
			n++
		}
	}
	return n
}

func stopWordSet(words []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		m[w] = true
	}
	return m
}
