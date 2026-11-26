// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type projectShareHandler struct {
	core        *entrypoint.GatewayCore
	projectRepo *store.ProjectRepo
	logger      *slog.Logger
}

func (h *projectShareHandler) handleGetShares(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := strings.ToLower(resolveUserID(r, h.core))

	project, err := h.projectRepo.GetByID(r.Context(), projectID)
	if err != nil {
		h.logger.Error("get project error", "projectID", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	svc := h.core.ProjectSharing()
	if strings.ToLower(project.UserID) != userID {
		if svc != nil {
			hasAccess, accessErr := svc.HasViewAccess(r.Context(), projectID, userID)
			if accessErr != nil {
				h.logger.Error("check shared access error", "error", accessErr)
				writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if !hasAccess {
				writeError(w, http.StatusNotFound, "project not found")
				return
			}
		} else {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
	}
	if svc == nil {
		writeJSON(w, http.StatusOK, entrypoint.ProjectSharesResponse{
			ProjectID:  projectID,
			OwnerEmail: project.UserID,
			Shares:     []entrypoint.ShareResponse{},
		})
		return
	}

	resp, err := svc.GetProjectShares(r.Context(), projectID)
	if err != nil {
		h.logger.Error("get project shares error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *projectShareHandler) handleCreateShares(w http.ResponseWriter, r *http.Request) {
	svc := h.core.ProjectSharing()
	if svc == nil {
		writeError(w, http.StatusNotImplemented, "project sharing not configured")
		return
	}

	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := strings.ToLower(resolveUserID(r, h.core))

	project, err := h.projectRepo.GetByID(r.Context(), projectID)
	if err != nil {
		h.logger.Error("get project error", "projectID", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if strings.ToLower(project.UserID) != userID {
		writeError(w, http.StatusForbidden, "only the project owner can manage shares")
		return
	}

	var req entrypoint.BatchShareRequest
	if decErr := decodeJSON(r, &req); decErr != nil {
		badRequestFromDecode(w, decErr)
		return
	}
	if len(req.Shares) == 0 {
		writeError(w, http.StatusBadRequest, "shares list cannot be empty")
		return
	}
	if len(req.Shares) > 50 {
		writeError(w, http.StatusBadRequest, "maximum 50 shares per request")
		return
	}

	for _, s := range req.Shares {
		if strings.ToLower(s.UserEmail) == userID {
			writeError(w, http.StatusBadRequest, "cannot share a project with yourself")
			return
		}
	}

	resp, shareErr := svc.BatchShare(r.Context(), projectID, req.Shares, userID)
	if shareErr != nil {
		h.logger.Error("batch share error", "error", shareErr)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *projectShareHandler) handleDeleteShares(w http.ResponseWriter, r *http.Request) {
	svc := h.core.ProjectSharing()
	if svc == nil {
		writeError(w, http.StatusNotImplemented, "project sharing not configured")
		return
	}

	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := strings.ToLower(resolveUserID(r, h.core))

	project, err := h.projectRepo.GetByID(r.Context(), projectID)
	if err != nil {
		h.logger.Error("get project error", "projectID", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if strings.ToLower(project.UserID) != userID {
		writeError(w, http.StatusForbidden, "only the project owner can manage shares")
		return
	}

	var req entrypoint.BatchDeleteRequest
	if decErr := decodeJSON(r, &req); decErr != nil {
		badRequestFromDecode(w, decErr)
		return
	}
	if len(req.UserEmails) == 0 {
		writeError(w, http.StatusBadRequest, "userEmails list cannot be empty")
		return
	}

	resp, unshareErr := svc.BatchUnshare(r.Context(), projectID, req.UserEmails)
	if unshareErr != nil {
		h.logger.Error("batch unshare error", "error", unshareErr)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
