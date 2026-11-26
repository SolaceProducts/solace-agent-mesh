// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestHILWaitOfBoundsTheTimeoutAMisbehavingToolAsks(t *testing.T) {
	type wait struct {
		RequestID string
		Timeout   time.Duration
	}
	tests := []struct {
		name string
		data map[string]any
		want wait
	}{
		{name: "no timeout takes the default", data: map[string]any{"hil_request_id": "r"}, want: wait{"r", 45 * time.Minute}},
		{name: "a zero timeout takes the default", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": float64(0)}, want: wait{"r", 45 * time.Minute}},
		{name: "a negative timeout takes the default", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": float64(-5)}, want: wait{"r", 45 * time.Minute}},
		{name: "a timeout of the wrong type takes the default", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": "an hour"}, want: wait{"r", 45 * time.Minute}},
		{name: "an ordinary timeout is kept", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": float64(time.Hour)}, want: wait{"r", time.Hour}},
		{name: "a timeout of exactly thirty days is kept", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": float64(30 * 24 * time.Hour)}, want: wait{"r", 30 * 24 * time.Hour}},
		{name: "a timeout above thirty days is cut to thirty days", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": float64(30*24*time.Hour + time.Hour)}, want: wait{"r", 30 * 24 * time.Hour}},
		{name: "a timeout too large for a duration is cut to thirty days", data: map[string]any{"hil_request_id": "r", "hil_timeout_ns": 1e30}, want: wait{"r", 30 * 24 * time.Hour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, timeout := hilWaitOf(&tool.Result{Status: tool.StatusPending, Data: tt.data})

			if diff := cmp.Diff(tt.want, wait{id, timeout}); diff != "" {
				t.Errorf("hilWaitOf(%v) (-want +got):\n%s", tt.data, diff)
			}
		})
	}
}

func TestDispatchRefusesAPersonRequestNobodyCouldAnswer(t *testing.T) {
	type outcome struct {
		Paused  bool
		Refused []string
	}
	const (
		unusable = `The tool asked a person for an answer but gave the request an id that cannot be used, so the answer could not be collected.`
		inUse    = `The tool asked a person for an answer with a request id that another task already uses, so the answer could not be collected.`
	)
	tests := []struct {
		name         string
		requestID    string
		callsInBatch int
		want         outcome
	}{
		{name: "a usable id pauses the task", requestID: "hil-ok", callsInBatch: 1, want: outcome{Paused: true}},
		{name: "an id of exactly 256 bytes pauses the task", requestID: strings.Repeat("r", 256), callsInBatch: 1, want: outcome{Paused: true}},
		{name: "an empty id is refused", requestID: "", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id of 257 bytes is refused", requestID: strings.Repeat("r", 257), callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id with a NUL byte is refused", requestID: "hil-\x00-nul", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id with a newline is refused", requestID: "hil-line\nbreak", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id with a delete character is refused", requestID: "hil-\x7f-del", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id with a C1 control character is refused", requestID: "hil-\u0085-c1", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id that is not valid UTF-8 is refused", requestID: "hil-\xff\xfe", callsInBatch: 1, want: outcome{Refused: []string{unusable}}},
		{name: "an id with ordinary non-ASCII letters pauses the task", requestID: "hil-caf\u00e9", callsInBatch: 1, want: outcome{Paused: true}},
		{name: "an id another paused task waits on is refused", requestID: "hil-shared", callsInBatch: 1, want: outcome{Refused: []string{inUse}}},
		{name: "two calls of one batch that use the same id pause once and refuse the second", requestID: "hil-twice", callsInBatch: 2, want: outcome{Paused: true, Refused: []string{inUse}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := newTestAgentWithStubTool(t, "test_hil_tool", func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return &tool.Result{Status: tool.StatusPending, Message: "Waiting...", Data: map[string]any{
					"pending_kind":   "hil_approval",
					"hil_request_id": tt.requestID,
				}}, nil
			})
			pausedStateWithHILEntry(t, agent, "task-other", "call-other", "hil-shared")
			state := newTestTaskState(agent)
			pca := defaultPauseCallArgs(agent, state)
			var calls []llm.ToolCall
			for i := range tt.callsInBatch {
				calls = append(calls, singleCall("test_hil_tool", "call-"+string(rune('a'+i)), "{}")...)
			}

			results, paused := agent.dispatchToolCallsMayPause(t.Context(), state, calls, pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger)
			t.Cleanup(func() {
				for _, pc := range state.PendingCalls {
					if pc.TimeoutTimer != nil {
						pc.TimeoutTimer.Stop()
					}
				}
			})

			got := outcome{Paused: paused}
			if paused {
				for _, r := range state.SyncResults {
					got.Refused = append(got.Refused, r.Message)
				}
			}
			for _, r := range results {
				got.Refused = append(got.Refused, r.Message)
			}
			sort.Strings(got.Refused)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("dispatching a pending request for a person's answer (-want +got):\n%s", diff)
			}
		})
	}
}
