// Package audit persists decisions/events and streams them to the SOC analysis
// plane. The MVP uses an in-memory / stdout sink; production writes to
// Postgres/ClickHouse and publishes to NATS/Kafka.
package audit

import (
	"encoding/json"
	"log"

	"github.com/dedarek/agent-security-gateway/api"
)

// Sink is where decision events go.
type Sink interface {
	Write(ev api.Event) error
}

// StdoutSink is a trivial sink for the MVP: pretty-prints each event.
type StdoutSink struct{}

func (StdoutSink) Write(ev api.Event) error {
	b, _ := json.Marshal(struct {
		Session  string `json:"session"`
		Tool     string `json:"tool"`
		Verdict  string `json:"verdict"`
		Risk     int    `json:"risk"`
		Rational string `json:"rationale"`
	}{
		Session:  ev.SessionID,
		Tool:     ev.Call.ToolID,
		Verdict:  ev.Decision.Final.String(),
		Risk:     ev.Decision.Risk,
		Rational: ev.Decision.Rationale,
	})
	log.Printf("EVENT %s", b)
	return nil
}
