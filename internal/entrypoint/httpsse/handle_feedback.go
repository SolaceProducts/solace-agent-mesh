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
	"net/url"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/google/uuid"
)

const (
	msgFeedbackAlreadySubmitted = "feedback for this task was already submitted and cannot be changed"

	msgFeedbackSessionNotOwned = "feedback rejected: session belongs to another user"
)

type feedbackHandler struct {
	core         *entrypoint.GatewayCore
	feedbackRepo *store.FeedbackRepo
	taskRepo     *store.TaskRepo
	sessRepo     *store.SessionRepo
	eventRepo    *store.TaskEventRepo
	logger       *slog.Logger

	publishTimeout time.Duration

	nameResolver func(string) string
}

type SubmitFeedbackRequest struct {
	TaskID           string  `json:"taskId"`
	SessionID        string  `json:"sessionId"`
	FeedbackType     string  `json:"feedbackType"`
	FeedbackText     *string `json:"feedbackText"`
	ShareTaskContent bool    `json:"shareTaskContent"`
}

type FeedbackResponse struct {
	ID               string  `json:"id"`
	SessionID        string  `json:"sessionId"`
	TaskID           string  `json:"taskId"`
	UserID           string  `json:"userId"`
	Rating           string  `json:"rating"`
	Comment          *string `json:"comment"`
	ShareTaskContent bool    `json:"shareTaskContent"`
	CreatedAt        string  `json:"createdAt"`
}

type CrossUserFeedbackResponse struct {
	ID               string  `json:"id"`
	SessionID        string  `json:"sessionId"`
	TaskID           string  `json:"taskId"`
	UserID           string  `json:"userId"`
	Rating           string  `json:"rating"`
	Comment          *string `json:"comment"`
	ShareTaskContent bool    `json:"shareTaskContent"`
	SubjectID        string  `json:"subjectId"`
	SubjectType      string  `json:"subjectType"`
	SubjectName      string  `json:"subjectName"`
	EntrypointType   string  `json:"entrypointType"`
	CreatedAt        string  `json:"createdAt"`
}

func visibilityFromReach(reach rbac.FeedbackReach) store.FeedbackVisibility {
	return store.FeedbackVisibility{
		Agents:    store.SubjectScope{All: reach.AgentRead.All, IDs: reach.AgentRead.IDs},
		Workflows: store.SubjectScope{All: reach.WorkflowRead.All, IDs: reach.WorkflowRead.IDs},
	}
}

func hasFeedbackReadReach(reach rbac.FeedbackReach) bool {
	hasAny := func(s rbac.SubjectReach) bool { return s.All || len(s.IDs) > 0 }
	return hasAny(reach.AgentRead) || hasAny(reach.WorkflowRead)
}

const (
	msgCrossUserFeedbackRead = "cross-user feedback read"
	msgFeedbackReadDenied    = "cross-user feedback read denied"
)

func auditCrossUserFeedbackRead(logger *slog.Logger, callerUserID string, reach rbac.FeedbackReach, rowCount int) {
	if logger == nil {
		return
	}
	logger.Info(msgCrossUserFeedbackRead,
		"callerUser", callerUserID,
		"agentReachAll", reach.AgentRead.All,
		"agentReachCount", len(reach.AgentRead.IDs),
		"workflowReachAll", reach.WorkflowRead.All,
		"workflowReachCount", len(reach.WorkflowRead.IDs),
		"rowCount", rowCount,
		"crossUser", true)
}

func warnFeedbackReadDenied(logger *slog.Logger, callerUserID string, hasIdentity bool) {
	if logger == nil {
		return
	}
	logger.Warn(msgFeedbackReadDenied,
		"callerUser", callerUserID,
		"hasIdentity", hasIdentity)
}

func (h *feedbackHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req SubmitFeedbackRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	if req.TaskID == "" || req.SessionID == "" || req.FeedbackType == "" {
		writeError(w, http.StatusBadRequest, "taskId, sessionId, and feedbackType are required")
		return
	}
	if !requireUUID(w, req.TaskID, "taskId") {
		return
	}
	if !requireUUID(w, req.SessionID, "sessionId") {
		return
	}
	if req.FeedbackType != "up" && req.FeedbackType != "down" {
		writeError(w, http.StatusBadRequest, "feedbackType must be 'up' or 'down'")
		return
	}

	userID := resolveUserID(r, h.core)

	subject, ok := h.resolveOwnedTaskSubject(w, r, req.TaskID, userID)
	if !ok {
		return
	}
	if !h.requireOwnedFeedbackSession(w, r, req, userID) {
		return
	}

	result, err := entrypoint.RecordFeedback(r.Context(), h.feedbackRepo, store.FeedbackSubmission{
		SessionID:      req.SessionID,
		TaskID:         req.TaskID,
		UserID:         userID,
		Rating:         req.FeedbackType,
		Comment:        req.FeedbackText,
		SubjectID:      subject.ID,
		SubjectType:    subject.Type,
		EntrypointType: store.EntrypointTypeWebUI,
	}, entrypoint.FeedbackPublishParams{
		Core:           h.core,
		TaskRepo:       h.taskRepo,
		EventRepo:      h.eventRepo,
		Logger:         h.logger,
		PublishTimeout: h.publishTimeout,
	})
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		writeError(w, http.StatusConflict, msgFeedbackAlreadySubmitted)
		return
	case err != nil:
		h.logger.Error("save feedback error", "taskID", req.TaskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}
	feedback := result.Feedback

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     feedback.ID,
		"status": "received",
	})
}

func (h *feedbackHandler) resolveOwnedTaskSubject(w http.ResponseWriter, r *http.Request, taskID, userID string) (entrypoint.FeedbackSubject, bool) {
	subject, task, err := entrypoint.FeedbackSubjectForTask(r.Context(), h.taskRepo, taskID)
	if task != nil && task.UserID != userID {
		warnTaskAccessDenied(h.logger, userID, taskID, denyWriteNotOwner)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return entrypoint.FeedbackSubject{}, false
	}
	switch {
	case errors.Is(err, entrypoint.ErrFeedbackTaskNotFound):
		h.logger.Warn("feedback rejected: task not found", "taskID", taskID, "userID", userID)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
	case errors.Is(err, entrypoint.ErrFeedbackTaskUnclassified):
		h.logger.Warn("feedback rejected: task has no classified handler to attribute", "taskID", taskID, "userID", userID)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
	case err != nil:
		h.logger.Error("get task error", "taskID", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
	default:
		return subject, true
	}
	return entrypoint.FeedbackSubject{}, false
}

func (h *feedbackHandler) requireOwnedFeedbackSession(w http.ResponseWriter, r *http.Request, req SubmitFeedbackRequest, userID string) bool {
	session, err := h.sessRepo.GetByID(r.Context(), req.SessionID)
	if err != nil {
		h.logger.Error("get session error", "sessionID", req.SessionID, "taskID", req.TaskID, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return false
	}
	switch {
	case session == nil:
		h.logger.Warn("feedback rejected: session not found", "sessionID", req.SessionID, "taskID", req.TaskID, "userID", userID)
	case session.UserID != userID:
		h.logger.Warn(msgFeedbackSessionNotOwned, "sessionID", req.SessionID, "taskID", req.TaskID, "userID", userID)
	default:
		return true
	}
	writeError(w, http.StatusNotFound, sessionNotFoundMsg)
	return false
}

func (h *feedbackHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, h.core)
	q := r.URL.Query()

	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}

	startDate, endDate, dateErr := parseFeedbackDateRange(q)
	if dateErr != nil {
		writeError(w, http.StatusBadRequest, dateErr.Error())
		return
	}

	taskID := q.Get("taskId")
	sessionID := q.Get("sessionId")
	rating := q.Get("rating")

	if taskID != "" && !requireUUID(w, taskID, "task_id") {
		return
	}
	if sessionID != "" && !requireUUID(w, sessionID, "session_id") {
		return
	}

	if taskID != "" && !h.requireFilterTaskOwned(w, r, taskID, userID) {
		return
	}

	offset := (page - 1) * pageSize
	feedbacks, err := h.feedbackRepo.Search(r.Context(), userID, startDate, endDate,
		taskID, sessionID, rating, pageSize, offset)
	if err != nil {
		h.logger.Error("search feedback error", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	result := make([]FeedbackResponse, 0, len(feedbacks))
	for _, f := range feedbacks {
		result = append(result, FeedbackResponse{
			ID:               f.ID,
			SessionID:        f.SessionID,
			TaskID:           f.TaskID,
			UserID:           f.UserID,
			Rating:           f.Rating,
			Comment:          f.Comment,
			ShareTaskContent: f.ShareContent,
			CreatedAt:        epochMsToISO8601(f.CreatedAt),
		})
	}

	total := len(result)
	writeJSON(w, http.StatusOK, PaginatedResponse[FeedbackResponse]{
		Data: result,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *feedbackHandler) requireFilterTaskOwned(w http.ResponseWriter, r *http.Request, taskID, userID string) bool {
	task, ok := loadTaskForOwnership(w, r, h.taskRepo, h.logger, taskID)
	if !ok {
		return false
	}
	if task == nil {
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return false
	}
	if task.UserID != userID {
		warnTaskAccessDenied(h.logger, userID, taskID, denyReadNotOwner)
		writeError(w, http.StatusNotFound, taskNotFoundMsg)
		return false
	}
	return true
}

func (h *feedbackHandler) resolveSubjectName(subjectID, subjectType string) string {
	return resolveSubjectDisplayName(h.nameResolver, subjectID, subjectType)
}

func resolveSubjectDisplayName(resolver func(string) string, subjectID, subjectType string) string {
	if resolver == nil || subjectID == "" || subjectType == store.SubjectTypeUnknown {
		return ""
	}
	if name := resolver(subjectID); name != "" {
		return name
	}
	if _, err := uuid.Parse(subjectID); err != nil {
		return ""
	}
	kind := a2a.KindAgent
	if subjectType == store.SubjectTypeWorkflow {
		kind = a2a.KindWorkflow
	}
	return resolver(a2a.InstanceName(kind, subjectID))
}

func (h *feedbackHandler) handleListCrossUser(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil {
		warnFeedbackReadDenied(h.logger, "", false)
		writeError(w, http.StatusForbidden, "feedback read access required")
		return
	}
	reach := rbac.ResolveFeedbackReach(id.Scopes())
	if !hasFeedbackReadReach(reach) {
		warnFeedbackReadDenied(h.logger, id.UserID(), true)
		writeError(w, http.StatusForbidden, "feedback read access required")
		return
	}

	q := r.URL.Query()
	if q.Get("status") != "" {
		writeError(w, http.StatusBadRequest, "status filtering is not supported")
		return
	}

	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}

	startDate, endDate, dateErr := parseFeedbackDateRange(q)
	if dateErr != nil {
		writeError(w, http.StatusBadRequest, dateErr.Error())
		return
	}

	agentIDs := queryStringSlice(r, "agentIds")
	if !requireWithinInClauseCap(w, agentIDs, "agentIds") {
		return
	}
	workflowIDs := queryStringSlice(r, "workflowIds")
	if !requireWithinInClauseCap(w, workflowIDs, "workflowIds") {
		return
	}
	userIDs := queryStringSlice(r, "userIds")
	if !requireWithinInClauseCap(w, userIDs, "userIds") {
		return
	}

	rows, total, err := h.feedbackRepo.SearchCrossUser(
		r.Context(),
		visibilityFromReach(reach),
		store.CrossUserFeedbackFilter{
			UserIDs:     userIDs,
			AgentIDs:    agentIDs,
			WorkflowIDs: workflowIDs,
			Rating:      q.Get("rating"),
			StartDate:   startDate,
			EndDate:     endDate,
		},
		pageSize, (page-1)*pageSize,
	)
	if err != nil {
		h.logger.Error("list cross-user feedback", "error", err)
		writeError(w, http.StatusInternalServerError, msgInternalErr)
		return
	}

	auditCrossUserFeedbackRead(h.logger, id.UserID(), reach, len(rows))

	data := make([]CrossUserFeedbackResponse, 0, len(rows))
	for _, f := range rows {
		data = append(data, h.crossUserResponse(f))
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[CrossUserFeedbackResponse]{
		Data: data,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *feedbackHandler) crossUserResponse(f *store.Feedback) CrossUserFeedbackResponse {
	return CrossUserFeedbackResponse{
		ID:               f.ID,
		SessionID:        f.SessionID,
		TaskID:           f.TaskID,
		UserID:           f.UserID,
		Rating:           f.Rating,
		Comment:          f.Comment,
		ShareTaskContent: f.ShareContent,
		SubjectID:        f.SubjectID,
		SubjectType:      f.SubjectType,
		SubjectName:      h.resolveSubjectName(f.SubjectID, f.SubjectType),
		EntrypointType:   f.EntrypointType,
		CreatedAt:        epochMsToISO8601(f.CreatedAt),
	}
}

func requireWithinInClauseCap(w http.ResponseWriter, ids []string, param string) bool {
	if err := inClauseCapError(ids); err != nil {
		writeError(w, http.StatusBadRequest, param+" "+err.Error())
		return false
	}
	return true
}

func inClauseCapError(ids []string) error {
	if len(ids) <= dbutil.InClauseCap {
		return nil
	}
	return fmt.Errorf("must not exceed %d values", dbutil.InClauseCap)
}

func parseFeedbackDateRange(q url.Values) (startDate, endDate int64, err error) {
	startDate, err = parseFeedbackDateParam(q, "startDate")
	if err != nil {
		return 0, 0, err
	}
	endDate, err = parseFeedbackDateParam(q, "endDate")
	if err != nil {
		return 0, 0, err
	}
	return startDate, endDate, nil
}

func parseFeedbackDateParam(q url.Values, key string) (int64, error) {
	raw := q.Get(key)
	if raw == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s format (expected ISO 8601)", key)
	}
	return t.UnixMilli(), nil
}

const feedbackPublishTimeout = entrypoint.DefaultFeedbackPublishTimeout
