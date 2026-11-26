// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type peopleHandler struct {
	core    *entrypoint.GatewayCore
	maxQLen int
	maxLim  int
}

func (h *peopleHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		q = r.URL.Query().Get("query")
	}

	limit := 10
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	empty := unpagedResponse([]*identity.Profile{})

	idSvc := h.core.IdentityService()
	if idSvc == nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	if q == "" || len(q) > h.maxQLen {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > h.maxLim {
		limit = h.maxLim
	}

	results, err := idSvc.SearchUsers(r.Context(), q, limit)
	if err != nil {
		h.core.Logger().Warn("people search: identity service error",
			"queryLen", len(q), "error", err)
		writeJSON(w, http.StatusOK, empty)
		return
	}
	if results == nil {
		results = []*identity.Profile{}
	}
	writeJSON(w, http.StatusOK, unpagedResponse(results))
}
