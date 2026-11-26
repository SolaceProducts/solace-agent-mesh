// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/capabilities"
)

func TestHandleCapabilities_EnvelopeAndWireShape(t *testing.T) {
	h := &configHandler{capabilityRegistry: capabilities.NewRegistryForPlatform("windows", nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config/capabilities", h.handleCapabilities)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/capabilities", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.Bytes()
	var resp PaginatedResponse[capabilities.Resolved]
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Meta.Pagination.Count != len(resp.Data) || resp.Meta.Pagination.PageNumber != 1 {
		t.Errorf("unexpected pagination meta: %+v", resp.Meta.Pagination)
	}

	var gateway *capabilities.Resolved
	for i := range resp.Data {
		if resp.Data[i].Key == "event_mesh_gateway" {
			gateway = &resp.Data[i]
		}
	}
	if gateway == nil {
		t.Fatal("event_mesh_gateway missing from response")
	}
	if gateway.Status != capabilities.MaturityBeta || gateway.Platform != "windows" {
		t.Errorf("got status=%q platform=%q, want beta/windows", gateway.Status, gateway.Platform)
	}

	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	switch got := raw.Data[0]["status"]; got {
	case "experimental", "stable", "beta", "early_access", "unavailable":
	default:
		t.Errorf("wire status not lowercase enum: %v", got)
	}
}

func TestHandleCapabilities_NilRegistryReturnsEmptyEnvelope(t *testing.T) {
	h := &configHandler{}
	rec := httptest.NewRecorder()
	h.handleCapabilities(rec, httptest.NewRequest("GET", "/api/v1/config/capabilities", nil))

	var resp PaginatedResponse[capabilities.Resolved]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty data, got %d items", len(resp.Data))
	}
}
