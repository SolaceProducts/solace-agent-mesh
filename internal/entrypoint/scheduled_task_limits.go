// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
)

const (
	MaxTaskRetries       = scheduler.MaxTaskRetries
	MaxRetryDelaySeconds = scheduler.MaxRetryDelaySeconds
	MinTimeoutSeconds    = scheduler.MinTimeoutSeconds
	MaxTimeoutSeconds    = scheduler.MaxTimeoutSeconds
)

type TaskLimit struct {
	Field    string
	Min, Max int
}

var (
	MaxRetriesLimit        = TaskLimit{Field: "maxRetries", Min: 0, Max: MaxTaskRetries}
	RetryDelaySecondsLimit = TaskLimit{Field: "retryDelaySeconds", Min: 0, Max: MaxRetryDelaySeconds}
	TimeoutSecondsLimit    = TaskLimit{Field: "timeoutSeconds", Min: MinTimeoutSeconds, Max: MaxTimeoutSeconds}
)

func (l TaskLimit) Check(v int) error {
	if v < l.Min || v > l.Max {
		return fmt.Errorf("%s must be between %d and %d", l.Field, l.Min, l.Max)
	}
	return nil
}

var errInvalidTaskMessage = errors.New("taskMessage must be a non-empty string or a list containing at least one non-empty text or data part")

func ValidateTaskMessage(raw json.RawMessage) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return errInvalidTaskMessage
		}
		return nil
	}

	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return errInvalidTaskMessage
	}
	for _, part := range parts {
		if partCarriesInput(part) {
			return nil
		}
	}
	return errInvalidTaskMessage
}

func partCarriesInput(part map[string]any) bool {
	kind, _ := part["kind"].(string)
	if kind == "" {
		kind, _ = part["type"].(string)
	}
	switch kind {
	case "text", "":
		text, _ := part["text"].(string)
		return strings.TrimSpace(text) != ""
	case "data":
		data, ok := part["data"]
		return ok && data != nil
	default:
		return false
	}
}

const (
	TaskSourceUI       = "ui"
	TaskSourceChat     = "chat"
	taskSourceWorkflow = "workflow"
)

func IsAPISettableTaskSource(source string) bool {
	return source == TaskSourceUI || source == TaskSourceChat
}

func isServiceSettableTaskSource(source string) bool {
	return IsAPISettableTaskSource(source) || source == taskSourceWorkflow
}

func validateServiceLimits(p CreateScheduledTaskParams) error {
	if err := MaxRetriesLimit.Check(p.MaxRetries); err != nil {
		return err
	}
	if p.RetryDelaySeconds != 0 {
		if err := RetryDelaySecondsLimit.Check(p.RetryDelaySeconds); err != nil {
			return err
		}
	}
	if p.TimeoutSeconds != 0 {
		if err := TimeoutSecondsLimit.Check(p.TimeoutSeconds); err != nil {
			return err
		}
	}
	return nil
}
