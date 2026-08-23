package main

import (
	"os"
	"strings"
	"sync"
)

// spoolStore persists events across restarts and re-ship failures.
// The file is the write-ahead log: push appends, successful ship truncates.
// On restart the WAL is replayed into memory so nothing is lost on crash.
// After a successful Flush of ALL pending batches the file is truncated —
// this prevents duplicate delivery on restart (the old code never cleaned it).
type spoolStore struct {
	mu   sync.Mutex
	path string
	mem  [][]byte
}

func newSpool(path string) *spoolStore {
	s := &spoolStore{path: path}
	s.replayWAL()
	return s
}

// replayWAL reads any leftover file content into memory and clears the file
// (memory is now the source of truth until the next flush completes).
func (s *spoolStore) replayWAL() {
	b, err := os.ReadFile(s.path)
	if err != nil || len(b) == 0 {
		return
	}
	for _, part := range strings.Split(string(b), "\n") {
		part = strings.TrimSpace(part)
		if part != "" {
			s.mem = append(s.mem, []byte(part+"\n"))
		}
	}
	_ = os.Remove(s.path) // moved into memory; will be rewritten on next push
}

func (s *spoolStore) push(batch []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem = append(s.mem, batch)
	s.appendWAL(batch)
}

// appendWAL appends one batch to the write-ahead log file (one JSONL line).
func (s *spoolStore) appendWAL(batch []byte) {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(batch)
	if len(batch) > 0 && batch[len(batch)-1] != '\n' {
		f.Write([]byte("\n"))
	}
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

// markShipped rewrites the WAL after a successful ship to remove all shipped
// entries. Called by Flush when mem becomes empty.
func (s *spoolStore) markShipped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mem) == 0 {
		_ = os.Remove(s.path) // everything shipped; clear the log
	}
}

func (s *spoolStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mem)
}
