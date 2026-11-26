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

type promptHandler struct {
	core       *entrypoint.GatewayCore
	groupRepo  *store.PromptGroupRepo
	promptRepo *store.PromptRepo
	shareRepo  *store.ResourceAccessShareRepo
	logger     *slog.Logger
}

type PromptResponse struct {
	ID          string  `json:"id"`
	PromptText  string  `json:"promptText"`
	GroupID     string  `json:"groupId"`
	UserID      string  `json:"userId"`
	CreatedBy   string  `json:"createdBy"`
	Version     int     `json:"version"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Category    *string `json:"category"`
	Command     *string `json:"command"`
	CreatedTime string  `json:"createdAt"`
	UpdatedTime string  `json:"updatedAt"`
}

type CreatePromptRequest struct {
	PromptText  string  `json:"promptText"`
	Description *string `json:"description,omitempty"`
}

type UpdatePromptRequest struct {
	PromptText  string  `json:"promptText"`
	Description *string `json:"description,omitempty"`
}

func (h *promptHandler) checkGroupPermission(r *http.Request, groupID, userID, required string) (int, string) {
	role, err := resolvePromptGroupRole(r.Context(), h.core, h.groupRepo, h.shareRepo, groupID, userID)
	if err != nil {
		h.logger.Error("check permission error", "groupID", groupID, "userID", userID, "error", err)
		return http.StatusInternalServerError, msgInternalErr
	}
	if role == "" {
		return http.StatusNotFound, "prompt group not found"
	}
	switch required {
	case "read":
		return 0, ""
	case "write":
		if role == "viewer" {
			return http.StatusForbidden, "not authorized to modify this prompt group"
		}
		return 0, ""
	case "delete":
		if role != "owner" {
			return http.StatusForbidden, "not authorized to delete this prompt"
		}
		return 0, ""
	}
	return 0, ""
}

func (h *promptHandler) handlePromptPatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/prompts/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	promptID := parts[0]
	suffix := ""
	if len(parts) > 1 {
		suffix = parts[1]
	}

	switch suffix {
	case "makeProduction":
		r.SetPathValue("id", promptID)
		h.handleMakeProduction(w, r)
	case "":
		r.SetPathValue("id", promptID)
		h.handleUpdate(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *promptHandler) handleListVersions(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	groupID := r.PathValue("id")
	if err := validatePathUUID(groupID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	if code, msg := h.checkGroupPermission(r, groupID, userID, "read"); code != 0 {
		writeError(w, code, msg)
		return
	}

	prompts, err := h.promptRepo.ListByGroup(r.Context(), groupID)
	if err != nil {
		h.logger.Error("list prompts error", "groupID", groupID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]PromptResponse, 0, len(prompts))
	for _, p := range prompts {
		result = append(result, promptToResponse(p))
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

func (h *promptHandler) handleCreateVersion(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	groupID := r.PathValue("id")
	if err := validatePathUUID(groupID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	if code, msg := h.checkGroupPermission(r, groupID, userID, "write"); code != 0 {
		writeError(w, code, msg)
		return
	}

	var req CreatePromptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.PromptText == "" || len(req.PromptText) > 10000 {
		writeError(w, http.StatusBadRequest, "promptText is required (1-10000 characters)")
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, "prompt group not found")
		return
	}

	maxVer, _ := h.promptRepo.MaxVersion(r.Context(), groupID)

	prompt := &store.Prompt{
		PromptText:  req.PromptText,
		GroupID:     groupID,
		UserID:      userID,
		Version:     maxVer + 1,
		Name:        &group.Name,
		Description: group.Description,
		Category:    group.Category,
		Command:     group.Command,
	}

	if err := h.promptRepo.Create(r.Context(), prompt); err != nil {
		h.logger.Error("create prompt version error", "groupID", groupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	writeJSON(w, http.StatusCreated, promptToResponse(prompt))
}

func (h *promptHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	promptID := r.PathValue("id")
	if err := validatePathUUID(promptID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	prompt, err := h.promptRepo.GetByID(r.Context(), promptID)
	if err != nil {
		h.logger.Error("fetch prompt for update", "promptID", promptID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if prompt == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	if code, msg := h.checkGroupPermission(r, prompt.GroupID, userID, "write"); code != 0 {
		writeError(w, code, msg)
		return
	}

	var req UpdatePromptRequest
	if err = decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.PromptText == "" || len(req.PromptText) > 10000 {
		writeError(w, http.StatusBadRequest, "promptText is required (1-10000 characters)")
		return
	}

	prompt.PromptText = req.PromptText
	if err = h.promptRepo.Update(r.Context(), prompt); err != nil {
		h.logger.Error("update prompt error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), prompt.GroupID)
	if err != nil {
		h.logger.Warn("fetch group for updated_at refresh", "error", err, "groupID", prompt.GroupID)
	}
	if group != nil {
		if err := h.groupRepo.Update(r.Context(), group); err != nil {
			h.logger.Warn("refresh group updated_at", "error", err, "groupID", prompt.GroupID)
		}
	}

	writeJSON(w, http.StatusOK, promptToResponse(prompt))
}

func (h *promptHandler) handleMakeProduction(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	promptID := r.PathValue("id")
	if err := validatePathUUID(promptID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	prompt, err := h.promptRepo.GetByID(r.Context(), promptID)
	if err != nil {
		h.logger.Error("fetch prompt for promote", "promptID", promptID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if prompt == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	if code, msg := h.checkGroupPermission(r, prompt.GroupID, userID, "write"); code != 0 {
		writeError(w, code, msg)
		return
	}

	if err := h.groupRepo.SetProductionPrompt(r.Context(), prompt.GroupID, promptID); err != nil {
		h.logger.Error("set production prompt error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	writeJSON(w, http.StatusOK, promptToResponse(prompt))
}

func (h *promptHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	promptID := r.PathValue("id")
	if err := validatePathUUID(promptID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	prompt, err := h.promptRepo.GetByID(r.Context(), promptID)
	if err != nil {
		h.logger.Error("fetch prompt for delete", "promptID", promptID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if prompt == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	if code, msg := h.checkGroupPermission(r, prompt.GroupID, userID, "delete"); code != 0 {
		writeError(w, code, msg)
		return
	}

	count, err := h.promptRepo.CountPrompts(r.Context(), prompt.GroupID)
	if err != nil {
		h.logger.Error("count prompts in group", "groupID", prompt.GroupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if count <= 1 {
		if !deletePromptGroupShares(w, r, h.core, h.logger, prompt.GroupID, userID) {
			return
		}
		if err = h.groupRepo.Delete(r.Context(), prompt.GroupID); err != nil {
			h.logger.Error("delete prompt group error", "groupID", prompt.GroupID, "error", err)
			writeError(w, http.StatusInternalServerError, msgInternalErr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), prompt.GroupID)
	if err != nil {
		h.logger.Warn("fetch group for production prompt auto-promote", "error", err, "groupID", prompt.GroupID)
	}
	if group != nil && group.ProductionPromptID != nil && *group.ProductionPromptID == promptID {
		latest, latestErr := h.promptRepo.GetLatestExcluding(r.Context(), prompt.GroupID, promptID)
		if latestErr != nil {
			h.logger.Warn("fetch latest prompt for auto-promote", "error", latestErr, "groupID", prompt.GroupID)
		}
		if latest != nil {
			if err := h.groupRepo.SetProductionPrompt(r.Context(), prompt.GroupID, latest.ID); err != nil {
				h.logger.Warn("auto-promote production prompt", "error", err, "groupID", prompt.GroupID, "promptID", latest.ID)
			}
		}
	}

	if err := h.promptRepo.Delete(r.Context(), promptID); err != nil {
		h.logger.Error("delete prompt error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
