// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const feedbackNotFoundMsg = "feedback not found"

func (h *feedbackHandler) handleGetItem(w http.ResponseWriter, r *http.Request) {
	feedbackID := r.PathValue("id")
	if err := validatePathUUID(feedbackID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	callerUserID := resolveUserID(r, h.core)

	row, err := h.feedbackRepo.GetByID(r.Context(), feedbackID)
	if err != nil {
		h.logger.Error("get feedback item", "feedbackID", feedbackID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, feedbackNotFoundMsg)
		return
	}

	if row.UserID != callerUserID {
		id, ok := auth.SubjectFromContext(r.Context())
		if !ok || id == nil {
			warnFeedbackReadDenied(h.logger, callerUserID, false)
			writeError(w, http.StatusNotFound, feedbackNotFoundMsg)
			return
		}
		reach := rbac.ResolveFeedbackReach(id.Scopes())
		if !reach.ReadsSubject(row.SubjectType, row.SubjectID) {
			warnFeedbackReadDenied(h.logger, id.UserID(), true)
			writeError(w, http.StatusNotFound, feedbackNotFoundMsg)
			return
		}
		auditCrossUserFeedbackRead(h.logger, id.UserID(), reach, 1)
	}

	writeJSON(w, http.StatusOK, h.crossUserResponse(row))
}
