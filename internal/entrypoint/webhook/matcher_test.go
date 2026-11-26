// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouter_MatchAndParams(t *testing.T) {
	routes := []*RouteConfig{
		{Name: "exact", Match: RouteMatch{Path: "/orders", Method: "POST"}},
		{Name: "param", Match: RouteMatch{Path: "/orders/{id}", Method: "PUT"}},
		{Name: "ping", Match: RouteMatch{Path: "/ping", Method: "POST"}},
	}

	var (
		rt      *router
		matched string
		params  map[string]string
	)
	rt = buildRouter(routes, func(route *RouteConfig, _ http.ResponseWriter, r *http.Request) {
		matched = route.Name
		params = rt.extractPathParams(route.Name, r)
	})

	tests := []struct {
		method, path string
		wantRoute    string
		wantStatus   int
		wantParam    string
	}{
		{"POST", "/orders", "exact", 200, ""},
		{"PUT", "/orders/42", "param", 200, "42"},
		{"POST", "/ping", "ping", 200, ""},
		{"GET", "/orders", "", http.StatusMethodNotAllowed, ""},
		{"POST", "/nope", "", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		matched, params = "", nil
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rt.Handler().ServeHTTP(rec, req)
		if tt.wantStatus != 200 {
			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s: status = %d, want %d", tt.method, tt.path, rec.Code, tt.wantStatus)
			}
			continue
		}
		if matched != tt.wantRoute {
			t.Errorf("%s %s: matched %q, want %q", tt.method, tt.path, matched, tt.wantRoute)
		}
		if tt.wantParam != "" && params["id"] != tt.wantParam {
			t.Errorf("%s %s: param id = %q, want %q", tt.method, tt.path, params["id"], tt.wantParam)
		}
	}
}

func TestPathParamNames(t *testing.T) {
	got := pathParamNames("/a/{id}/b/{kind}")
	if len(got) != 2 || got[0] != "id" || got[1] != "kind" {
		t.Errorf("pathParamNames = %v, want [id kind]", got)
	}
	if n := pathParamNames("/static/path"); len(n) != 0 {
		t.Errorf("expected no params, got %v", n)
	}
}
