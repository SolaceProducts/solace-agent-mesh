// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type sonnet55LLM struct {
	mockLLMClient
	sent []string
}

func (m *sonnet55LLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	sent := req.ToolChoice
	if req.Temperature != nil {
		sent += "+t"
	}
	m.sent = append(m.sent, sent)
	switch {
	case req.Temperature != nil:
		return nil, &llm.Error{StatusCode: 400, Message: "`temperature` is deprecated for this model."}
	case req.ToolChoice != "":
		return nil, &llm.Error{StatusCode: 400, Message: `tool_choice: type "tool" and "any" are not supported for this model.`}
	}
	return m.mockLLMClient.ChatCompletion(ctx, req)
}

func TestTheBuildersProbeOncePerModel(t *testing.T) {
	firstTurn := []string{"required+t", "required", ""}
	want := slices.Concat(firstTurn, []string{""}, firstTurn)
	for name, turn := range map[string]func(t *testing.T, client llm.Client) func(){
		"task builder": func(t *testing.T, client llm.Client) func() {
			h := setupTaskBuilderTest(t, client)
			body, _ := json.Marshal(TaskBuilderChatRequest{Message: "Generate a daily report at 9 AM"})
			return func() {
				h.handleChat(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body)))
			}
		},
		"prompt builder": func(t *testing.T, client llm.Client) func() {
			h := setupPromptBuilderWithLLM(t, client)
			return func() {
				h.handleChat(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"a greeting template"}`)))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			model := &sonnet55LLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}}
			client := llm.NewReconfigurableClient(model)
			chat := turn(t, client)
			chat()
			chat()
			client.Reconfigure(model)
			chat()
			if !slices.Equal(model.sent, want) {
				t.Errorf("requests sent = %q, want %q", model.sent, want)
			}
		})
	}
}

func TestDefaultClientKeyFollowsReconfiguration(t *testing.T) {
	client := llm.NewReconfigurableClient(&mockLLMClient{})
	before := defaultClientKey(client)
	client.Reconfigure(&mockLLMClient{})
	if after := defaultClientKey(client); before == "" || after == "" || after == before {
		t.Errorf("keys %q then %q, want a new key after a reconfiguration", before, after)
	}
	if key := defaultClientKey(&mockLLMClient{}); key != "" {
		t.Errorf("a client that is not reconfigurable got key %q, want none: it could wrap one that changes", key)
	}
}

func TestParseAndValidateResponse_EncodedArguments(t *testing.T) {
	agents := []AvailableAgent{{Name: "ReportAgent"}}
	idx := agentNameIndex(agents)
	for name, tc := range map[string]struct {
		args        string
		wantName    string
		wantMessage string
		wantReady   bool
		wantPicker  bool
	}{
		"message packed after the task": {
			args:     `{"taskUpdates":"{\"name\":\"Daily Report\"},\"message\":\"Set up your report.\",\"readyToSave\":true}"}`,
			wantName: "Daily Report", wantMessage: "Set up your report.", wantReady: true,
		},
		"undecodable task is not ready": {
			args:        `{"message":"Saving your task now.","taskUpdates":"{\"name\":\"Daily","readyToSave":true}`,
			wantMessage: "Saving your task now.",
		},
		"encoded inline component": {
			args: fmt.Sprintf(`{"message":"Here is the task so far.","inlineComponent":%q}`,
				`{"type":"agent_picker","prompt":"Pick an agent","suggestions":[{"name":"ReportAgent","reason":"reports"}],"allowOther":true}`),
			wantMessage: "Here is the task so far.", wantPicker: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp := parseAndValidateResponse(tc.args, agents, idx, nil, nil)
			if got, _ := resp.TaskUpdates["name"].(string); got != tc.wantName {
				t.Errorf("task name = %q, want %q", got, tc.wantName)
			}
			if resp.Message != tc.wantMessage || resp.ReadyToSave != tc.wantReady || (resp.InlineComponent != nil) != tc.wantPicker {
				t.Errorf("got message %q, ready %v, picker %v; want %q, %v, %v", resp.Message, resp.ReadyToSave,
					resp.InlineComponent != nil, tc.wantMessage, tc.wantReady, tc.wantPicker)
			}
		})
	}
}

func TestPromptBuilderChat_ArgumentsInProseAreRead(t *testing.T) {
	mock := &mockLLMClient{noToolCall: true,
		response: `{"message":"Drafted","templateUpdates":"{\"name\":\"Action items\"},\"confidence\":0.8}"}`}
	h := setupPromptBuilderWithLLM(t, mock)
	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"notes to action items"}`)))
	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "Drafted" || resp.TemplateUpdates == nil || resp.TemplateUpdates.Name != "Action items" || resp.Confidence != 0.8 {
		t.Errorf("got %+v (updates %+v), want the template and confidence the reply carried", resp, resp.TemplateUpdates)
	}
}

func TestWithoutALiveModelKeyEveryTurnProbes(t *testing.T) {
	model := &sonnet55LLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}}
	h := setupPromptBuilderWithLLM(t, model)
	for range 2 {
		h.handleChat(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"a greeting template"}`)))
	}
	if want := []string{"required+t", "required", "", "required+t", "required", ""}; !slices.Equal(model.sent, want) {
		t.Errorf("requests sent = %q, want %q", model.sent, want)
	}
}
