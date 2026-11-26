// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func marshalWithin(t *testing.T, agent *Agent, result *tool.Result) json.RawMessage {
	t.Helper()
	done := make(chan json.RawMessage, 1)
	go func() { done <- agent.checkpoints.marshalResult("task-big", result) }()
	select {
	case payload := <-done:
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("recording an oversized result did not return")
		return nil
	}
}

func TestARecordedResultTooLargeToKeepInFullStaysARequestForAPersonWhenThatIsWhatItWas(t *testing.T) {
	type recorded struct {
		PersonRequest bool
		RequestID     string
		Timeout       time.Duration
		Payload       bool
		Bounded       bool
	}
	padding := strings.Repeat("x", 2<<20)
	tests := []struct {
		name   string
		result *tool.Result
		want   recorded
	}{
		{
			name: "a request for a person keeps its id and timeout",
			result: &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": "req-big", "hil_timeout_ns": float64(time.Hour), "details": padding,
			}},
			want: recorded{PersonRequest: true, RequestID: "req-big", Timeout: time.Hour, Payload: true, Bounded: true},
		},
		{
			name: "a request for a person whose message alone is huge still keeps its id and timeout",
			result: &tool.Result{Status: tool.StatusPending, Message: padding, Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": "req-big", "hil_timeout_ns": float64(time.Hour),
			}},
			want: recorded{PersonRequest: true, RequestID: "req-big", Timeout: time.Hour, Payload: true, Bounded: true},
		},
		{
			name: "a request id too large to keep is recorded as a request with no id",
			result: &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": padding, "hil_timeout_ns": float64(time.Hour),
			}},
			want: recorded{PersonRequest: true, Timeout: time.Hour, Payload: true, Bounded: true},
		},
		{
			name: "a request id of 300 bytes beside padded details is recorded as a request with no id",
			result: &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": strings.Repeat("r", 300), "hil_timeout_ns": float64(time.Hour), "details": padding,
			}},
			want: recorded{PersonRequest: true, Timeout: time.Hour, Payload: true, Bounded: true},
		},
		{
			name: "an empty request id beside padded details is recorded as a request with no id",
			result: &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_timeout_ns": float64(time.Hour), "details": padding,
			}},
			want: recorded{PersonRequest: true, Timeout: time.Hour, Payload: true, Bounded: true},
		},
		{
			name: "a timeout too large to keep is dropped and the request keeps its id",
			result: &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": "req-big", "hil_timeout_ns": padding,
			}},
			want: recorded{PersonRequest: true, RequestID: "req-big", Timeout: hilApprovalDefaultTimeout, Payload: true, Bounded: true},
		},
		{
			name:   "any other result is recorded as its message only",
			result: tool.OK("done", tool.WithData(map[string]any{"details": padding})),
			want:   recorded{Bounded: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := replicaOf(t, session.NewMemoryStore(), "owner")

			payload := marshalWithin(t, agent, tt.result)

			got := recorded{Payload: payload != nil, Bounded: len(payload) <= 1<<20}
			if back := resultFromPayload(payload); isPersonRequest(back) {
				got.PersonRequest = true
				got.RequestID, got.Timeout = hilWaitOf(back)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("recorded form of an oversized result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestARequestForAPersonWithATimeoutThatIsNotAFiniteNumberIsRecordedAsARequestForAPersonWithTheDefaultTimeout(t *testing.T) {
	type recorded struct {
		PersonRequest bool
		RequestID     string
		Timeout       time.Duration
	}
	tests := []struct {
		name    string
		timeout float64
	}{
		{name: "not a number", timeout: math.NaN()},
		{name: "positive infinity", timeout: math.Inf(1)},
		{name: "negative infinity", timeout: math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := replicaOf(t, session.NewMemoryStore(), "owner")
			result := &tool.Result{Status: tool.StatusPending, Message: "waiting for approval", Data: map[string]any{
				"pending_kind": "hil_approval", "hil_request_id": "req-big", "hil_timeout_ns": tt.timeout,
			}}

			payload := marshalWithin(t, agent, result)

			got := recorded{}
			if back := resultFromPayload(payload); isPersonRequest(back) {
				got.PersonRequest = true
				got.RequestID, got.Timeout = hilWaitOf(back)
			}
			want := recorded{PersonRequest: true, RequestID: "req-big", Timeout: hilApprovalDefaultTimeout}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("recorded form of a request for a person with a timeout that is not finite (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAToolReplyAskingForAPersonWithAnOversizedRequestIdIsRefusedToTheModelInsteadOfHangingTheTask(t *testing.T) {
	reply := strReplyWith("pending", map[string]any{
		"pending_kind":   "hil_approval",
		"hil_request_id": strings.Repeat("x", 2<<20),
		"hil_timeout_ns": float64(time.Hour),
	})
	agent, mock, _ := pausedOnARemoteCallReplying(t, session.NewMemoryStore(), reply)

	if !waitUntil(func() bool { return finishedWithAnswer(agent, mock) }) {
		t.Fatalf("LLM calls = %d and task still paused = %v, want the task resumed to completion", len(mock.getCalls()), agent.pausedTasks.Load(personWaitTaskID) != nil)
	}
	if texts := toolTextsOf(mock.getCalls()[1]); len(texts) != 1 || !strings.Contains(texts[0], unusableRequestIDMessage) {
		t.Errorf("tool results shown to the model = %q, want the one refusing the unusable request id", texts)
	}
}
