package activity

import (
	"testing"
	"time"
)

func TestStoreAddAndList(t *testing.T) {
	s := New()
	s.Add(Step{AgentID: "a1", Kind: "tool_use", ToolName: "Read"})
	s.Add(Step{AgentID: "a1", Kind: "tool_use", ToolName: "Write"})
	list := s.List("a1")
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].ToolName != "Read" || list[1].ToolName != "Write" {
		t.Fatalf("order wrong: %v", list)
	}
}

func TestStoreRecent(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		s.Add(Step{AgentID: "a1", Kind: "tool_use", ToolName: "T"})
	}
	recent := s.Recent("a1", 2)
	if len(recent) != 2 {
		t.Fatalf("recent len = %d", len(recent))
	}
}

func TestStoreBounded(t *testing.T) {
	s := New()
	s.maxPerAgent = 3
	for i := 0; i < 5; i++ {
		s.Add(Step{AgentID: "a1", Kind: "tool_use", ToolName: "T"})
	}
	if n := s.Count("a1"); n != 3 {
		t.Fatalf("bounded count = %d", n)
	}
}

func TestStoreEmptyAgentIDIgnored(t *testing.T) {
	s := New()
	s.Add(Step{AgentID: "", Kind: "tool_use"})
	if len(s.AllAgents()) != 0 {
		t.Fatal("empty agent_id should be ignored")
	}
}

func TestStoreAtDefault(t *testing.T) {
	s := New()
	before := time.Now().UTC()
	s.Add(Step{AgentID: "a1", Kind: "session_start"})
	after := time.Now().UTC()
	list := s.List("a1")
	if list[0].At.Before(before) || list[0].At.After(after) {
		t.Fatalf("At not defaulted correctly: %v", list[0].At)
	}
}
