// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestHealthEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] == "" {
		t.Error("expected non-empty status message")
	}
}

func newTestPeopleHandler(t *testing.T, idSvc *fakeIdentitySvc) *peopleHandler {
	t.Helper()
	adapter, _, _ := setupTestAdapter(t)
	if idSvc != nil {
		adapter.core.SetIdentityService(idSvc)
	}
	return &peopleHandler{core: adapter.core, maxQLen: 50, maxLim: 25}
}

func TestPeopleSearchReturnsEmptyList(t *testing.T) {
	h := newTestPeopleHandler(t, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/people/search", h.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/people/search?q=alice", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[any]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data == nil {
		t.Error("expected data to be non-nil empty array")
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty data array, got %d items", len(resp.Data))
	}
}

type fakeIdentitySvc struct {
	results []*identity.Profile
	err     error
	gotQ    string
	gotLim  int
}

func (f *fakeIdentitySvc) Resolve(_ context.Context, _ map[string]any) (*identity.Resolution, error) {
	return nil, nil
}

func (f *fakeIdentitySvc) SearchUsers(_ context.Context, q string, limit int) ([]*identity.Profile, error) {
	f.gotQ = q
	f.gotLim = limit
	return f.results, f.err
}

func TestPeopleSearchReturnsResultsFromIdentityService(t *testing.T) {
	fake := &fakeIdentitySvc{
		results: []*identity.Profile{
			{ID: "alice@example.com", DisplayName: "Alice", WorkEmail: "alice@example.com"},
		},
	}
	h := newTestPeopleHandler(t, fake)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/people/search", h.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/people/search?q=alice&pageSize=5", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp PaginatedResponse[*identity.Profile]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "alice@example.com" {
		t.Errorf("data = %+v, want one Alice", resp.Data)
	}
	if fake.gotQ != "alice" || fake.gotLim != 5 {
		t.Errorf("searcher called with (%q, %d), want (alice, 5)", fake.gotQ, fake.gotLim)
	}
}

func TestPeopleSearch_LimitClampedToMax(t *testing.T) {
	fake := &fakeIdentitySvc{}
	h := newTestPeopleHandler(t, fake)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/people/search", h.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/people/search?q=al&pageSize=999", nil)
	mux.ServeHTTP(rec, req)

	if fake.gotLim != 25 {
		t.Errorf("limit = %d, want clamped to 25", fake.gotLim)
	}
}

func TestPeopleSearch_SearcherErrorReturnsEmpty(t *testing.T) {
	fake := &fakeIdentitySvc{err: errors.New("upstream timeout")}
	h := newTestPeopleHandler(t, fake)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/people/search", h.handleSearch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/people/search?q=al", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (errors swallowed for autocomplete), got %d", rec.Code)
	}
	var resp PaginatedResponse[*identity.Profile]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("want empty data on error, got %d", len(resp.Data))
	}
}
