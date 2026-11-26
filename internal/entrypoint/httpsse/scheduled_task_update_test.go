// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBuildScheduledTaskUpdateFields_InvalidScheduleType(t *testing.T) {
	raw := map[string]json.RawMessage{
		"scheduleType": json.RawMessage(`"weird"`),
	}
	_, _, err := buildScheduledTaskUpdateFields(raw)
	if err == nil || err.code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestBuildScheduledTaskUpdateFields_InvalidTimezone(t *testing.T) {
	raw := map[string]json.RawMessage{
		"timezone": json.RawMessage(`"Fake/Zone"`),
	}
	_, _, err := buildScheduledTaskUpdateFields(raw)
	if err == nil || err.code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestBuildScheduledTaskUpdateFields_InvalidTargetType(t *testing.T) {
	raw := map[string]json.RawMessage{
		"targetType": json.RawMessage(`"frog"`),
	}
	_, _, err := buildScheduledTaskUpdateFields(raw)
	if err == nil || err.code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestBuildScheduledTaskUpdateFields_NonIntegerIntFieldIsRejectedNotDropped(t *testing.T) {
	for _, key := range []string{"maxRetries", "retryDelaySeconds", "timeoutSeconds"} {
		t.Run(key, func(t *testing.T) {
			raw := map[string]json.RawMessage{
				key: json.RawMessage(`"5"`),
			}
			_, _, err := buildScheduledTaskUpdateFields(raw)
			if err == nil || err.code != http.StatusBadRequest {
				t.Fatalf("err = %v, want 400", err)
			}
		})
	}
}

func TestBuildScheduledTaskUpdateFields_NotificationConfigCleared(t *testing.T) {
	raw := map[string]json.RawMessage{
		"notificationConfig": json.RawMessage(`null`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v, ok := fields["notification_config"]; !ok || v != nil {
		t.Errorf("notification_config = %v (ok=%v), want nil entry present", v, ok)
	}
}

func TestBuildScheduledTaskUpdateFields_ScheduleChangedFlag(t *testing.T) {
	raw := map[string]json.RawMessage{
		"scheduleExpression": json.RawMessage(`"0 9 * * *"`),
	}
	_, scheduleChanged, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !scheduleChanged {
		t.Error("scheduleChanged = false, want true when schedule_expression present")
	}
}

func TestBuildScheduledTaskUpdateFields_NameTrimmed(t *testing.T) {
	raw := map[string]json.RawMessage{
		"name": json.RawMessage(`"  Daily Report  "`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := fields["name"]; got != "Daily Report" {
		t.Errorf("name = %v, want \"Daily Report\"", got)
	}
}

func TestBuildScheduledTaskUpdateFields_DescriptionExplicitNullClears(t *testing.T) {
	raw := map[string]json.RawMessage{
		"description": json.RawMessage(`null`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	v, ok := fields["description"]
	if !ok || v != nil {
		t.Errorf("description = %v (ok=%v), want nil entry present", v, ok)
	}
}

func TestBuildScheduledTaskUpdateFields_DescriptionTrimmed(t *testing.T) {
	raw := map[string]json.RawMessage{
		"description": json.RawMessage(`"  has whitespace  "`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got, ok := fields["description"].(*string)
	if !ok || got == nil || *got != "has whitespace" {
		t.Errorf("description = %v, want \"has whitespace\"", got)
	}
}

func TestBuildScheduledTaskUpdateFields_TargetAgentNameSetWhenNonEmpty(t *testing.T) {
	raw := map[string]json.RawMessage{
		"targetAgentName": json.RawMessage(`"my-agent"`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := fields["target_agent_name"]; got != "my-agent" {
		t.Errorf("target_agent_name = %v, want my-agent", got)
	}
}

func TestBuildScheduledTaskUpdateFields_IntFields(t *testing.T) {
	raw := map[string]json.RawMessage{
		"maxRetries":        json.RawMessage(`3`),
		"retryDelaySeconds": json.RawMessage(`120`),
		"timeoutSeconds":    json.RawMessage(`600`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fields["max_retries"] != 3 {
		t.Errorf("max_retries = %v", fields["max_retries"])
	}
	if fields["retry_delay_seconds"] != 120 {
		t.Errorf("retry_delay_seconds = %v", fields["retry_delay_seconds"])
	}
	if fields["timeout_seconds"] != 600 {
		t.Errorf("timeout_seconds = %v", fields["timeout_seconds"])
	}
}

func TestBuildScheduledTaskUpdateFields_EnabledBoolToInt(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{`true`, 1}, {`false`, 0}} {
		raw := map[string]json.RawMessage{"enabled": json.RawMessage(tc.raw)}
		fields, _, err := buildScheduledTaskUpdateFields(raw)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got := fields["enabled"]; got != tc.want {
			t.Errorf("enabled[%s] = %v, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestBuildScheduledTaskUpdateFields_PassthroughFields(t *testing.T) {
	raw := map[string]json.RawMessage{
		"taskMessage":  json.RawMessage(`[{"type":"text","text":"hi"}]`),
		"taskMetadata": json.RawMessage(`{"k":"v"}`),
	}
	fields, _, err := buildScheduledTaskUpdateFields(raw)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got, ok := fields["task_message"].(string); !ok || got == "" {
		t.Errorf("task_message = %v", fields["task_message"])
	}
	if got, ok := fields["task_metadata"].(*string); !ok || got == nil || *got != `{"k":"v"}` {
		t.Errorf("task_metadata = %v", fields["task_metadata"])
	}
}

func TestBuildScheduledTaskUpdateFields_NotificationConfigInvalid(t *testing.T) {
	raw := map[string]json.RawMessage{
		"notificationConfig": json.RawMessage(`{"on_success": true, "channels": [{"type": "unknown", "config": {}}]}`),
	}
	_, _, err := buildScheduledTaskUpdateFields(raw)
	if err == nil || err.code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400 for invalid notification_config", err)
	}
}

func TestBadRequest(t *testing.T) {
	e := badRequest("nope")
	if e.code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", e.code)
	}
	if e.message != "nope" {
		t.Errorf("message = %q", e.message)
	}
}
