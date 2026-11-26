// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func newV2Platform(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	probe := httptest.NewRequest(http.MethodGet, reconcile.APIV2ProbePath+reconcile.APIV2ProbeQuery, nil)
	if _, pattern := mux.Handler(probe); pattern == "" {
		mux.HandleFunc("GET "+reconcile.APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, []struct{}{})
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newV2PlatformClient(t *testing.T, mux *http.ServeMux) *reconcile.Client {
	t.Helper()
	return reconcile.NewClient(target.Resolved{URL: newV2Platform(t, mux).URL})
}

func writeV2Single(w http.ResponseWriter, v any) {
	entplatform.WriteJSON(w, http.StatusOK, entplatform.DataResponse[any]{Data: v})
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

func writeV2Error(w http.ResponseWriter, status int, message string) {
	entplatform.WriteErrorWithLoggedID(w, status, message, nil, "00000000-0000-7000-8000-000000000000")
}
