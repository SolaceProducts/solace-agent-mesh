// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	msgPromptSharingDisabled    = "prompt sharing is disabled"
	msgShareValidationFailed    = "share validation failed"
	msgPromptSharingUnavailable = "prompt sharing is not configured on this entrypoint"
	msgPromptGroupNotFound      = "prompt group not found"
	msgGetPromptGroupErr        = "get prompt group error"
	msgPromptOwnerOnly          = "only the prompt-group owner can manage shares"
	msgSharingNeedsUserAccount  = "prompt sharing requires a user account"
	msgInternalErr              = "internal error"
)

type promptShareHandler struct {
	core            *entrypoint.GatewayCore
	promptGroupRepo *store.PromptGroupRepo
	logger          *slog.Logger
}

func isPromptSharingEnabled(core *entrypoint.GatewayCore) bool {
	return core.IdentityService() != nil
}

func (h *promptShareHandler) resolveOwnedGroup(w http.ResponseWriter, r *http.Request, notOwnerStatus int) (userID, ownerEmail, groupID string, ok bool) {
	groupID = r.PathValue("id")
	if err := validatePathUUID(groupID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", "", "", false
	}
	userID = strings.ToLower(resolveUserID(r, h.core))
	ownerEmail, emailOK := resolveCallerEmail(r)
	if !emailOK {
		if rbac.IsSystemPrincipal(userID) {
			writeError(w, http.StatusForbidden, msgSharingNeedsUserAccount)
			return "", "", "", false
		}
		writeAuditActorMissing(w, h.logger, r, userID)
		return "", "", "", false
	}

	group, err := h.promptGroupRepo.GetByID(r.Context(), groupID)
	if err != nil {
		h.logger.Error(msgGetPromptGroupErr, "groupID", groupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return "", "", "", false
	}
	if group == nil {
		writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		return "", "", "", false
	}
	if strings.ToLower(group.UserID) != userID {
		if notOwnerStatus == http.StatusNotFound {
			writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		} else {
			writeError(w, notOwnerStatus, msgPromptOwnerOnly)
		}
		return "", "", "", false
	}
	return userID, ownerEmail, groupID, true
}

func resolveCallerEmail(r *http.Request) (string, bool) {
	caller, ok := auth.CallerFromRequest(r)
	if !ok {
		return "", false
	}
	email := strings.ToLower(strings.TrimSpace(caller.Email))
	if email == "" {
		return "", false
	}
	return email, true
}

func writeAuditActorMissing(w http.ResponseWriter, logger *slog.Logger, r *http.Request, userID string) {
	errID := idgen.New()
	if logger != nil {
		logger.Error("audit actor unavailable: identity has no email claim",
			"errorId", errID, "userID", userID, "path", r.URL.Path)
	}
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{
		Message: msgInternalErr,
		ErrorID: errID,
	})
}

func resolveRecipientEmail(w http.ResponseWriter, r *http.Request) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(r.PathValue("recipientEmail")))
	if !looksLikeEmail(email) {
		writeError(w, http.StatusBadRequest, "recipientEmail must be a valid email address")
		return "", false
	}
	return email, true
}

var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func looksLikeEmail(s string) bool { return emailRegex.MatchString(s) }

var allowedAccessLevels = map[string]struct{}{
	"resource_editor": {},
	"resource_viewer": {},
}

const (
	errCodeInvalidEmail        = "invalid_email"
	errCodeDuplicateEmail      = "duplicate_email"
	errCodeCannotShareWithSelf = "cannot_share_with_self"
	errCodeInvalidAccessLevel  = "invalid_access_level"
)

const maxSharesPerRequest = 100

func validateShareBatch(shares []entrypoint.ShareRequest, ownerEmail string) map[string][]string {
	errs := map[string][]string{}
	owner := strings.ToLower(strings.TrimSpace(ownerEmail))
	seen := map[string]struct{}{}
	for i, s := range shares {
		field := fmt.Sprintf("shares[%d].userEmail", i)
		email := strings.ToLower(strings.TrimSpace(s.UserEmail))
		if email == owner {
			errs[field] = append(errs[field], errCodeCannotShareWithSelf)
		}
		if !looksLikeEmail(email) {
			errs[field] = append(errs[field], errCodeInvalidEmail)
		} else if _, found := seen[email]; found {
			errs[field] = append(errs[field], errCodeDuplicateEmail)
		} else {
			seen[email] = struct{}{}
		}
		if s.AccessLevel != "" {
			if _, ok := allowedAccessLevels[s.AccessLevel]; !ok {
				levelField := fmt.Sprintf("shares[%d].accessLevel", i)
				errs[levelField] = append(errs[levelField], errCodeInvalidAccessLevel)
			}
		}
	}
	return errs
}

func writeValidationError(w http.ResponseWriter, logger *slog.Logger, msg string, errs map[string][]string) {
	errID := idgen.New()
	if logger != nil {
		logger.Warn(msg, "errorId", errID, "validationDetails", errs)
	}
	writeJSON(w, http.StatusBadRequest, ErrorResponse{
		Message:           "validation failed",
		ErrorID:           errID,
		ValidationDetails: errs,
	})
}

func (h *promptShareHandler) handleGetShares(w http.ResponseWriter, r *http.Request) {
	if !isPromptSharingEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, msgPromptSharingDisabled)
		return
	}
	_, _, groupID, ok := h.resolveOwnedGroup(w, r, http.StatusNotFound)
	if !ok {
		return
	}

	svc := h.core.PromptSharing()
	var shares []entrypoint.PromptShareResponse
	if svc != nil {
		var err error
		shares, err = svc.GetShares(r.Context(), groupID)
		if err != nil {
			h.logger.Error("get prompt shares error", "groupID", groupID, "error", err)
			writeError(w, http.StatusInternalServerError, msgInternalErr)
			return
		}
	}
	if shares == nil {
		shares = []entrypoint.PromptShareResponse{}
	}
	writeJSON(w, http.StatusOK, unpagedResponse(shares))
}

func (h *promptShareHandler) resolveShareMutation(w http.ResponseWriter, r *http.Request, allowEmpty bool) (svc entrypoint.PromptSharingService, ownerEmail, groupID string, req entrypoint.BatchShareRequest, ok bool) {
	if !isPromptSharingEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, msgPromptSharingDisabled)
		return nil, "", "", req, false
	}
	svc = h.core.PromptSharing()
	if svc == nil {
		writeError(w, http.StatusNotImplemented, msgPromptSharingUnavailable)
		return nil, "", "", req, false
	}
	_, email, gid, resolved := h.resolveOwnedGroup(w, r, http.StatusForbidden)
	if !resolved {
		return nil, "", "", req, false
	}

	if decErr := decodeJSON(r, &req); decErr != nil {
		badRequestFromDecode(w, decErr)
		return nil, "", "", req, false
	}
	if !allowEmpty && len(req.Shares) == 0 {
		writeError(w, http.StatusBadRequest, "shares list cannot be empty")
		return nil, "", "", req, false
	}
	if len(req.Shares) > maxSharesPerRequest {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("shares list exceeds %d entries", maxSharesPerRequest))
		return nil, "", "", req, false
	}
	if errs := validateShareBatch(req.Shares, email); len(errs) > 0 {
		writeValidationError(w, h.logger, msgShareValidationFailed, errs)
		return nil, "", "", req, false
	}
	return svc, email, gid, req, true
}

func (h *promptShareHandler) handleCreateShares(w http.ResponseWriter, r *http.Request) {
	svc, ownerEmail, groupID, req, ok := h.resolveShareMutation(w, r, false)
	if !ok {
		return
	}

	resp, shareErr := svc.BatchShare(r.Context(), groupID, req.Shares, ownerEmail)
	if shareErr != nil {
		h.logger.Error("batch share error", "groupID", groupID, "error", shareErr)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *promptShareHandler) handleReplaceShares(w http.ResponseWriter, r *http.Request) {
	svc, ownerEmail, groupID, req, ok := h.resolveShareMutation(w, r, true)
	if !ok {
		return
	}

	if _, shareErr := svc.BulkReplaceShares(r.Context(), groupID, req.Shares, ownerEmail); shareErr != nil {
		h.logger.Error("bulk replace error", "groupID", groupID, "error", shareErr)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *promptShareHandler) resolveSingleShareMutation(w http.ResponseWriter, r *http.Request) (svc entrypoint.PromptSharingService, groupID, recipientEmail, actor string, ok bool) {
	if !isPromptSharingEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, msgPromptSharingDisabled)
		return nil, "", "", "", false
	}
	svc = h.core.PromptSharing()
	if svc == nil {
		writeError(w, http.StatusNotImplemented, msgPromptSharingUnavailable)
		return nil, "", "", "", false
	}
	_, ownerEmail, gid, resolved := h.resolveOwnedGroup(w, r, http.StatusForbidden)
	if !resolved {
		return nil, "", "", "", false
	}
	email, emailOK := resolveRecipientEmail(w, r)
	if !emailOK {
		return nil, "", "", "", false
	}
	return svc, gid, email, ownerEmail, true
}

func (h *promptShareHandler) handleUpdateShare(w http.ResponseWriter, r *http.Request) {
	svc, groupID, email, actor, ok := h.resolveSingleShareMutation(w, r)
	if !ok {
		return
	}

	var req entrypoint.UpdatePromptShareRequest
	if decErr := decodeJSON(r, &req); decErr != nil {
		badRequestFromDecode(w, decErr)
		return
	}
	if _, ok := allowedAccessLevels[req.AccessLevel]; !ok {
		writeValidationError(w, h.logger, msgShareValidationFailed, map[string][]string{
			"accessLevel": {errCodeInvalidAccessLevel},
		})
		return
	}

	resp, err := svc.UpdateShare(r.Context(), groupID, email, req.AccessLevel, actor)
	if errors.Is(err, entrypoint.ErrShareNotFound) {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}
	if err != nil {
		h.logger.Error("update share error", "groupID", groupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *promptShareHandler) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	svc, groupID, email, actor, ok := h.resolveSingleShareMutation(w, r)
	if !ok {
		return
	}

	err := svc.DeleteShare(r.Context(), groupID, email, actor)
	if errors.Is(err, entrypoint.ErrShareNotFound) {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}
	if err != nil {
		h.logger.Error("delete share error", "groupID", groupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
