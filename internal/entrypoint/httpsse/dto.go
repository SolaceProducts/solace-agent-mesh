// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

type JSONRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  MessageParams `json:"params"`
}

type MessageParams struct {
	Message MessageDTO `json:"message"`
}

type MessageDTO struct {
	Kind      string         `json:"kind"`
	MessageID string         `json:"messageId"`
	Role      string         `json:"role"`
	Parts     []PartDTO      `json:"parts"`
	ContextID string         `json:"contextId,omitempty"`
	TaskID    string         `json:"taskId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type PartDTO struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text,omitempty"`
	Data     any            `json:"data,omitempty"`
	File     *FileDTO       `json:"file,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type FileDTO struct {
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *ErrorDTO `json:"error,omitempty"`
}

type ErrorDTO struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type TaskResult struct {
	ID        string `json:"id"`
	ContextID string `json:"contextId,omitempty"`
	Kind      string `json:"kind"`
	TraceID   string `json:"traceId,omitempty"`
}

const (
	codeParseError    = -32700
	codeInvalidParams = -32602
	codeAgentNotFound = -32001
	codeForbidden     = -32003
	codeInternalError = -32603
)

func newSuccessResponse(id string, result any) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func newErrorResponse(id string, code int, msg string) *JSONRPCResponse {
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &ErrorDTO{Code: code, Message: msg}}
}

func newRejectedResponse(id string, code int, msg, traceID string) *JSONRPCResponse {
	resp := newErrorResponse(id, code, msg)
	if traceID != "" {
		resp.Error.Data = map[string]any{"traceId": traceID}
	}
	return resp
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type DataResponse[T any] struct {
	Data T `json:"data"`
}

type PaginatedResponse[T any] struct {
	Data []T            `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

type PaginationMeta struct {
	Pagination PaginationInfo `json:"pagination"`
}

type PaginationInfo struct {
	PageNumber int  `json:"pageNumber"`
	PageSize   int  `json:"pageSize"`
	TotalPages int  `json:"totalPages"`
	Count      int  `json:"count"`
	NextPage   *int `json:"nextPage"`
}

func newPaginationInfo(pageNumber, pageSize, total int) PaginationInfo {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	var nextPage *int
	if pageNumber < totalPages {
		n := pageNumber + 1
		nextPage = &n
	}
	return PaginationInfo{
		PageNumber: pageNumber,
		PageSize:   pageSize,
		TotalPages: totalPages,
		Count:      total,
		NextPage:   nextPage,
	}
}

func unpagedResponse[T any](data []T) PaginatedResponse[T] {
	return PaginatedResponse[T]{
		Data: nonNilList(data),
		Meta: PaginationMeta{Pagination: newPaginationInfo(1, len(data), len(data))},
	}
}

func nonNilList[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

type ErrorResponse struct {
	Message           string              `json:"message"`
	ErrorID           string              `json:"errorId"`
	ValidationDetails map[string][]string `json:"validationDetails,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeErrorWithID(w, status, msg, idgen.New())
}

func writeErrorWithID(w http.ResponseWriter, status int, msg, errID string) {
	logger := slog.Default().With("logger_name", "gateway-http")
	switch {
	case status >= 500:
		logger.Error("http error response", "errorId", errID, "httpStatus", status)
	case status == http.StatusUnauthorized:
		logger.Warn("http 401 unauthorized", "errorId", errID, "httpStatus", status, "message", msg)
	case status == http.StatusForbidden:
		logger.Warn("http 403 forbidden", "errorId", errID, "httpStatus", status, "message", msg)
	default:
		logger.Warn("http error response", "errorId", errID, "httpStatus", status, "message", msg)
	}
	writeJSON(w, status, ErrorResponse{
		Message: msg,
		ErrorID: errID,
	})
}

func requireUUID(w http.ResponseWriter, value, fieldName string) bool {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid %s: must be a canonical lowercase UUID (8-4-4-4-12 hex digits)", fieldName))
		return false
	}
	return true
}

type ProjectRef struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type SessionResponse struct {
	ID                       string      `json:"id"`
	UserID                   string      `json:"userId"`
	CreatedBy                string      `json:"createdBy"`
	Name                     *string     `json:"name"`
	AgentID                  *string     `json:"agentId"`
	Project                  *ProjectRef `json:"project"`
	Source                   *string     `json:"source"`
	HasRunningBackgroundTask bool        `json:"hasRunningBackgroundTask"`
	ScheduledTaskID          *string     `json:"scheduledTaskId"`
	ScheduledTaskName        *string     `json:"scheduledTaskName"`
	CreatedTime              string      `json:"createdAt"`
	UpdatedTime              *string     `json:"updatedAt"`
	LastViewedAt             *string     `json:"lastViewedAt"`
	IsEphemeralTest          bool        `json:"isEphemeralTest"`
}

type CreateSessionRequest struct {
	ID      string  `json:"id"`
	AgentID *string `json:"agentId,omitempty"`
	Source  *string `json:"source,omitempty"`
}

type RenameSessionRequest struct {
	Name    string `json:"name"`
	AgentID string `json:"agentId,omitempty"`
}

type AssignProjectRequest struct {
	ProjectID *string `json:"projectId"`
}

type GenerateTitleRequest struct {
	UserMessage   string `json:"userMessage"`
	AgentResponse string `json:"agentResponse"`
	Force         bool   `json:"force"`
}

type SaveChatTaskRequest struct {
	TaskID            string  `json:"id"`
	UserMessage       *string `json:"userMessage"`
	MessageBubbles    string  `json:"messageBubbles"`
	TaskMetadata      *string `json:"taskMetadata"`
	AutoGenerateTitle bool    `json:"autoGenerateTitle"`
}

type TaskResponse struct {
	ID             string  `json:"id"`
	SessionID      string  `json:"sessionId"`
	CreatedBy      string  `json:"createdBy"`
	UserMessage    *string `json:"userMessage"`
	MessageBubbles string  `json:"messageBubbles"`
	TaskMetadata   *string `json:"taskMetadata"`
	CreatedTime    string  `json:"createdAt"`
	UpdatedTime    *string `json:"updatedAt"`
}

type UnconsumedEventsResponse struct {
	HasEvents    bool                             `json:"hasEvents"`
	TaskIDs      []string                         `json:"taskIds"`
	SessionID    string                           `json:"sessionId"`
	EventsByTask map[string]*UnconsumedTaskEvents `json:"eventsByTask"`
}

type UnconsumedTaskEvents struct {
	EventsBuffered bool              `json:"eventsBuffered"`
	Events         []UnconsumedEvent `json:"events"`
}

type UnconsumedEvent struct {
	Sequence  int    `json:"sequence"`
	EventType string `json:"eventType"`
	Data      any    `json:"data"`
}

type VersionResponse struct {
	Products         []ProductInfo `json:"products"`
	MinClientVersion string        `json:"minClientVersion,omitempty"`
	APIVersion       string        `json:"apiVersion,omitempty"`
}

type ProductInfo struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

type OperationStatus string

const (
	OperationPending    OperationStatus = "pending"
	OperationInProgress OperationStatus = "inProgress"
	OperationSucceeded  OperationStatus = "succeeded"
	OperationFailed     OperationStatus = "failed"
)

type OperationResponse struct {
	ID          string          `json:"id"`
	Status      OperationStatus `json:"status"`
	ResourceURL string          `json:"resourceUrl,omitempty"`
}

func epochMsToISO8601(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	return t.Format("2006-01-02T15:04:05.000000+00:00")
}

func optionalISO8601(ms *int64) *string {
	if ms == nil {
		return nil
	}
	s := epochMsToISO8601(*ms)
	return &s
}

func int64PtrIfNonZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func sessionToResponse(s *store.Session) SessionResponse {
	var project *ProjectRef
	if s.ProjectID != nil && *s.ProjectID != "" {
		project = &ProjectRef{ID: *s.ProjectID}
	}
	return SessionResponse{
		ID:                       s.ID,
		UserID:                   s.UserID,
		CreatedBy:                s.UserID,
		Name:                     s.Name,
		AgentID:                  s.AgentID,
		Project:                  project,
		Source:                   s.Source,
		HasRunningBackgroundTask: false,
		ScheduledTaskID:          nil,
		ScheduledTaskName:        nil,
		CreatedTime:              epochMsToISO8601(s.CreatedTime),
		UpdatedTime:              optionalISO8601(int64PtrIfNonZero(s.UpdatedTime)),
		LastViewedAt:             optionalISO8601(s.LastViewedAt),
	}
}

func chatTaskToResponse(ct *store.ChatTask) TaskResponse {
	return TaskResponse{
		ID:             ct.ID,
		SessionID:      ct.SessionID,
		CreatedBy:      ct.UserID,
		UserMessage:    ct.UserMessage,
		MessageBubbles: ct.MessageBubbles,
		TaskMetadata:   ct.TaskMetadata,
		CreatedTime:    epochMsToISO8601(ct.CreatedTime),
		UpdatedTime:    optionalISO8601(ct.UpdatedTime),
	}
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	return v
}

func parsePagination(r *http.Request) (pageNumber, pageSize int, err error) {
	pageNumber, err = parsePageNumber(r.URL.Query())
	if err != nil {
		return 0, 0, err
	}
	pageSize, err = parsePageSize(r.URL.Query())
	if err != nil {
		return 0, 0, err
	}
	return pageNumber, pageSize, nil
}

func parsePageNumber(q url.Values) (int, error) {
	raw := q.Get("pageNumber")
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, &paginationError{"pageNumber must be a positive integer"}
	}
	return n, nil
}

func parsePageSize(q url.Values) (int, error) {
	raw := q.Get("pageSize")
	if raw == "" {
		return 20, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, &paginationError{"pageSize must be between 1 and 100"}
	}
	return n, nil
}

func hasPaginationParams(r *http.Request) bool {
	q := r.URL.Query()
	return q.Get("pageNumber") != "" || q.Get("pageSize") != ""
}

type paginationError struct{ msg string }

func (e *paginationError) Error() string { return e.msg }

func parseExpand(r *http.Request) map[string]bool {
	raw := r.URL.Query().Get("expand")
	if raw == "" {
		return map[string]bool{}
	}
	result := make(map[string]bool)
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field != "" {
			result[field] = true
		}
	}
	return result
}

func queryStringSlice(r *http.Request, key string) []string {
	return splitCommaList(r.URL.Query().Get(key))
}

func splitCommaList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
