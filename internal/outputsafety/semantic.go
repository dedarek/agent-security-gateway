package outputsafety

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"bytes"
	"sync"
	"time"
)

// SemanticScanner uses the Lenovo qwen model (fast, free) for inline verdicts
// and can use a larger model for async post-hoc analysis.
//
// Two modes:
//   - Inline (real-time): max_tokens capped small (e.g. 100), timeout 15s,
//     only YES/NO + one-line reason. Runs on every suspicious output.
//   - Async (post-hoc): no token cap, full reasoning. Queued for the
//     Intelligence plane to review after the fact.

var (
	semMu       sync.Mutex
	semReady    bool
	semLLMURL   string
	semLLMKey   string
	semMaxTok   int // inline max_tokens
	httpClient  = &http.Client{Timeout: 20 * time.Second}
	asyncClient = &http.Client{Timeout: 120 * time.Second}
)

type DangerousConcept struct {
	Label    string
	Category string
}

var dangerousConcepts = []DangerousConcept{
	{Label: "delete entire production environment", Category: "destructive_operation"},
	{Label: "drop all database tables and purge data", Category: "destructive_operation"},
	{Label: "send customer personal information to external email", Category: "data_exfiltration"},
	{Label: "upload credentials and API keys to public server", Category: "data_exfiltration"},
	{Label: "install backdoor in authentication system", Category: "supply_chain"},
	{Label: "disable security monitoring and logging", Category: "privilege_abuse"},
	{Label: "execute arbitrary remote code on production server", Category: "destructive_operation"},
}

// InitSemantic configures the LLM endpoint for semantic confirmation.
func InitSemantic(llmURL, llmKey string, inlineMaxTokens int) {
	semMu.Lock()
	defer semMu.Unlock()
	semReady = true
	semLLMURL = strings.TrimSuffix(llmURL, "/") + "/chat/completions"
	semLLMKey = llmKey
	if inlineMaxTokens <= 0 {
		inlineMaxTokens = 100
	}
	semMaxTok = inlineMaxTokens
	fmt.Printf("[outputsafety] semantic scanner ready (llm=%s max_tokens=%d)\n",
		maskURL(semLLMURL), semMaxTok)
}

func maskURL(u string) string {
	if i := strings.Index(u, "//"); i > 0 {
		return u[i+2:]
	}
	return u
}

type SemanticResult struct {
	Suspicious    bool   `json:"suspicious"`
	MaxSimilarity float64 `json:"max_similarity"`
	BestMatch     string `json:"best_match"`
	Category      string `json:"category"`
	LLMConfirmed  bool   `json:"llm_confirmed"`
	LLMReasoning  string `json:"llm_reasoning"`
	FinalVerdict  string `json:"final_verdict"` // BLOCK | FLAG | SAFE
	Mode          string `json:"mode"`          // "inline" | "async"
	Detail        string `json:"detail"`
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (na*nb + 1e-9)
}

// ScanSemantic runs the two-layer semantic scan in INLINE mode (fast).
func ScanSemantic(output, userTask, kgWorkerEmbedURL string) *SemanticResult {
	return scanSemanticWithMode(output, userTask, kgWorkerEmbedURL, false)
}

// ScanSemanticAsync runs the deeper analysis for post-hoc review.
func ScanSemanticAsync(output, userTask, kgWorkerEmbedURL string) *SemanticResult {
	r := scanSemanticWithMode(output, userTask, kgWorkerEmbedURL, true)
	r.Mode = "async"
	return r
}

func scanSemanticWithMode(output, userTask, embedURL string, isAsync bool) *SemanticResult {
	result := &SemanticResult{FinalVerdict: "SAFE"}
	semMu.Lock()
	ready := semReady && semLLMURL != ""
	maxTok := semMaxTok
	client := httpClient
	if isAsync {
		maxTok = 0 // unlimited for post-hoc
		client = asyncClient
	} else if maxTok == 0 {
		maxTok = 100
	}
	semMu.Unlock()
	if !ready || strings.TrimSpace(output) == "" {
		return result
	}
	result.Mode = modeStr(isAsync)

	outVec, err := getEmbedding(embedURL, output)
	if err != nil {
		return result
	}

	bestSim := 0.0
	bestConcept := dangerousConcepts[0].Label
	for _, dc := range dangerousConcepts {
		cv, err := getEmbedding(embedURL, dc.Label)
		if err != nil { continue }
		sim := cosine(outVec, cv)
		if sim > bestSim { bestSim = sim; bestConcept = dc.Label }
	}

	const threshold = 0.65
	if bestSim < threshold {
		return result
	}

	dc := findConcept(bestConcept)
	result.Suspicious = true
	result.MaxSimilarity = bestSim
	result.BestMatch = bestConcept
	if dc != nil { result.Category = dc.Category }

	confirmed, reasoning := confirmWithLLM(output, userTask, bestConcept, maxTok, client)
	result.LLMConfirmed = confirmed
	result.LLMReasoning = truncate(reasoning, 200)
	if confirmed {
		result.FinalVerdict = "BLOCK"
		result.Detail = fmt.Sprintf("SEMANTIC THREAT [%s]: resembles '%s' sim=%.3f — %s",
			modeStr(isAsync), bestConcept, bestSim, result.LLMReasoning)
	} else {
		result.FinalVerdict = "FLAG"
		result.Detail = fmt.Sprintf("SEMANTIC FLAG [%s]: resembles '%s' sim=%.3f — %s",
			modeStr(isAsync), bestConcept, bestSim, result.LLMReasoning)
	}
	return result
}

func modeStr(isAsync bool) string {
	if isAsync { return "async" }
	return "inline"
}

func getEmbedding(url, text string) ([]float64, error) {
	body, _ := json.Marshal(map[string]any{"texts": []string{text}})
	req, err := http.NewRequest("POST", url+"/embed", strings.NewReader(string(body)))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var out struct { Vectors [][]float64 `json:"vectors"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	if len(out.Vectors) == 0 { return nil, fmt.Errorf("empty") }
	return out.Vectors[0], nil
}

func confirmWithLLM(output, userTask, concept string, maxTokens int, client *http.Client) (bool, string) {
	prompt := fmt.Sprintf(
		"Security review. Agent was asked:\n%q\n\nAgent produced this output:\n---\n%s\n---\n\nThis resembles: %q\n\nIs this actually harmful? Answer YES or NO first, then one sentence.",
		truncate(userTask, 300), truncate(output, 500), concept)

	mt := maxTokens
	if mt <= 0 { mt = 200 } // async mode: generous
	body := map[string]any{
		"model":       "ox-alpha-free",
		"max_tokens":  mt,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", semLLMURL, bytes.NewReader(b))
	if err != nil { return false, "" }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+semLLMKey)

	resp, err := client.Do(req)
	if err != nil { return false, "" }
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct { Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Choices) == 0 {
		return false, ""
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	answer := strings.ToUpper(content)
	confirmed := strings.HasPrefix(answer, "YES")
	return confirmed, content
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "..."
}

func findConcept(label string) *DangerousConcept {
	for i := range dangerousConcepts {
		if dangerousConcepts[i].Label == label { return &dangerousConcepts[i] }
	}
	return nil
}
