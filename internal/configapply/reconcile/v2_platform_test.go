// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func newV2Platform(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	probe := httptest.NewRequest(http.MethodGet, APIV2ProbePath+APIV2ProbeQuery, nil)
	if _, pattern := mux.Handler(probe); pattern == "" {
		mux.HandleFunc("GET "+APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, []struct{}{})
		})
	}
	version := httptest.NewRequest(http.MethodGet, versionEndpoint, nil)
	if _, pattern := mux.Handler(version); pattern != "GET "+versionEndpoint {
		mux.HandleFunc("GET "+versionEndpoint, http.NotFound)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newV2PlatformClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	return NewClient(target.Resolved{URL: newV2Platform(t, mux).URL})
}

func servedOnV2(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == APIV2ProbePath:
			writeV2Page(w, []struct{}{})
		case r.Method == http.MethodGet && r.URL.Path == versionEndpoint:
			http.NotFound(w, r)
		default:
			h(w, r)
		}
	})
}

func writeV2Single(w http.ResponseWriter, status int, v any) {
	entplatform.WriteJSON(w, status, entplatform.DataResponse[any]{Data: v})
}

func writeV2Page[T any](w http.ResponseWriter, rows []T) {
	if rows == nil {
		rows = []T{}
	}
	entplatform.WriteJSON(w, http.StatusOK, entplatform.PaginatedResponse[T]{
		Data: rows,
		Meta: entplatform.NewPaginationMeta(1, max(len(rows), 1), len(rows)),
	})
}

func writeV2Error(w http.ResponseWriter, status int, message string, details map[string][]string) {
	entplatform.WriteErrorWithLoggedID(w, status, message, details, "00000000-0000-7000-8000-000000000000")
}
