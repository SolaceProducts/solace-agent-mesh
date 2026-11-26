// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type choiceRefusingLLM struct {
	mockLLMClient
	choices []string
}

func (m *choiceRefusingLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.choices = append(m.choices, req.ToolChoice)
	if req.ToolChoice != "" {
		return nil, &llm.Error{StatusCode: 400, Message: `tool_choice: type "tool" and "any" are not supported for this model.`}
	}
	return m.mockLLMClient.ChatCompletion(ctx, req)
}

func TestTheBuildersNeverForceAToolOnAStandaloneGatewaysKnownRefuser(t *testing.T) {
	builders := map[string]func(t *testing.T, client llm.Client) func(){
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
	}
	reply := `{"message":"done","confidence":0.9}`
	for name, builder := range builders {
		for _, tc := range []struct {
			label, model string
			firstChoice  string
		}{
			{"claude-sonnet-5-5", "claude-sonnet-5-5", ""},
			{"claude-opus-5-5", "claude-opus-5-5", ""},
			{"claude-sonnet-5", "claude-sonnet-5", "required"},
			{"unnamed", "", "required"},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				model := &choiceRefusingLLM{mockLLMClient: mockLLMClient{response: reply}}
				chat := builder(t, llm.NewReconfigurableClientFor(model, tc.model))
				chat()
				chat()
				if len(model.choices) == 0 || model.choices[0] != tc.firstChoice {
					t.Errorf("tool choices sent = %q, want the first to be %q", model.choices, tc.firstChoice)
				}
				if tc.firstChoice == "" && slices.Contains(model.choices, "required") {
					t.Errorf("tool choices sent = %q, want none forced for a known refuser", model.choices)
				}
			})
		}
	}
}

func TestDefaultClientKeyCarriesTheModelName(t *testing.T) {
	client := llm.NewReconfigurableClientFor(&mockLLMClient{}, "claude-sonnet-5-5")
	if key := defaultClientKey(client); !llm.ModelRefusesForcedToolChoice(key) {
		t.Errorf("key %q of a client built for claude-sonnet-5-5 names no known refuser", key)
	}
	client.ReconfigureTo(&mockLLMClient{}, "cfg-1", "claude-sonnet-4-6")
	if key := defaultClientKey(client); llm.ModelRefusesForcedToolChoice(key) || !strings.Contains(key, "claude-sonnet-4-6") {
		t.Errorf("key %q after a swap to claude-sonnet-4-6 still names the old model or omits the new one", key)
	}
	client.Reconfigure(&mockLLMClient{})
	if key := defaultClientKey(client); strings.Contains(key, "/") {
		t.Errorf("key %q of a client that was swapped without a model carries a model name", key)
	}
}

func TestTaskBuilderChat_AModelThatRefusesAForcedToolStillBuilds(t *testing.T) {
	mock := &choiceRefusingLLM{mockLLMClient: mockLLMClient{
		response: `{"message":"A daily report at 9.","taskUpdates":{"name":"Daily Report"},"confidence":0.85,"readyToSave":false}`,
	}}
	h := setupTaskBuilderTest(t, mock)

	body, _ := json.Marshal(TaskBuilderChatRequest{Message: "Generate a daily report at 9 AM"})
	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/builder/chat", bytes.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp TaskBuilderChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TaskUpdates["name"] != "Daily Report" {
		t.Errorf("got %+v, want the task the model built once left to choose", resp)
	}
	if len(mock.choices) != 2 || mock.choices[1] != "" {
		t.Errorf("tool choices sent = %q, want the forced one and then none", mock.choices)
	}
}

func TestPromptBuilderChat_AModelThatRefusesAForcedToolStillBuilds(t *testing.T) {
	mock := &choiceRefusingLLM{mockLLMClient: mockLLMClient{
		response: `{"message":"Here is your template","confidence":0.9,"readyToSave":false}`,
	}}
	h := setupPromptBuilderWithLLM(t, mock)

	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"create a greeting template"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.IsError || resp.Message != "Here is your template" {
		t.Errorf("got %+v, want the template the model built once left to choose", resp)
	}
	if len(mock.choices) != 2 || mock.choices[1] != "" {
		t.Errorf("tool choices sent = %q, want the forced one and then none", mock.choices)
	}
}
