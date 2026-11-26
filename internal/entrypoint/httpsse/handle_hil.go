// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
)

type hilHandler struct {
	core             *entrypoint.GatewayCore
	eventLog         eventlog.TaskEventLog
	sseManager       *SSEManager
	logger           *slog.Logger
	publishLogRecord func(ctx context.Context, taskID string, payload []byte) error
}

func (h *hilHandler) publishMarker(ctx context.Context, taskID string, payload []byte) error {
	if h.publishLogRecord != nil {
		return h.publishLogRecord(ctx, taskID, payload)
	}
	return h.core.PublishLogRecord(ctx, taskID, payload)
}

type hilRespondRequest struct {
	TaskID    string         `json:"taskId"`
	AgentName string         `json:"agentName"`
	RequestID string         `json:"requestId"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
}

func (h *hilHandler) handleRespond(w http.ResponseWriter, r *http.Request) {
	var req hilRespondRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	if req.TaskID == "" || req.AgentName == "" || req.RequestID == "" || req.Status == "" {
		writeError(w, http.StatusBadRequest, "taskId, agentName, requestId, and status are required")
		return
	}

	userID := gwauth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)
	if ref := h.respond(r.Context(), requestCaller(r, userID), userID, req, false); ref != nil {
		writeError(w, ref.status, ref.msg)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

const noOpenQuestionMsg = "the task is not waiting on that requestId"

func (h *hilHandler) questionIsOpen(ctx context.Context, taskID, userID, requestID string) bool {
	if h.eventLog == nil {
		return true
	}
	pending := h.pendingInput(ctx, taskID, userID)
	if pending == nil {
		return false
	}
	pendingID, _ := pending.request["request_id"].(string)
	return pendingID == requestID
}

type hilRefusal struct {
	status int
	msg    string
}

func (h *hilHandler) respond(ctx context.Context, caller *gwauth.Subject, userID string, req hilRespondRequest, requireOpenQuestion bool) *hilRefusal {
	if !callerIsLiveTaskOwnerCtx(ctx, h.core, h.logger, req.TaskID, userID) {
		return &hilRefusal{http.StatusNotFound, taskNotFoundMsg}
	}

	if h.core.AgentRegistry().Get(req.AgentName) == nil {
		return &hilRefusal{http.StatusNotFound, "agent not found: " + req.AgentName}
	}

	if requireOpenQuestion && !h.questionIsOpen(ctx, req.TaskID, userID, req.RequestID) {
		return &hilRefusal{http.StatusConflict, noOpenQuestionMsg}
	}

	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: req.RequestID,
		Status:    req.Status,
		Data:      req.Data,
	}

	taskLogger := h.core.TaskScopedLogger(req.TaskID, req.AgentName, "")

	if err := h.core.SubmitUserInputResponseAttributedTo(ctx, req.AgentName, req.TaskID, resp, caller, userID); err != nil {
		if errors.Is(err, entrypoint.ErrAgentDelegationDenied) {
			return &hilRefusal{http.StatusForbidden, err.Error()}
		}
		if errors.Is(err, entrypoint.ErrNotTaskOwner) {
			return &hilRefusal{http.StatusNotFound, taskNotFoundMsg}
		}
		taskLogger.Error("failed to submit user_input_response", "error", err)
		return &hilRefusal{http.StatusInternalServerError, "failed to submit response"}
	}

	if h.eventLog != nil {
		marker, err := answeredMarker(req.TaskID, h.taskSession(req.TaskID), req.RequestID, req.Status)
		if err == nil {
			err = h.publishMarker(context.WithoutCancel(ctx), req.TaskID, marker)
		}
		if err != nil {
			taskLogger.Warn("failed to record user_input_response marker; the form may show again after a reload", "error", err)
		}
	}
	return nil
}

func (h *hilHandler) taskSession(taskID string) string {
	if h.sseManager != nil {
		if sessionID, _ := h.sseManager.TaskContext(taskID); sessionID != "" {
			return sessionID
		}
	}
	if info := h.core.Tasks().Get(taskID); info != nil {
		return info.SessionID
	}
	return ""
}

func answeredMarker(taskID, sessionID, requestID, status string) ([]byte, error) {
	return json.Marshal(a2a.Response{
		JSONRPC: "2.0",
		ID:      taskID,
		Result: &a2a.TaskStatusUpdateEvent{
			TaskID:    taskID,
			ContextID: sessionID,
			Kind:      "status-update",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateWorking,
				Message: &a2a.Message{
					Kind:      "message",
					MessageID: "user-input-response-" + requestID,
					Role:      a2a.RoleAgent,
					Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
						"type":       "user_input_response",
						"request_id": requestID,
						"status":     status,
					})},
					TaskID:    taskID,
					ContextID: sessionID,
				},
			},
		},
	})
}

func (h *hilHandler) handlePendingInput(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	if taskID == "" {
		http.Error(w, "taskId is required", http.StatusBadRequest)
		return
	}
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	pending := h.pendingInput(r.Context(), taskID, resolveUserID(r, h.core))
	if pending == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"taskId":    pending.taskID,
		"agentName": pending.agentName,
		"request":   pending.request,
	})
}

type pendingQuestion struct {
	taskID    string
	agentName string
	request   map[string]any
}

func (h *hilHandler) pendingInput(ctx context.Context, taskID, userID string) *pendingQuestion {
	if !h.core.IsTaskRunning(taskID) {
		return nil
	}

	if !callerMayReadLiveTaskCtx(ctx, h.core, h.logger, taskID, userID) {
		return nil
	}

	pendingRequestID, recorded := h.core.PendingUserInput(taskID)
	if recorded && pendingRequestID == "" {
		return nil
	}

	if h.eventLog == nil {
		return nil
	}

	payload := h.eventLog.FindLastEventByDataType(ctx, taskID, "user_input_request")
	if payload == nil {
		return nil
	}

	hilData := extractUserInputRequestFromPayload(payload)
	if hilData == nil {
		return nil
	}

	requestID, _ := hilData["request_id"].(string)
	if recorded && requestID != pendingRequestID {
		return nil
	}

	if requestID != "" {
		responsePayload := h.eventLog.FindLastEventByDataType(ctx, taskID, "user_input_response")
		if responsePayload != nil {
			respData := extractDataPartFromPayload(responsePayload, "user_input_response")
			if respData != nil {
				respRequestID, _ := respData["request_id"].(string)
				if respRequestID == requestID {
					return nil
				}
			}
		}
	}

	return &pendingQuestion{taskID: taskID, agentName: h.core.TaskAgentName(taskID), request: hilData}
}

func extractUserInputRequestFromPayload(payload []byte) map[string]any {
	return extractDataPartFromPayload(payload, "user_input_request")
}

func extractDataPartFromPayload(payload []byte, dataType string) map[string]any {
	var rpc struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &rpc); err != nil {
		return nil
	}

	var event struct {
		Status struct {
			Message struct {
				Parts []struct {
					Kind string          `json:"kind"`
					Data json.RawMessage `json:"data"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rpc.Result, &event); err != nil {
		return nil
	}

	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(part.Data, &data); err != nil {
			continue
		}
		if t, ok := data["type"].(string); ok && t == dataType {
			return data
		}
	}
	return nil
}
