// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemovedV1Routes_AnswerGoneWithUpgradeMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/budgets", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	registerRemovedV1Routes(mux)

	removed := httptest.NewRecorder()
	mux.ServeHTTP(removed, httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil))
	if removed.Code != http.StatusGone {
		t.Fatalf("removed route status = %d, want %d", removed.Code, http.StatusGone)
	}
	var body struct {
		Message string `json:"message"`
		ErrorID string `json:"errorId"`
	}
	if err := json.Unmarshal(removed.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Message != UpgradeCLIMessage || body.ErrorID == "" {
		t.Errorf("body = %+v, want the upgrade message and an errorId", body)
	}

	retained := httptest.NewRecorder()
	mux.ServeHTTP(retained, httptest.NewRequest(http.MethodGet, "/api/v1/platform/budgets", nil))
	if retained.Code != http.StatusOK {
		t.Errorf("retained route status = %d, want %d", retained.Code, http.StatusOK)
	}
}
