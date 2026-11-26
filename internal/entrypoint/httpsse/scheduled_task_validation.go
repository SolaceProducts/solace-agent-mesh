// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const msgTaskAlreadyExecuting = "Task is currently executing; wait for it to finish before running again."

func isActiveExecutionStatus(status string) bool {
	return status == "pending" || status == "running"
}

func isUnfinishedExecution(exec *store.ScheduledTaskExecution) bool {
	return isActiveExecutionStatus(exec.Status) || exec.FinalizedAt == nil
}

func limitError(limit entrypoint.TaskLimit, value int) *scheduledTaskFieldError {
	if err := limit.Check(value); err != nil {
		return badRequest(err.Error())
	}
	return nil
}

func validateTaskMessage(raw json.RawMessage) *scheduledTaskFieldError {
	if err := entrypoint.ValidateTaskMessage(raw); err != nil {
		return badRequest(err.Error())
	}
	return nil
}

func validateTaskMetadata(raw json.RawMessage) *scheduledTaskFieldError {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var meta map[string]any
	if json.Unmarshal(raw, &meta) != nil {
		return badRequest("taskMetadata must be an object or null")
	}
	return nil
}

func dropUnchangedLimits(raw map[string]json.RawMessage, task *store.ScheduledTask) {
	stored := map[string]int{
		entrypoint.MaxRetriesLimit.Field:        task.MaxRetries,
		entrypoint.RetryDelaySecondsLimit.Field: task.RetryDelaySeconds,
		entrypoint.TimeoutSecondsLimit.Field:    task.TimeoutSeconds,
	}
	for key, current := range stored {
		r, ok := raw[key]
		if !ok || bytes.Equal(bytes.TrimSpace(r), []byte("null")) {
			continue
		}
		var v int
		if json.Unmarshal(r, &v) == nil && v == current {
			delete(raw, key)
		}
	}
}
