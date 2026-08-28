package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dedarek/agent-security-gateway/internal/db"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func TestPoliciesAllReturnsEmptyArrayNotNull(t *testing.T) {
	// Setup in-memory DB with empty policies table
	sqlDB, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	oldDB := policiesDB
	SetPoliciesDB(sqlDB)
	defer SetPoliciesDB(oldDB)

	// Need a Server instance; New requires a Store but handlePoliciesList only uses policiesDB global.
	st, _ := store.Open("")
	s := New(st, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/policies?all=true", nil)
	w := httptest.NewRecorder()
	s.handlePoliciesList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	got := strings.TrimSpace(w.Body.String())
	if got == "null" {
		t.Fatalf("want [], got null")
	}
	if got != "[]" {
		t.Fatalf("want [], got %q", got)
	}
}
