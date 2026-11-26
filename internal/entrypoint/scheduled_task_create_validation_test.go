// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestCreateScheduledTask_SharesTheHTTPInputRules(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CreateScheduledTaskParams)
	}{
		{"blank text message", func(p *CreateScheduledTaskParams) { p.TaskMessage = `[{"kind":"text","text":"   "}]` }},
		{"whitespace-only name", func(p *CreateScheduledTaskParams) { p.Name = "  \t " }},
		{"empty message list", func(p *CreateScheduledTaskParams) { p.TaskMessage = `[]` }},
		{"source config", func(p *CreateScheduledTaskParams) { p.Source = "config" }},
		{"source arbitrary", func(p *CreateScheduledTaskParams) { p.Source = "anything" }},
		{"retries above the limit", func(p *CreateScheduledTaskParams) { p.MaxRetries = 11 }},
		{"negative retries", func(p *CreateScheduledTaskParams) { p.MaxRetries = -1 }},
		{"retry delay above the limit", func(p *CreateScheduledTaskParams) { p.RetryDelaySeconds = 86401 }},
		{"timeout above the limit", func(p *CreateScheduledTaskParams) { p.TimeoutSeconds = 604801 }},
		{"negative timeout", func(p *CreateScheduledTaskParams) { p.TimeoutSeconds = -1 }},
		{"negative retry delay", func(p *CreateScheduledTaskParams) { p.RetryDelaySeconds = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newSchedulerSvcForTest(t, 0)
			p := workflowCreateParams()
			tt.edit(&p)

			_, err := svc.CreateScheduledTask(t.Context(), p)

			var ce *CreateError
			if !errors.As(err, &ce) || ce.Kind != CreateErrorValidation {
				t.Fatalf("err = %v, want a validation error", err)
			}
		})
	}
}

func TestCreateScheduledTask_StoresTheTrimmedName(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	p := workflowCreateParams()
	p.Name = "  nightly-reconcile  "

	task, err := svc.CreateScheduledTask(t.Context(), p)

	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.Name != "nightly-reconcile" {
		t.Fatalf("stored name = %q, want %q", task.Name, "nightly-reconcile")
	}
}

func TestCreateScheduledTask_AcceptsTheEdgesAndTheServiceSources(t *testing.T) {
	for _, source := range []string{"", "ui", "chat", "workflow"} {
		t.Run("source "+source, func(t *testing.T) {
			svc := newSchedulerSvcForTest(t, 0)
			p := workflowCreateParams()
			p.Source = source
			p.MaxRetries = 10
			p.RetryDelaySeconds = 86400
			p.TimeoutSeconds = 604800

			if _, err := svc.CreateScheduledTask(t.Context(), p); err != nil {
				t.Fatalf("CreateScheduledTask: %v", err)
			}
		})
	}
}

func TestHandleScheduleRequest_RejectsABlankMessageAndOversizedTimeout(t *testing.T) {
	tests := []struct {
		name string
		edit func(*a2a.ScheduleRequest)
	}{
		{"whitespace-only message", func(r *a2a.ScheduleRequest) { r.TaskMessage = "   \n " }},
		{"timeout above the limit", func(r *a2a.ScheduleRequest) { r.TimeoutSeconds = 2592000 }},
		{"source config", func(r *a2a.ScheduleRequest) { r.Source = "config" }},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newSchedulerSvcForTest(t, 0)
			req := a2a.ScheduleRequest{
				RequestID:          "00000000-0000-7000-8000-0000000000b" + string(rune('0'+i)),
				Name:               "from-agent",
				ScheduleType:       "one_time",
				ScheduleExpression: "2099-01-01T00:00:00Z",
				TargetType:         "workflow",
				TargetName:         "reconcile",
				TaskMessage:        "do the thing",
			}
			tt.edit(&req)
			payload, _ := json.Marshal(req)
			acked := false
			msg := broker.NewDeliveredMessage("m", "t", payload, func() { acked = true }, func() { t.Error("a rejected request must not be nacked") })
			msg.UserProperties = map[string]string{a2a.PropUserID: "bob@example.com"}

			svc.handleScheduleRequest(t.Context(), msg)

			if !acked {
				t.Error("a rejected request must be acked so it is not redelivered")
			}
			if got, _ := svc.repo.GetByID(t.Context(), req.RequestID); got != nil {
				t.Fatalf("a task was created from an invalid request: %+v", got)
			}
		})
	}
}
