// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type callerRequest struct {
	Caller caller `json:"-"`
}

func TestHandle_BindsCaller(t *testing.T) {
	var got caller
	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", Handle(func(_ context.Context, req callerRequest) (struct{}, error) {
		got = req.Caller
		return struct{}{}, nil
	}, "Unable to read caller").ServeHTTP)

	for name, tc := range map[string]struct {
		configured, origin, host, forwardedHost, want string
	}{
		"the arrival host":                     {host: "sam.example.com", want: "http://sam.example.com"},
		"an origin on another host is ignored": {origin: "http://169.254.169.254", host: "sam.example.com", want: "http://sam.example.com"},
		"a forwarded host is ignored":          {forwardedHost: "169.254.169.254", host: "sam.example.com", want: "http://sam.example.com"},
		"the configured address wins":          {configured: "https://gw.example.com/", origin: "http://evil.example.com", forwardedHost: "evil.example.com", host: "internal:8080", want: "https://gw.example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.configured)
			req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			req.Header.Set("Authorization", "Bearer abc")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			expectStatus(t, rec, http.StatusNoContent)
			if got.BaseURL != tc.want || got.Authorization != "Bearer abc" {
				t.Errorf("caller = %+v, want base URL %q and the Authorization header", got, tc.want)
			}
		})
	}
}

func TestHandle_CallerNeedsAHostOrAValidConfiguredAddress(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /whoami", Handle(func(context.Context, callerRequest) (struct{}, error) {
		return struct{}{}, nil
	}, "Unable to read caller").ServeHTTP)

	for name, tc := range map[string]struct{ configured, host string }{
		"no host":                          {},
		"a configured address with a path": {configured: "https://gw.example.com/app", host: "sam.example.com"},
		"a configured address, no scheme":  {configured: "gw.example.com", host: "sam.example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.configured)
			req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			expectDetail(t, rec, http.StatusBadRequest, "Host")
		})
	}
}

type locatedWidget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (w locatedWidget) Location() string { return platpkg.APIV2Prefix + "/gadgets/" + w.ID }

func TestHandle_LocatorSetsLocation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /widgets/{id}/gadgets", Handle(func(context.Context, IDRequest) (locatedWidget, error) {
		return locatedWidget{ID: widgetID, Type: "gadget"}, nil
	}, "Unable to create gadget").ServeHTTP)

	rec := serveWidget(mux, http.MethodPost, "/widgets/"+widgetID+"/gadgets", "")
	expectStatus(t, rec, http.StatusCreated)
	if want := platpkg.APIV2Prefix + "/gadgets/" + widgetID; rec.Header().Get("Location") != want {
		t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), want)
	}
}

func serveWidget(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}
