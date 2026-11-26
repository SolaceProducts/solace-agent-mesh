// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func (h *sessionHandler) chatV2Routes(chatH *chatTaskHandler) []apicontract.Route {
	return chatv2.SessionRoutes(chatv2.Deps{
		ListSessions:         h.listChatSessions,
		SearchSessions:       h.searchChatSessions,
		CreateSession:        h.createChatSession,
		GetSession:           h.getChatSession,
		UpdateSession:        h.updateChatSession,
		DeleteSession:        h.deleteChatSession,
		SetSessionProject:    h.setChatSessionProject,
		MarkSessionViewed:    h.markChatSessionViewed,
		GenerateSessionTitle: h.generateChatSessionTitle,
		ListSessionMessages:  h.listChatSessionMessages,
		ReadUnconsumedEvents: h.readChatUnconsumedEvents,
		ReadContextUsage:     h.readChatContextUsage,
		CompactSession:       h.compactChatSession,
		SaveChatTask:         chatH.saveChatSessionTask,
		ListChatTasks:        chatH.listChatSessionTasks,
	})
}

func (h *sessionHandler) callerID(ctx context.Context) string {
	return resolveUserIDCtx(ctx, h.core)
}

func (h *sessionHandler) listQueryOf(ctx context.Context, page apicontract.Page, projectID, source, include, exclude string) sessionListQuery {
	return sessionListQuery{
		userID: h.callerID(ctx), page: page.Number, pageSize: page.Size, projectID: projectID, source: source,
		includeAgentIDs: splitCommaList(include),
		excludeAgentIDs: h.internalAgentIDs(splitCommaList(exclude)),
	}
}

func chatSessionOf(s SessionResponse, row *store.Session) chatv2.Session {
	out := chatv2.Session{
		ID:                       s.ID,
		Type:                     "session",
		Name:                     s.Name,
		AgentID:                  s.AgentID,
		Source:                   s.Source,
		HasRunningBackgroundTask: s.HasRunningBackgroundTask,
		ScheduledTaskID:          s.ScheduledTaskID,
		ScheduledTaskName:        s.ScheduledTaskName,
		IsEphemeralTest:          s.IsEphemeralTest,
		CreatedBy:                s.CreatedBy,
	}
	if s.Project != nil {
		out.Project = &chatv2.SessionProject{ID: s.Project.ID, Name: s.Project.Name}
	}
	if row != nil {
		out.CreatedTime = apicontract.Instant(row.CreatedTime)
		out.UpdatedTime = apicontract.Instant(row.UpdatedTime)
		out.LastViewedTime = apicontract.InstantOf(row.LastViewedAt)
	}
	return out
}

func (h *sessionHandler) chatSessionsOf(page sessionPage) []chatv2.Session {
	out := make([]chatv2.Session, len(page.sessions))
	for i := range page.sessions {
		out[i] = chatSessionOf(page.sessions[i], page.rows[i])
	}
	return out
}

func (h *sessionHandler) chatSession(ctx context.Context, row *store.Session) chatv2.Session {
	return chatSessionOf(h.sessionResponse(ctx, row), row)
}

func (h *sessionHandler) listChatSessions(ctx context.Context, req chatv2.ListSessionsRequest) (apicontract.List[chatv2.Session], error) {
	if !validProjectFilter(req.ProjectID) {
		return apicontract.List[chatv2.Session]{}, malformedQuery("projectId", errSessionProjectFilterInvalid)
	}
	if !validSourceFilter(req.Source) {
		return apicontract.List[chatv2.Session]{}, invalidField("source", sessionSourceProblem)
	}
	page, fail := h.listSessions(ctx, h.listQueryOf(ctx, req.Page, req.ProjectID, req.Source, req.AgentIDs, req.ExcludeAgentIDs))
	if fail != nil {
		return apicontract.List[chatv2.Session]{}, fail.asError()
	}
	return apicontract.List[chatv2.Session]{Items: h.chatSessionsOf(page), Total: page.consistentTotal(req.Page.Number, req.Page.Size)}, nil
}

func (h *sessionHandler) searchChatSessions(ctx context.Context, req chatv2.SearchSessionsRequest) (apicontract.List[chatv2.Session], error) {
	text := strings.TrimSpace(req.Query)
	if text == "" {
		return apicontract.List[chatv2.Session]{}, invalidField("query", "is required")
	}
	if !validProjectFilter(req.ProjectID) {
		return apicontract.List[chatv2.Session]{}, malformedQuery("projectId", errSessionProjectFilterInvalid)
	}
	page, fail := h.searchSessions(ctx, h.listQueryOf(ctx, req.Page, req.ProjectID, "", req.AgentIDs, req.ExcludeAgentIDs), text)
	if fail != nil {
		return apicontract.List[chatv2.Session]{}, fail.asError()
	}
	return apicontract.List[chatv2.Session]{Items: h.chatSessionsOf(page), Total: page.total}, nil
}

func (h *sessionHandler) createChatSession(ctx context.Context, req chatv2.CreateSessionRequest) (chatv2.Session, error) {
	if req.Source != nil && *req.Source != ephemeralTestSessionSource {
		return chatv2.Session{}, invalidField("source", "must be builder_test")
	}
	created, fail := h.createSession(ctx, h.callerID(ctx), idgen.New(), req.AgentID, req.Source)
	if fail != nil {
		return chatv2.Session{}, fail.asError()
	}
	return chatSessionOf(created.session, created.row), nil
}

func (h *sessionHandler) getChatSession(ctx context.Context, req apicontract.IDRequest) (chatv2.Session, error) {
	row := ownedSession(ctx, h.sessRepo, h.logger, req.ID, h.callerID(ctx))
	if row == nil {
		return chatv2.Session{}, chatv2.SessionNotFound()
	}
	return h.chatSession(ctx, row), nil
}

func (h *sessionHandler) updateChatSession(ctx context.Context, req chatv2.UpdateSessionRequest) (chatv2.Session, error) {
	details := map[string][]string{}
	_, nameSet := apicontract.RequiredPatch(details, "name", req.Name)
	_, agentSet := apicontract.RequiredPatch(details, "agentId", req.AgentID)
	if len(details) > 0 {
		return chatv2.Session{}, &apicontract.CallerError{Status: http.StatusUnprocessableEntity, Message: apicontract.MsgValidationFailed, Details: details}
	}
	if !nameSet && !agentSet {
		return chatv2.Session{}, invalidField("name", "name or agentId is required")
	}
	row, fail := h.updateSession(ctx, h.callerID(ctx), req.ID, req.Name.Value, req.AgentID.Value)
	if fail != nil {
		return chatv2.Session{}, fail.asError()
	}
	return h.chatSession(ctx, row), nil
}

func (h *sessionHandler) deleteChatSession(ctx context.Context, req apicontract.IDRequest) (struct{}, error) {
	return struct{}{}, h.deleteSession(ctx, h.callerID(ctx), req.ID).asError()
}

func (h *sessionHandler) setChatSessionProject(ctx context.Context, req chatv2.SessionProjectRequest) (chatv2.Session, error) {
	if !req.ProjectID.Set {
		return chatv2.Session{}, invalidField("projectId", "is required; send null to detach the session from its project")
	}
	if !req.ProjectID.Null && !isCanonicalUUID(req.ProjectID.Value) {
		return chatv2.Session{}, invalidField("projectId", errSessionProjectIDInvalid)
	}
	row, fail := h.assignSessionProject(ctx, h.callerID(ctx), req.ID, req.ProjectID.Value)
	if fail != nil {
		return chatv2.Session{}, fail.asError()
	}
	return h.chatSession(ctx, row), nil
}

func (h *sessionHandler) markChatSessionViewed(ctx context.Context, req apicontract.IDRequest) (chatv2.Session, error) {
	userID := h.callerID(ctx)
	if _, fail := h.markSessionViewed(ctx, userID, req.ID); fail != nil {
		return chatv2.Session{}, fail.asError()
	}
	return h.getChatSession(ctx, req)
}

func (h *sessionHandler) generateChatSessionTitle(ctx context.Context, req chatv2.GenerateSessionTitleRequest) (chatv2.Session, error) {
	caller, _ := auth.SubjectFromContext(ctx)
	_, fail := h.generateTitle(ctx, titleRequest{
		userID: h.callerID(ctx), sessionID: req.ID, userMessage: req.UserMessage, agentResponse: req.AgentResponse,
		force: req.Force, caller: caller, surface: httputil.ParseClientSurface(req.ClientSurface),
	})
	if fail != nil {
		return chatv2.Session{}, fail.asError()
	}
	return h.getChatSession(ctx, apicontract.IDRequest{ID: req.ID})
}

func (h *sessionHandler) listChatSessionMessages(ctx context.Context, req chatv2.SessionMessagesRequest) (apicontract.List[chatv2.SessionMessage], error) {
	userID := h.callerID(ctx)
	if ownedSession(ctx, h.sessRepo, h.logger, req.ID, userID) == nil {
		return apicontract.List[chatv2.SessionMessage]{}, chatv2.SessionNotFound()
	}
	if h.chatRepo == nil {
		return apicontract.List[chatv2.SessionMessage]{Items: []chatv2.SessionMessage{}}, nil
	}
	messages, total, fail := h.sessionMessagesPage(ctx, req.ID, userID, req.Page.Number, req.Page.Size)
	if fail != nil {
		return apicontract.List[chatv2.SessionMessage]{}, fail.asError()
	}
	items := make([]chatv2.SessionMessage, len(messages))
	for i, m := range messages {
		items[i] = chatv2.SessionMessage{
			ID: m.ID, Type: "sessionMessage", SessionID: m.SessionID, Text: m.Message,
			SenderType: m.SenderType, SenderName: m.SenderName, CreatedTime: apicontract.Instant(m.createdMs),
		}
	}
	return apicontract.List[chatv2.SessionMessage]{Items: items, Total: total}, nil
}

func (h *sessionHandler) readChatUnconsumedEvents(ctx context.Context, req chatv2.UnconsumedEventsRequest) (chatv2.UnconsumedEvents, error) {
	taskIDs, rowsByTask, fail := h.unconsumedEvents(ctx, h.callerID(ctx), req.ID)
	if fail != nil {
		return chatv2.UnconsumedEvents{}, fail.asError()
	}
	byTask := make(map[string]chatv2.UnconsumedTaskEvents, len(taskIDs))
	for _, taskID := range taskIDs {
		events := make([]chatv2.UnconsumedEvent, len(rowsByTask[taskID]))
		for i, row := range rowsByTask[taskID] {
			events[i] = chatv2.UnconsumedEvent{Sequence: row.EventSequence, EventType: row.EventType, Data: eventObject(row.EventData)}
		}
		byTask[taskID] = chatv2.UnconsumedTaskEvents{Events: events}
	}
	return chatv2.UnconsumedEvents{ID: req.ID, Type: "unconsumedEvents", HasEvents: len(taskIDs) > 0, TaskIDs: taskIDs, EventsByTask: byTask}, nil
}

func (h *sessionHandler) readChatContextUsage(ctx context.Context, req chatv2.ContextUsageRequest) (chatv2.ContextUsage, error) {
	usage, fail := h.contextUsage(ctx, h.callerID(ctx), req.ID, req.Model)
	if fail != nil {
		return chatv2.ContextUsage{}, fail.asError()
	}
	return chatv2.ContextUsage{
		ID: usage.SessionID, Type: "contextUsage", CurrentContextTokens: usage.CurrentContextTokens, PromptTokens: usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens, CachedTokens: usage.CachedTokens, MaxInputTokens: usage.MaxInputTokens,
		UsagePercentage: usage.UsagePercentage, Model: usage.Model, TotalEvents: usage.TotalEvents,
		TotalMessages: usage.TotalMessages, TotalTasks: usage.TotalTasks, HasCompaction: usage.HasCompaction,
	}, nil
}

func (h *sessionHandler) requireAgentAccess(ctx context.Context, caller *auth.Subject, userID, sessionID string) *sessionFailure {
	session := ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID)
	if session == nil || session.AgentID == nil || *session.AgentID == "" {
		return nil
	}
	if !h.core.CanDelegate(ctx, caller, *session.AgentID) {
		return failSession(http.StatusForbidden, "you may not use this session's agent")
	}
	return nil
}

func (h *sessionHandler) compactChatSession(ctx context.Context, req chatv2.CompactSessionRequest) (chatv2.Compaction, error) {
	userID := h.callerID(ctx)
	caller, _ := auth.SubjectFromContext(ctx)
	if fail := h.requireAgentAccess(ctx, caller, userID, req.ID); fail != nil {
		return chatv2.Compaction{}, fail.asError()
	}
	result, fail := h.compactSession(ctx, userID, req.ID, req.TargetPercent,
		h.core.RequestOrigin(caller, userID, httputil.ParseClientSurface(req.ClientSurface)))
	if fail != nil {
		return chatv2.Compaction{}, fail.asError()
	}
	return chatv2.Compaction{
		ID: result.SessionID, Type: "compaction", EventsCompacted: result.EventsCompacted, Summary: result.Summary,
		RemainingEvents: result.RemainingEvents, RemainingTokens: result.RemainingTokens,
		SummarizerCostUsd: result.SummarizerCostUsd, SummarizerInputTokens: result.SummarizerInputTokens,
		SummarizerOutputTokens: result.SummarizerOutputTokens,
	}, nil
}

func (h *chatTaskHandler) saveChatSessionTask(ctx context.Context, req chatv2.SaveChatTaskRequest) (chatv2.SessionChatTask, error) {
	userID := resolveUserIDCtx(ctx, h.core)
	session := ownedSession(ctx, h.sessRepo, h.logger, req.SessionID, userID)
	if session == nil {
		return chatv2.SessionChatTask{}, chatv2.SessionNotFound()
	}
	if problems := req.Problems(); len(problems) > 0 {
		return chatv2.SessionChatTask{}, &apicontract.CallerError{Status: http.StatusUnprocessableEntity, Message: apicontract.MsgValidationFailed, Details: problems}
	}
	bubbles, err := chatv2.StoredBubbles(req.MessageBubbles)
	if err != nil {
		return chatv2.SessionChatTask{}, fmt.Errorf("encode message bubbles: %w", err)
	}
	var metadata *string
	if req.TaskMetadata != nil {
		raw, err := chatv2.StoredMetadata(*req.TaskMetadata)
		if err != nil {
			return chatv2.SessionChatTask{}, err
		}
		text := string(raw)
		metadata = &text
	}
	caller, _ := auth.SubjectFromContext(ctx)
	saved, fail := h.saveChatTask(ctx, savedChatTask{
		userID: userID, session: session, taskID: req.ID, userMessage: req.UserMessage,
		messageBubbles: string(bubbles), taskMetadata: metadata, autoGenerateTitle: req.AutoGenerateTitle,
		caller: caller, surface: httputil.ParseClientSurface(req.ClientSurface),
	})
	if fail != nil {
		return chatv2.SessionChatTask{}, fail.asError()
	}
	return sessionChatTaskOf(saved)
}

func (h *chatTaskHandler) listChatSessionTasks(ctx context.Context, req chatv2.ListChatTasksRequest) (apicontract.List[chatv2.SessionChatTask], error) {
	if ownedSession(ctx, h.sessRepo, h.logger, req.ID, resolveUserIDCtx(ctx, h.core)) == nil {
		return apicontract.List[chatv2.SessionChatTask]{}, chatv2.SessionNotFound()
	}
	tasks, total, err := h.chatRepo.ListBySessionPage(ctx, req.ID, req.Page.Size, (req.Page.Number-1)*req.Page.Size)
	if err != nil {
		return apicontract.List[chatv2.SessionChatTask]{}, fmt.Errorf("list chat tasks of session %s: %w", req.ID, err)
	}
	items := make([]chatv2.SessionChatTask, 0, len(tasks))
	for _, ct := range tasks {
		item, err := sessionChatTaskOf(ct)
		if err != nil {
			h.logger.Warn("skipping a stored chat task that does not decode", "sessionID", req.ID, "taskID", ct.ID, "error", err)
			continue
		}
		items = append(items, item)
	}
	return apicontract.List[chatv2.SessionChatTask]{Items: items, Total: total}, nil
}

func sessionChatTaskOf(ct *store.ChatTask) (chatv2.SessionChatTask, error) {
	bubbles := []chatv2.Bubble{}
	if ct.MessageBubbles != "" {
		if err := json.Unmarshal([]byte(ct.MessageBubbles), &bubbles); err != nil {
			return chatv2.SessionChatTask{}, fmt.Errorf("decode message bubbles: %w", err)
		}
		if bubbles == nil {
			bubbles = []chatv2.Bubble{}
		}
	}
	var metadata *chatv2.ChatTaskMetadata
	if ct.TaskMetadata != nil && *ct.TaskMetadata != "" {
		metadata = new(chatv2.ChatTaskMetadata)
		if err := json.Unmarshal([]byte(*ct.TaskMetadata), metadata); err != nil {
			return chatv2.SessionChatTask{}, fmt.Errorf("decode task metadata: %w", err)
		}
	}
	return chatv2.SessionChatTask{
		ID: ct.ID, Type: "sessionChatTask", SessionID: ct.SessionID, CreatedBy: ct.UserID,
		UserMessage: ct.UserMessage, MessageBubbles: bubbles, TaskMetadata: metadata,
		CreatedTime: apicontract.Instant(ct.CreatedTime), UpdatedTime: apicontract.InstantOf(ct.UpdatedTime),
	}, nil
}

func eventObject(raw string) map[string]any {
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return map[string]any{}
	}
	return data
}

func malformedQuery(field, problem string) error {
	return &apicontract.CallerError{
		Status: http.StatusBadRequest, Message: apicontract.MsgValidationFailed,
		Details: map[string][]string{field: {problem}},
	}
}

func malformedPath(field, problem string) error {
	return &apicontract.CallerError{
		Status: http.StatusBadRequest, Message: apicontract.MsgInvalidPathParam,
		Details: map[string][]string{field: {problem}},
	}
}

func invalidField(field, problem string) error {
	return &apicontract.CallerError{
		Status: http.StatusUnprocessableEntity, Message: apicontract.MsgValidationFailed,
		Details: map[string][]string{field: {problem}},
	}
}
