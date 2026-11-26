// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestValidatePathUUID(t *testing.T) {
	if validatePathUUID("") == nil {
		t.Error("empty id should be rejected")
	}
	if validatePathUUID("not-a-uuid") == nil {
		t.Error("malformed id should be rejected")
	}
	if err := validatePathUUID(idgen.New()); err != nil {
		t.Errorf("valid UUID rejected: %v", err)
	}
}

func TestDecodeJSON(t *testing.T) {
	type body struct {
		A int `json:"a"`
	}
	var v body
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("")), &v); err == nil {
		t.Error("empty body should error")
	}
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"b":1}`)), &v); err == nil {
		t.Error("unknown field should error")
	}
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":3}`)), &v); err != nil || v.A != 3 {
		t.Errorf("valid decode failed: v=%+v err=%v", v, err)
	}
}

func TestParsePagination(t *testing.T) {
	pn, ps := parsePagination(httptest.NewRequest(http.MethodGet, "/?pageNumber=2&pageSize=50", nil))
	if pn != 2 || ps != 50 {
		t.Errorf("got (%d,%d), want (2,50)", pn, ps)
	}
	_, ps = parsePagination(httptest.NewRequest(http.MethodGet, "/?pageSize=999", nil))
	if ps != 20 {
		t.Errorf("oversized pageSize = %d, want default 20", ps)
	}
	pn, ps = parsePagination(httptest.NewRequest(http.MethodGet, "/", nil))
	if pn != 1 || ps != 20 {
		t.Errorf("defaults = (%d,%d), want (1,20)", pn, ps)
	}
}

func TestResolveUserID_NoIdentity(t *testing.T) {
	rr := httptest.NewRecorder()
	if _, ok := resolveUserID(rr, httptest.NewRequest(http.MethodGet, "/", nil), rbac.ScopeNotifyReadOthers); ok {
		t.Error("expected resolveUserID to fail without an identity")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}
