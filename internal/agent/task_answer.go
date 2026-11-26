// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func FinalResponse(taskID, contextID string, state a2a.TaskState, parts []a2a.Part, metadata map[string]any) *a2a.Response {
	return taskResponse(taskID, contextID, fmt.Sprintf("msg-%s-resp", taskID), state, parts, metadata)
}

func CancelledResponse(taskID, contextID string, metadata map[string]any) *a2a.Response {
	parts := []a2a.Part{a2a.NewTextPart("Task was cancelled.")}
	return taskResponse(taskID, contextID, fmt.Sprintf("msg-%s-cancelled", taskID), a2a.TaskStateCancelled, parts, metadata)
}

func taskResponse(taskID, contextID, messageID string, state a2a.TaskState, parts []a2a.Part, metadata map[string]any) *a2a.Response {
	message := a2a.Message{
		Kind:      "message",
		MessageID: messageID,
		Role:      a2a.RoleAgent,
		Parts:     parts,
	}
	task := a2a.Task{
		ID:        taskID,
		ContextID: contextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     state,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message:   &message,
		},
		Metadata: metadata,
	}
	return a2a.NewResponse(taskID, task)
}
