// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateTaskReader apicontract.Gate = "taskReader"

const GateTaskTrace apicontract.Gate = "taskTrace"

const GateTaskOwner apicontract.Gate = "taskOwner"

const taskNotFoundMessage = "task not found"

var tasksTag = apicontract.Tag{Name: "Chat Tasks", Group: tagGroup, Text: "Read the result of a chat message, follow it as it happens, answer the questions it asks and stop it while it runs."}

const (
	TaskStateSubmitted    = string(a2a.TaskStateSubmitted)
	TaskStateWorking      = string(a2a.TaskStateWorking)
	TaskStateCompleted    = string(a2a.TaskStateCompleted)
	TaskStateFailed       = string(a2a.TaskStateFailed)
	TaskStateCanceled     = string(a2a.TaskStateCancelled)
	TaskStateInputNeeded  = string(a2a.TaskStateInputNeeded)
	TaskStateAuthRequired = string(a2a.TaskStateAuthRequired)
	TaskStateRejected     = string(a2a.TaskStateRejected)
	TaskStateUnknown      = string(a2a.TaskStateUnknown)
)

type Task struct {
	ID            string              `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type          string              `json:"type" example:"task"`
	ContextID     *string             `json:"contextId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Status        TaskStatus          `json:"status"`
	Artifacts     []TaskArtifact      `json:"artifacts"`
	ErrorMessage  *string             `json:"errorMessage" example:"The agent could not reach the model."`
	CreatedTime   apicontract.Instant `json:"createdTime"`
	CompletedTime apicontract.Instant `json:"completedTime"`
}

type TaskStatus struct {
	State   string       `json:"state" example:"completed"`
	Message *TaskMessage `json:"message"`
}

type TaskMessage struct {
	Parts []map[string]any `json:"parts" example:"[{\"kind\":\"text\",\"text\":\"The report is ready.\"}]"`
}

type TaskArtifact struct {
	ArtifactID string           `json:"artifactId" example:"report-2026-11"`
	Name       string           `json:"name" example:"report.pdf"`
	Parts      []map[string]any `json:"parts" example:"[{\"kind\":\"text\",\"text\":\"The report is ready.\"}]"`
}

func TaskNotFound() error {
	return &apicontract.CallerError{Status: http.StatusNotFound, Message: taskNotFoundMessage}
}

const ExportTaskFormat = "application/yaml"

type taskHandler struct {
	deps Deps
}

func (h *taskHandler) get(ctx context.Context, req apicontract.IDRequest) (Task, error) {
	if h.deps.ReadTask == nil {
		return Task{}, unavailable(tasksUnavailableMessage)
	}
	return h.deps.ReadTask(ctx, req.ID)
}

func (h *taskHandler) export(ctx context.Context, req apicontract.IDRequest) (apicontract.Download, error) {
	if h.deps.ExportTask == nil {
		return apicontract.Download{}, unavailable(tasksUnavailableMessage)
	}
	return h.deps.ExportTask(ctx, req.ID)
}

func (h *taskHandler) cancel(ctx context.Context, req apicontract.IDRequest) (Task, error) {
	if h.deps.CancelTask == nil {
		return Task{}, unavailable(tasksUnavailableMessage)
	}
	return h.deps.CancelTask(ctx, req.ID)
}

const tasksUnavailableMessage = "Chat tasks are not available on this gateway."

func unavailable(message string) error {
	return &apicontract.CallerError{Status: http.StatusServiceUnavailable, Message: message}
}

func TaskRoutes(deps Deps) []apicontract.Route {
	h := &taskHandler{deps: deps}
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/tasks/{id}", Gate: GateTaskReader,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatTask", Summary: "Get a task",
				Text: "Returns the task a message started, with its state and, once it has ended, the agent's closing message and artifacts. " +
					"To receive the answer as one result, send the message, then read this task every second or two until status.state is completed, failed, canceled or rejected. " +
					"A task that is input-required or auth-required is waiting for the user. " +
					"Reading a task is safe to repeat. A task that belongs to another user answers 404, the same as one that does not exist.",
				Tag: tasksTag},
			Handler: apicontract.Handle(h.get, "Unable to get task")},
		{Method: http.MethodGet, Path: "/tasks/{id}/export", Gate: GateTaskTrace,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "exportChatTask", Summary: "Get an export of a task",
				Text: "Downloads the task and every task it started or was started by as a STIM file: the task's full event trace, as YAML. " +
					"This is the file the Activity Monitor offers for download. Use Get a task to read the answer. A task that belongs to another user answers 404, the same as one that does not exist.",
				FileTypes: []string{ExportTaskFormat},
				Tag:       tasksTag},
			Handler: apicontract.Handle(h.export, "Unable to export task")},
		{Method: http.MethodPut, Path: "/tasks/{id}/cancel", Gate: GateTaskOwner,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "cancelChatTask", Summary: "Update a task to cancel it",
				Text: "Asks the agent to stop the task and every task it started, and returns the task as it stands. " +
					"The agent stops on its own time, so the state is still working in the response and becomes canceled once the agent confirms. Read the task to see it. " +
					"Cancelling a task that has ended returns it as it is and still stops any task it started that is running. Only the user who started the task can cancel it. A task that belongs to another user answers 404, the same as one that does not exist.",
				Tag: tasksTag},
			Handler: apicontract.Handle(h.cancel, "Unable to cancel task")},
	}
}

func TaskStateOf(stored *string) string {
	if stored == nil || *stored == "" {
		return TaskStateWorking
	}
	if a2a.IsFailureStatus(*stored) {
		return TaskStateFailed
	}
	switch *stored {
	case "cancelled":
		return TaskStateCanceled
	case TaskStateSubmitted, TaskStateWorking, TaskStateCompleted, TaskStateFailed, TaskStateCanceled,
		TaskStateInputNeeded, TaskStateAuthRequired, TaskStateRejected:
		return *stored
	default:
		return TaskStateUnknown
	}
}

func IsFinalTaskState(state string) bool {
	switch state {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected:
		return true
	default:
		return false
	}
}
