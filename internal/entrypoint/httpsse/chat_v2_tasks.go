// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const finalReplyWriteGrace = 30 * time.Second

func (h *taskHandler) chatV2Routes() []apicontract.Route {
	return chatv2.TaskRoutes(chatv2.Deps{ReadTask: h.readChatTask, ExportTask: h.exportChatTask, CancelTask: h.cancelChatTask})
}

func (h *taskHandler) readChatTask(ctx context.Context, taskID string) (chatv2.Task, error) {
	task, err := h.findTask(ctx, taskID)
	if err != nil || task == nil {
		return chatv2.Task{}, notFoundOr(err)
	}
	userID := resolveUserIDCtx(ctx, h.core)
	if task.UserID != userID {
		if !callerReadsAnyTaskCtx(ctx) {
			warnTaskAccessDenied(h.logger, userID, taskID, denyNoGrant)
			return chatv2.Task{}, chatv2.TaskNotFound()
		}
		auditCrossUserTaskRead(h.logger, userID, task.UserID, taskID, grantActivityRead)
	}
	return h.chatTaskOf(ctx, task)
}

func (h *taskHandler) exportChatTask(ctx context.Context, taskID string) (apicontract.Download, error) {
	task, err := h.findTask(ctx, taskID)
	if err != nil || task == nil {
		return apicontract.Download{}, notFoundOr(err)
	}
	userID := resolveUserIDCtx(ctx, h.core)
	decision, err := resolveTraceAccessCtx(ctx, h.traceDeps(), task, userID)
	if err != nil {
		return apicontract.Download{}, fmt.Errorf("resolve trace access for task %s: %w", taskID, err)
	}
	if decision.tier != traceFull {
		warnTaskAccessDenied(h.logger, userID, taskID, decision.reason)
		return apicontract.Download{}, chatv2.TaskNotFound()
	}
	if task.UserID != userID {
		auditCrossUserTaskRead(h.logger, userID, task.UserID, taskID, decision.grant)
	}
	data, rootID, err := h.buildStimExport(ctx, task, decision.scopedTo)
	if err != nil {
		return apicontract.Download{}, err
	}
	return apicontract.Download{Content: data, MimeType: chatv2.ExportTaskFormat, Filename: rootID + ".stim"}, nil
}

func (h *taskHandler) cancelChatTask(ctx context.Context, taskID string) (chatv2.Task, error) {
	task, err := h.findTask(ctx, taskID)
	if err != nil || task == nil {
		return chatv2.Task{}, notFoundOr(err)
	}
	userID := resolveUserIDCtx(ctx, h.core)
	if task.UserID != userID {
		warnTaskAccessDenied(h.logger, userID, taskID, denyNoGrant)
		return chatv2.Task{}, chatv2.TaskNotFound()
	}
	h.cancelTaskTree(ctx, taskID)
	return h.chatTaskOf(ctx, task)
}

func notFoundOr(err error) error {
	if err != nil {
		return err
	}
	return chatv2.TaskNotFound()
}

func (h *taskHandler) findTask(ctx context.Context, taskID string) (*store.Task, error) {
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task %s: %w", taskID, err)
	}
	if task != nil {
		return task, nil
	}
	info := h.core.Tasks().Get(taskID)
	if info == nil || info.UserID == "" {
		return nil, nil
	}
	pending := &store.Task{ID: taskID, UserID: info.UserID, StartTime: info.StartedAt}
	if info.SessionID != "" {
		pending.SessionID = &info.SessionID
	}
	return pending, nil
}

func (h *taskHandler) chatTaskOf(ctx context.Context, task *store.Task) (chatv2.Task, error) {
	state := chatv2.TaskStateOf(task.Status)
	out := chatv2.Task{
		ID:          task.ID,
		Type:        "task",
		ContextID:   task.SessionID,
		Status:      chatv2.TaskStatus{State: state},
		Artifacts:   []chatv2.TaskArtifact{},
		CreatedTime: apicontract.Instant(task.StartTime),
	}
	if !chatv2.IsFinalTaskState(state) {
		return out, nil
	}
	reply, err := h.finalReply(ctx, task)
	if err != nil {
		return chatv2.Task{}, err
	}
	if !reply.recorded && state == chatv2.TaskStateCompleted && h.endedJustNow(task) {
		out.Status.State = chatv2.TaskStateWorking
		return out, nil
	}
	if task.EndTime != nil {
		out.CompletedTime = apicontract.Instant(*task.EndTime)
	}
	if state == chatv2.TaskStateFailed {
		if msg := h.errorMessageForTask(ctx, task); msg != "" {
			out.ErrorMessage = &msg
		}
	}
	out.Status.Message = reply.message
	out.Artifacts = reply.artifacts
	return out, nil
}

func (h *taskHandler) endedJustNow(task *store.Task) bool {
	return task.EndTime == nil || time.Since(time.UnixMilli(*task.EndTime)) < finalReplyWriteGrace
}

type finalReply struct {
	recorded  bool
	message   *chatv2.TaskMessage
	artifacts []chatv2.TaskArtifact
}

type finalResponsePayload struct {
	Result struct {
		Status struct {
			Message *struct {
				Parts []map[string]any `json:"parts"`
			} `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			ArtifactID string           `json:"artifactId"`
			Name       string           `json:"name"`
			Parts      []map[string]any `json:"parts"`
		} `json:"artifacts"`
	} `json:"result"`
}

func (h *taskHandler) finalReply(ctx context.Context, task *store.Task) (finalReply, error) {
	reply := finalReply{artifacts: []chatv2.TaskArtifact{}}
	data, found, err := h.sseRepo.FinalResponse(ctx, task.ID)
	if err != nil {
		return reply, fmt.Errorf("load final response of task %s: %w", task.ID, err)
	}
	if !found {
		reply.message = h.replyFromChatTask(ctx, task)
		reply.recorded = reply.message != nil
		return reply, nil
	}
	var payload finalResponsePayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return reply, fmt.Errorf("decode final response of task %s: %w", task.ID, err)
	}
	reply.recorded = true
	if msg := payload.Result.Status.Message; msg != nil {
		reply.message = &chatv2.TaskMessage{Parts: nonNilList(msg.Parts)}
	}
	for _, a := range payload.Result.Artifacts {
		reply.artifacts = append(reply.artifacts, chatv2.TaskArtifact{ArtifactID: a.ArtifactID, Name: a.Name, Parts: nonNilList(a.Parts)})
	}
	return reply, nil
}

func (h *taskHandler) replyFromChatTask(ctx context.Context, task *store.Task) *chatv2.TaskMessage {
	if h.chatRepo == nil || task.SessionID == nil || *task.SessionID == "" {
		return nil
	}
	ct, err := h.chatRepo.GetBySessionAndID(ctx, *task.SessionID, task.ID)
	if err != nil || ct == nil {
		return nil
	}
	text := extractAgentResponseFromBubbles(ct.MessageBubbles)
	if text == "" {
		return nil
	}
	return &chatv2.TaskMessage{Parts: []map[string]any{{"kind": "text", "text": text}}}
}
