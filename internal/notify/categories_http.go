// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type CategoryPreferenceDTO struct {
	Category string `json:"category"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
}

type setCategoryPreferenceRequest struct {
	Category string `json:"category"`
	Enabled  *bool  `json:"enabled"`
}

func (n *NotifyCore) handleCategoryPreferencesList(w http.ResponseWriter, r *http.Request) {
	if n.categoryPrefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences are not enabled (no database configured)", n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifyReadOthers)
	if !ok {
		return
	}
	disabled, err := n.categoryPrefs.DisabledCategories(r.Context(), userID)
	if err != nil {
		writeServerError(w, "failed to list category preferences", err, n.logger)
		return
	}
	caller, _ := auth.SubjectFromContext(r.Context())
	cats := Categories()
	data := make([]CategoryPreferenceDTO, 0, len(cats))
	for _, c := range cats {
		if c.offeredTo(caller) {
			data = append(data, CategoryPreferenceDTO{Category: c.Key, Label: c.Label, Enabled: !disabled[c.Key]})
		}
	}
	pageNumber, pageSize := parsePagination(r)
	writeJSON(w, http.StatusOK, PaginatedResponse[CategoryPreferenceDTO]{
		Data: data,
		Meta: paginationMeta{Pagination: newPaginationInfo(pageNumber, pageSize, len(data))},
	})
}

func (n *NotifyCore) handleCategoryPreferencesSet(w http.ResponseWriter, r *http.Request) {
	if n.categoryPrefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences are not enabled (no database configured)", n.logger)
		return
	}
	userID, ok := n.targetUser(w, r, rbac.ScopeNotifySendOthers)
	if !ok {
		return
	}
	var req setCategoryPreferenceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), n.logger)
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required", n.logger)
		return
	}
	cat, err := n.lookupOfferedCategory(r, req.Category)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), n.logger)
		return
	}
	if err := n.categoryPrefs.SetCategory(r.Context(), userID, cat.Key, *req.Enabled); err != nil {
		writeServerError(w, "failed to set category preference", err, n.logger)
		return
	}
	writeJSON(w, http.StatusOK, CategoryPreferenceDTO{Category: cat.Key, Label: cat.Label, Enabled: *req.Enabled})
}

func (n *NotifyCore) lookupOfferedCategory(r *http.Request, key string) (Category, error) {
	cat, err := LookupCategory(key)
	if err != nil {
		return Category{}, err
	}
	if caller, _ := auth.SubjectFromContext(r.Context()); !cat.offeredTo(caller) {
		return Category{}, unknownCategoryError(key)
	}
	return cat, nil
}
