// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/stim"
)

func clientGone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

type taskHandler struct {
	core         *entrypoint.GatewayCore
	taskRepo     *store.TaskRepo
	eventRepo    *store.TaskEventRepo
	sseRepo      *store.SSEEventRepo
	sessRepo     *store.SessionRepo
	chatRepo     *store.ChatTaskRepo
	feedbackRepo *store.FeedbackRepo
	logger       *slog.Logger
}

func (h *taskHandler) traceDeps() traceAccessDeps {
	return traceAccessDeps{tasks: h.taskRepo, events: h.eventRepo}
}

func (h *taskHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	task := requireOwnedTask(w, r, h.traceDeps(), h.logger, taskID, resolveUserID(r, h.core))
	if task == nil {
		return
	}

	isRunning := h.core.IsTaskRunning(taskID)
	isBackground := task.ExecutionMode == "background"

	canReconnect := task.EventsBuffered && !task.EventsConsumed

	resp := map[string]any{
		"task":         taskToStatusResponse(task),
		"isRunning":    isRunning,
		"isBackground": isBackground,
		"canReconnect": canReconnect,
		"errorMessage": nil,
	}

	if task.Status != nil && a2a.IsFailureStatus(*task.Status) {
		if msg := h.errorMessageForTask(r.Context(), task); msg != "" {
			resp["errorMessage"] = msg
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *taskHandler) errorMessageForTask(ctx context.Context, task *store.Task) string {
	if task == nil {
		return ""
	}
	if task.ErrorMessage != nil && *task.ErrorMessage != "" {
		return *task.ErrorMessage
	}
	return h.extractErrorMessage(ctx, task.ID)
}

func (h *taskHandler) extractErrorMessage(ctx context.Context, taskID string) string {
	events, err := h.eventRepo.GetByTaskID(ctx, taskID)
	if err != nil || len(events) == 0 {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		var payload map[string]any
		if err := json.Unmarshal([]byte(events[i].Payload), &payload); err != nil {
			continue
		}
		if msg := errorMessageFromPayload(payload); msg != "" {
			return msg
		}
	}
	return ""
}

func errorMessageFromPayload(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg, _ := errObj["message"].(string); msg != "" {
			return msg
		}
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		return ""
	}
	status, ok := result["status"].(map[string]any)
	if !ok {
		return ""
	}
	msgObj, ok := status["message"].(map[string]any)
	if !ok {
		return ""
	}
	if parts, ok := msgObj["parts"].([]any); ok {
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if kind, _ := part["kind"].(string); kind != "" && kind != "text" {
				continue
			}
			if text, _ := part["text"].(string); text != "" {
				return text
			}
		}
	}
	if text, _ := msgObj["text"].(string); text != "" {
		return text
	}
	return ""
}

func (h *taskHandler) handleActiveBackground(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, h.core)

	tasks, err := h.taskRepo.ListActiveBackground(r.Context(), userID)
	if err != nil {
		h.logger.Error("failed to list active background tasks", "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, taskToStatusResponse(t))
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

const maxTaskSearchLen = 256

const maxTaskSessionIDs = 100

func (h *taskHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, h.core)
	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}

	var startDate, endDate int64
	if s := r.URL.Query().Get("startDate"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "startDate must be ISO 8601 (RFC 3339)")
			return
		}
		startDate = t.UnixMilli()
	}
	if s := r.URL.Query().Get("endDate"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "endDate must be ISO 8601 (RFC 3339)")
			return
		}
		endDate = t.UnixMilli()
	}

	kindParam := strings.TrimSpace(r.URL.Query().Get("kind"))
	var kindFilter string
	switch kindParam {
	case "":
		kindFilter = store.TaskKindNormal
	case "all":
		kindFilter = ""
	case store.TaskKindNormal, store.TaskKindCompactionCost:
		kindFilter = kindParam
	default:
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message: "validation error",
			ErrorID: idgen.New(),
			ValidationDetails: map[string][]string{
				"kind": {"must be one of: task, compaction-cost, all"},
			},
		})
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > maxTaskSearchLen {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           idgen.New(),
			ValidationDetails: map[string][]string{"search": {fmt.Sprintf("must be at most %d characters", maxTaskSearchLen)}},
		})
		return
	}

	sessionIDs := queryStringSlice(r, "sessionIds")
	if len(sessionIDs) > maxTaskSessionIDs {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           idgen.New(),
			ValidationDetails: map[string][]string{"sessionIds": {fmt.Sprintf("must list at most %d sessions", maxTaskSessionIDs)}},
		})
		return
	}

	sortField, sortDir, sortErr := parseTaskSort(r.URL.Query().Get("sort"))
	if sortErr != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           idgen.New(),
			ValidationDetails: map[string][]string{"sort": {sortErr.Error()}},
		})
		return
	}

	offset := (page - 1) * pageSize
	visibility := callerTaskVisibility(r, userID)
	tasks, total, err := h.taskRepo.Search(r.Context(), visibility, store.TaskSearchOpts{
		StartDate:  startDate,
		EndDate:    endDate,
		Kind:       kindFilter,
		UserID:     strings.TrimSpace(r.URL.Query().Get("userId")),
		SessionIDs: sessionIDs,
		Search:     search,
		SortField:  sortField,
		SortDir:    sortDir,
	}, pageSize, offset)
	if err != nil {
		h.logger.Error("search tasks error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	auditCrossUserTaskList(h.logger, userID, visibility, tasks)

	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	eventCounts, err := h.eventRepo.CountByTaskIDs(r.Context(), taskIDs)
	if err != nil {
		h.logger.Warn("count task events error", "error", err)
		eventCounts = nil
	}
	var sessionNames map[string]string
	if parseExpand(r)["sessionName"] {
		sessionNames, err = h.sessRepo.NamesByIDs(r.Context(), sessionTitleIDs(tasks, visibility))
		if err != nil {
			h.logger.Warn("load session names error", "error", err)
		}
	}

	resolver := h.core.AgentRegistry().DisplayNameResolver()
	result := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		resp := taskToStatusResponse(t)
		resp["eventCount"] = eventCounts[t.ID]
		if t.SessionID != nil && callerReadsSession(t, visibility) {
			if name, ok := sessionNames[*t.SessionID]; ok {
				resp["sessionName"] = name
			}
		}
		if subject, ok := traceEntrySubject(t, resolver); ok {
			resp["subject"] = subject
		}
		result = append(result, resp)
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[map[string]any]{
		Data: result,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func callerReadsSession(t *store.Task, vis store.TaskVisibility) bool {
	return vis.Everything || t.UserID == vis.OwnerUserID
}

func sessionTitleIDs(tasks []*store.Task, vis store.TaskVisibility) []string {
	seen := make(map[string]struct{}, len(tasks))
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.SessionID == nil || *t.SessionID == "" || !callerReadsSession(t, vis) {
			continue
		}
		if _, dup := seen[*t.SessionID]; dup {
			continue
		}
		seen[*t.SessionID] = struct{}{}
		ids = append(ids, *t.SessionID)
	}
	return ids
}

func auditCrossUserTaskList(logger *slog.Logger, callerUserID string, vis store.TaskVisibility, tasks []*store.Task) {
	if logger == nil {
		return
	}
	crossUser := 0
	for _, t := range tasks {
		if t.UserID != callerUserID {
			crossUser++
		}
	}
	if crossUser == 0 {
		return
	}
	grant := grantAgentTrace
	if vis.Everything {
		grant = grantActivityRead
	}
	logger.Info("task list",
		"callerUser", callerUserID,
		"crossUser", true,
		"crossUserCount", crossUser,
		"grant", grant)
}

func parseTaskSort(raw string) (store.TaskSortField, store.TaskSortDir, error) {
	if raw == "" {
		return store.TaskSortStartTime, store.TaskSortDesc, nil
	}
	parts := strings.SplitN(raw, ":", 2)
	var fieldPart, dirPart string
	fieldPart = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		dirPart = strings.TrimSpace(parts[1])
	}

	var field store.TaskSortField
	switch fieldPart {
	case "startTime":
		field = store.TaskSortStartTime
	case "lastActivityTime":
		field = store.TaskSortLastActivity
	default:
		return "", "", fmt.Errorf("field must be one of: startTime, lastActivityTime")
	}

	var dir store.TaskSortDir
	switch dirPart {
	case "", "desc":
		dir = store.TaskSortDesc
	case "asc":
		dir = store.TaskSortAsc
	default:
		return "", "", fmt.Errorf("direction must be asc or desc")
	}
	return field, dir, nil
}

func (h *taskHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	callerUserID := resolveUserID(r, h.core)
	task, ok := loadTaskForOwnership(w, r, h.taskRepo, h.logger, taskID)
	if !ok {
		return
	}
	if task == nil {
		writeJSON(w, http.StatusOK, unpagedResponse([]map[string]any{}))
		return
	}

	decision, err := resolveTraceAccess(r, h.traceDeps(), task, callerUserID)
	if err != nil {
		if clientGone(err) {
			return
		}
		h.logger.Error("resolve trace access", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	if decision.tier == traceDenied {
		warnTaskAccessDenied(h.logger, callerUserID, taskID, decision.reason)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return
	}
	if task.UserID != callerUserID {
		auditCrossUserTaskRead(h.logger, callerUserID, task.UserID, task.ID, decision.grant)
	}

	ctx := r.Context()

	var entries map[string]map[string]any
	var tasks map[string]*store.Task
	if len(decision.scopedTo) > 0 {
		entries, tasks, err = collectTaskEventsForIDs(ctx, h.taskRepo, h.eventRepo, decision.scopedTo, true, h.logger)
	} else {
		entries, tasks, err = collectTaskEvents(ctx, h.taskRepo, h.eventRepo, []string{taskID}, true, h.logger)
	}
	if err != nil {
		if clientGone(err) {
			h.logger.Debug("collect task events canceled by client", "taskID", taskID)
			return
		}
		h.logger.Error("collect task events error", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	resolver := h.core.AgentRegistry().DisplayNameResolver()
	taskEvents := make([]map[string]any, 0, len(entries))
	for id, entry := range entries {
		if subject, ok := traceEntrySubject(tasks[id], resolver); ok {
			entry["subject"] = subject
		}
		taskEvents = append(taskEvents, entry)
	}

	writeJSON(w, http.StatusOK, unpagedResponse(taskEvents))
}

func traceEntrySubject(task *store.Task, resolver func(string) string) (map[string]any, bool) {
	subject, ok := entrypoint.DeriveFeedbackSubject(task)
	if !ok {
		return nil, false
	}
	return map[string]any{
		"id":   subject.ID,
		"type": subject.Type,
		"name": resolveSubjectDisplayName(resolver, subject.ID, subject.Type),
	}, true
}

func collectTaskEvents(ctx context.Context, taskRepo *store.TaskRepo, eventRepo *store.TaskEventRepo, seedTaskIDs []string, includeSessionID bool, logger *slog.Logger) (map[string]map[string]any, map[string]*store.Task, error) {
	if taskRepo == nil || eventRepo == nil {
		return map[string]map[string]any{}, map[string]*store.Task{}, nil
	}
	ids, err := expandTaskTree(ctx, taskRepo, seedTaskIDs)
	if err != nil {
		return nil, nil, err
	}
	return collectTaskEventsForIDs(ctx, taskRepo, eventRepo, ids, includeSessionID, logger)
}

func expandTaskTree(ctx context.Context, taskRepo *store.TaskRepo, seedTaskIDs []string) ([]string, error) {
	allTaskIDs := make(map[string]bool, len(seedTaskIDs))
	for _, seed := range seedTaskIDs {
		if seed == "" {
			continue
		}
		parentChain, err := taskRepo.GetParentChain(ctx, seed, traceParentChainMaxDepth)
		if err != nil {
			return nil, fmt.Errorf("get parent chain for %s: %w", seed, err)
		}
		for _, id := range parentChain {
			allTaskIDs[id] = true
		}
		allTaskIDs[seed] = true

		rootID := seed
		if len(parentChain) > 0 {
			rootID = parentChain[len(parentChain)-1]
		}
		descendants, err := taskRepo.GetAllDescendantIDs(ctx, rootID)
		if err != nil {
			return nil, fmt.Errorf("get descendants for %s: %w", rootID, err)
		}
		for _, id := range descendants {
			allTaskIDs[id] = true
		}
	}

	taskIDList := make([]string, 0, len(allTaskIDs))
	for id := range allTaskIDs {
		taskIDList = append(taskIDList, id)
	}
	return taskIDList, nil
}

func collectTaskEventsForIDs(ctx context.Context, taskRepo *store.TaskRepo, eventRepo *store.TaskEventRepo, taskIDList []string, includeSessionID bool, logger *slog.Logger) (map[string]map[string]any, map[string]*store.Task, error) {
	if taskRepo == nil || eventRepo == nil {
		return map[string]map[string]any{}, map[string]*store.Task{}, nil
	}
	allTaskIDs := make(map[string]bool, len(taskIDList))
	for _, id := range taskIDList {
		allTaskIDs[id] = true
	}

	events, err := eventRepo.GetByTaskIDs(ctx, taskIDList)
	if err != nil {
		return nil, nil, fmt.Errorf("get task events: %w", err)
	}

	tasks := make(map[string]*store.Task, len(taskIDList))
	if len(taskIDList) > 0 {
		fetched, ferr := taskRepo.GetByIDs(ctx, taskIDList)
		if ferr != nil {
			logger.Warn("batch get tasks error", "error", ferr)
		} else {
			for _, t := range fetched {
				tasks[t.ID] = t
			}
		}
	}

	eventsByTask := make(map[string][]map[string]any)
	for _, e := range events {
		eventsByTask[e.TaskID] = append(eventsByTask[e.TaskID], formatTaskEvent(e, logger))
	}

	out := make(map[string]map[string]any, len(allTaskIDs))
	for id := range allTaskIDs {
		evts := eventsByTask[id]
		if evts == nil {
			evts = []map[string]any{}
		}
		entry := map[string]any{
			"taskId": id,
			"events": evts,
		}
		if t, ok := tasks[id]; ok {
			if t.InitialRequestText != nil {
				entry["initialRequestText"] = *t.InitialRequestText
			}
			if includeSessionID && t.SessionID != nil {
				entry["sessionId"] = *t.SessionID
			}
		}
		out[id] = entry
	}
	return out, tasks, nil
}

func (h *taskHandler) handleBufferedEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	callerUserID := resolveUserID(r, h.core)

	if _, ok := authorizeRawFrameRead(w, r, h.traceDeps(), h.logger, taskID, callerUserID); !ok {
		return
	}

	events, err := h.sseRepo.GetByTask(r.Context(), taskID, 0)
	if err != nil {
		h.logger.Error("get buffered events error", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]map[string]any, 0, len(events))
	for _, e := range events {
		if !bufferedEventReadableBy(e, callerUserID, r) {
			continue
		}
		result = append(result, map[string]any{
			"sequence":  e.EventSequence,
			"eventType": e.EventType,
			"data": map[string]any{
				"event": e.EventType,
				"data":  e.EventData,
			},
		})
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

func (h *taskHandler) handleDeleteBufferedEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	task, ok := authorizeOwnedTaskWrite(w, r, h.taskRepo, h.logger, taskID, resolveUserID(r, h.core))
	if !ok {
		return
	}
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	running, known := h.core.TaskRunState(taskID)
	if !known {
		writeError(w, http.StatusServiceUnavailable, "task state unavailable, retry later")
		return
	}
	if running {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.sseRepo.DeleteByTask(r.Context(), taskID); err != nil {
		h.logger.Error("delete buffered events error", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *taskHandler) handleTitleData(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}

	ctx := r.Context()

	task := requireOwnedTaskRawFrames(w, r, h.traceDeps(), h.logger, taskID, resolveUserID(r, h.core))
	if task == nil {
		return
	}

	var userMessage, agentResponse, sessionID string

	if task.SessionID != nil {
		sessionID = *task.SessionID
	}

	if sessionID != "" && h.chatRepo != nil {
		if ct, _ := h.chatRepo.GetBySessionAndID(ctx, sessionID, taskID); ct != nil {
			if ct.UserMessage != nil {
				userMessage = *ct.UserMessage
			}
			agentResponse = extractAgentResponseFromBubbles(ct.MessageBubbles)
		}
	}

	if userMessage == "" && task.InitialRequestText != nil {
		userMessage = *task.InitialRequestText
	}

	if agentResponse == "" {
		agentResponse = h.extractAgentResponseFromSSE(ctx, taskID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"userMessage":   userMessage,
		"agentResponse": agentResponse,
		"taskId":        taskID,
		"sessionId":     sessionID,
	})
}

func (h *taskHandler) handleTaskPost(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if strings.HasSuffix(path, ":cancel") {
		taskID := strings.TrimSuffix(path, ":cancel")
		if err := validatePathUUID(taskID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.handleCancel(w, r, taskID)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (h *taskHandler) handleCancel(w http.ResponseWriter, r *http.Request, taskID string) {

	ctx := r.Context()

	owner := liveTaskOwner(r, h.core, h.logger, taskID)
	userID := resolveUserID(r, h.core)
	if owner != "" && owner != userID {
		warnTaskAccessDenied(h.logger, userID, taskID, denyNoGrant)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return
	}
	if owner == "" {
		h.logger.Debug("cancel skipped: no owner authority for task", "taskID", taskID)
		writeCancelAccepted(w, taskID)
		return
	}

	h.cancelTaskTree(ctx, taskID)

	writeCancelAccepted(w, taskID)
}

func (h *taskHandler) cancelTaskTree(ctx context.Context, taskID string) {
	agentName := h.core.TaskAgentName(taskID)
	if agentName == "" {
		agentName = h.agentNameForTask(ctx, taskID)
	}
	taskLogger := h.core.TaskScopedLogger(taskID, agentName, "")

	if agentName != "" {
		if err := h.core.CancelTask(ctx, taskID, agentName); err != nil {
			taskLogger.Warn("cancel task error", "error", err)
		}
	}

	h.cancelChildren(ctx, taskID, 0, taskLogger)
}

func writeCancelAccepted(w http.ResponseWriter, taskID string) {
	resourceURL := "/api/v1/tasks/" + taskID
	w.Header().Set("Location", resourceURL)
	writeJSON(w, http.StatusAccepted, OperationResponse{
		ID:          taskID,
		Status:      OperationInProgress,
		ResourceURL: resourceURL,
	})
}

const maxChildCancelDepth = 50

func (h *taskHandler) cancelChildren(ctx context.Context, parentID string, depth int, logger *slog.Logger) {
	if depth >= maxChildCancelDepth {
		return
	}

	children, err := h.taskRepo.FindActiveChildren(ctx, parentID)
	if err != nil {
		logger.Warn("find active children error", "parentID", parentID, "error", err)
		return
	}

	for _, childID := range children {
		childAgent := h.core.TaskAgentName(childID)
		if childAgent == "" {
			childAgent = h.agentNameForTask(ctx, childID)
		}
		if childAgent != "" {
			if err := h.core.CancelTask(ctx, childID, childAgent); err != nil {
				logger.Warn("cancel child task error", "taskID", childID, "error", err)
			}
		}
		h.cancelChildren(ctx, childID, depth+1, logger)
	}
}

func (h *taskHandler) agentNameForTask(ctx context.Context, taskID string) string {
	if task, err := h.taskRepo.GetByID(ctx, taskID); err == nil && task != nil &&
		task.AgentName != nil && *task.AgentName != "" {
		return *task.AgentName
	}
	return h.extractAgentFromEvents(ctx, taskID)
}

func (h *taskHandler) extractAgentFromEvents(ctx context.Context, taskID string) string {
	events, err := h.eventRepo.GetByTaskID(ctx, taskID)
	if err != nil {
		return ""
	}
	for _, e := range events {
		if e.Direction != "request" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
			continue
		}
		if params, ok := payload["params"].(map[string]any); ok {
			if msg, ok := params["message"].(map[string]any); ok {
				if meta, ok := msg["metadata"].(map[string]any); ok {
					if name, ok := meta["workflow_name"].(string); ok && name != "" {
						return name
					}
					if name, ok := meta["agent_name"].(string); ok && name != "" {
						return name
					}
				}
			}
		}
	}
	return ""
}

func (h *taskHandler) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	callerUserID := resolveUserID(r, h.core)
	task, authorizedIDs := requireOwnedTaskOrTraceFull(w, r, h, taskID, callerUserID)
	if task == nil {
		return
	}

	h.serveStimExport(w, r, task, authorizedIDs)
}

func (h *taskHandler) serveStimExport(w http.ResponseWriter, r *http.Request, task *store.Task, authorizedIDs []string) {
	data, rootID, err := h.buildStimExport(r.Context(), task, authorizedIDs)
	if err != nil {
		if clientGone(err) {
			h.logger.Debug("stim export canceled by client", "taskID", task.ID)
			return
		}
		h.logger.Error("build stim export", "error", err, "taskID", task.ID)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.stim"`, rootID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *taskHandler) buildStimExport(ctx context.Context, task *store.Task, authorizedIDs []string) ([]byte, string, error) {
	parentChain, err := h.taskRepo.GetParentChain(ctx, task.ID, stimExportParentChainMaxDepth)
	if err != nil {
		return nil, "", fmt.Errorf("get parent chain: %w", err)
	}

	descendants, err := h.taskRepo.GetAllDescendantIDs(ctx, task.ID)
	if err != nil {
		return nil, "", fmt.Errorf("get descendants: %w", err)
	}

	idSet := make(map[string]bool)
	var allIDs []string
	for _, id := range parentChain {
		if !idSet[id] {
			idSet[id] = true
			allIDs = append(allIDs, id)
		}
	}
	for _, id := range descendants {
		if !idSet[id] {
			idSet[id] = true
			allIDs = append(allIDs, id)
		}
	}

	if len(authorizedIDs) > 0 {
		allowed := make(map[string]bool, len(authorizedIDs))
		for _, id := range authorizedIDs {
			allowed[id] = true
		}
		confined := make([]string, 0, len(allIDs))
		confinedSet := make(map[string]bool, len(allIDs))
		for _, id := range allIDs {
			if allowed[id] {
				confined = append(confined, id)
				confinedSet[id] = true
			}
		}
		allIDs, idSet = confined, confinedSet
	}

	events, err := h.eventRepo.GetByTaskIDs(ctx, allIDs)
	if err != nil {
		return nil, "", fmt.Errorf("get task events: %w", err)
	}

	doc := stim.BuildDocument(entrypoint.StimHeaderFromTask(task, len(idSet), len(descendants) > 0), entrypoint.StimEventsFromStore(events))

	data, err := stim.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("marshal STIM YAML: %w", err)
	}

	rootID := task.ID
	if len(parentChain) > 0 {
		rootID = parentChain[len(parentChain)-1]
	}

	return data, rootID, nil
}

func resolveUserID(r *http.Request, core *entrypoint.GatewayCore) string {
	return resolveUserIDCtx(r.Context(), core)
}

func resolveUserIDCtx(ctx context.Context, core *entrypoint.GatewayCore) string {
	if subject, ok := auth.SubjectFromContext(ctx); ok {
		caller := subject.Caller()
		if caller.Sub != "" {
			return caller.Sub
		}
		if caller.Email != "" {
			return caller.Email
		}
	}
	cfg := core.Config()
	if cfg.DefaultUserIdentity != "" {
		return cfg.DefaultUserIdentity
	}
	return "anonymous"
}

func taskToStatusResponse(t *store.Task) map[string]any {
	resp := map[string]any{
		"id":             t.ID,
		"userId":         t.UserID,
		"executionMode":  t.ExecutionMode,
		"startTime":      epochMsToISO8601(t.StartTime),
		"eventsBuffered": t.EventsBuffered,
		"eventsConsumed": t.EventsConsumed,
	}
	if t.ParentTaskID != nil {
		resp["parentTaskId"] = *t.ParentTaskID
	}
	if t.LastActivityTime != nil {
		resp["lastActivityTime"] = epochMsToISO8601(*t.LastActivityTime)
	}
	if t.EndTime != nil {
		resp["endTime"] = epochMsToISO8601(*t.EndTime)
	}
	if t.Status != nil {
		resp["status"] = *t.Status
	}
	if t.InitialRequestText != nil {
		resp["initialRequestText"] = *t.InitialRequestText
	}
	if t.SessionID != nil {
		resp["sessionId"] = *t.SessionID
	}
	if t.TotalInputTokens != nil {
		resp["totalInputTokens"] = *t.TotalInputTokens
	}
	if t.TotalOutputTokens != nil {
		resp["totalOutputTokens"] = *t.TotalOutputTokens
	}
	if trigger := taskTriggerResponse(t); len(trigger) > 0 {
		resp["trigger"] = trigger
	}
	return resp
}

func taskTriggerResponse(t *store.Task) map[string]any {
	source := t.DecodedTriggerSource()
	trigger := map[string]any{}
	for key, value := range map[string]*string{
		"kind":           t.TriggerKind,
		"channel":        t.Channel,
		"entrypointId":   t.EntrypointID,
		"entrypointName": t.EntrypointName,
		"route":          &source.Route,
		"event":          &source.Event,
		"deliveryId":     &source.DeliveryID,
	} {
		if value != nil && *value != "" {
			trigger[key] = *value
		}
	}
	return trigger
}

const statusUpdateKind = "status-update"

func formatTaskEvent(e *store.TaskEvent, logger *slog.Logger) map[string]any {
	directionMap := map[string]string{
		"request":  "request",
		"response": "task",
		"status":   statusUpdateKind,
		"error":    "error_response",
	}
	direction := directionMap[e.Direction]
	if direction == "" {
		direction = e.Direction
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil && logger != nil {
		logger.Debug("unmarshal task event payload",
			"taskID", e.TaskID, "eventID", e.ID, "error", err)
	}

	sourceEntity := "unknown"
	targetEntity := "unknown"

	if payload != nil {
		sourceEntity, targetEntity = extractEntities(e.Direction, payload)
	}

	result := map[string]any{
		"event_type":    "a2a_message",
		"timestamp":     epochMsToISO8601(e.CreatedTime),
		"solace_topic":  e.Topic,
		"direction":     direction,
		"source_entity": sourceEntity,
		"target_entity": targetEntity,
		"task_id":       e.TaskID,
		"full_payload":  payload,
	}

	if payload != nil {
		if id, ok := payload["id"].(string); ok {
			result["message_id"] = id
		}
	}

	if payload != nil {
		method, _ := payload["method"].(string)
		result["payload_summary"] = map[string]any{
			"method":         method,
			"params_preview": nil,
		}
	}

	return result
}

func extractEntities(direction string, payload map[string]any) (source, target string) {
	source = "unknown"
	target = "unknown"

	switch direction {
	case "request":
		if params, ok := payload["params"].(map[string]any); ok {
			if msg, ok := params["message"].(map[string]any); ok {
				if meta, ok := msg["metadata"].(map[string]any); ok {
					if name, ok := meta["agent_name"].(string); ok && name != "" {
						target = name
					}
				}
			}
		}
	case "response", "status", "error":
		if result, ok := payload["result"].(map[string]any); ok {
			if meta, ok := result["metadata"].(map[string]any); ok {
				if name, ok := meta["agent_name"].(string); ok && name != "" {
					source = name
				}
			}
			if taskData, ok := result["task"].(map[string]any); ok {
				if meta, ok := taskData["metadata"].(map[string]any); ok {
					if name, ok := meta["agent_name"].(string); ok && name != "" {
						source = name
					}
				}
			}
			if status, ok := result["status"].(map[string]any); ok {
				if msg, ok := status["message"].(map[string]any); ok {
					if meta, ok := msg["metadata"].(map[string]any); ok {
						if name, ok := meta["agent_name"].(string); ok && name != "" {
							source = name
						}
					}
				}
			}
		}
	}

	return source, target
}

func extractAgentResponseFromBubbles(bubbles string) string {
	if bubbles == "" {
		return ""
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(bubbles), &parsed); err != nil {
		return ""
	}
	for _, bubble := range parsed {
		kind, _ := bubble["type"].(string)
		if kind == "" {
			kind, _ = bubble["role"].(string)
		}
		if kind != "agent" && kind != "assistant" {
			continue
		}
		if text, ok := bubble["text"].(string); ok && text != "" {
			return text
		}
		if parts, ok := bubble["parts"].([]any); ok {
			for _, p := range parts {
				part, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := part["text"].(string); ok && text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func (h *taskHandler) extractAgentResponseFromSSE(ctx context.Context, taskID string) string {
	events, err := h.sseRepo.GetByTask(ctx, taskID, 0)
	if err != nil || len(events) == 0 {
		return ""
	}

	for _, e := range events {
		if e.EventType != "final_response" {
			continue
		}
		if text := extractTextFromSSEEvent(e.EventData, "task"); text != "" {
			return text
		}
	}

	var accumulated strings.Builder
	for _, e := range events {
		if e.EventType != "status_update" || isFinalStatusUpdate(e.EventData) {
			continue
		}
		accumulated.WriteString(extractTextFromSSEEvent(e.EventData, statusUpdateKind))
	}
	return accumulated.String()
}

func isFinalStatusUpdate(eventData string) bool {
	var parsed struct {
		Result struct {
			Final bool `json:"final"`
		} `json:"result"`
	}
	return json.Unmarshal([]byte(eventData), &parsed) == nil && parsed.Result.Final
}

func extractTextFromSSEEvent(eventData string, kind string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(eventData), &parsed); err != nil {
		return ""
	}

	result, ok := parsed["result"].(map[string]any)
	if !ok {
		return ""
	}

	switch kind {
	case "task":
		taskData, ok := result["task"].(map[string]any)
		if !ok {
			return ""
		}
		artifacts, ok := taskData["artifacts"].([]any)
		if !ok {
			return ""
		}
		for _, a := range artifacts {
			artifact, ok := a.(map[string]any)
			if !ok {
				continue
			}
			parts, ok := artifact["parts"].([]any)
			if !ok {
				continue
			}
			for _, p := range parts {
				part, ok := p.(map[string]any)
				if !ok {
					continue
				}
				if partKind, _ := part["kind"].(string); partKind == "text" {
					if text, ok := part["text"].(string); ok && text != "" {
						return text
					}
				}
			}
		}

	case statusUpdateKind:
		status, ok := result["status"].(map[string]any)
		if !ok {
			return ""
		}
		msg, ok := status["message"].(map[string]any)
		if !ok {
			return ""
		}
		parts, ok := msg["parts"].([]any)
		if !ok {
			return ""
		}
		for _, p := range parts {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok && text != "" {
				return text
			}
		}
	}

	return ""
}
