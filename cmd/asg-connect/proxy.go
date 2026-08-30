package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
)

var observedModel atomic.Value
var observedProvider atomic.Value

func init() {
	observedModel.Store("")
	observedProvider.Store("")
}

// copyRequest reads the request body, restores it for downstream use,
// and captures the "model" field from JSON into observedModel.
// It mirrors the sidecar sniffing pattern: traffic is the source of truth.
func copyRequest(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	// Restore body for subsequent reads.
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > 0 {
		var payload struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(body, &payload) == nil && payload.Model != "" {
			observedModel.Store(payload.Model)
		}
	}
	return body, nil
}

// setObservedProvider stores provider name observed from routing.
func setObservedProvider(name string) {
	if name != "" {
		observedProvider.Store(name)
	}
}

func getObservedModel() string {
	if v := observedModel.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getObservedProvider() string {
	if v := observedProvider.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// captureModel is a helper for non-HTTP paths (tests, direct body capture).
func captureModel(body []byte) {
	if len(body) == 0 {
		return
	}
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Model != "" {
		observedModel.Store(payload.Model)
	}
}
