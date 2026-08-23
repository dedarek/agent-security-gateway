// Python bridge: feeds the Go-built graph + event texts into Semantica
// (KG persistence, fastembed semantic retrieval, LLM Q&A) via a small
// long-running worker. The Go gateway talks to it over HTTP.
package kgbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
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
	cmd := exec.Command(b.pythonBin, b.workerScript,
		"--port", fmt.Sprint(b.port), "--semantica-path", b.semanticaPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start semantica worker: %w", err)
	}
	b.cmd = cmd
	return nil
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

func (b *Bridge) url() string { return fmt.Sprintf("http://127.0.0.1:%d", b.port) }

type SearchHit struct {
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	EventID string `json:"event_id,omitempty"`
}
