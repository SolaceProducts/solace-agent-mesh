// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type publishedAsk struct {
	source  string
	surface json.RawMessage
	timeout time.Duration
}

type askUserSpy struct {
	published []string
	asks      []publishedAsk
	waited    int
	answer    *a2a.UserInputResponseData
}

func (s *askUserSpy) publish(_ context.Context, requestID, source string, surface json.RawMessage, timeout time.Duration) {
	s.published = append(s.published, requestID)
	s.asks = append(s.asks, publishedAsk{source: source, surface: surface, timeout: timeout})
}

func (s *askUserSpy) wait(context.Context, string, string, json.RawMessage, time.Duration) *a2a.UserInputResponseData {
	s.waited++
	return s.answer
}

func regionQuestions() []any {
	return []any{map[string]any{
		"question": "Which region?", "header": "Region",
		"options": []any{
			map[string]any{"label": "EMEA", "description": "Europe"},
			map[string]any{"label": "APAC", "description": "Asia Pacific"},
		},
	}}
}

type askOutcome struct {
	Status    string
	Message   string
	Data      map[string]any
	Published int
	Waited    int
	Trusted   tool.TrustedPendingKind
}

func executeAsk(t *testing.T, spy *askUserSpy, toolCtx *tool.Context) (askOutcome, []string) {
	t.Helper()
	result, err := (&AskUserQuestionTool{}).Execute(t.Context(), map[string]any{"questions": regionQuestions()}, toolCtx)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return askOutcome{Status: result.Status, Message: result.Message, Data: result.Data, Published: len(spy.published), Waited: spy.waited, Trusted: result.TrustedPending}, spy.published
}

func TestAskUserQuestionPausesWhenItCanPublish(t *testing.T) {
	tests := []struct {
		name          string
		toolConfig    map[string]any
		wantTimeoutNs float64
		wantTimeout   time.Duration
	}{
		{name: "the default timeout is 45 minutes", wantTimeoutNs: 2.7e12, wantTimeout: 45 * time.Minute},
		{name: "a configured timeout of 30 minutes is used", toolConfig: map[string]any{"hil_default_timeout": "30m"}, wantTimeoutNs: 1.8e12, wantTimeout: 30 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &askUserSpy{}
			toolCtx := &tool.Context{
				PauseResume:      true,
				ToolConfig:       tt.toolConfig,
				WaitForUserInput: spy.wait, //nolint:staticcheck // the legacy waiter, set to prove the tool does not fall back to it
				PublishUserInput: spy.publish,
			}

			got, published := executeAsk(t, spy, toolCtx)

			if len(published) != 1 {
				t.Fatalf("published requests = %v, want exactly one", published)
			}
			want := askOutcome{
				Status:  "pending",
				Message: "Waiting for the user's answer",
				Data: map[string]any{
					"pending_kind":   "hil_ask",
					"hil_request_id": published[0],
					"hil_timeout_ns": tt.wantTimeoutNs,
					"questions":      regionQuestions(),
				},
				Published: 1,
				Trusted:   tool.TrustedPendingHILAsk,
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Execute() outcome mismatch (-want +got):\n%s", diff)
			}
			ask := spy.asks[0]
			if ask.source != "ask_user_question" || ask.timeout != tt.wantTimeout {
				t.Errorf("the prompt was published with source %q and timeout %v, want %q and %v", ask.source, ask.timeout, "ask_user_question", tt.wantTimeout)
			}
			if !json.Valid(ask.surface) || !strings.Contains(string(ask.surface), "Which region?") || !strings.Contains(string(ask.surface), published[0]) {
				t.Errorf("the prompt surface = %s, want valid JSON that shows the question and carries the request id", ask.surface)
			}
		})
	}
}

func answeredRegionSpy() *askUserSpy {
	return &askUserSpy{answer: &a2a.UserInputResponseData{
		Status: "answered", Data: map[string]any{"answers": map[string]any{"q0": "EMEA"}},
	}}
}

func wantRegionAnswered() askOutcome {
	return askOutcome{
		Status:  "success",
		Message: `{"answers":{"Which region?":"EMEA"},"status":"answered"}`,
		Data: map[string]any{
			"status":  "answered",
			"answers": map[string]string{"Which region?": "EMEA"},
		},
		Waited: 1,
	}
}

func TestAskUserQuestionBlocksForTheAnswerWhenPauseResumeIsOff(t *testing.T) {
	spy := answeredRegionSpy()
	toolCtx := &tool.Context{
		PauseResume:      false,
		WaitForUserInput: spy.wait, //nolint:staticcheck // the legacy waiter, the path under test
		PublishUserInput: spy.publish,
	}

	got, _ := executeAsk(t, spy, toolCtx)

	if diff := cmp.Diff(wantRegionAnswered(), got); diff != "" {
		t.Errorf("Execute() outcome with PauseResume off (-want +got):\n%s", diff)
	}
}

func TestAskUserQuestionBlocksForTheAnswerWhenThereIsNoPublisher(t *testing.T) {
	spy := answeredRegionSpy()
	toolCtx := &tool.Context{
		PauseResume:      true,
		WaitForUserInput: spy.wait, //nolint:staticcheck // the legacy waiter, the path under test
	}

	got, _ := executeAsk(t, spy, toolCtx)

	if diff := cmp.Diff(wantRegionAnswered(), got); diff != "" {
		t.Errorf("Execute() outcome with no publisher (-want +got):\n%s", diff)
	}
}
