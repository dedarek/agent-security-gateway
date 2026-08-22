package main

import (
	"os"
	"strings"
	"sync"
)

// spoolStore persists events across restarts and re-ship failures. The
// in-memory buffer is what Flush ships; the file is the durability layer.
type spoolStore struct {
	mu   sync.Mutex
	path string
	mem  [][]byte // pending batches (each a JSONL chunk)
}

func newSpool(path string) *spoolStore {
	s := &spoolStore{path: path}
	// replay any leftover file content into memory so nothing is lost on crash
	if b, err := os.ReadFile(path); err == nil {
		for _, part := range strings.Split(string(b), "\n\n") {
			if strings.TrimSpace(part) != "" {
				s.mem = append(s.mem, []byte(part))
			}
		}
		_ = os.Remove(path) // moved into memory
	}
	return s
}

func (s *spoolStore) push(batch []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem = append(s.mem, batch)
	_ = appendFile(s.path, append(append([]byte{}, batch...), '\n'))
}

func (s *spoolStore) pop() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mem) == 0 {
		return nil, false
	}
	b := s.mem[0]
	s.mem = s.mem[1:]
	return b, true
}

func (s *spoolStore) unpop(batch []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem = append([][]byte{batch}, s.mem...)
}

func (s *spoolStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mem)
}
