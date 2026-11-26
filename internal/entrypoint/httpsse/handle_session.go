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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const sessionNotFoundMsg = "session not found"

const placeholderSessionTitle = "New Chat"

type sessionHandler struct {
	core          *entrypoint.GatewayCore
	sessRepo      *store.SessionRepo
	chatRepo      *store.ChatTaskRepo
	taskRepo      *store.TaskRepo
	projectRepo   *store.ProjectRepo
	sseRepo       *store.SSEEventRepo
	scheduledRepo *store.ScheduledTaskRepo
	sessKeyRepo   *store.GatewaySessionKeyRepo
	logger        *slog.Logger

	tempPolicy llm.TemperaturePolicy

	countCache *runtime.MapCache

	compactLocks gatewaySessionLocks

	titleLocks gatewaySessionLocks
}

func (h *sessionHandler) internalAgentIDs(explicit []string) []string {
	internal := h.core.AgentRegistry().InternalNames()
	if len(internal) == 0 {
		return explicit
	}
	return append(explicit, internal...)
}

func (h *sessionHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	projectID := r.URL.Query().Get("projectId")
	if !validProjectFilter(projectID) {
		writeError(w, http.StatusBadRequest, "invalid projectId: "+errSessionProjectFilterInvalid)
		return
	}
	source := r.URL.Query().Get("source")
	if !validSourceFilter(source) {
		writeError(w, http.StatusBadRequest, errSessionSourceInvalid)
		return
	}
	result, fail := h.listSessions(r.Context(), sessionListQuery{
		userID: userID, page: page, pageSize: pageSize, projectID: projectID, source: source,
		includeAgentIDs: queryStringSlice(r, "includeAgentIds"),
		excludeAgentIDs: h.internalAgentIDs(queryStringSlice(r, "excludeAgentIds")),
	})
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[SessionResponse]{
		Data: result.sessions,
		Meta: PaginationMeta{Pagination: sessionListPaginationInfo(page, pageSize, result.total, result.hasMore)},
	})
}

const sessionCountCacheTTL = 10 * time.Second

func sessionCountCacheKey(userID, projectID, source string, excludeAgentIDs, includeAgentIDs []string) string {
	ex := slices.Clone(excludeAgentIDs)
	inc := slices.Clone(includeAgentIDs)
	slices.Sort(ex)
	slices.Sort(inc)
	return strings.Join([]string{userID, projectID, source, strings.Join(ex, ","), strings.Join(inc, ",")}, "\x00")
}

func (h *sessionHandler) cachedSessionCount(ctx context.Context, key string, load func() (int, error)) int {
	if h.countCache != nil {
		if v, ok := h.countCache.Get(key); ok {
			if total, ok := v.(int); ok {
				return total
			}
		}
	}
	total, err := load()
	if err != nil {
		h.logger.Warn("session count: falling back to zero total", "error", err)
		return 0
	}
	if h.countCache != nil {
		h.countCache.Set(key, total, runtime.WithTTL(sessionCountCacheTTL))
	}
	return total
}

func sessionListPaginationInfo(pageNumber, pageSize, total int, hasMore bool) PaginationInfo {
	info := newPaginationInfo(pageNumber, pageSize, total)
	if hasMore {
		next := pageNumber + 1
		info.NextPage = &next
	} else {
		info.NextPage = nil
	}
	return info
}

func (h *sessionHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		writeError(w, http.StatusBadRequest, `the "query" query parameter is required`)
		return
	}
	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	projectID := r.URL.Query().Get("projectId")
	if projectID != "" && projectID != store.UnassignedProjectFilter && !requireUUID(w, projectID, "projectId") {
		return
	}
	result, fail := h.searchSessions(r.Context(), sessionListQuery{
		userID: userID, page: page, pageSize: pageSize, projectID: projectID,
		includeAgentIDs: queryStringSlice(r, "includeAgentIds"),
		excludeAgentIDs: h.internalAgentIDs(queryStringSlice(r, "excludeAgentIds")),
	}, query)
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, PaginatedResponse[SessionResponse]{
		Data: result.sessions,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, result.total)},
	})
}

func (h *sessionHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	var req CreateSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if !requireUUID(w, req.ID, "id") {
		return
	}
	created, fail := h.createSession(r.Context(), userID, req.ID, req.AgentID, req.Source)
	if fail != nil {
		fail.write(w)
		return
	}
	status := http.StatusCreated
	if created.existed {
		status = http.StatusOK
	}
	writeJSON(w, status, created.session)
}

func (h *sessionHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	session, fail := h.getSession(r.Context(), userID, r.PathValue("id"))
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *sessionHandler) handleRename(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	var req RenameSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.Name == "" && req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "name or agentId is required")
		return
	}
	session, fail := h.updateSession(r.Context(), userID, r.PathValue("id"), req.Name, req.AgentID)
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, sessionToResponse(session))
}

func (h *sessionHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	if fail := h.deleteSession(r.Context(), userID, r.PathValue("id")); fail != nil {
		fail.write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *sessionHandler) handleAssignProject(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	var req AssignProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	projectID := ""
	if req.ProjectID != nil {
		projectID = *req.ProjectID
	}
	session, fail := h.assignSessionProject(r.Context(), userID, sessionID, projectID)
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, sessionToResponse(session))
}

type markViewedResponse struct {
	LastViewedAt string `json:"lastViewedAt"`
}

func (h *sessionHandler) handleMarkViewed(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	viewedAt, fail := h.markSessionViewed(r.Context(), userID, r.PathValue("id"))
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, markViewedResponse{LastViewedAt: epochMsToISO8601(viewedAt)})
}

func (h *sessionHandler) handleUnconsumedEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	taskIDs, rowsByTask, fail := h.unconsumedEvents(r.Context(), userID, sessionID)
	if fail != nil {
		fail.write(w)
		return
	}
	eventsByTask := make(map[string]*UnconsumedTaskEvents, len(taskIDs))
	for _, taskID := range taskIDs {
		events := make([]UnconsumedEvent, 0, len(rowsByTask[taskID]))
		for _, row := range rowsByTask[taskID] {
			events = append(events, UnconsumedEvent{
				Sequence:  row.EventSequence,
				EventType: row.EventType,
				Data:      map[string]any{"event": row.EventType, "data": row.EventData},
			})
		}
		eventsByTask[taskID] = &UnconsumedTaskEvents{EventsBuffered: true, Events: events}
	}
	writeJSON(w, http.StatusOK, UnconsumedEventsResponse{
		HasEvents:    len(taskIDs) > 0,
		TaskIDs:      taskIDs,
		SessionID:    sessionID,
		EventsByTask: eventsByTask,
	})
}

func (h *sessionHandler) unconsumedEvents(ctx context.Context, userID, sessionID string) ([]string, map[string][]*store.SSEEventBufferRow, *sessionFailure) {
	if ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID) == nil {
		return nil, nil, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	buffered, err := h.sseRepo.GetUnconsumedBySession(ctx, sessionID)
	if err != nil {
		return nil, nil, h.loggedFailure(http.StatusInternalServerError, "failed to get unconsumed events", "get unconsumed events", err)
	}
	taskIDs := make([]string, 0, len(buffered))
	for taskID := range buffered {
		if running, known := h.core.TaskRunState(taskID); running || !known {
			delete(buffered, taskID)
			continue
		}
		taskIDs = append(taskIDs, taskID)
	}
	slices.Sort(taskIDs)
	return taskIDs, buffered, nil
}

type titleOutcome int

const (
	titleSkippedExisting titleOutcome = iota
	titleStoredFallback
	titleAlreadyRunning
	titleStarted
)

type titleRequest struct {
	userID        string
	sessionID     string
	userMessage   string
	agentResponse string
	force         bool
	caller        *auth.Subject
	surface       string
}

func (h *sessionHandler) handleGenerateTitle(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	var req GenerateTitleRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	outcome, fail := h.generateTitle(r.Context(), titleRequest{
		userID: userID, sessionID: sessionID, userMessage: req.UserMessage, agentResponse: req.AgentResponse,
		force: req.Force, caller: contextSubject(r), surface: httputil.ClientSurface(r),
	})
	if fail != nil {
		fail.write(w)
		return
	}
	switch outcome {
	case titleSkippedExisting:
		writeJSON(w, http.StatusOK, map[string]any{"message": "Title already exists", "skipped": true})
	case titleStoredFallback:
		writeJSON(w, http.StatusOK, map[string]any{"message": "Title generated (fallback)"})
	default:
		resourceURL := fmt.Sprintf("/api/v1/sessions/%s", sessionID)
		w.Header().Set("Location", resourceURL)
		writeJSON(w, http.StatusAccepted, OperationResponse{ID: sessionID, Status: OperationPending, ResourceURL: resourceURL})
	}
}

func (h *sessionHandler) generateTitle(ctx context.Context, req titleRequest) (titleOutcome, *sessionFailure) {
	session := ownedSession(ctx, h.sessRepo, h.logger, req.sessionID, req.userID)
	if session == nil {
		return 0, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	if !req.force && session.Name != nil {
		current := strings.TrimSpace(*session.Name)
		if current != "" && current != placeholderSessionTitle {
			return titleSkippedExisting, nil
		}
	}
	userMessage, agentResponse := req.userMessage, req.agentResponse
	if userMessage == "" || agentResponse == "" {
		h.enrichTitleMessages(ctx, req.sessionID, &userMessage, &agentResponse)
	}
	model := h.titleModel(ctx)
	if model.client == nil {
		if err := h.saveTitle(ctx, req.sessionID, truncate(userMessage, 80), req.force); err != nil {
			return 0, h.loggedFailure(http.StatusInternalServerError, "failed to store fallback title", "title generation: no LLM configured and fallback rename failed", err, "sessionID", req.sessionID)
		}
		h.logger.Debug("title generation: no LLM configured, stored fallback title", "sessionID", req.sessionID)
		return titleStoredFallback, nil
	}
	release, gotLock := h.titleLocks.tryLock(req.sessionID)
	if !gotLock {
		h.logger.Debug("title generation: already in flight for session, skipping duplicate", "sessionID", req.sessionID)
		return titleAlreadyRunning, nil
	}
	attribution := h.core.RequestSpend(req.caller, req.userID, req.surface,
		spend.SurfaceSessionTitle, model.spendKey, model.configID, req.sessionID)
	go h.generateTitleAsync(req.sessionID, userMessage, agentResponse, model, attribution, req.force, release)
	return titleStarted, nil
}

func (h *sessionHandler) enrichTitleMessages(ctx context.Context, sessionID string, userMessage, agentResponse *string) {
	if h.chatRepo != nil && (*userMessage == "" || *agentResponse == "") {
		chatTasks, err := h.chatRepo.ListBySession(ctx, sessionID)
		if err == nil && len(chatTasks) > 0 {
			ct := chatTasks[len(chatTasks)-1]
			if *userMessage == "" && ct.UserMessage != nil {
				*userMessage = *ct.UserMessage
			}
			if *agentResponse == "" {
				*agentResponse = extractAgentResponseFromBubbles(ct.MessageBubbles)
			}
		}
	}

	if h.taskRepo != nil && (*userMessage == "" || *agentResponse == "") {
		tasks := h.findSessionTasks(ctx, sessionID)
		if len(tasks) > 0 {
			task := tasks[0]
			if *userMessage == "" && task.InitialRequestText != nil {
				*userMessage = *task.InitialRequestText
			}
			if *agentResponse == "" && h.sseRepo != nil {
				*agentResponse = h.extractAgentResponseFromSSEBuffer(ctx, task.ID)
			}
		}
	}
}

func (h *sessionHandler) findSessionTasks(ctx context.Context, sessionID string) []*store.Task {
	task, err := h.taskRepo.GetBySessionID(ctx, sessionID)
	if err != nil || task == nil {
		return nil
	}
	return []*store.Task{task}
}

func (h *sessionHandler) extractAgentResponseFromSSEBuffer(ctx context.Context, taskID string) string {
	events, err := h.sseRepo.GetByTask(ctx, taskID, 0)
	if err != nil || len(events) == 0 {
		return ""
	}

	for _, e := range events {
		if e.EventType == "final_response" {
			if text := extractTextFromSSEEvent(e.EventData, "task"); text != "" {
				return text
			}
		}
	}

	var acc string
	for _, e := range events {
		if e.EventType == "status_update" {
			if text := extractTextFromSSEEvent(e.EventData, "status-update"); text != "" {
				acc += text
			}
		}
	}
	return acc
}

const (
	titleMaxUserMessageLen   = 200
	titleMaxAgentResponseLen = 200
	titleMaxLen              = 100
	titleCharLimit           = 50
	titleTemperature         = 1.0
	titleMaxTokens           = 2048
)

const msgTitleNoUsableTitle = "title generation: model returned no usable title, using fallback"

func titleModelAlias(cfg *entrypoint.GatewayConfig) string {
	if cfg == nil || cfg.ModelRaw == nil {
		return ""
	}
	m, ok := cfg.ModelRaw.(map[string]any)
	if !ok {
		return ""
	}
	v, _ := m["llm_service_title_model_name"].(string)
	return v
}

func (h *sessionHandler) titleModel(ctx context.Context) generalModel {
	if titleModel := titleModelAlias(h.core.Config()); titleModel != "" {
		svc := h.core.Services()
		if svc == nil || svc.LLM(titleModel) == nil {
			return generalModel{}
		}
		return generalModel{
			client:  h.core.MeteredClient(svc.LLM(titleModel), titleModel),
			memoKey: titleModel,
		}
	}
	return resolveGeneralModel(ctx, h.core)
}

var (
	titleBoldItalicAsterisk = regexp.MustCompile(`\*{1,3}(.*?)\*{1,3}`)
	titleBoldItalicUnder    = regexp.MustCompile(`_{1,3}(.*?)_{1,3}`)
	titleStrikethrough      = regexp.MustCompile(`~~(.*?)~~`)
	titleInlineCode         = regexp.MustCompile("`(.*?)`")
	titleHeadingPrefix      = regexp.MustCompile(`^#{1,6}\s+`)
)

func stripTitleMarkdown(s string) string {
	if s == "" {
		return s
	}
	s = titleBoldItalicAsterisk.ReplaceAllString(s, "$1")
	s = titleBoldItalicUnder.ReplaceAllString(s, "$1")
	s = titleStrikethrough.ReplaceAllString(s, "$1")
	s = titleInlineCode.ReplaceAllString(s, "$1")
	s = titleHeadingPrefix.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func cleanTitle(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.Trim(t, `"'`)
	t = stripTitleMarkdown(t)
	t = strings.Join(strings.Fields(t), " ")
	return strutil.Truncate(t, titleMaxLen, "")
}

func (h *sessionHandler) generateTitleAsync(sessionID, userMessage, agentResponse string, model generalModel, attribution *llm.SpendAttribution, force bool, release func()) {
	defer release()
	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Error("title generation: panic recovered", "sessionID", sessionID, "panic", rec)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	userText := strutil.Truncate(userMessage, titleMaxUserMessageLen, "...")
	responseText := strutil.Truncate(agentResponse, titleMaxAgentResponseLen, "...")

	prompt := fmt.Sprintf(
		"Generate a concise, specific title (under %d characters) for this conversation.\n"+
			"Output ONLY plain text. Do NOT use any markdown formatting such as bold (**), italic (*), headings (#), or backticks.\n"+
			"Avoid generic titles like \"New Chat\" or \"Conversation\".\n"+
			"Focus on the main topic or question.\n\n"+
			"User: \"%s\"\n"+
			"Agent: \"%s\"\n\n"+
			"Title:",
		titleCharLimit, userText, responseText,
	)

	titlePolicy, titleKey := temperaturePolicyFor(model.memoKey, &h.tempPolicy, "gateway-session-title")

	resp, err := llm.ChatCompletionWithTemperatureFallback(ctx, model.client, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens:   titleMaxTokens,
		Temperature: new(titleTemperature),
		Observability: llm.ObservabilityContext{
			ComponentName: "gateway-session-title",
		},
		Spend: attribution,
	}, titlePolicy, titleKey, h.logger)

	title := ""
	if err != nil {
		logAssistantLLMFailure(h.logger, "title generation: LLM error, using fallback", err, "sessionID", sessionID)
		title = fallbackTitle(userMessage)
	} else {
		title = cleanTitle(resp.Content)
		if title == "" {
			h.logger.Warn(msgTitleNoUsableTitle,
				"sessionID", sessionID,
				"contentLen", len(resp.Content),
				"finishReason", resp.FinishReason,
				"maxTokens", titleMaxTokens)
		}
	}

	if title == "" {
		title = fallbackTitle(userMessage)
	}

	if err := h.saveTitle(context.Background(), sessionID, title, force); err != nil {
		h.logger.Error("title generation: failed to save title", "error", err, "sessionID", sessionID)
	}
}

func (h *sessionHandler) saveTitle(ctx context.Context, sessionID, title string, force bool) error {
	if force {
		return h.sessRepo.Rename(ctx, sessionID, title)
	}
	saved, err := h.sessRepo.SetTitleIfUnset(ctx, sessionID, title, placeholderSessionTitle)
	if err == nil && !saved {
		h.logger.Debug("title generation: session already titled, generated title not saved", "sessionID", sessionID)
	}
	return err
}

func titleNeedsGeneration(session *store.Session) bool {
	if session == nil {
		return false
	}
	if session.Name == nil {
		return true
	}
	current := strings.TrimSpace(*session.Name)
	return current == "" || current == placeholderSessionTitle
}

func (h *sessionHandler) maybeAutoGenerateTitle(ctx context.Context, caller *auth.Subject, surface, userID string, session *store.Session) {
	if !titleNeedsGeneration(session) {
		return
	}

	var userMessage, agentResponse string
	h.enrichTitleMessages(ctx, session.ID, &userMessage, &agentResponse)
	if strings.TrimSpace(userMessage) == "" {
		return
	}

	model := h.titleModel(ctx)
	if model.client == nil {
		if err := h.saveTitle(ctx, session.ID, truncate(userMessage, 80), false); err != nil {
			h.logger.Warn("auto title: fallback rename failed", "error", err, "sessionID", session.ID)
		}
		return
	}

	release, gotLock := h.titleLocks.tryLock(session.ID)
	if !gotLock {
		h.logger.Debug("auto title: generation already in flight for session, skipping", "sessionID", session.ID)
		return
	}

	if fresh, err := h.sessRepo.GetByID(ctx, session.ID); err == nil && !titleNeedsGeneration(fresh) {
		release()
		return
	}

	attribution := h.core.RequestSpend(caller, userID, surface,
		spend.SurfaceSessionTitle, model.spendKey, model.configID, session.ID)
	go h.generateTitleAsync(session.ID, userMessage, agentResponse, model, attribution, false, release)
}

func fallbackTitle(userMessage string) string {
	t := strings.TrimSpace(userMessage)
	if t == "" {
		return placeholderSessionTitle
	}
	return strutil.Truncate(t, titleCharLimit, "...")
}

func truncate(s string, maxLen int) string {
	return strutil.Truncate(s, maxLen, "")
}

func (h *sessionHandler) enrichSessions(ctx context.Context, sessions []SessionResponse) {
	if len(sessions) == 0 {
		return
	}

	projectIDs := make([]string, 0)
	sessionIDs := make([]string, 0, len(sessions))
	projectSet := make(map[string]struct{})
	for _, s := range sessions {
		sessionIDs = append(sessionIDs, s.ID)
		if s.Project != nil && s.Project.ID != "" {
			if _, exists := projectSet[s.Project.ID]; !exists {
				projectIDs = append(projectIDs, s.Project.ID)
				projectSet[s.Project.ID] = struct{}{}
			}
		}
	}

	projectNames := make(map[string]string)
	if h.projectRepo != nil && len(projectIDs) > 0 {
		projects, err := h.projectRepo.GetByIDs(ctx, projectIDs)
		if err != nil {
			h.logger.Warn("enrich: failed to batch get projects", "error", err)
		} else {
			for _, p := range projects {
				projectNames[p.ID] = p.Name
			}
		}
	}

	bgTasks := make(map[string]bool)
	if h.taskRepo != nil && len(sessionIDs) > 0 {
		var err error
		bgTasks, err = h.taskRepo.HasActiveBackgroundTaskBatch(ctx, sessionIDs)
		if err != nil {
			h.logger.Warn("enrich: failed to batch check background tasks", "error", err)
		}
	}

	for i := range sessions {
		if sessions[i].Project != nil {
			if name, ok := projectNames[sessions[i].Project.ID]; ok {
				sessions[i].Project.Name = &name
			}
		}
		sessions[i].HasRunningBackgroundTask = bgTasks[sessions[i].ID]
		sessions[i].IsEphemeralTest = (sessions[i].Source != nil && *sessions[i].Source == ephemeralTestSessionSource) || h.isEphemeralTestSession(sessions[i].AgentID)
	}

	h.enrichSchedulerSessions(ctx, sessions)
}

const ephemeralTestSessionSource = "builder_test"

const evalSessionSource = "eval"

func (h *sessionHandler) isEphemeralTestSession(agentID *string) bool {
	if agentID == nil || *agentID == "" {
		return false
	}
	registry := h.core.AgentRegistry()
	if registry == nil {
		return false
	}
	card := registry.Get(*agentID)
	if card == nil {
		return false
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != agent.ExtensionURIDisplayName {
			continue
		}
		name, _ := ext.Params["display_name"].(string)
		if a2a.IsEphemeralTestAgentName(name) {
			return true
		}
	}
	return false
}

func (h *sessionHandler) enrichSchedulerSessions(ctx context.Context, sessions []SessionResponse) {
	if h.scheduledRepo == nil || h.sessKeyRepo == nil || len(sessions) == 0 {
		return
	}
	schedulerSessionIDs := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s.Source != nil && *s.Source == "scheduler" {
			schedulerSessionIDs = append(schedulerSessionIDs, s.ID)
		}
	}
	if len(schedulerSessionIDs) == 0 {
		return
	}

	sessionToExec, err := h.sessKeyRepo.GetByGatewayAndSessionIDs(ctx, store.GatewayTypeScheduled, schedulerSessionIDs)
	if err != nil {
		h.logger.Warn("enrich scheduler sessions: mapping lookup failed", "error", err)
		return
	}
	executionIDs := make([]string, 0, len(sessionToExec))
	for _, execID := range sessionToExec {
		executionIDs = append(executionIDs, execID)
	}
	if len(executionIDs) == 0 {
		return
	}

	info, err := h.scheduledRepo.FindTaskInfoForExecutions(ctx, executionIDs)
	if err != nil {
		h.logger.Warn("enrich scheduler sessions: batch lookup failed", "error", err)
		return
	}
	byExecution := make(map[string]store.SchedulerSessionTaskInfo, len(info))
	for _, row := range info {
		byExecution[row.ExecutionID] = row
	}

	for i := range sessions {
		if sessions[i].Source == nil || *sessions[i].Source != "scheduler" {
			continue
		}
		execID, ok := sessionToExec[sessions[i].ID]
		if !ok {
			continue
		}
		if row, ok := byExecution[execID]; ok {
			stID := row.ScheduledTaskID
			stName := row.TaskName
			sessions[i].ScheduledTaskID = &stID
			sessions[i].ScheduledTaskName = &stName
		}
	}
}

type sessionMessageResponse struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionId"`
	Message     string `json:"message"`
	SenderType  string `json:"senderType"`
	SenderName  string `json:"senderName"`
	MessageType string `json:"messageType"`
	CreatedTime string `json:"createdAt"`
	createdMs   int64
}

func (h *sessionHandler) handleMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	if requireOwnedSession(w, r, h.sessRepo, h.logger, sessionID, userID) == nil {
		return
	}
	if h.chatRepo == nil {
		writeJSON(w, http.StatusOK, unpagedResponse([]sessionMessageResponse{}))
		return
	}
	if hasPaginationParams(r) {
		page, pageSize, pageErr := parsePagination(r)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		messages, total, fail := h.sessionMessagesPage(r.Context(), sessionID, userID, page, pageSize)
		if fail != nil {
			fail.write(w)
			return
		}
		writeJSON(w, http.StatusOK, PaginatedResponse[sessionMessageResponse]{
			Data: messages,
			Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
		})
		return
	}
	chatTasks, err := h.chatRepo.ListBySession(r.Context(), sessionID)
	if err != nil {
		h.logger.Error("list chat tasks for messages", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retrieve session history")
		return
	}
	writeJSON(w, http.StatusOK, unpagedResponse(flattenChatTasksToMessages(chatTasks, sessionID, userID)))
}

func (h *sessionHandler) sessionMessagesPage(ctx context.Context, sessionID, userID string, page, pageSize int) ([]sessionMessageResponse, int, *sessionFailure) {
	chatTasks, total, err := h.chatRepo.ListBySessionPage(ctx, sessionID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, h.loggedFailure(http.StatusInternalServerError, "failed to retrieve session history", "list chat tasks page for messages", err)
	}
	return flattenChatTasksToMessages(chatTasks, sessionID, userID), total, nil
}

const legacySnakeCaseAgentNameKey = "agent_name"

func flattenChatTasksToMessages(chatTasks []*store.ChatTask, sessionID, userID string) []sessionMessageResponse {
	messages := make([]sessionMessageResponse, 0, len(chatTasks))
	for _, ct := range chatTasks {
		var bubbles []map[string]any
		if err := json.Unmarshal([]byte(ct.MessageBubbles), &bubbles); err != nil {
			continue
		}

		agentName := "agent"
		if ct.TaskMetadata != nil {
			var meta map[string]any
			if err := json.Unmarshal([]byte(*ct.TaskMetadata), &meta); err == nil {
				if name, ok := meta["agentName"].(string); ok && name != "" {
					agentName = name
				} else if name, ok := meta[legacySnakeCaseAgentNameKey].(string); ok && name != "" {
					agentName = name
				}
			}
		}

		for _, bubble := range bubbles {
			bubbleType, _ := bubble["type"].(string)
			senderType := "agent"
			senderName := agentName
			if bubbleType == "user" {
				senderType = "user"
				senderName = userID
			}

			id, _ := bubble["id"].(string)
			if id == "" {
				id = ct.ID
			}
			text, _ := bubble["text"].(string)

			messages = append(messages, sessionMessageResponse{
				ID:          id,
				SessionID:   sessionID,
				Message:     text,
				SenderType:  senderType,
				SenderName:  senderName,
				MessageType: "text",
				CreatedTime: epochMsToISO8601(ct.CreatedTime),
				createdMs:   ct.CreatedTime,
			})
		}
	}
	return messages
}
