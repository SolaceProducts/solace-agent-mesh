// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func workflowCreateParams() CreateScheduledTaskParams {
	return CreateScheduledTaskParams{
		Name:               "nightly-reconcile",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        `[{"kind":"text","text":"run reconciliation"}]`,
		Source:             "workflow",
		CreatedBy:          "alice@example.com",
	}
}

func TestCreateScheduledTask_WorkflowTarget(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	task, err := svc.CreateScheduledTask(ctx, workflowCreateParams())
	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.ID == "" || task.TargetType != "workflow" || task.TargetAgentName != "reconcile" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.UserID == nil || *task.UserID != "alice@example.com" || task.CreatedBy != "alice@example.com" {
		t.Errorf("owner/creator mismatch: %+v", task)
	}
	got, err := svc.repo.GetByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByID: row=%v err=%v", got, err)
	}
	if got.TimeoutSeconds != 3600 || got.RetryDelaySeconds != 60 {
		t.Errorf("defaults not applied: %+v", got)
	}
}

func TestCreateScheduledTask_Validation(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	base := workflowCreateParams()
	mut := func(f func(*CreateScheduledTaskParams)) CreateScheduledTaskParams {
		p := base
		f(&p)
		return p
	}
	cases := []struct {
		name string
		p    CreateScheduledTaskParams
	}{
		{"no name", mut(func(p *CreateScheduledTaskParams) { p.Name = "" })},
		{"no scheduleType", mut(func(p *CreateScheduledTaskParams) { p.ScheduleType = "" })},
		{"bad scheduleType", mut(func(p *CreateScheduledTaskParams) { p.ScheduleType = "weekly" })},
		{"no target", mut(func(p *CreateScheduledTaskParams) { p.TargetName = "" })},
		{"no message", mut(func(p *CreateScheduledTaskParams) { p.TaskMessage = "" })},
		{"bad targetType", mut(func(p *CreateScheduledTaskParams) { p.TargetType = "robot" })},
		{"bad cron", mut(func(p *CreateScheduledTaskParams) {
			p.ScheduleType = "cron"
			p.ScheduleExpression = "not a cron"
		})},
		{"no createdBy", mut(func(p *CreateScheduledTaskParams) { p.CreatedBy = "" })},
		{"no scheduleExpression", mut(func(p *CreateScheduledTaskParams) { p.ScheduleExpression = "" })},
		{"bad timezone", mut(func(p *CreateScheduledTaskParams) { p.Timezone = "Mars/Phobos" })},
		{"Local timezone", mut(func(p *CreateScheduledTaskParams) { p.Timezone = "Local" })},
		{"bad notificationConfig", mut(func(p *CreateScheduledTaskParams) {
			bad := "{not json"
			p.NotificationConfig = &bad
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateScheduledTask(ctx, tc.p)
			var ce *CreateError
			if !errors.As(err, &ce) || ce.Kind != CreateErrorValidation {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestCreateScheduledTask_IdempotentConflict(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	p := workflowCreateParams()
	p.ID = "00000000-0000-7000-8000-000000000001"

	if _, err := svc.CreateScheduledTask(ctx, p); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateScheduledTask(ctx, p)
	var ce *CreateError
	if !errors.As(err, &ce) || ce.Kind != CreateErrorConflict {
		t.Fatalf("re-creating the same id must conflict, got %v", err)
	}
}

func TestHandleScheduleRequest_CreatesTask(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	req := a2a.ScheduleRequest{
		RequestID:          "00000000-0000-7000-8000-0000000000aa",
		Name:               "from-workflow",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        "kick off reconciliation",
		SenderUserID:       "payload-claimed-user",
	}
	payload, _ := json.Marshal(req)
	msg := &broker.Message{
		Payload:        payload,
		UserProperties: map[string]string{a2a.PropUserID: "bob@example.com"},
	}

	svc.handleScheduleRequest(ctx, msg)

	got, err := svc.repo.GetByID(ctx, req.RequestID)
	if err != nil || got == nil {
		t.Fatalf("task should have been created: row=%v err=%v", got, err)
	}
	if got.CreatedBy != "bob@example.com" {
		t.Errorf("task must be created under the stamped principal, got createdBy=%q", got.CreatedBy)
	}
	if got.TargetType != "workflow" || got.TargetAgentName != "reconcile" || got.Source != "workflow" {
		t.Errorf("unexpected task: %+v", got)
	}
	var parts []map[string]any
	if err := json.Unmarshal([]byte(got.TaskMessage), &parts); err != nil || len(parts) != 1 {
		t.Errorf("task message should be a JSON parts array, got %q", got.TaskMessage)
	}
}

func TestCreateScheduledTask_NameConflict(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	first := workflowCreateParams()
	first.ID = "00000000-0000-7000-8000-000000000010"
	if _, err := svc.CreateScheduledTask(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := workflowCreateParams()
	second.ID = "00000000-0000-7000-8000-000000000011"
	_, err := svc.CreateScheduledTask(ctx, second)
	var ce *CreateError
	if !errors.As(err, &ce) || ce.Kind != CreateErrorNameConflict {
		t.Fatalf("reusing a name with a new id must be a name conflict, got %v", err)
	}
}

func TestCreateScheduledTask_NameReusableAcrossOwners(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	first := workflowCreateParams()
	first.ID = "00000000-0000-7000-8000-000000000020"
	if _, err := svc.CreateScheduledTask(ctx, first); err != nil {
		t.Fatalf("first owner create: %v", err)
	}

	second := workflowCreateParams()
	second.ID = "00000000-0000-7000-8000-000000000021"
	second.CreatedBy = "bob@example.com"
	if _, err := svc.CreateScheduledTask(ctx, second); err != nil {
		t.Fatalf("a second owner must be able to reuse the name: %v", err)
	}
}

func TestHandleScheduleRequest_RequiresStampedPrincipal(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	req := a2a.ScheduleRequest{
		RequestID:          "00000000-0000-7000-8000-0000000000ee",
		Name:               "no-principal",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        "go",
		SenderUserID:       "payload-claimed-user",
	}
	payload, _ := json.Marshal(req)
	svc.handleScheduleRequest(ctx, &broker.Message{Payload: payload})

	got, _ := svc.repo.GetByID(ctx, req.RequestID)
	if got != nil {
		t.Fatalf("a principal-less request must not create a task, got %+v", got)
	}
}

func TestCreateScheduledTask_AgentTargetValidation(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	p := workflowCreateParams()
	p.TargetType = "agent"
	p.TargetName = "ghost-agent"
	_, err := svc.CreateScheduledTask(ctx, p)
	var ce *CreateError
	if !errors.As(err, &ce) || ce.Kind != CreateErrorValidation {
		t.Fatalf("unknown agent target must be rejected, got %v", err)
	}

	svc.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "ghost-agent"})
	if _, err := svc.CreateScheduledTask(ctx, p); err != nil {
		t.Fatalf("registered agent target should create: %v", err)
	}
}

func TestCreateScheduledTask_AcceptsDisplayNameTarget(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	const slug = "agent_019ea471_5051_73c7_893a_7679460fd074"
	svc.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: slug,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "Orchestrator"},
			}},
		},
	})

	p := workflowCreateParams()
	p.TargetType = "agent"
	p.TargetName = "Orchestrator"
	task, err := svc.CreateScheduledTask(ctx, p)
	if err != nil {
		t.Fatalf("display-name target should be accepted (gateways route it), got %v", err)
	}
	if task.TargetAgentName != "Orchestrator" {
		t.Errorf("stored target = %q, want the friendly name preserved for re-resolution on fire", task.TargetAgentName)
	}
}

func TestHandleScheduleRequest_RepliesAcceptAndReject(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	brk := svc.core.svc.Broker()

	do := func(t *testing.T, req a2a.ScheduleRequest) a2a.ScheduleResponse {
		t.Helper()
		q, err := brk.CreateTemporaryQueue()
		if err != nil {
			t.Fatalf("reply queue: %v", err)
		}
		if subErr := brk.Subscribe(q, req.ReplyTo); subErr != nil {
			t.Fatalf("subscribe reply: %v", subErr)
		}
		payload, _ := json.Marshal(req)
		svc.handleScheduleRequest(ctx, &broker.Message{
			Payload:        payload,
			UserProperties: map[string]string{a2a.PropUserID: "bob@example.com"},
		})
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		msg, err := brk.Receive(rctx, q)
		if err != nil || msg == nil {
			t.Fatalf("no ScheduleResponse published to ReplyTo: err=%v", err)
		}
		msg.Ack()
		var resp a2a.ScheduleResponse
		if err := json.Unmarshal(msg.Payload, &resp); err != nil {
			t.Fatalf("reply not a ScheduleResponse: %v", err)
		}
		return resp
	}

	t.Run("accept", func(t *testing.T) {
		const id = "00000000-0000-7000-8000-0000000000b1"
		resp := do(t, a2a.ScheduleRequest{
			RequestID: id, Name: "reply-ok", ScheduleType: "one_time",
			ScheduleExpression: "2099-01-01T00:00:00Z", TargetType: "workflow",
			TargetName: "reconcile", TaskMessage: "go",
			ReplyTo: a2a.ScheduleReplyTopic("test-ns", id),
		})
		if !resp.OK || resp.TaskID != id {
			t.Errorf("expected OK reply carrying the task id, got %+v", resp)
		}
	})

	t.Run("reject", func(t *testing.T) {
		const id = "00000000-0000-7000-8000-0000000000b2"
		resp := do(t, a2a.ScheduleRequest{
			RequestID: id, Name: "reply-bad", ScheduleType: "cron",
			ScheduleExpression: "not-a-cron", TargetType: "workflow",
			TargetName: "reconcile", TaskMessage: "go",
			ReplyTo: a2a.ScheduleReplyTopic("test-ns", id),
		})
		if resp.OK || resp.Error == "" || resp.ErrorKind != "validation" {
			t.Errorf("expected a validation rejection reply with a reason, got %+v", resp)
		}
	})

	t.Run("name conflict", func(t *testing.T) {
		const first = "00000000-0000-7000-8000-0000000000b3"
		if resp := do(t, a2a.ScheduleRequest{
			RequestID: first, Name: "dupe", ScheduleType: "one_time",
			ScheduleExpression: "2099-01-01T00:00:00Z", TargetType: "workflow",
			TargetName: "reconcile", TaskMessage: "go",
			ReplyTo: a2a.ScheduleReplyTopic("test-ns", first),
		}); !resp.OK {
			t.Fatalf("seeding the first task should succeed, got %+v", resp)
		}
		const second = "00000000-0000-7000-8000-0000000000b4"
		resp := do(t, a2a.ScheduleRequest{
			RequestID: second, Name: "dupe", ScheduleType: "one_time",
			ScheduleExpression: "2099-01-01T00:00:00Z", TargetType: "workflow",
			TargetName: "reconcile", TaskMessage: "go",
			ReplyTo: a2a.ScheduleReplyTopic("test-ns", second),
		})
		if resp.OK || resp.ErrorKind != "name_conflict" {
			t.Errorf("expected a name_conflict rejection, got %+v", resp)
		}
	})
}

func TestHandleScheduleRequest_RepliesOnMissingPrincipal(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()
	brk := svc.core.svc.Broker()
	const id = "00000000-0000-7000-8000-0000000000c9"
	replyTopic := a2a.ScheduleReplyTopic("test-ns", id)

	q, err := brk.CreateTemporaryQueue()
	if err != nil {
		t.Fatalf("reply queue: %v", err)
	}
	if subErr := brk.Subscribe(q, replyTopic); subErr != nil {
		t.Fatalf("subscribe reply: %v", subErr)
	}
	req := a2a.ScheduleRequest{
		RequestID: id, Name: "no-principal", ScheduleType: "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z", TargetType: "workflow",
		TargetName: "reconcile", TaskMessage: "go", ReplyTo: replyTopic,
	}
	payload, _ := json.Marshal(req)
	svc.handleScheduleRequest(ctx, &broker.Message{Payload: payload})

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil || msg == nil {
		t.Fatalf("expected a rejection reply, got none: err=%v", err)
	}
	msg.Ack()
	var resp a2a.ScheduleResponse
	if uErr := json.Unmarshal(msg.Payload, &resp); uErr != nil {
		t.Fatalf("reply not a ScheduleResponse: %v", uErr)
	}
	if resp.OK || resp.Error == "" {
		t.Errorf("expected a rejection with a reason, got %+v", resp)
	}
}

func TestCreateScheduledTask_EnabledWritesNextRunAt(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	p := workflowCreateParams()
	p.ID = "00000000-0000-7000-8000-000000000020"
	p.Enabled = true
	if _, err := svc.CreateScheduledTask(ctx, p); err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	got, err := svc.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if got.NextRunAt == nil || *got.NextRunAt != want {
		t.Fatalf("next_run_at = %v, want %d", got.NextRunAt, want)
	}
}

func TestHandleScheduleRequest_Settlement(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	settle := func(payload []byte, props map[string]string) (acked, nacked bool) {
		msg := broker.NewDeliveredMessage("m1", a2a.ScheduleRequestTopic("test-ns"), payload,
			func() { acked = true }, func() { nacked = true })
		msg.UserProperties = props
		svc.handleScheduleRequest(ctx, msg)
		return
	}
	good := func(id string) []byte {
		b, _ := json.Marshal(a2a.ScheduleRequest{
			RequestID: id, Name: "s-" + id, ScheduleType: "one_time",
			ScheduleExpression: "2099-01-01T00:00:00Z", TargetType: "workflow",
			TargetName: "reconcile", TaskMessage: "go",
		})
		return b
	}
	principal := map[string]string{a2a.PropUserID: "bob@example.com"}

	if a, n := settle([]byte("{bad"), principal); !a || n {
		t.Errorf("invalid payload should Ack (poison), got ack=%v nack=%v", a, n)
	}
	if a, n := settle(good("00000000-0000-7000-8000-0000000000f1"), nil); !a || n {
		t.Errorf("missing principal should Ack (drop), got ack=%v nack=%v", a, n)
	}
	if a, n := settle(good("00000000-0000-7000-8000-0000000000f2"), principal); !a || n {
		t.Errorf("success should Ack, got ack=%v nack=%v", a, n)
	}

}

func TestCreateError_Error(t *testing.T) {
	if got := (&CreateError{Kind: CreateErrorValidation, msg: "boom"}).Error(); got != "boom" {
		t.Errorf("Error() = %q, want boom", got)
	}
}

func TestHandleScheduleRequest_MetadataAndDescription(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	req := a2a.ScheduleRequest{
		RequestID:          "00000000-0000-7000-8000-0000000000bb",
		Name:               "with-meta",
		Description:        "nightly reconcile",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        "go",
		Metadata:           map[string]any{"trace_id": "tr-1"},
		SenderUserID:       "bob@example.com",
	}
	payload, _ := json.Marshal(req)
	svc.handleScheduleRequest(ctx, &broker.Message{
		Payload:        payload,
		UserProperties: map[string]string{a2a.PropUserID: "bob@example.com"},
	})

	got, err := svc.repo.GetByID(ctx, req.RequestID)
	if err != nil || got == nil {
		t.Fatalf("task should have been created: row=%v err=%v", got, err)
	}
	if got.Description == nil || *got.Description != "nightly reconcile" {
		t.Errorf("description not propagated: %+v", got.Description)
	}
	if got.TaskMetadata == nil || *got.TaskMetadata == "" {
		t.Error("metadata should be persisted as JSON")
	}
}

func TestHandleScheduleRequest_InvalidPayload(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	svc.handleScheduleRequest(t.Context(), &broker.Message{Payload: []byte("{not json")})
}

func TestHandleScheduleRequest_NilMessage(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	svc.handleScheduleRequest(t.Context(), nil)
}

func TestHandleScheduleRequest_IdempotentRedelivery(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx := t.Context()

	req := a2a.ScheduleRequest{
		RequestID:          "00000000-0000-7000-8000-0000000000cc",
		Name:               "dupe",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        "go",
		SenderUserID:       "bob@example.com",
	}
	payload, _ := json.Marshal(req)
	msg := func() *broker.Message {
		return &broker.Message{Payload: payload, UserProperties: map[string]string{a2a.PropUserID: "bob@example.com"}}
	}
	svc.handleScheduleRequest(ctx, msg())
	svc.handleScheduleRequest(ctx, msg())

	got, err := svc.repo.GetByID(ctx, req.RequestID)
	if err != nil || got == nil {
		t.Fatalf("task should still exist after redelivery: row=%v err=%v", got, err)
	}
}

func TestScheduleRequestLoop_EndToEnd(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		svc.scheduleRequestLoop(ctx)
		close(done)
	}()

	req := a2a.ScheduleRequest{
		RequestID:          "00000000-0000-7000-8000-0000000000dd",
		Name:               "looped",
		ScheduleType:       "one_time",
		ScheduleExpression: "2099-01-01T00:00:00Z",
		TargetType:         "workflow",
		TargetName:         "reconcile",
		TaskMessage:        "go",
		SenderUserID:       "bob@example.com",
	}
	payload, _ := json.Marshal(req)
	brk := svc.core.svc.Broker()
	publish := func() {
		_ = brk.PublishGuaranteed(ctx, a2a.ScheduleRequestTopic("test-ns"),
			&broker.Message{Payload: payload, UserProperties: map[string]string{a2a.PropUserID: "bob@example.com"}})
	}

	deadline := time.After(5 * time.Second)
	for {
		publish()
		got, _ := svc.repo.GetByID(ctx, req.RequestID)
		if got != nil {
			if got.CreatedBy != "bob@example.com" {
				t.Errorf("createdBy = %q, want bob@example.com", got.CreatedBy)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("loop did not create the task within 5s")
		case <-time.After(50 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduleRequestLoop did not exit on context cancel")
	}
}
