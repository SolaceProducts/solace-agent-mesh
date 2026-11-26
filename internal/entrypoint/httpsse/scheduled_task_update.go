// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type scheduledTaskFieldError struct {
	code    int
	message string
}

func badRequest(msg string) *scheduledTaskFieldError {
	return &scheduledTaskFieldError{code: http.StatusBadRequest, message: msg}
}

type stringUpdateField struct {
	key             string
	column          string
	changesSchedule bool
	normalize       func(value string) (string, *scheduledTaskFieldError)
}

func oneOf(message string, allowed ...string) func(string) (string, *scheduledTaskFieldError) {
	return func(value string) (string, *scheduledTaskFieldError) {
		if !slices.Contains(allowed, value) {
			return "", badRequest(message)
		}
		return value, nil
	}
}

func normalizeTimezoneField(value string) (string, *scheduledTaskFieldError) {
	tz, ok := entrypoint.NormalizeUserTimezone(value)
	if !ok {
		return "", badRequest("invalid timezone: " + value)
	}
	return tz, nil
}

var stringUpdateFields = []stringUpdateField{
	{key: "name", column: "name"},
	{key: "scheduleType", column: "schedule_type", changesSchedule: true,
		normalize: oneOf("scheduleType must be one of: cron, interval, one_time", "cron", "interval", "one_time")},
	{key: "scheduleExpression", column: "schedule_expression", changesSchedule: true},
	{key: "timezone", column: "timezone", changesSchedule: true, normalize: normalizeTimezoneField},
	{key: "targetAgentName", column: "target_agent_name"},
	{key: "targetType", column: "target_type", normalize: oneOf("targetType must be agent or workflow", "agent", "workflow")},
}

var updatableTaskFields = []string{
	"description", "taskMessage", "taskMetadata", "maxRetries", "retryDelaySeconds",
	"timeoutSeconds", "enabled", "notificationConfig", runAsKey,
}

func buildScheduledTaskUpdateFields(rawFields map[string]json.RawMessage) (map[string]any, bool, *scheduledTaskFieldError) {
	if err := rejectUnknownUpdateFields(rawFields); err != nil {
		return nil, false, err
	}

	fields := make(map[string]any)
	scheduleChanged, err := applyStringFields(rawFields, fields)
	if err != nil {
		return nil, false, err
	}
	for _, apply := range []func(map[string]json.RawMessage, map[string]any) *scheduledTaskFieldError{
		applyDescriptionField,
		applyPassthroughFields,
		applyIntFields,
		applyEnabledField,
		applyNotificationConfigField,
	} {
		if err := apply(rawFields, fields); err != nil {
			return nil, false, err
		}
	}
	return fields, scheduleChanged, nil
}

func rejectUnknownUpdateFields(raw map[string]json.RawMessage) *scheduledTaskFieldError {
	var unknown []string
	for key := range raw {
		known := slices.Contains(updatableTaskFields, key) ||
			slices.ContainsFunc(stringUpdateFields, func(f stringUpdateField) bool { return f.key == key })
		if !known {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return badRequest(fmt.Sprintf("unknown or read-only fields: %s", strings.Join(unknown, ", ")))
}

func applyStringFields(raw map[string]json.RawMessage, fields map[string]any) (bool, *scheduledTaskFieldError) {
	scheduleChanged := false
	for _, f := range stringUpdateFields {
		r, ok := raw[f.key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(r, &value); err != nil {
			return false, badRequest(fmt.Sprintf("%s must be a string", f.key))
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return false, badRequest(fmt.Sprintf("%s must not be empty", f.key))
		}
		if f.normalize != nil {
			normalized, err := f.normalize(value)
			if err != nil {
				return false, err
			}
			value = normalized
		}
		fields[f.column] = value
		scheduleChanged = scheduleChanged || f.changesSchedule
	}
	return scheduleChanged, nil
}

func applyDescriptionField(raw map[string]json.RawMessage, fields map[string]any) *scheduledTaskFieldError {
	r, ok := raw["description"]
	if !ok {
		return nil
	}
	var v *string
	if json.Unmarshal(r, &v) != nil {
		return badRequest("description must be a string or null")
	}
	if v == nil {
		fields["description"] = nil
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	fields["description"] = &trimmed
	return nil
}

func applyPassthroughFields(raw map[string]json.RawMessage, fields map[string]any) *scheduledTaskFieldError {
	if r, ok := raw["taskMessage"]; ok {
		if err := validateTaskMessage(r); err != nil {
			return err
		}
		fields["task_message"] = string(r)
	}
	if r, ok := raw["taskMetadata"]; ok {
		if err := validateTaskMetadata(r); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(r), []byte("null")) {
			fields["task_metadata"] = nil
			return nil
		}
		s := string(r)
		fields["task_metadata"] = &s
	}
	return nil
}

func applyIntFields(raw map[string]json.RawMessage, fields map[string]any) *scheduledTaskFieldError {
	intFields := []struct {
		limit  entrypoint.TaskLimit
		column string
	}{
		{entrypoint.MaxRetriesLimit, "max_retries"},
		{entrypoint.RetryDelaySecondsLimit, "retry_delay_seconds"},
		{entrypoint.TimeoutSecondsLimit, "timeout_seconds"},
	}
	for _, f := range intFields {
		r, ok := raw[f.limit.Field]
		if !ok {
			continue
		}
		var v *int
		if err := json.Unmarshal(r, &v); err != nil || v == nil {
			return badRequest(fmt.Sprintf("%s must be an integer", f.limit.Field))
		}
		if err := limitError(f.limit, *v); err != nil {
			return err
		}
		fields[f.column] = *v
	}
	return nil
}

func applyEnabledField(raw map[string]json.RawMessage, fields map[string]any) *scheduledTaskFieldError {
	r, ok := raw["enabled"]
	if !ok {
		return nil
	}
	var v *bool
	if err := json.Unmarshal(r, &v); err != nil || v == nil {
		return badRequest("enabled must be a boolean")
	}
	fields["enabled"] = dbutil.BoolToInt(*v)
	return nil
}

func applyNotificationConfigField(raw map[string]json.RawMessage, fields map[string]any) *scheduledTaskFieldError {
	r, ok := raw["notificationConfig"]
	if !ok {
		return nil
	}
	ncStr := string(r)
	if ncStr == "null" {
		fields["notification_config"] = nil
		return nil
	}
	if err := entrypoint.ValidateNotificationConfig(ncStr); err != nil {
		return badRequest("invalid notificationConfig: " + err.Error())
	}
	fields["notification_config"] = &ncStr
	return nil
}
