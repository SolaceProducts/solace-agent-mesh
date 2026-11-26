// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func isV2Probe(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == reconcile.APIV2ProbePath && "?"+r.URL.RawQuery == reconcile.APIV2ProbeQuery
}

func servedOnV2(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isV2Probe(r) {
			writeV2Page(w, []struct{}{})
			return
		}
		h(w, r)
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

func writeV2Error(w http.ResponseWriter, status int, message string) {
	entplatform.WriteErrorWithLoggedID(w, status, message, nil, "00000000-0000-7000-8000-000000000000")
}
