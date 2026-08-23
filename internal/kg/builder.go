// Package kg builds a security knowledge graph from gateway events and
// exposes semantic retrieval (local fastembed) plus KG-grounded Q&A via the
// probe's free-model LLM endpoint. Semantica supplies the graph, embedding
// and provenance machinery; the security domain model is ours.
package kg

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dedarek/agent-security-gateway/api"
)

// Entity/Relationship mirror Semantica's KnowledgeGraph dicts.
type Entity struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Props map[string]any `json:"props,omitempty"`
}

type Relationship struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
	Props  map[string]any `json:"props,omitempty"`
}

// Builder accumulates entities/relationships from event streams.
type Builder struct {
	mu     sync.Mutex
	ents   map[string]*Entity
	rels   []Relationship
}

func NewBuilder() *Builder { return &Builder{ents: map[string]*Entity{}} }

func (b *Builder) addEntity(id, typ string, props map[string]any) {
	if _, ok := b.ents[id]; !ok {
		b.ents[id] = &Entity{ID: id, Type: typ, Props: props}
	}
}

func (b *Builder) addRel(src, rel, dst string, props map[string]any) {
	b.rels = append(b.rels, Relationship{Source: src, Target: dst, Type: rel, Props: props})
}

// Ingest converts one gateway event into graph nodes.
func (b *Builder) Ingest(ev api.Event) {
	sid := ev.SessionID
	tenant := sid
	if i := strings.Index(sid, "-"); i > 0 && strings.HasPrefix(sid, "tenant-") {
		tenant = sid[len("tenant-"):]
	}
	trace := ev.TraceID
	if trace == "" {
		trace = "trace-" + ev.Call.CallID
	}

	b.addEntity("agent:"+ev.Call.Principal.UserID+"@"+tenant, "Agent", map[string]any{
		"role": ev.Call.Principal.Role, "machine": tenant,
	})
	b.addEntity("tool:"+lastSeg(ev.Call.ToolID), "Tool", nil)
	b.addEntity("evt:"+ev.Call.CallID, "Event", map[string]any{
		"verdict": ev.Decision.Final.String(), "risk": ev.Decision.Risk,
		"rationale": ev.Decision.Rationale,
	})
	if trace != "" {
		b.addEntity("trace:"+trace, "Trace", nil)
	}
	if ev.Result != nil && len(ev.Result.Output) > 0 && looksExternal(string(ev.Result.Output)) {
		b.addEntity("ext:"+hash8(string(ev.Result.Output)), "ExternalActor",
			map[string]any{"sample": truncate(string(ev.Result.Output), 60)})
	}

	b.addRel("agent:"+ev.Call.Principal.UserID+"@"+tenant, "performed", "evt:"+ev.Call.CallID, nil)
	b.addRel("evt:"+ev.Call.CallID, "used", "tool:"+lastSeg(ev.Call.ToolID), nil)
	if trace != "" {
		b.addRel("trace:"+trace, "includes", "evt:"+ev.Call.CallID, nil)
	}
}

// Export returns the accumulated graph in Semantica KnowledgeGraph shape.
func (b *Builder) Export() (entities []map[string]any, relationships []map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.ents {
		entities = append(entities, map[string]any{
			"id": e.ID, "type": e.Type, "props": e.Props,
		})
	}
	for _, r := range b.rels {
		relationships = append(relationships, map[string]any{
			"source": r.Source, "target": r.Target, "type": r.Type,
		})
	}
	return
}

// Stats returns node/link counts for the UI.
func (b *Builder) Stats() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("%d entities, %d relationships", len(b.ents), len(b.rels))
}

func lastSeg(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

func looksExternal(s string) bool {
	return strings.Contains(s, "@") || strings.Contains(s, "http")
}

func hash8(s string) string {
	h := sha8(s)
	return h
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
