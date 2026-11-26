// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type promptGroupHandler struct {
	core       *entrypoint.GatewayCore
	groupRepo  *store.PromptGroupRepo
	promptRepo *store.PromptRepo
	shareRepo  *store.ResourceAccessShareRepo
	logger     *slog.Logger
}

type CreatePromptGroupRequest struct {
	Name          string  `json:"name"`
	Description   *string `json:"description"`
	Category      *string `json:"category"`
	Command       *string `json:"command"`
	InitialPrompt string  `json:"initialPrompt"`
}

type UpdatePromptGroupRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Category      *string `json:"category"`
	Command       *string `json:"command"`
	InitialPrompt *string `json:"initialPrompt"`
}

type PromptGroupResponse struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        *string         `json:"description"`
	Category           *string         `json:"category"`
	Command            *string         `json:"command"`
	UserID             string          `json:"userId"`
	CreatedBy          string          `json:"createdBy"`
	AuthorName         *string         `json:"authorName"`
	ProductionPromptID *string         `json:"productionPromptId"`
	ProductionPrompt   *PromptResponse `json:"productionPrompt"`
	IsShared           bool            `json:"isShared"`
	IsPinned           bool            `json:"isPinned"`
	AccessLevel        string          `json:"accessLevel,omitempty"`
	CreatedTime        string          `json:"createdAt"`
	UpdatedTime        string          `json:"updatedAt"`
}

type PromptExportResponse struct {
	Version    string              `json:"version"`
	ExportedAt string              `json:"exportedAt"`
	Prompt     PromptExportPayload `json:"prompt"`
}

type PromptExportPayload struct {
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Category    *string        `json:"category"`
	Command     *string        `json:"command"`
	PromptText  string         `json:"promptText"`
	Metadata    map[string]any `json:"metadata"`
}

type PromptImportRequest struct {
	PromptData PromptImportData    `json:"promptData"`
	Options    PromptImportOptions `json:"options"`
}

type PromptImportData struct {
	Version    string              `json:"version"`
	ExportedAt json.RawMessage     `json:"exportedAt"`
	Prompt     PromptExportPayload `json:"prompt"`
}

type PromptImportOptions struct {
	PreserveCommand  *bool `json:"preserveCommand"`
	PreserveCategory *bool `json:"preserveCategory"`
}

type PromptImportResponse struct {
	Success       bool     `json:"success"`
	PromptGroupID string   `json:"promptGroupId"`
	Warnings      []string `json:"warnings"`
}

func isPromptsEnabled(core *entrypoint.GatewayCore) bool {
	return core.Store() != nil
}

func (h *promptGroupHandler) checkPermission(r *http.Request, groupID, userID, required string) (int, string) {
	role, err := resolvePromptGroupRole(r.Context(), h.core, h.groupRepo, h.shareRepo, groupID, userID)
	if err != nil {
		h.logger.Error("check permission error", "error", err)
		return http.StatusInternalServerError, msgInternalErr
	}
	if role == "" {
		return http.StatusNotFound, msgPromptGroupNotFound
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
			return http.StatusForbidden, "not authorized to delete this prompt group"
		}
		return 0, ""
	}
	return 0, ""
}

func resolveSharedGroupRole(ctx context.Context, groupRepo *store.PromptGroupRepo, shareRepo *store.ResourceAccessShareRepo, groupID, userID string) (string, error) {
	if shareRepo == nil {
		return "", fmt.Errorf("resolveSharedGroupRole: shareRepo not wired")
	}
	userID = strings.ToLower(userID)
	group, err := groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return "", err
	}
	if group == nil {
		return "", nil
	}
	if strings.ToLower(group.UserID) == userID {
		return "owner", nil
	}
	levels, err := shareRepo.GetUserAccessLevels(ctx, userID, store.ResourceTypePromptGroup)
	if err != nil {
		return "", err
	}
	switch levels[groupID] {
	case "resource_editor":
		return "editor", nil
	case "resource_viewer":
		return "viewer", nil
	}
	return "", nil
}

func resolvePromptGroupRole(ctx context.Context, core *entrypoint.GatewayCore, groupRepo *store.PromptGroupRepo, shareRepo *store.ResourceAccessShareRepo, groupID, userID string) (string, error) {
	if isPromptSharingEnabled(core) {
		return resolveSharedGroupRole(ctx, groupRepo, shareRepo, groupID, userID)
	}
	group, err := groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return "", err
	}
	if group == nil {
		return "", nil
	}
	if strings.EqualFold(group.UserID, userID) {
		return "owner", nil
	}
	return "", nil
}

func (h *promptGroupHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	var req CreatePromptGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || len(req.Name) > 255 {
		writeError(w, http.StatusBadRequest, "name is required (1-255 characters)")
		return
	}
	if req.InitialPrompt == "" || len(req.InitialPrompt) > 10000 {
		writeError(w, http.StatusBadRequest, "initialPrompt is required (1-10000 characters)")
		return
	}

	userID := resolveUserID(r, h.core)

	if req.Command != nil && *req.Command != "" {
		exists, err := h.groupRepo.CommandExistsForUser(r.Context(), *req.Command, userID, "")
		if err != nil {
			h.logger.Error("check command error", "error", err)
			writeError(w, http.StatusInternalServerError, msgInternalErr)
			return
		}
		if exists {
			writeError(w, http.StatusConflict, "command already exists for this user")
			return
		}
	}

	group := &store.PromptGroup{
		Name:        req.Name,
		Description: req.Description,
		Category:    req.Category,
		Command:     req.Command,
		UserID:      userID,
	}

	if err := h.groupRepo.Create(r.Context(), group); err != nil {
		h.logger.Error("create prompt group error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	prompt := &store.Prompt{
		PromptText:  req.InitialPrompt,
		GroupID:     group.ID,
		UserID:      userID,
		Version:     1,
		Name:        &group.Name,
		Description: group.Description,
		Category:    group.Category,
		Command:     group.Command,
	}
	if err := h.promptRepo.Create(r.Context(), prompt); err != nil {
		h.logger.Error("create initial prompt error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	group.ProductionPromptID = &prompt.ID
	if err := h.groupRepo.SetProductionPrompt(r.Context(), group.ID, prompt.ID); err != nil {
		h.logger.Error("set production prompt error", "error", err)
	}

	resp := groupToResponse(group)
	pr := promptToResponse(prompt)
	resp.ProductionPrompt = &pr
	writeJSON(w, http.StatusCreated, resp)
}

func (h *promptGroupHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	userID := resolveUserID(r, h.core)
	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	category := r.URL.Query().Get("category")
	search := r.URL.Query().Get("search")

	var sharedIDs []string
	if isPromptSharingEnabled(h.core) {
		levels, err := h.shareRepo.GetUserAccessLevels(r.Context(), strings.ToLower(userID), store.ResourceTypePromptGroup)
		if err != nil {
			h.logger.Error("list shared prompt-group ids", "userID", userID, "error", err)
		}
		sharedIDs = make([]string, 0, len(levels))
		for id := range levels {
			sharedIDs = append(sharedIDs, id)
		}
	}

	offset := (page - 1) * pageSize
	groups, total, err := h.groupRepo.ListByUser(r.Context(), userID, sharedIDs, category, search, pageSize, offset)
	if err != nil {
		h.logger.Error("list prompt groups error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]PromptGroupResponse, 0, len(groups))
	promptIDs := make([]string, 0)
	for _, g := range groups {
		result = append(result, groupToResponse(g))
		if g.ProductionPromptID != nil {
			promptIDs = append(promptIDs, *g.ProductionPromptID)
		}
	}

	if len(promptIDs) > 0 {
		prompts, err := h.promptRepo.GetByIDs(r.Context(), promptIDs)
		if err == nil {
			promptMap := make(map[string]*store.Prompt, len(prompts))
			for _, p := range prompts {
				promptMap[p.ID] = p
			}
			for i, g := range groups {
				if g.ProductionPromptID != nil {
					if p, ok := promptMap[*g.ProductionPromptID]; ok {
						pr := promptToResponse(p)
						result[i].ProductionPrompt = &pr
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[PromptGroupResponse]{
		Data: result,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *promptGroupHandler) handleListAll(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	userID := strings.ToLower(resolveUserID(r, h.core))
	sharingEnabled := isPromptSharingEnabled(h.core)

	var sharedAccess map[string]string
	var sharedIDs []string
	if sharingEnabled {
		levels, err := h.shareRepo.GetUserAccessLevels(r.Context(), userID, store.ResourceTypePromptGroup)
		if err != nil {
			h.logger.Error("list shared prompt-group access levels", "userID", userID, "error", err)
		}
		sharedAccess = levels
		sharedIDs = make([]string, 0, len(levels))
		for id := range levels {
			sharedIDs = append(sharedIDs, id)
		}
	}

	groups, err := h.groupRepo.ListAllByUser(r.Context(), userID, sharedIDs)
	if err != nil {
		h.logger.Error("list all prompt groups error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]PromptGroupResponse, 0, len(groups))
	promptIDs := make([]string, 0)
	for _, g := range groups {
		resp := groupToResponse(g)
		if sharingEnabled {
			if strings.ToLower(g.UserID) == userID {
				resp.AccessLevel = "owner"
			} else if level, ok := sharedAccess[g.ID]; ok {
				resp.AccessLevel = level
			}
		}
		result = append(result, resp)
		if g.ProductionPromptID != nil {
			promptIDs = append(promptIDs, *g.ProductionPromptID)
		}
	}

	if len(promptIDs) > 0 {
		prompts, err := h.promptRepo.GetByIDs(r.Context(), promptIDs)
		if err == nil {
			promptMap := make(map[string]*store.Prompt, len(prompts))
			for _, p := range prompts {
				promptMap[p.ID] = p
			}
			for i, g := range groups {
				if g.ProductionPromptID != nil {
					if p, ok := promptMap[*g.ProductionPromptID]; ok {
						pr := promptToResponse(p)
						result[i].ProductionPrompt = &pr
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

func (h *promptGroupHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	groupID := r.PathValue("id")
	if err := validatePathUUID(groupID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := strings.ToLower(resolveUserID(r, h.core))

	if code, msg := h.checkPermission(r, groupID, userID, "read"); code != 0 {
		writeError(w, code, msg)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil {
		h.logger.Error(msgGetPromptGroupErr, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if group == nil {
		writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		return
	}

	resp := groupToResponse(group)
	if isPromptSharingEnabled(h.core) {
		if strings.ToLower(group.UserID) == userID {
			resp.AccessLevel = "owner"
		} else {
			levels, err := h.shareRepo.GetUserAccessLevels(r.Context(), userID, store.ResourceTypePromptGroup)
			if err != nil {
				h.logger.Error("get prompt-group access level", "userID", userID, "groupID", groupID, "error", err)
			} else if lvl := levels[groupID]; lvl != "" {
				resp.AccessLevel = lvl
			}
		}
	}
	h.attachProductionPrompt(r, &resp, group)
	writeJSON(w, http.StatusOK, resp)
}

func (h *promptGroupHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
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

	if code, msg := h.checkPermission(r, groupID, userID, "write"); code != 0 {
		writeError(w, code, msg)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		return
	}

	var req UpdatePromptGroupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name != nil {
		if *req.Name == "" || len(*req.Name) > 255 {
			writeError(w, http.StatusBadRequest, "name must be 1-255 characters")
			return
		}
		group.Name = *req.Name
	}
	if req.Description != nil {
		group.Description = req.Description
	}
	if req.Category != nil {
		group.Category = req.Category
	}
	if req.Command != nil {
		if isPromptSharingEnabled(h.core) {
			role, roleErr := resolveSharedGroupRole(r.Context(), h.groupRepo, h.shareRepo, groupID, userID)
			if roleErr != nil {
				h.logger.Error("prompt command update role lookup error", "error", roleErr)
				writeError(w, http.StatusInternalServerError, msgInternalErr)
				return
			}
			if role != "owner" {
				writeError(w, http.StatusForbidden, "only the owner can change the chat shortcut")
				return
			}
		}
		if *req.Command != "" {
			exists, err := h.groupRepo.CommandExistsForUser(r.Context(), *req.Command, userID, groupID)
			if err != nil {
				h.logger.Error("check command error", "error", err)
				writeError(w, http.StatusInternalServerError, msgInternalErr)
				return
			}
			if exists {
				writeError(w, http.StatusConflict, "command already exists for this user")
				return
			}
		}
		group.Command = req.Command
	}

	if err := h.groupRepo.Update(r.Context(), group); err != nil {
		h.logger.Error("update prompt group error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	if req.InitialPrompt != nil && *req.InitialPrompt != "" {
		maxVer, _ := h.promptRepo.MaxVersion(r.Context(), groupID)
		prompt := &store.Prompt{
			PromptText:  *req.InitialPrompt,
			GroupID:     groupID,
			UserID:      userID,
			Version:     maxVer + 1,
			Name:        &group.Name,
			Description: group.Description,
			Category:    group.Category,
			Command:     group.Command,
		}
		if err := h.promptRepo.Create(r.Context(), prompt); err != nil {
			h.logger.Error("create prompt version error", "error", err)
		} else {
			group.ProductionPromptID = &prompt.ID
			if err := h.groupRepo.SetProductionPrompt(r.Context(), groupID, prompt.ID); err != nil {
				h.logger.Warn("set production prompt after version create", "error", err, "groupID", groupID, "promptID", prompt.ID)
			}
		}
	}

	resp := groupToResponse(group)
	h.attachProductionPrompt(r, &resp, group)
	writeJSON(w, http.StatusOK, resp)
}

func (h *promptGroupHandler) handlePin(w http.ResponseWriter, r *http.Request) {
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

	if code, msg := h.checkPermission(r, groupID, userID, "write"); code != 0 {
		writeError(w, code, msg)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		return
	}

	group.IsPinned = !group.IsPinned
	if err := h.groupRepo.Update(r.Context(), group); err != nil {
		h.logger.Error("toggle pin error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	resp := groupToResponse(group)
	h.attachProductionPrompt(r, &resp, group)
	writeJSON(w, http.StatusOK, resp)
}

func (h *promptGroupHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
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

	if code, msg := h.checkPermission(r, groupID, userID, "delete"); code != 0 {
		writeError(w, code, msg)
		return
	}

	if !deletePromptGroupShares(w, r, h.core, h.logger, groupID, userID) {
		return
	}
	if err := h.groupRepo.Delete(r.Context(), groupID); err != nil {
		h.logger.Error("delete prompt group error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func deletePromptGroupShares(w http.ResponseWriter, r *http.Request, core *entrypoint.GatewayCore, logger *slog.Logger, groupID, userID string) bool {
	if !isPromptSharingEnabled(core) {
		return true
	}
	svc := core.PromptSharing()
	if svc == nil {
		return true
	}
	actor, ok := resolveCallerEmail(r)
	if !ok {
		if rbac.IsSystemPrincipal(userID) {
			return true
		}
		writeAuditActorMissing(w, logger, r, userID)
		return false
	}
	if err := svc.DeleteAllShares(r.Context(), groupID, actor); err != nil {
		logger.Error("delete prompt shares error", "promptGroupID", groupID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return false
	}
	return true
}

func (h *promptGroupHandler) handleExport(w http.ResponseWriter, r *http.Request) {
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

	if code, msg := h.checkPermission(r, groupID, userID, "read"); code != 0 {
		writeError(w, code, msg)
		return
	}

	group, err := h.groupRepo.GetByID(r.Context(), groupID)
	if err != nil || group == nil {
		writeError(w, http.StatusNotFound, msgPromptGroupNotFound)
		return
	}

	if group.ProductionPromptID == nil {
		writeError(w, http.StatusNotFound, "no production prompt set")
		return
	}

	prompt, err := h.promptRepo.GetByID(r.Context(), *group.ProductionPromptID)
	if err != nil || prompt == nil {
		writeError(w, http.StatusNotFound, "production prompt not found")
		return
	}

	metadata := map[string]any{
		"originalVersion":   prompt.Version,
		"originalCreatedAt": prompt.CreatedAt,
	}
	if group.AuthorName != nil {
		metadata["authorName"] = *group.AuthorName
	}

	writeJSON(w, http.StatusOK, PromptExportResponse{
		Version:    "1.0",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Prompt: PromptExportPayload{
			Name:        group.Name,
			Description: group.Description,
			Category:    group.Category,
			Command:     group.Command,
			PromptText:  prompt.PromptText,
			Metadata:    metadata,
		},
	})
}

func (h *promptGroupHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	var req PromptImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.PromptData.Version != "1.0" {
		writeError(w, http.StatusBadRequest, "unsupported import version: "+req.PromptData.Version)
		return
	}

	userID := resolveUserID(r, h.core)
	pd := req.PromptData.Prompt
	var warnings []string

	if len(pd.Name) > 255 {
		pd.Name = truncateStr(pd.Name, 255)
		warnings = append(warnings, "name was truncated to 255 characters")
	}
	if len(pd.PromptText) > 10000 {
		pd.PromptText = truncateStr(pd.PromptText, 10000)
		warnings = append(warnings, "promptText was truncated to 10000 characters")
	}

	var command *string
	preserveCommand := req.Options.PreserveCommand == nil || *req.Options.PreserveCommand
	if preserveCommand && pd.Command != nil && *pd.Command != "" {
		cmd := *pd.Command
		exists, _ := h.groupRepo.CommandExistsForUser(r.Context(), cmd, userID, "")
		if exists {
			resolved := false
			for i := 2; i <= 100; i++ {
				candidate := fmt.Sprintf("%s-%d", cmd, i)
				exists, _ = h.groupRepo.CommandExistsForUser(r.Context(), candidate, userID, "")
				if !exists {
					cmd = candidate
					warnings = append(warnings, fmt.Sprintf("command renamed to '%s' to avoid conflict", cmd))
					resolved = true
					break
				}
			}
			if !resolved {
				command = nil
				warnings = append(warnings, "could not resolve command conflict, command removed")
			} else {
				command = &cmd
			}
		} else {
			command = &cmd
		}
	}

	var category *string
	preserveCategory := req.Options.PreserveCategory == nil || *req.Options.PreserveCategory
	if preserveCategory {
		category = pd.Category
	}

	group := &store.PromptGroup{
		Name:        pd.Name,
		Description: pd.Description,
		Category:    category,
		Command:     command,
		UserID:      userID,
	}
	if md := pd.Metadata; md != nil {
		if an, ok := md["authorName"].(string); ok {
			group.AuthorName = &an
		}
	}

	if err := h.groupRepo.Create(r.Context(), group); err != nil {
		h.logger.Error("import create group error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	prompt := &store.Prompt{
		ID:          idgen.New(),
		PromptText:  pd.PromptText,
		GroupID:     group.ID,
		UserID:      userID,
		Version:     1,
		Name:        &group.Name,
		Description: group.Description,
		Category:    category,
		Command:     command,
	}
	if err := h.promptRepo.Create(r.Context(), prompt); err != nil {
		h.logger.Error("import create prompt error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	if err := h.groupRepo.SetProductionPrompt(r.Context(), group.ID, prompt.ID); err != nil {
		h.logger.Warn("set production prompt during import", "error", err, "groupID", group.ID, "promptID", prompt.ID)
	}

	if warnings == nil {
		warnings = []string{}
	}

	writeJSON(w, http.StatusOK, PromptImportResponse{
		Success:       true,
		PromptGroupID: group.ID,
		Warnings:      warnings,
	})
}

func (h *promptGroupHandler) attachProductionPrompt(r *http.Request, resp *PromptGroupResponse, group *store.PromptGroup) {
	if group.ProductionPromptID == nil {
		return
	}
	prompt, err := h.promptRepo.GetByID(r.Context(), *group.ProductionPromptID)
	if err != nil || prompt == nil {
		return
	}
	pr := promptToResponse(prompt)
	resp.ProductionPrompt = &pr
}

func groupToResponse(g *store.PromptGroup) PromptGroupResponse {
	return PromptGroupResponse{
		ID:                 g.ID,
		Name:               g.Name,
		Description:        g.Description,
		Category:           g.Category,
		Command:            g.Command,
		UserID:             g.UserID,
		CreatedBy:          g.UserID,
		AuthorName:         g.AuthorName,
		ProductionPromptID: g.ProductionPromptID,
		IsShared:           g.IsShared,
		IsPinned:           g.IsPinned,
		CreatedTime:        epochMsToISO8601(g.CreatedAt),
		UpdatedTime:        epochMsToISO8601(g.UpdatedAt),
	}
}

func promptToResponse(p *store.Prompt) PromptResponse {
	return PromptResponse{
		ID:          p.ID,
		PromptText:  p.PromptText,
		GroupID:     p.GroupID,
		UserID:      p.UserID,
		CreatedBy:   p.UserID,
		Version:     p.Version,
		Name:        p.Name,
		Description: p.Description,
		Category:    p.Category,
		Command:     p.Command,
		CreatedTime: epochMsToISO8601(p.CreatedAt),
		UpdatedTime: epochMsToISO8601(p.UpdatedAt),
	}
}
