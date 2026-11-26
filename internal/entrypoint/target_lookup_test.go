// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestSubmitTask_NoCardAsksThePlatform(t *testing.T) {
	const (
		displayName = "Uppercaser"
		cardName    = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	)
	tests := []struct {
		name        string
		target      control.Target
		wantRefused bool
		wantTopic   string
	}{
		{name: "deployed goes to its card name", target: control.Target{State: control.TargetDeployed, CardName: cardName}, wantTopic: cardName},
		{name: "not deployed is refused", target: control.Target{State: control.TargetNotDeployed, CardName: cardName}, wantRefused: true},
		{name: "unknown goes to the name given", target: control.Target{State: control.TargetUnknown}, wantTopic: displayName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, brk, _ := setupTestGateway(t)
			ctx := t.Context()
			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if err := core.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = core.Stop(context.Background()) }()
			var asked string
			core.SetTargetLookup(func(_ context.Context, name string) control.Target {
				asked = name
				return tc.target
			})
			queue, err := brk.CreateQueue("test-lookup-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, topic := range []string{cardName, displayName} {
				if subErr := brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", topic)); subErr != nil {
					t.Fatal(subErr)
				}
			}

			msg := a2a.Message{Kind: "message", MessageID: "m", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}, ContextID: "s"}
			err = core.SubmitTask(ctx, displayName, "task-lookup", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"}))

			if asked != displayName {
				t.Errorf("lookup asked for %q, want %q", asked, displayName)
			}
			if tc.wantRefused {
				if !IsAgentNotAvailable(err) || SubmitFailureReason(err) != taxonomy.RejectAgentNotFound {
					t.Fatalf("SubmitTask error = %v, want an agent-not-available refusal", err)
				}
				if core.Tasks().Get("task-lookup") != nil {
					t.Error("a refused task was recorded")
				}
				return
			}
			if err != nil {
				t.Fatalf("SubmitTask: %v", err)
			}
			recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			got, err := brk.Receive(recvCtx, queue)
			if err != nil {
				t.Fatalf("no task published: %v", err)
			}
			if want := a2a.AgentRequestTopic("test-ns", tc.wantTopic); got.Topic != want {
				t.Errorf("published to %q, want %q", got.Topic, want)
			}
			if name := core.Tasks().AgentName("task-lookup"); name != tc.wantTopic {
				t.Errorf("task recorded against %q, want %q", name, tc.wantTopic)
			}
		})
	}
}

func TestFollowUpsLiveAsLongAsTheTask(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(context.Background()) }()
	queue, err := brk.CreateQueue("test-followup-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatal(err)
	}
	caller := auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})
	msg := a2a.Message{Kind: "message", MessageID: "m", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}, ContextID: "s"}
	if err := core.SubmitTask(ctx, "target-agent", "task-followups", msg, caller); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	answer := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}
	if err := core.SubmitUserInputResponse(ctx, "target-agent", "task-followups", answer, caller); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}
	if err := core.SubmitAuthResponse(ctx, "target-agent", "task-followups", msg, "user-1"); err != nil {
		t.Fatalf("SubmitAuthResponse: %v", err)
	}
	if err := core.CancelTask(ctx, "task-followups", "target-agent"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	want := time.Duration(core.cfg.TaskHardTimeoutSeconds) * time.Second
	for _, what := range []string{"request", "user input response", "authorization response", "cancel"} {
		recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		got, err := brk.Receive(recvCtx, queue)
		cancel()
		if err != nil {
			t.Fatalf("%s not published: %v", what, err)
		}
		if got.TTL != want {
			t.Errorf("%s TTL = %v, want the hard timeout %v", what, got.TTL, want)
		}
	}
}
