// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestScheduleTaskTool_BuildsRequest(t *testing.T) {
	var captured a2a.ScheduleRequest
	tc := &tool.Context{
		UserID:       "alice@example.com",
		AgentName:    "reconcile",
		ParentTaskID: "task-1",
		TraceID:      "tr-1",
		ScheduleTask: func(_ context.Context, req a2a.ScheduleRequest) error {
			captured = req
			return nil
		},
	}
	args := map[string]any{
		"target_name":     "reconcile",
		"target_type":     "workflow",
		"schedule_type":   "one_time",
		"schedule":        "2099-01-01T00:00:00Z",
		"message":         "run the reconciliation",
		"timeout_seconds": float64(120),
	}

	res, err := (&ScheduleTaskTool{}).Execute(context.Background(), args, tc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("expected success, got %s: %s", res.Status, res.Message)
	}
	if captured.TargetName != "reconcile" || captured.TargetType != "workflow" {
		t.Errorf("target mismatch: %+v", captured)
	}
	if captured.ScheduleType != "one_time" || captured.ScheduleExpression != "2099-01-01T00:00:00Z" {
		t.Errorf("schedule mismatch: %+v", captured)
	}
	if captured.TaskMessage != "run the reconciliation" {
		t.Errorf("message mismatch: %q", captured.TaskMessage)
	}
	if captured.SenderUserID != "alice@example.com" || captured.SenderAgent != "reconcile" {
		t.Errorf("sender mismatch: %+v", captured)
	}
	if captured.RequestID == "" || captured.Name == "" {
		t.Errorf("request id/name should be set: %+v", captured)
	}
	if captured.TimeoutSeconds != 120 {
		t.Errorf("timeout = %d, want 120", captured.TimeoutSeconds)
	}
	if got, _ := res.Data["scheduled_task_id"].(string); got != captured.RequestID {
		t.Errorf("result scheduled_task_id %q != request id %q", got, captured.RequestID)
	}
}

func TestScheduleTaskTool_DefaultName(t *testing.T) {
	var captured a2a.ScheduleRequest
	tc := &tool.Context{
		UserID:       "u",
		ScheduleTask: func(_ context.Context, req a2a.ScheduleRequest) error { captured = req; return nil },
	}
	_, _ = (&ScheduleTaskTool{}).Execute(context.Background(), map[string]any{
		"target_name": "a", "schedule_type": "interval", "schedule": "15m", "message": "ping",
	}, tc)
	if captured.Name == "" {
		t.Error("a name should be generated when omitted")
	}
}

func TestScheduleTaskTool_Validation(t *testing.T) {
	tc := &tool.Context{ScheduleTask: func(context.Context, a2a.ScheduleRequest) error { return nil }}
	cases := []map[string]any{
		{"schedule_type": "cron", "schedule": "* * * * *", "message": "m"},
		{"target_name": "a", "schedule": "* * * * *", "message": "m"},
		{"target_name": "a", "schedule_type": "cron", "message": "m"},
		{"target_name": "a", "schedule_type": "cron", "schedule": "* * * * *"},
	}
	for i, args := range cases {
		res, err := (&ScheduleTaskTool{}).Execute(context.Background(), args, tc)
		if err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
		if res.Status != tool.StatusError {
			t.Errorf("case %d: expected validation error, got status %s", i, res.Status)
		}
	}
}

func TestScheduleTaskTool_Metadata(t *testing.T) {
	tl := &ScheduleTaskTool{}
	if tl.Name() != "schedule_task" {
		t.Errorf("Name() = %q", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description() is empty")
	}
	schema := tl.Parameters()
	if schema == nil || schema.Type != "object" {
		t.Fatalf("Parameters() = %+v", schema)
	}
	for _, req := range []string{"target_name", "schedule_type", "schedule", "message"} {
		if _, ok := schema.Properties[req]; !ok {
			t.Errorf("Parameters() missing property %q", req)
		}
	}
	want := []string{"target_name", "schedule_type", "schedule", "message"}
	if len(schema.Required) != len(want) {
		t.Errorf("Required = %v, want %v", schema.Required, want)
	}
}

func TestScheduleTaskTool_PublishError(t *testing.T) {
	tc := &tool.Context{
		UserID:       "u",
		ScheduleTask: func(context.Context, a2a.ScheduleRequest) error { return errors.New("broker down") },
	}
	res, err := (&ScheduleTaskTool{}).Execute(context.Background(), map[string]any{
		"target_name": "a", "schedule_type": "cron", "schedule": "* * * * *", "message": "m",
	}, tc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("expected error status when publish fails, got %s", res.Status)
	}
}

func TestScheduleTaskTool_TimeoutCoercion(t *testing.T) {
	cases := map[string]struct {
		val  any
		want int
	}{
		"float64":     {float64(90), 90},
		"int":         {300, 300},
		"int64":       {int64(45), 45},
		"json.Number": {json.Number("120"), 120},
		"unset":       {nil, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var captured a2a.ScheduleRequest
			toolCtx := &tool.Context{
				UserID:       "u",
				ScheduleTask: func(_ context.Context, req a2a.ScheduleRequest) error { captured = req; return nil },
			}
			args := map[string]any{
				"target_name": "a", "schedule_type": "interval", "schedule": "15m", "message": "m",
			}
			if tc.val != nil {
				args["timeout_seconds"] = tc.val
			}
			if _, err := (&ScheduleTaskTool{}).Execute(context.Background(), args, toolCtx); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if captured.TimeoutSeconds != tc.want {
				t.Errorf("TimeoutSeconds = %d, want %d", captured.TimeoutSeconds, tc.want)
			}
		})
	}
}

func TestScheduleTaskTool_BadTimeout(t *testing.T) {
	tc := &tool.Context{
		UserID:       "u",
		ScheduleTask: func(context.Context, a2a.ScheduleRequest) error { return nil },
	}
	res, err := (&ScheduleTaskTool{}).Execute(context.Background(), map[string]any{
		"target_name": "a", "schedule_type": "interval", "schedule": "15m", "message": "m",
		"timeout_seconds": "ten minutes",
	}, tc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("expected error for unparseable timeout_seconds, got %s", res.Status)
	}
}

func TestScheduleTaskTool_Unavailable(t *testing.T) {
	tc := &tool.Context{UserID: "u"}
	res, err := (&ScheduleTaskTool{}).Execute(context.Background(), map[string]any{
		"target_name": "a", "schedule_type": "cron", "schedule": "* * * * *", "message": "m",
	}, tc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("expected error when scheduling unavailable, got %s", res.Status)
	}
}
