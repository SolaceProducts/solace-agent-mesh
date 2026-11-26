// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

type chatTaskHandler struct {
	core     *entrypoint.GatewayCore
	sessRepo *store.SessionRepo
	chatRepo *store.ChatTaskRepo
	sseRepo  *store.SSEEventRepo
	sessH    *sessionHandler
	logger   *slog.Logger
}

func (h *chatTaskHandler) handleSave(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if err := validatePathUUID(sessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)

	var req SaveChatTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	session := requireOwnedSession(w, r, h.sessRepo, h.logger, sessionID, userID)
	if session == nil {
		return
	}
	if !requireUUID(w, req.TaskID, "id") {
		return
	}
	if req.MessageBubbles == "" {
		writeError(w, http.StatusBadRequest, "messageBubbles is required")
		return
	}
	if !json.Valid([]byte(req.MessageBubbles)) {
		writeError(w, http.StatusBadRequest, "messageBubbles must be valid JSON")
		return
	}
	saved, fail := h.saveChatTask(r.Context(), savedChatTask{
		userID: userID, session: session, taskID: req.TaskID, userMessage: req.UserMessage,
		messageBubbles: req.MessageBubbles, taskMetadata: req.TaskMetadata, autoGenerateTitle: req.AutoGenerateTitle,
		caller: contextSubject(r), surface: httputil.ClientSurface(r),
	})
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, chatTaskToResponse(saved))
}

type savedChatTask struct {
	userID            string
	session           *store.Session
	taskID            string
	userMessage       *string
	messageBubbles    string
	taskMetadata      *string
	autoGenerateTitle bool
	caller            *auth.Subject
	surface           string
}

func (h *chatTaskHandler) saveChatTask(ctx context.Context, in savedChatTask) (*store.ChatTask, *sessionFailure) {
	sessionID := in.session.ID
	if fail := h.taskNotOwnedByAnother(ctx, in.taskID, in.userID); fail != nil {
		return nil, fail
	}
	metadata, fail := h.metadataToStore(ctx, sessionID, in.taskID, in.taskMetadata)
	if fail != nil {
		return nil, fail
	}
	written, err := h.chatRepo.Upsert(ctx, &store.ChatTask{
		ID: in.taskID, SessionID: sessionID, UserID: in.userID,
		UserMessage: in.userMessage, MessageBubbles: in.messageBubbles, TaskMetadata: metadata,
	})
	if err != nil {
		return nil, h.loggedFailure(http.StatusInternalServerError, "failed to save chat task", "upsert chat task", err, "taskID", in.taskID, "sessionID", sessionID)
	}
	if !written {
		warnTaskAccessDenied(h.logger, in.userID, in.taskID, denyChatTaskHeldElsewhere)
		return nil, failSession(http.StatusNotFound, taskNotFoundMsg)
	}
	if touchErr := h.sessRepo.Touch(ctx, sessionID); touchErr != nil {
		h.logger.Warn("touch session after chat task save", "error", touchErr, "sessionID", sessionID)
	}
	if in.autoGenerateTitle && h.sessH != nil {
		h.sessH.maybeAutoGenerateTitle(ctx, in.caller, in.surface, in.userID, in.session)
	}
	if !isPendingSave(in.taskMetadata) {
		if markErr := h.sseRepo.MarkConsumedInSession(ctx, in.taskID, sessionID); markErr != nil {
			h.logger.Warn("mark SSE events consumed after chat task save", "error", markErr, "taskID", in.taskID, "sessionID", sessionID)
		}
	}
	saved, err := h.chatRepo.GetByID(ctx, in.taskID)
	if err != nil {
		return nil, h.loggedFailure(http.StatusInternalServerError, "failed to fetch saved task", "fetch saved chat task", err)
	}
	return saved, nil
}

func isPendingSave(taskMetadata *string) bool {
	if taskMetadata == nil {
		return false
	}
	var meta struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(*taskMetadata), &meta) != nil {
		return false
	}
	return meta.Status == "pending"
}

func (h *chatTaskHandler) taskNotOwnedByAnother(ctx context.Context, taskID, userID string) *sessionFailure {
	owner := liveTaskOwnerCtx(ctx, h.core, h.logger, taskID)
	if owner == "" || owner == store.TaskUserUnknown || owner == userID {
		return nil
	}
	warnTaskAccessDenied(h.logger, userID, taskID, denyTaskOwnedByAnotherUser)
	return failSession(http.StatusNotFound, taskNotFoundMsg)
}

func (h *chatTaskHandler) metadataToStore(ctx context.Context, sessionID, taskID string, sent *string) (*string, *sessionFailure) {
	stored, err := h.chatRepo.GetBySessionAndID(ctx, sessionID, taskID)
	if err != nil {
		return nil, h.loggedFailure(http.StatusInternalServerError, "failed to save chat task", "read stored chat task", err, "taskID", taskID, "sessionID", sessionID)
	}
	if stored == nil {
		return sent, nil
	}
	return entrypoint.PreserveForkMarkers(stored.TaskMetadata, sent), nil
}

func (h *chatTaskHandler) handleList(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if err := validatePathUUID(sessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)

	if requireOwnedSession(w, r, h.sessRepo, h.logger, sessionID, userID) == nil {
		return
	}

	if hasPaginationParams(r) {
		page, pageSize, pageErr := parsePagination(r)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		offset := (page - 1) * pageSize
		tasks, total, err := h.chatRepo.ListBySessionPage(r.Context(), sessionID, pageSize, offset)
		if err != nil {
			h.logger.Error("list chat tasks page", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list chat tasks")
			return
		}
		resp := make([]TaskResponse, 0, len(tasks))
		for _, ct := range tasks {
			resp = append(resp, chatTaskToResponse(ct))
		}
		writeJSON(w, http.StatusOK, PaginatedResponse[TaskResponse]{
			Data: resp,
			Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
		})
		return
	}

	tasks, err := h.chatRepo.ListBySession(r.Context(), sessionID)
	if err != nil {
		h.logger.Error("list chat tasks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list chat tasks")
		return
	}

	resp := make([]TaskResponse, 0, len(tasks))
	for _, ct := range tasks {
		resp = append(resp, chatTaskToResponse(ct))
	}

	writeJSON(w, http.StatusOK, unpagedResponse(resp))
}
