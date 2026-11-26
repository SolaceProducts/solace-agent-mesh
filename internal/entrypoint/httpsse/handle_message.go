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
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type messageHandler struct {
	core        *entrypoint.GatewayCore
	sseManager  *SSEManager
	sessRepo    *store.SessionRepo
	chatRepo    *store.ChatTaskRepo
	projectRepo *store.ProjectRepo
	logger      *slog.Logger
	indexer     *projectIndexer
}

func (h *messageHandler) indexSearchable(ctx context.Context, bearer string, project projectContext, indexData []byte) bool {
	if h.indexer == nil {
		return len(indexData) > 0
	}
	return h.indexer.reconcileIndex(ctx, project.id, project.ownerID, bearer, indexData)
}

func (h *messageHandler) log() *slog.Logger {
	if h.logger != nil {
		return h.logger
	}
	return h.core.Logger()
}

func (h *messageHandler) taskLogger(taskID, agentName, sessionID string) *slog.Logger {
	if h.logger != nil {
		return h.logger.With("taskID", taskID, "agentName", agentName, "sessionID", sessionID)
	}
	return h.core.TaskScopedLogger(taskID, agentName, sessionID)
}

func (h *messageHandler) handleMessageStream(w http.ResponseWriter, r *http.Request) {
	h.handleMessage(w, r)
}

func (h *messageHandler) handleMessageSend(w http.ResponseWriter, r *http.Request) {
	h.handleMessage(w, r)
}

func (h *messageHandler) ensureSessionAgent(ctx context.Context, existing *store.Session, chat chatSession, logger *slog.Logger) {
	if h.sessRepo == nil {
		return
	}
	if existing == nil {
		h.createChatSession(ctx, chat, logger)
		return
	}
	if existing.AgentID == nil || *existing.AgentID == "" {
		if _, err := h.sessRepo.BackfillAgentID(ctx, chat.contextID, chat.userID, chat.agentName); err != nil {
			logger.Warn("backfill session agent", "error", err)
		}
	}
}

func (h *messageHandler) createChatSession(ctx context.Context, chat chatSession, logger *slog.Logger) {
	sess := &store.Session{ID: chat.contextID, UserID: chat.userID, AgentID: &chat.agentName}
	if chat.projectID != "" {
		sess.ProjectID = &chat.projectID
	}
	if chat.source != "" {
		sess.Source = &chat.source
	}
	err := h.sessRepo.Create(ctx, sess)
	if err == nil {
		return
	}
	if !dbutil.IsUniqueViolation(err) {
		logger.Warn("auto-create session", "error", err, "userID", chat.userID)
		return
	}
	updated, backfillErr := h.sessRepo.BackfillAgentID(ctx, chat.contextID, chat.userID, chat.agentName)
	switch {
	case backfillErr != nil:
		logger.Warn("backfill session agent", "error", backfillErr)
	case updated:
		logger.Debug("auto-create session: id already held; agent recorded on the caller's own row")
	default:
		logger.Warn("auto-create session: id already held", "error", err, "userID", chat.userID)
	}
}

type chatSession struct {
	contextID string
	userID    string
	agentName string
	projectID string
	source    string
}

func allowedSessionSource(metadata map[string]any) string {
	if src, _ := metadata["session_source"].(string); src == evalSessionSource {
		return src
	}
	return ""
}

func stripForgedSchedulerSource(metadata map[string]any) {
	if s, _ := metadata["source"].(string); s == a2a.SourceScheduler {
		delete(metadata, "source")
	}
}

func (h *messageHandler) handleMessage(w http.ResponseWriter, r *http.Request) {
	traceID := idgen.New()

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.rejectSubmit(w, r, "", traceID, rejection{status: http.StatusBadRequest, code: codeParseError, msg: "parse error: " + err.Error(), reason: taxonomy.RejectMalformed})
		return
	}
	if req.JSONRPC != "2.0" {
		h.rejectSubmit(w, r, req.ID, traceID, rejection{status: http.StatusBadRequest, code: codeParseError, msg: "invalid jsonrpc version", reason: taxonomy.RejectMalformed})
		return
	}

	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	in := submitInput{
		reqID:   req.ID,
		traceID: traceID,
		message: req.Params.Message,
		userID:  userID,
		caller:  requestCaller(r, userID),
		client:  submitClientOf(r),
	}
	result, ref := h.submit(r.Context(), in)
	if ref != nil {
		h.answerRefusal(w, in, ref)
		return
	}
	writeJSON(w, http.StatusOK, newSuccessResponse(req.ID, &result))
}

const maxSchedulerHistoryEntries = 50

func (h *messageHandler) maybeAttachSchedulerHistory(ctx context.Context, msg *a2a.Message, sess *store.Session, contextID, userID string) {
	if sess == nil || sess.Source == nil || *sess.Source != "scheduler" {
		return
	}
	history := h.loadSchedulerConversationHistory(ctx, contextID, userID)
	if len(history) == 0 {
		return
	}
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]any)
	}
	msg.Metadata["schedulerConversationHistory"] = history
}

func (h *messageHandler) loadSchedulerConversationHistory(ctx context.Context, contextID, userID string) []map[string]string {
	if h.chatRepo == nil {
		return nil
	}
	tasks, err := h.chatRepo.ListRecentBySessionForUser(ctx, contextID, userID, maxSchedulerHistoryEntries)
	if err != nil {
		h.log().Warn("failed to load scheduler conversation history",
			"error", err, "sessionID", contextID)
		return nil
	}
	slices.Reverse(tasks)

	var history []map[string]string
	var malformed int
	for _, t := range tasks {
		if t.MessageBubbles == "" {
			continue
		}
		var bubbles []map[string]any
		if err := json.Unmarshal([]byte(t.MessageBubbles), &bubbles); err != nil {
			malformed++
			continue
		}
		for _, b := range bubbles {
			text, _ := b["text"].(string)
			if text == "" {
				continue
			}
			if isErr, _ := b["isError"].(bool); isErr {
				continue
			}
			switch b["type"] {
			case "user":
				history = append(history, map[string]string{"role": "user", "content": text})
			case "agent":
				history = append(history, map[string]string{"role": "assistant", "content": text})
			}
		}
	}
	if malformed > 0 {
		h.log().Warn("skipped malformed scheduler chat_task bubble rows",
			"count", malformed, "sessionID", contextID)
	}

	if len(history) > maxSchedulerHistoryEntries {
		history = history[len(history)-maxSchedulerHistoryEntries:]
	}
	return history
}

func convertMessage(dto MessageDTO, taskID, contextID, messageID string) a2a.Message {
	parts := make([]a2a.Part, 0, len(dto.Parts))
	for _, p := range dto.Parts {
		part := a2a.Part{
			Kind:     p.Kind,
			Text:     p.Text,
			Data:     p.Data,
			Metadata: p.Metadata,
		}
		if p.File != nil {
			part.File = &a2a.FileContent{
				Name:     p.File.Name,
				MIMEType: p.File.MIMEType,
				URI:      p.File.URI,
				Bytes:    p.File.Bytes,
			}
		}
		parts = append(parts, part)
	}

	return a2a.Message{
		Kind:      "message",
		MessageID: messageID,
		Role:      a2a.Role(dto.Role),
		Parts:     parts,
		TaskID:    taskID,
		ContextID: contextID,
		Metadata:  dto.Metadata,
	}
}
