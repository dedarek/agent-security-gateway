// Python bridge: feeds the Go-built graph + event texts into Semantica
// (KG persistence, fastembed semantic retrieval, LLM Q&A) via a small
// long-running worker. The Go gateway talks to it over HTTP.
package kgbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Bridge manages the Semantica worker process and its HTTP endpoints.
type Bridge struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	port          int
	pythonBin     string
	semanticaPath string
	workerScript  string
}

func New(pythonBin, workerScript, semanticaPath string, port int) *Bridge {
	return &Bridge{pythonBin: pythonBin, workerScript: workerScript,
		semanticaPath: semanticaPath, port: port}
}

// Start launches the Semantica worker (kg_worker.py in this package dir).
func (b *Bridge) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd != nil {
		return nil // already running
	}
	// A worker may have been started independently (for example by the local
	// cluster script). Reuse it when it is healthy instead of launching a
	// second process on the same port and then losing the KG API registration.
	if health, err := b.Health(); err == nil {
		if ready, ok := health["graph_ready"].(bool); ok && ready {
			return nil
		}
	}
	startupToken := fmt.Sprintf("asg-%d", time.Now().UnixNano())
	cmd := exec.Command(b.pythonBin, b.workerScript,
		"--port", fmt.Sprint(b.port), "--semantica-path", b.semanticaPath,
		"--worker-token", startupToken)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start semantica worker: %w", err)
	}
	b.cmd = cmd
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			b.cmd = nil
			return fmt.Errorf("semantica worker exited before ready: %w", err)
		default:
		}
		if health, err := b.Health(); err == nil {
			workerToken, tokenOK := health["worker_token"].(string)
			ready, readyOK := health["graph_ready"].(bool)
			if tokenOK && workerToken == startupToken && readyOK && ready {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	b.cmd = nil
	return fmt.Errorf("semantica worker did not become ready at %s", b.URL())
}

// Ingest posts graph deltas (entities/relationships) to the worker.
func (b *Bridge) Ingest(entities, relationships []map[string]any) error {
	body, _ := json.Marshal(map[string]any{
		"entities": entities, "relationships": relationships,
	})
	resp, err := http.Post(b.url()+"/ingest", "application/json", bytesReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("worker ingest status %d", resp.StatusCode)
	}
	return nil
}

// IndexEvents posts raw event texts for fastembed semantic indexing.
func (b *Bridge) IndexEvents(texts, eventIDs []string) error {
	body, _ := json.Marshal(map[string]any{"texts": texts, "event_ids": eventIDs})
	resp, err := http.Post(b.url()+"/events", "application/json", bytesReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// Health returns the worker's actual readiness and data counts.
func (b *Bridge) Health() (map[string]any, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(b.URL() + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worker health status %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// GraphNodes returns all nodes in the Semantica graph session.
func (b *Bridge) GraphNodes() (json.RawMessage, error) {
	resp, err := http.Get(b.url() + "/graph/nodes")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// GraphEdges returns all edges in the Semantica graph session.
func (b *Bridge) GraphEdges() (json.RawMessage, error) {
	resp, err := http.Get(b.url() + "/graph/edges")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// GraphPath finds the shortest path between two entities (链路追溯).
func (b *Bridge) GraphPath(source, target string) (json.RawMessage, error) {
	resp, err := http.Get(b.url() + "/graph/path?source=" +
		urlQueryEscape(source) + "&target=" + urlQueryEscape(target))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Search does local-fastembed semantic similarity over past events.
func (b *Bridge) Search(query string, topK int) ([]SearchHit, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "top_k": topK})
	resp, err := http.Post(b.url()+"/search", "application/json", bytesReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Hits []SearchHit `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}

// Ask grounds the free-model LLM on the KG context and returns the answer.
func (b *Bridge) Ask(question string) (string, error) {
	body, _ := json.Marshal(map[string]any{"question": question})
	resp, err := http.Post(b.url()+"/ask", "application/json", bytesReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Answer, nil
}

func (b *Bridge) url() string { return b.URL() }
func (b *Bridge) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", b.port) }

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

type SearchHit struct {
	Text    string  `json:"text"`
	Score   float64 `json:"score"`
	EventID string  `json:"event_id,omitempty"`
}
