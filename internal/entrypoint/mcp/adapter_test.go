// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func TestSanitizeToolName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"mixed case with underscore is lowercased", "MyAgent_MySkill", "myagent_myskill"},
		{"hyphens become underscores", "agent-one_skill-two", "agent_one_skill_two"},
		{"spaces become underscores", "Agent With Spaces", "agent_with_spaces"},
		{"leading digit gets tool prefix", "123numeric", "tool_123numeric"},
		{"leading and trailing separators are dropped", "---leading---", "leading"},
		{"runs of underscores collapse", "a__b___c", "a_b_c"},
		{"special characters become one underscore", "special!@#chars", "special_chars"},
		{"empty name gets placeholder", "", "unnamed_tool"},
		{"blank name gets placeholder", "  ", "unnamed_tool"},
		{"upper case is lowercased", "UPPERCASE", "uppercase"},
		{"mixed separators collapse to underscores", "mix-of_separators here", "mix_of_separators_here"},
		{"non-ASCII letters alone get placeholder", "検索", "unnamed_tool"},
		{"non-ASCII katakana alone gets placeholder", "日本語エージェント", "unnamed_tool"},
		{"trailing non-ASCII letters are dropped with their separator", "support_desk_検索", "support_desk"},
		{"accented letter is replaced by a separator", "Café Lookup", "caf_lookup"},
		{"accented letters inside a word are replaced", "Ünïcodé Agent_Skill", "n_cod_agent_skill"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeToolName(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeToolName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatSkillDescription(t *testing.T) {
	skill := a2a.AgentSkill{
		Description: "A helpful skill",
		Examples:    []string{"do thing 1", "do thing 2", "do thing 3", "do thing 4"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text", "file"},
		Tags:        []string{"general", "utility"},
	}
	const want = "Agent: Support Desk\n\nA helpful skill\n\n" +
		"Examples:\n- do thing 1\n- do thing 2\n- do thing 3\n\n" +
		"Input modes: text\nOutput modes: text, file\nTags: general, utility"

	got := formatSkillDescription(&skill, "Support Desk")

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("formatSkillDescription(skill, %q) mismatch (-want +got):\n%s", "Support Desk", diff)
	}
}

func TestShouldRegisterTool(t *testing.T) {
	markitdownFilters := map[string]any{
		"include_tools": []any{
			".*",
			"markitdownagent_content_extraction",
			"content_extraction",
		},
		"exclude_tools": []any{
			"mockmcpagent_*",
			"markitdownagent*",
			"builder",
			"WebAgent",
		},
	}

	tests := []struct {
		name  string
		raw   map[string]any
		agent string
		skill string
		tool  string
		want  bool
	}{
		{"no filters registers every tool", nil, "agent", "skill", "agent_skill", true},
		{"exact exclude drops the excluded tool", map[string]any{"exclude_tools": []any{"agent_skill"}}, "agent", "skill", "agent_skill", false},
		{"exact exclude keeps other tools", map[string]any{"exclude_tools": []any{"agent_skill"}}, "agent", "other", "agent_other", true},
		{"exact include keeps the included tool", map[string]any{"include_tools": []any{"agent_skill"}}, "agent", "skill", "agent_skill", true},
		{"exact include drops other tools", map[string]any{"include_tools": []any{"agent_skill"}}, "agent", "other", "agent_other", false},
		{"exclude wins over include of the same name", map[string]any{"include_tools": []any{"agent"}, "exclude_tools": []any{"agent"}}, "agent", "skill", "agent_skill", false},
		{"regex include keeps a matching tool", map[string]any{"include_tools": []any{"^helper.*"}}, "helper_agent", "skill", "helper_agent_skill", true},
		{"regex include drops a non-matching tool", map[string]any{"include_tools": []any{"^helper.*"}}, "main_agent", "skill", "main_agent_skill", false},
		{"exact include beats regex exclude", markitdownFilters, "markitdownagent", "content_extraction", "markitdownagent_content_extraction", true},
		{"regex exclude still drops other tools on the same agent", markitdownFilters, "markitdownagent", "other", "markitdownagent_other", false},
		{"exact exclude beats regex include", markitdownFilters, "builder", "anything", "builder_anything", false},
		{"exact exclude is case insensitive", markitdownFilters, "WebAgent", "do_something", "webagent_do_something", false},
		{"regex exclude beats regex include", markitdownFilters, "mockmcpagent", "foo", "mockmcpagent_foo", false},
		{"second exact include matches on the skill name", markitdownFilters, "someother", "content_extraction", "someother_content_extraction", true},
		{"regex include accepts a tool no exclude matches", markitdownFilters, "randomagent", "doit", "randomagent_doit", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{cfg: mustParseConfig(t, tc.raw)}

			got := a.shouldRegisterTool(tc.agent, tc.skill, tc.tool)

			if got != tc.want {
				t.Errorf("shouldRegisterTool(%q, %q, %q) = %v, want %v", tc.agent, tc.skill, tc.tool, got, tc.want)
			}
		})
	}
}

func TestRegisterAgentTools(t *testing.T) {
	tests := []struct {
		name string
		card *a2a.AgentCard
		want map[string]toolMapping
	}{
		{
			name: "each skill becomes a tool mapped to its agent and skill id",
			card: &a2a.AgentCard{
				Name: "test-agent",
				Skills: []a2a.AgentSkill{
					{ID: "s1", Name: "greet", Description: "Say hello"},
					{ID: "s2", Name: "calc", Description: "Do math"},
				},
			},
			want: map[string]toolMapping{
				"test_agent_greet": {agentName: "test-agent", skillID: "s1"},
				"test_agent_calc":  {agentName: "test-agent", skillID: "s2"},
			},
		},
		{
			name: "loadable skills extension does not become tools",
			card: &a2a.AgentCard{
				Name:   "test-agent",
				Skills: []a2a.AgentSkill{{ID: "s1", Name: "greet", Description: "Say hello"}},
				Capabilities: a2a.AgentCapabilities{
					Extensions: []a2a.AgentExtension{{
						URI: "https://solace.com/a2a/extensions/sam/skills",
						Params: map[string]any{
							"skills": []any{
								map[string]any{"name": "pptx", "description": "PowerPoint"},
								map[string]any{"name": "xlsx", "description": "Spreadsheets"},
							},
						},
					}},
				},
			},
			want: map[string]toolMapping{
				"test_agent_greet": {agentName: "test-agent", skillID: "s1"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newToolRegistryAdapter(t)

			a.registerAgentTools(tc.card)

			a.toolMu.RLock()
			got := maps.Clone(a.toolMap)
			a.toolMu.RUnlock()
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(toolMapping{})); diff != "" {
				t.Errorf("registerAgentTools(%q) tool map mismatch (-want +got):\n%s", tc.card.Name, diff)
			}
		})
	}
}

func TestDeregisterAgentTools(t *testing.T) {
	a := newToolRegistryAdapter(t)
	a.registerAgentTools(&a2a.AgentCard{
		Name: "test-agent",
		Skills: []a2a.AgentSkill{
			{ID: "s1", Name: "greet", Description: "Say hello"},
			{ID: "s2", Name: "calc", Description: "Do math"},
		},
	})

	a.deregisterAgentTools("test-agent")

	a.toolMu.RLock()
	got := maps.Clone(a.toolMap)
	a.toolMu.RUnlock()
	if diff := cmp.Diff(map[string]toolMapping{}, got, cmp.AllowUnexported(toolMapping{})); diff != "" {
		t.Errorf("tool map after deregisterAgentTools(%q) mismatch (-want +got):\n%s", "test-agent", diff)
	}
}

func newTaskAdapter(t *testing.T, registry *mockTaskRegistry) *Adapter {
	t.Helper()
	return &Adapter{
		cfg:               mustParseConfig(t, nil),
		toolMap:           make(map[string]toolMapping),
		agentTools:        make(map[string][]string),
		tasks:             make(map[string]*taskState),
		logger:            noopLogger(),
		coreTasksOverride: registry,
	}
}

func drainResults(ts *taskState) []taskResult {
	var results []taskResult
	for {
		select {
		case result := <-ts.done:
			results = append(results, result)
		default:
			return results
		}
	}
}

type taskOutcome struct {
	Results []taskResult
	Tracked bool
	Removed map[string]bool
}

func TestOnStatusUpdate_AccumulatesText(t *testing.T) {
	a := newTaskAdapter(t, &mockTaskRegistry{})
	taskID := "test-task-status"
	ts := &taskState{
		toolName: "test_tool",
		done:     make(chan taskResult, 1),
	}
	a.tasks[taskID] = ts

	for _, text := range []string{"hello ", "world"} {
		a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
			Status: a2a.TaskStatus{
				Message: &a2a.Message{
					Parts: []a2a.Part{a2a.NewTextPart(text)},
				},
			},
		}, nil)
	}

	ts.mu.Lock()
	got := ts.textBuf.String()
	ts.mu.Unlock()
	if got != "hello world" {
		t.Errorf("accumulated text after two OnStatusUpdate calls = %q, want %q", got, "hello world")
	}
}

const (
	authPromptPrefix = "Authentication required (copy the URL, remove empty spaces, and paste it in your browser):\n\n"
	authPromptSuffix = "\n\nAfter authenticating, re-request your last operation."
	fullAuthURI      = "https://idp.example.com/oauth/authorize?response_type=code&client_id=abc&state=xyz&code_challenge=q"
	shortAuthURI     = "https://idp.example.com/oauth/authorize?state=xyz"
)

func TestOnStatusUpdate(t *testing.T) {
	tests := []struct {
		name     string
		taskID   string
		seedText string
		parts    []a2a.Part
		want     taskOutcome
	}{
		{
			name:   "authentication required with a URI completes the call with the sign-in instruction",
			taskID: "test-task-auth",
			parts: []a2a.Part{{
				Kind: "data",
				Data: map[string]any{
					"type":                          "authentication_required",
					"auth_uri":                      fullAuthURI,
					"target_agent":                  "AtlassianAgent",
					"credential_key":                "mcp_atlassian_oauth2",
					"target_agent_task_id":          "test-task-auth",
					"user_id":                       "alice",
					"client_id":                     "go-mcp-gw",
					"auth_request_function_call_id": "fc-1",
					"code_verifier":                 "pkce-verifier",
				},
			}},
			want: taskOutcome{
				Results: []taskResult{{text: authPromptPrefix + fullAuthURI + authPromptSuffix}},
				Removed: map[string]bool{"test-task-auth": true},
			},
		},
		{
			name:     "authentication required beside text and buffered text returns only the instruction",
			taskID:   "test-task-auth-mixed",
			seedText: "calling atlassianUserInfo...",
			parts: []a2a.Part{
				a2a.NewTextPart("authenticating with Atlassian... "),
				{
					Kind: "data",
					Data: map[string]any{
						"type":     "authentication_required",
						"auth_uri": shortAuthURI,
					},
				},
			},
			want: taskOutcome{
				Results: []taskResult{{text: authPromptPrefix + shortAuthURI + authPromptSuffix}},
				Removed: map[string]bool{"test-task-auth-mixed": true},
			},
		},
		{
			name:   "authentication required without a URI is ignored",
			taskID: "test-task-auth-empty",
			parts: []a2a.Part{{
				Kind: "data",
				Data: map[string]any{
					"type":     "authentication_required",
					"auth_uri": "",
				},
			}},
			want: taskOutcome{Tracked: true},
		},
		{
			name:   "data part of another type is ignored",
			taskID: "test-task-other-data",
			parts: []a2a.Part{{
				Kind: "data",
				Data: map[string]any{
					"type":        "agent_progress_update",
					"status_text": "thinking...",
				},
			}},
			want: taskOutcome{Tracked: true},
		},
		{
			name:   "data part that is not an object is ignored",
			taskID: "test-task-non-map",
			parts:  []a2a.Part{{Kind: "data", Data: "not-a-map"}},
			want:   taskOutcome{Tracked: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := &mockTaskRegistry{}
			a := newTaskAdapter(t, registry)
			ts := &taskState{
				toolName: "test_tool",
				done:     make(chan taskResult, 1),
			}
			ts.textBuf.WriteString(tc.seedText)
			a.tasks[tc.taskID] = ts

			a.OnStatusUpdate(t.Context(), tc.taskID, &a2a.TaskStatusUpdateEvent{
				Status: a2a.TaskStatus{Message: &a2a.Message{Parts: tc.parts}},
			}, nil)

			a.tasksMu.Lock()
			_, tracked := a.tasks[tc.taskID]
			a.tasksMu.Unlock()
			got := taskOutcome{Results: drainResults(ts), Tracked: tracked, Removed: registry.removed}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(taskResult{})); diff != "" {
				t.Errorf("OnStatusUpdate(%q) outcome mismatch (-want +got):\n%s", tc.taskID, diff)
			}
		})
	}
}

func TestOnStatusUpdate_IgnoresNilMessage(t *testing.T) {
	a := &Adapter{
		cfg:    mustParseConfig(t, nil),
		tasks:  make(map[string]*taskState),
		logger: noopLogger(),
	}

	a.OnStatusUpdate(t.Context(), "nonexistent", &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: nil},
	}, nil)
}

const workflowSummary = `Workflow "W" completed successfully.`

func TestOnFinalResponse(t *testing.T) {
	artifactSaved := a2a.NewDataPartWithMetadata(
		map[string]any{"filename": "report.md", "version": 0, "status": "success", "message": "Artifact has been created and provided to the requester"},
		map[string]any{"tool_name": "_notify_artifact_save"},
	)
	toolFailure := a2a.NewDataPart(map[string]any{
		"a2a_signal_type": "tool_execution_error",
		"code":            "TOOL_CRASHED",
		"stderr_tail":     "secret-stderr",
		"stack_trace":     "secret-stack",
	})

	tests := []struct {
		name       string
		taskID     string
		toolName   string
		isWorkflow bool
		seedText   string
		state      a2a.TaskState
		message    *a2a.Message
		want       taskOutcome
	}{
		{
			name:     "final message text completes the call",
			taskID:   "test-task-1",
			toolName: "test_tool",
			state:    a2a.TaskStateCompleted,
			message:  &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("hello world")}},
			want: taskOutcome{
				Results: []taskResult{{text: "hello world"}},
				Removed: map[string]bool{"test-task-1": true},
			},
		},
		{
			name:     "no final message falls back to the buffered text",
			taskID:   "test-task-buf",
			toolName: "test_tool",
			seedText: "buffered text",
			state:    a2a.TaskStateCompleted,
			want: taskOutcome{
				Results: []taskResult{{text: "buffered text"}},
				Removed: map[string]bool{"test-task-buf": true},
			},
		},
		{
			name:       "completed workflow returns its output beside the summary",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateCompleted,
			message: &a2a.Message{Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"recommendation": "Meridian Plus"}),
				a2a.NewTextPart(workflowSummary),
			}},
			want: taskOutcome{
				Results: []taskResult{{
					text:       workflowSummary + "\n\n{\n  \"recommendation\": \"Meridian Plus\"\n}",
					structured: map[string]any{"recommendation": "Meridian Plus"},
				}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:       "completed workflow output alone becomes the text",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateCompleted,
			message:    &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"answer": "42"})}},
			want: taskOutcome{
				Results: []taskResult{{
					text:       "{\n  \"answer\": \"42\"\n}",
					structured: map[string]any{"answer": "42"},
				}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:       "workflow output that cannot be serialized is dropped",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateCompleted,
			message: &a2a.Message{Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"ch": make(chan int)}),
				a2a.NewTextPart(workflowSummary),
			}},
			want: taskOutcome{
				Results: []taskResult{{text: workflowSummary}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:     "agent artifact notice stays out of a completed result",
			taskID:   "test-task",
			toolName: "a_tool",
			state:    a2a.TaskStateCompleted,
			message:  &a2a.Message{Parts: []a2a.Part{artifactSaved, a2a.NewTextPart("Done.")}},
			want: taskOutcome{
				Results: []taskResult{{text: "Done."}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:     "agent tool failure diagnostics stay out of a failed result",
			taskID:   "test-task",
			toolName: "a_tool",
			state:    a2a.TaskStateFailed,
			message:  &a2a.Message{Parts: []a2a.Part{toolFailure, a2a.NewTextPart("Tool execution failed.")}},
			want: taskOutcome{
				Results: []taskResult{{text: "Tool execution failed.", unsuccessful: true}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:       "failed workflow carries no output",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateFailed,
			message: &a2a.Message{Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"partial": "value"}),
				a2a.NewTextPart(`Workflow "W" failed: boom`),
			}},
			want: taskOutcome{
				Results: []taskResult{{text: `Workflow "W" failed: boom`, unsuccessful: true}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:       "cancelled workflow carries no output",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateCancelled,
			message: &a2a.Message{Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"partial": "value"}),
				a2a.NewTextPart(`Workflow "W" failed: boom`),
			}},
			want: taskOutcome{
				Results: []taskResult{{text: `Workflow "W" failed: boom`, unsuccessful: true}},
				Removed: map[string]bool{"test-task": true},
			},
		},
		{
			name:       "rejected workflow carries no output",
			taskID:     "test-task",
			toolName:   "a_tool",
			isWorkflow: true,
			state:      a2a.TaskStateRejected,
			message: &a2a.Message{Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"partial": "value"}),
				a2a.NewTextPart(`Workflow "W" failed: boom`),
			}},
			want: taskOutcome{
				Results: []taskResult{{text: `Workflow "W" failed: boom`, unsuccessful: true}},
				Removed: map[string]bool{"test-task": true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := &mockTaskRegistry{}
			a := newTaskAdapter(t, registry)
			ts := &taskState{toolName: tc.toolName, isWorkflow: tc.isWorkflow, done: make(chan taskResult, 1)}
			ts.textBuf.WriteString(tc.seedText)
			a.tasks[tc.taskID] = ts

			a.OnFinalResponse(t.Context(), tc.taskID, &a2a.Task{
				Status: a2a.TaskStatus{State: tc.state, Message: tc.message},
			}, nil)

			a.tasksMu.Lock()
			_, tracked := a.tasks[tc.taskID]
			a.tasksMu.Unlock()
			got := taskOutcome{Results: drainResults(ts), Tracked: tracked, Removed: registry.removed}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(taskResult{})); diff != "" {
				t.Errorf("OnFinalResponse(%q, state %q) outcome mismatch (-want +got):\n%s", tc.taskID, tc.state, diff)
			}
		})
	}
}

func TestFinalResultData(t *testing.T) {
	tests := []struct {
		name string
		msg  *a2a.Message
		want map[string]any
	}{
		{"nil message", nil, nil},
		{"no data parts", &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("hi")}}, nil},
		{"progress signal skipped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"type": string(a2a.SignalArtifactCreationProgress)}),
		}}, nil},
		{"auth signal skipped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"type": string(a2a.SignalAuthenticationRequired)}),
		}}, nil},
		{"structured invocation result skipped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"type": string(a2a.SignalStructuredInvocationRes)}),
		}}, nil},
		{"empty object skipped", &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{})}}, nil},
		{"nil data skipped", &a2a.Message{Parts: []a2a.Part{{Kind: "data"}}}, nil},
		{"single object", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"a": 1}),
		}}, map[string]any{"a": 1}},
		{"signal beside a single object leaves it unwrapped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"type": string(a2a.SignalArtifactCreationProgress)}),
			a2a.NewDataPart(map[string]any{"a": 1}),
		}}, map[string]any{"a": 1}},
		{"several objects wrapped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart(map[string]any{"a": 1}),
			a2a.NewDataPart(map[string]any{"b": 2}),
		}}, map[string]any{"results": []any{map[string]any{"a": 1}, map[string]any{"b": 2}}}},
		{"non-object wrapped", &a2a.Message{Parts: []a2a.Part{
			a2a.NewDataPart([]any{1, 2}),
		}}, map[string]any{"results": []any{[]any{1, 2}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := finalResultData(tc.msg)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("finalResultData mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestOnError_CompletesTask(t *testing.T) {
	registry := &mockTaskRegistry{}
	a := newTaskAdapter(t, registry)
	taskID := "test-task-2"
	ts := &taskState{
		toolName: "test_tool",
		done:     make(chan taskResult, 1),
	}
	a.tasks[taskID] = ts

	a.OnError(t.Context(), taskID, &a2a.Error{Code: 500, Message: "something broke"}, nil)

	a.tasksMu.Lock()
	_, tracked := a.tasks[taskID]
	a.tasksMu.Unlock()
	got := taskOutcome{Results: drainResults(ts), Tracked: tracked, Removed: registry.removed}
	want := taskOutcome{
		Results: []taskResult{{err: &a2a.Error{Code: 500, Message: "something broke"}}},
		Removed: map[string]bool{"test-task-2": true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(taskResult{})); diff != "" {
		t.Errorf("OnError(%q) outcome mismatch (-want +got):\n%s", taskID, diff)
	}
}

func TestParseConfig(t *testing.T) {
	type parsedConfig struct {
		ServerName         string
		Transport          string
		Port               int
		TaskTimeoutSeconds int
		IncludeTools       []string
		ExcludeTools       []string
		IncludeExact       int
		IncludeRegex       int
		ExcludeExact       int
		ExcludeRegex       int
	}

	tests := []struct {
		name string
		raw  map[string]any
		want parsedConfig
	}{
		{
			name: "no config uses the defaults",
			raw:  nil,
			want: parsedConfig{ServerName: "SAM MCP Entrypoint", Transport: "sse", Port: 8080, TaskTimeoutSeconds: 300},
		},
		{
			name: "explicit values override the defaults",
			raw: map[string]any{
				"server_name":          "My Server",
				"transport":            "stdio",
				"port":                 float64(9090),
				"task_timeout_seconds": float64(60),
				"include_tools":        []any{"agent_a"},
				"exclude_tools":        []any{"agent_b"},
			},
			want: parsedConfig{
				ServerName:         "My Server",
				Transport:          "stdio",
				Port:               9090,
				TaskTimeoutSeconds: 60,
				IncludeTools:       []string{"agent_a"},
				ExcludeTools:       []string{"agent_b"},
				IncludeExact:       1,
				ExcludeExact:       1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig(tc.raw)
			if err != nil {
				t.Fatalf("parseConfig(%v) error: %v", tc.raw, err)
			}

			got := parsedConfig{
				ServerName:         cfg.ServerName,
				Transport:          cfg.Transport,
				Port:               cfg.Port,
				TaskTimeoutSeconds: cfg.TaskTimeoutSeconds,
				IncludeTools:       cfg.IncludeTools,
				ExcludeTools:       cfg.ExcludeTools,
				IncludeExact:       len(cfg.compiledIncludeExact),
				IncludeRegex:       len(cfg.compiledIncludeRegex),
				ExcludeExact:       len(cfg.compiledExcludeExact),
				ExcludeRegex:       len(cfg.compiledExcludeRegex),
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseConfig(%v) mismatch (-want +got):\n%s", tc.raw, diff)
			}
		})
	}
}

func TestParseConfig_InvalidRegex(t *testing.T) {
	raw := map[string]any{"include_tools": []any{"[invalid"}}

	if _, err := parseConfig(raw); err == nil {
		t.Errorf("parseConfig(%v) error = nil, want an error for the invalid regex pattern", raw)
	}
}

func mustParseConfig(t *testing.T, raw map[string]any) *Config {
	t.Helper()
	cfg, err := parseConfig(raw)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestMCPServer() *server.MCPServer {
	return server.NewMCPServer("test", "1.0.0",
		server.WithToolCapabilities(true),
	)
}

func newToolRegistryAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := &Adapter{
		cfg:        mustParseConfig(t, nil),
		toolMap:    make(map[string]toolMapping),
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
		logger:     noopLogger(),
	}
	a.mcpServer = newTestMCPServer()
	return a
}

type mockTaskRegistry struct {
	removed     map[string]bool
	registerErr error
}

func (m *mockTaskRegistry) Register(string, string, string, entrypoint.TaskIdentity) error {
	return m.registerErr
}
func (m *mockTaskRegistry) Metadata(string) any                        { return nil }
func (m *mockTaskRegistry) SetMetadata(string, any)                    {}
func (m *mockTaskRegistry) SetRunModel(string, usagefacts.Model)       {}
func (m *mockTaskRegistry) Get(string) *entrypoint.TaskInfo            { return nil }
func (m *mockTaskRegistry) Holds(string) bool                          { return false }
func (m *mockTaskRegistry) SetWaiting(string, string)                  {}
func (m *mockTaskRegistry) ClearWaiting(string, string)                {}
func (m *mockTaskRegistry) ClaimRunReport(string) *entrypoint.TaskInfo { return nil }
func (m *mockTaskRegistry) AgentName(string) string                    { return "" }
func (m *mockTaskRegistry) RunState(string) (bool, bool)               { return false, true }
func (m *mockTaskRegistry) IsRunning(string) bool                      { return false }
func (m *mockTaskRegistry) Remove(taskID string) {
	if m.removed == nil {
		m.removed = make(map[string]bool)
	}
	m.removed[taskID] = true
}
func (m *mockTaskRegistry) AllByAgent(string) []string { return nil }

func newAdapterWithCfg(cfg *Config) *Adapter {
	return &Adapter{
		cfg:        cfg,
		toolMap:    make(map[string]toolMapping),
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
		logger:     noopLogger(),
	}
}

func TestStart_RefusesDoubleStart(t *testing.T) {
	a := newAdapterWithCfg(mustParseConfig(t, map[string]any{"transport": "sse"}))
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("first Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(t.Context()) })

	if err := a.Start(t.Context()); err == nil {
		t.Fatal("second Start should have returned an error; got nil")
	}
}

func TestStart_RollsBackOnTransportError(t *testing.T) {
	a := newAdapterWithCfg(&Config{Transport: "bogus-transport"})
	if err := a.Start(t.Context()); err == nil {
		t.Fatal("Start with unknown transport should have errored")
	}
	if a.cancel != nil {
		t.Error("Start error path did not roll back a.cancel — retry would be blocked by already-started guard")
	}
	a.cfg.Transport = "sse"
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("retry Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(t.Context()) })
}

func TestHandler(t *testing.T) {
	mcpServer := newTestMCPServer()

	tests := []struct {
		name       string
		raw        map[string]any
		mcpServer  *server.MCPServer
		httpServer *server.StreamableHTTPServer
		wantNil    bool
	}{
		{
			name:       "sse transport with an initialized HTTP server returns a handler",
			raw:        map[string]any{"transport": "sse"},
			mcpServer:  mcpServer,
			httpServer: server.NewStreamableHTTPServer(mcpServer),
			wantNil:    false,
		},
		{
			name:    "stdio transport has no handler",
			raw:     map[string]any{"transport": "stdio"},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newAdapterWithCfg(mustParseConfig(t, tc.raw))
			a.mcpServer = tc.mcpServer
			a.httpServer = tc.httpServer

			got := a.Handler()

			if (got == nil) != tc.wantNil {
				t.Errorf("Handler() = %v, want nil = %v", got, tc.wantNil)
			}
		})
	}
}

func TestMountIntent(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want entrypoint.MountSpec
	}{
		{"stdio transport owns no HTTP surface", map[string]any{"transport": "stdio"}, entrypoint.MountSpec{Kind: entrypoint.MountNone}},
		{"explicit sse transport reserves a named slot", map[string]any{"transport": "sse"}, entrypoint.MountSpec{Kind: entrypoint.MountNamed}},
		{"unspecified transport reserves a named slot", nil, entrypoint.MountSpec{Kind: entrypoint.MountNamed}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAdapter(tc.raw)
			if err != nil {
				t.Fatalf("NewAdapter(%v): %v", tc.raw, err)
			}

			got := a.MountIntent()

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("MountIntent() for %v mismatch (-want +got):\n%s", tc.raw, diff)
			}
		})
	}
}

func TestResolveValidator_NoFactoryReturnsHintError(t *testing.T) {
	_, err := resolveValidator("test-gw", "nonexistent-provider", map[string]any{})
	if err == nil {
		t.Fatal("expected error when no validator factory registered")
	}
	if !strings.Contains(err.Error(), "enable_auth") {
		t.Errorf("error message = %q, want hint about enable_auth", err)
	}
	if !strings.Contains(err.Error(), "test-gw:nonexistent-provider") {
		t.Errorf("error message = %q, want factory name", err)
	}
}

func TestResolveValidator_FoundFactoryReturnsValidator(t *testing.T) {
	auth.RegisterValidatorFactory("mcp-test-gw:fake", func(map[string]any) (auth.TokenValidator, error) {
		return auth.TokenValidatorFunc(func(_ context.Context, _ string) (*auth.Subject, error) {
			return auth.NewTestSubject(auth.SubjectFields{}), nil
		}), nil
	})

	v, err := resolveValidator("mcp-test-gw", "fake", map[string]any{})
	if err != nil {
		t.Fatalf("resolveValidator: %v", err)
	}
	if v == nil {
		t.Fatal("validator is nil")
	}
}

type fakeMcpauthIDPClient struct{}

func (fakeMcpauthIDPClient) BuildAuthURL(_ context.Context, _, _, _, _ string) (string, error) {
	return "https://idp.example.com/auth", nil
}
func (fakeMcpauthIDPClient) ExchangeCode(_ context.Context, _, _, _ string) (*mcpauth.IDPTokens, error) {
	return &mcpauth.IDPTokens{}, nil
}
func (fakeMcpauthIDPClient) GetUserInfo(_ context.Context, _ string) (*mcpauth.UserInfo, error) {
	return &mcpauth.UserInfo{}, nil
}
func (fakeMcpauthIDPClient) RefreshToken(_ context.Context, _ string) (*mcpauth.IDPTokens, error) {
	return &mcpauth.IDPTokens{}, nil
}

type fakeMcpauthMinter struct{}

func (fakeMcpauthMinter) MintForUser(_, _, _, _ string, _, _ []string, _ time.Duration) (string, error) {
	return "sam-token", nil
}
func (fakeMcpauthMinter) TTL() time.Duration { return time.Hour }

type fakeMcpauthResolver struct{}

func (fakeMcpauthResolver) Lookup(_ context.Context, _ string, _ map[string]any) (roles, scopes []string, platformUserID string, err error) {
	return nil, nil, "", nil
}

const browserOrigin = "http://127.0.0.1:3010"

const missingTokenChallenge = `Bearer realm="go-mcp-gw", resource_metadata="http://example.com/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp"`

func newOAuthHandler() *mcpauth.Handler {
	return mcpauth.New(mcpauth.Config{
		EnableAuth:     true,
		Issuer:         "https://mcp.example.com",
		Host:           "127.0.0.1",
		Port:           8090,
		RequirePKCE:    true,
		IDPClient:      fakeMcpauthIDPClient{},
		SAMTokenMinter: fakeMcpauthMinter{},
		AuthzResolver:  fakeMcpauthResolver{},
		Logger:         noopLogger(),
	})
}

type httpOutcome struct {
	Status          int
	AllowOrigin     string
	ExposeHeaders   string
	WWWAuthenticate string
	HasSessionID    bool
}

func serve(h http.Handler, req *http.Request) (httpOutcome, string) {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return httpOutcome{
		Status:          rr.Code,
		AllowOrigin:     rr.Header().Get("Access-Control-Allow-Origin"),
		ExposeHeaders:   rr.Header().Get("Access-Control-Expose-Headers"),
		WWWAuthenticate: rr.Header().Get(headerWWWAuthenticate),
		HasSessionID:    rr.Header().Get("Mcp-Session-Id") != "",
	}, rr.Body.String()
}

func TestBuildHTTPHandler(t *testing.T) {
	const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2024-11-05","capabilities":{},` +
		`"clientInfo":{"name":"cors-test","version":"1.0"}}}`
	preflightMCPServer := newTestMCPServer()
	initializeMCPServer := newTestMCPServer()

	tests := []struct {
		name        string
		cfg         *Config
		auth        *mcpauth.Handler
		mcpServer   *server.MCPServer
		httpServer  *server.StreamableHTTPServer
		method      string
		path        string
		headers     map[string]string
		body        string
		want        httpOutcome
		wantBodyHas string
	}{
		{
			name:        "OAuth metadata is mounted when auth is enabled",
			cfg:         &Config{Issuer: "https://mcp.example.com", Host: "127.0.0.1", Port: 8090, RequirePKCE: true},
			auth:        newOAuthHandler(),
			method:      "GET",
			path:        "/.well-known/oauth-authorization-server",
			want:        httpOutcome{Status: http.StatusOK},
			wantBodyHas: "https://mcp.example.com",
		},
		{
			name:       "CORS preflight is answered when auth is disabled",
			cfg:        mustParseConfig(t, map[string]any{"transport": "sse"}),
			mcpServer:  preflightMCPServer,
			httpServer: server.NewStreamableHTTPServer(preflightMCPServer),
			method:     "OPTIONS",
			path:       "/mcp",
			headers:    map[string]string{"Origin": browserOrigin, "Access-Control-Request-Method": "POST"},
			want:       httpOutcome{Status: http.StatusNoContent, AllowOrigin: browserOrigin},
		},
		{
			name: "initialize response exposes the session id header to browsers",
			cfg: mustParseConfig(t, map[string]any{
				"transport":            "sse",
				"cors_allowed_origins": []any{browserOrigin},
			}),
			mcpServer:  initializeMCPServer,
			httpServer: server.NewStreamableHTTPServer(initializeMCPServer),
			method:     "POST",
			path:       "/mcp",
			headers: map[string]string{
				"Origin":       browserOrigin,
				"Content-Type": "application/json",
				"Accept":       "application/json, text/event-stream",
			},
			body: initializeBody,
			want: httpOutcome{Status: http.StatusOK, AllowOrigin: browserOrigin, ExposeHeaders: "Mcp-Session-Id, Mcp-Protocol-Version", HasSessionID: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{cfg: tc.cfg, logger: noopLogger(), mcpauthHandler: tc.auth, mcpServer: tc.mcpServer, httpServer: tc.httpServer}
			h := a.buildHTTPHandler()
			if h == nil {
				t.Fatal("buildHTTPHandler returned nil")
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got, body := serve(h, req)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s %s through buildHTTPHandler mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
			if !strings.Contains(body, tc.wantBodyHas) {
				t.Errorf("%s %s body = %s, want it to contain %q", tc.method, tc.path, body, tc.wantBodyHas)
			}
		})
	}
}

func TestAdapter_PostMCP_OversizedBodyRejected(t *testing.T) {
	cfg := mustParseConfig(t, map[string]any{"transport": "sse"})
	a := &Adapter{cfg: cfg, logger: noopLogger()}
	a.mcpServer = newTestMCPServer()
	a.httpServer = newStreamableHTTPServer(a.mcpServer, cfg.streamableRequestBodyLimit())
	h := a.buildHTTPHandler()

	initializeWithPadding := func(padding int) string {
		return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"2024-11-05","capabilities":{},` +
			`"clientInfo":{"name":"size-test","version":"1.0"},"_pad":"` +
			strings.Repeat("x", padding) + `"}}`
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	if rr := post(initializeWithPadding(1 << 20)); rr.Code != http.StatusOK {
		t.Fatalf("1 MiB body status = %d (want 200); body = %s", rr.Code, rr.Body.String())
	}
	if rr := post(initializeWithPadding(maxRequestBodyBytes)); rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d (want 413)", rr.Code)
	}
}

func TestAdapter_PostMCP_FileInputBodyAllowedBeyondDefaultBound(t *testing.T) {
	cfg := mustParseConfig(t, map[string]any{"transport": "sse", "enable_file_input": true})
	if got := cfg.streamableRequestBodyLimit(); got != cfg.fileInputBodyLimit() {
		t.Fatalf("body limit = %d, want file input limit %d", got, cfg.fileInputBodyLimit())
	}
	a := &Adapter{cfg: cfg, logger: noopLogger()}
	a.mcpServer = newTestMCPServer()
	a.httpServer = newStreamableHTTPServer(a.mcpServer, cfg.streamableRequestBodyLimit())
	h := a.buildHTTPHandler()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2024-11-05","capabilities":{},` +
		`"clientInfo":{"name":"size-test","version":"1.0"},"_pad":"` +
		strings.Repeat("x", 2*maxRequestBodyBytes) + `"}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("8 MiB body with file input status = %d (want 200); body = %s", rr.Code, rr.Body.String())
	}
}

func TestBuildHTTPHandler_BearerValidation(t *testing.T) {
	cors := map[string]any{"transport": "sse", "cors_allowed_origins": []any{browserOrigin}}
	preflightMCPServer := newTestMCPServer()
	postMCPServer := newTestMCPServer()

	tests := []struct {
		name             string
		core             *entrypoint.GatewayCore
		mcpServer        *server.MCPServer
		httpServer       *server.StreamableHTTPServer
		method           string
		headers          map[string]string
		want             httpOutcome
		wantAuthRequired bool
	}{
		{
			name:       "CORS preflight is answered without consulting auth",
			mcpServer:  preflightMCPServer,
			httpServer: server.NewStreamableHTTPServer(preflightMCPServer),
			method:     "OPTIONS",
			headers: map[string]string{
				"Origin":                         browserOrigin,
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "authorization, content-type",
			},
			want: httpOutcome{Status: http.StatusNoContent, AllowOrigin: browserOrigin},
		},
		{
			name:       "POST without a bearer token is still refused",
			core:       newFakeGatewayCore(t, "go-mcp-gw"),
			mcpServer:  postMCPServer,
			httpServer: server.NewStreamableHTTPServer(postMCPServer),
			method:     "POST",
			want: httpOutcome{
				Status:          http.StatusUnauthorized,
				WWWAuthenticate: missingTokenChallenge,
			},
			wantAuthRequired: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validator := &stubValidator{acceptAny: true, subject: auth.NewTestSubject(auth.SubjectFields{})}
			a := &Adapter{
				core:           tc.core,
				cfg:            mustParseConfig(t, cors),
				logger:         noopLogger(),
				validator:      validator,
				mcpServer:      tc.mcpServer,
				httpServer:     tc.httpServer,
				mcpauthHandler: newOAuthHandler(),
			}
			req := httptest.NewRequest(tc.method, "/mcp", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got, body := serve(a.buildHTTPHandler(), req)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s /mcp through buildHTTPHandler mismatch (-want +got):\n%s", tc.method, diff)
			}
			if gotAuthRequired := strings.Contains(body, "authentication_required"); gotAuthRequired != tc.wantAuthRequired {
				t.Errorf("%s /mcp body = %s, want authentication_required in it = %v", tc.method, body, tc.wantAuthRequired)
			}
			if len(validator.tokens) != 0 {
				t.Errorf("%s /mcp consulted the validator with %q, want no call", tc.method, validator.tokens)
			}
		})
	}
}

func TestMetadataHandler(t *testing.T) {
	cors := map[string]any{"transport": "sse", "cors_allowed_origins": []any{browserOrigin}}

	tests := []struct {
		name    string
		cfg     *Config
		logger  *slog.Logger
		auth    *mcpauth.Handler
		method  string
		path    string
		headers map[string]string
		want    httpOutcome
	}{
		{
			name:    "cross-origin GET reflects allow-origin",
			cfg:     mustParseConfig(t, cors),
			logger:  noopLogger(),
			auth:    newOAuthHandler(),
			method:  "GET",
			path:    "/.well-known/oauth-protected-resource",
			headers: map[string]string{"Origin": browserOrigin},
			want:    httpOutcome{Status: http.StatusOK, AllowOrigin: browserOrigin, ExposeHeaders: "Mcp-Session-Id, Mcp-Protocol-Version"},
		},
		{
			name:    "preflight short-circuits",
			cfg:     mustParseConfig(t, cors),
			logger:  noopLogger(),
			auth:    newOAuthHandler(),
			method:  "OPTIONS",
			path:    "/.well-known/oauth-protected-resource",
			headers: map[string]string{"Origin": browserOrigin, "Access-Control-Request-Method": "GET"},
			want:    httpOutcome{Status: http.StatusNoContent, AllowOrigin: browserOrigin},
		},
		{
			name:   "protected resource metadata is served",
			auth:   mcpauth.New(mcpauth.Config{Issuer: "http://localhost:8000"}),
			method: "GET",
			path:   mcpauth.PathProtectedResource,
			want:   httpOutcome{Status: http.StatusOK},
		},
		{
			name:   "authorization server metadata is served",
			auth:   mcpauth.New(mcpauth.Config{Issuer: "http://localhost:8000"}),
			method: "GET",
			path:   mcpauth.PathWellKnown,
			want:   httpOutcome{Status: http.StatusOK},
		},
		{
			name:   "no auth handler answers not found",
			method: "GET",
			path:   mcpauth.PathProtectedResource,
			want:   httpOutcome{Status: http.StatusNotFound},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{cfg: tc.cfg, logger: tc.logger, mcpauthHandler: tc.auth}
			h := a.MetadataHandler()
			if h == nil {
				t.Fatal("MetadataHandler returned nil")
			}
			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got, _ := serve(h, req)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s %s through MetadataHandler mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
		})
	}
}

type stubValidator struct {
	accept    string
	acceptAny bool
	subject   *auth.Subject
	tokens    []string
}

func (v *stubValidator) Validate(_ context.Context, token string) (*auth.Subject, error) {
	v.tokens = append(v.tokens, token)
	if v.acceptAny || token == v.accept {
		return v.subject, nil
	}
	return nil, errors.New("token rejected")
}

type authOutcome struct {
	Status          int
	WWWAuthenticate string
	ErrorType       string
	Message         string
	Reached         bool
	Planted         []string
	Validated       []string
}

func runAuthMiddleware(a *Adapter, validator *stubValidator, req *http.Request) authOutcome {
	a.validator = validator
	var reached bool
	var planted []string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached = true
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			planted = append(planted, id.UserID())
		}
	})
	rr := httptest.NewRecorder()

	a.authMiddleware(next).ServeHTTP(rr, req)

	var errBody struct {
		Message   string `json:"message"`
		ErrorType string `json:"errorType"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &errBody)
	return authOutcome{
		Status:          rr.Code,
		WWWAuthenticate: rr.Header().Get(headerWWWAuthenticate),
		ErrorType:       errBody.ErrorType,
		Message:         errBody.Message,
		Reached:         reached,
		Planted:         planted,
		Validated:       validator.tokens,
	}
}

func TestAuthMiddleware_Challenges(t *testing.T) {
	const metadataBase = `resource_metadata="http://localhost:8000/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp"`

	tests := []struct {
		name       string
		cfg        *Config
		method     string
		target     string
		authHeader string
		want       authOutcome
	}{
		{
			name:   "OPTIONS preflight passes through without credentials",
			method: "OPTIONS",
			target: "/mcp",
			want:   authOutcome{Status: http.StatusOK, Reached: true},
		},
		{
			name:   "missing Authorization header is refused with a resource metadata challenge",
			method: "GET",
			target: "http://localhost:8000/mcp",
			want: authOutcome{
				Status:          http.StatusUnauthorized,
				WWWAuthenticate: `Bearer realm="go-mcp-gw", ` + metadataBase,
				ErrorType:       "authentication_required",
				Message:         "Missing Bearer token",
			},
		},
		{
			name:       "malformed scheme is refused as an invalid request",
			method:     "GET",
			target:     "http://localhost:8000/mcp",
			authHeader: "Token abc",
			want: authOutcome{
				Status:          http.StatusUnauthorized,
				WWWAuthenticate: `Bearer realm="go-mcp-gw", error="invalid_request", ` + metadataBase,
				ErrorType:       "invalid_token",
				Message:         `Authorization header uses unsupported scheme "Token" (expected Bearer)`,
			},
		},
		{
			name:       "rejected token is refused as invalid",
			method:     "GET",
			target:     "http://localhost:8000/mcp",
			authHeader: "Bearer bad",
			want: authOutcome{
				Status:          http.StatusUnauthorized,
				WWWAuthenticate: `Bearer realm="go-mcp-gw", error="invalid_token", ` + metadataBase,
				ErrorType:       "invalid_token",
				Message:         "Invalid or expired token",
				Validated:       []string{"bad"},
			},
		},
		{
			name:   "dev mode passes a request without credentials through",
			cfg:    &Config{DevMode: true},
			method: "POST",
			target: "/mcp",
			want:   authOutcome{Status: http.StatusOK, Reached: true, Planted: []string{"system:default"}},
		},
		{
			name:   "dev mode off still refuses a request without credentials",
			cfg:    &Config{DevMode: false},
			method: "POST",
			target: "/mcp",
			want: authOutcome{
				Status:          http.StatusUnauthorized,
				WWWAuthenticate: missingTokenChallenge,
				ErrorType:       "authentication_required",
				Message:         "Missing Bearer token",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{core: newFakeGatewayCore(t, "go-mcp-gw"), cfg: tc.cfg, logger: noopLogger()}
			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("Authorization", tc.authHeader)

			got := runAuthMiddleware(a, &stubValidator{}, req)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("authMiddleware %s %s (Authorization %q) mismatch (-want +got):\n%s", tc.method, tc.target, tc.authHeader, diff)
			}
		})
	}
}

func TestAuthMiddleware_Bearer(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *Config
		authHeader string
		validToken string
		acceptAny  bool
		subject    *auth.Subject
		want       authOutcome
	}{
		{
			name:      "missing Authorization header is refused",
			acceptAny: true,
			subject:   auth.NewTestSubject(auth.SubjectFields{}),
			want: authOutcome{
				Status:    http.StatusUnauthorized,
				ErrorType: "authentication_required",
				Message:   "Missing Bearer token",
			},
		},
		{
			name:       "rejected bearer token is refused",
			authHeader: "Bearer bad-token",
			want: authOutcome{
				Status:    http.StatusUnauthorized,
				ErrorType: "invalid_token",
				Message:   "Invalid or expired token",
				Validated: []string{"bad-token"},
			},
		},
		{
			name:       "accepted bearer token plants the caller's identity",
			authHeader: "Bearer good-token",
			validToken: "good-token",
			subject:    auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "u@example.com"}),
			want:       authOutcome{Status: http.StatusOK, Reached: true, Planted: []string{"u-1"}, Validated: []string{"good-token"}},
		},
		{
			name:       "accepted bearer token wins over run_as",
			cfg:        &Config{EnableAuth: true, RunAs: "orders-bot"},
			authHeader: "Bearer good-token",
			validToken: "good-token",
			subject:    auth.NewTestSubject(auth.SubjectFields{UserID: "u-1", Email: "u@example.com"}),
			want:       authOutcome{Status: http.StatusOK, Reached: true, Planted: []string{"u-1"}, Validated: []string{"good-token"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{cfg: tc.cfg, logger: noopLogger()}
			req := httptest.NewRequest("POST", "/mcp", nil)
			req.Header.Set("Authorization", tc.authHeader)

			got := runAuthMiddleware(a, &stubValidator{accept: tc.validToken, acceptAny: tc.acceptAny, subject: tc.subject}, req)

			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(authOutcome{}, "WWWAuthenticate")); diff != "" {
				t.Errorf("authMiddleware POST /mcp (Authorization %q) mismatch (-want +got):\n%s", tc.authHeader, diff)
			}
		})
	}
}

const mcpTestKey = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

func TestAdapter_authMiddleware_RefusesAnAPIKeyThroughTheRealValidator(t *testing.T) {
	km, err := trust.NewKeyManager("mcp-test-gw")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	authz, err := oidcauth.NewAuthorizationService(oidcauth.AuthorizationServiceConfig{Type: "none"}, "mcp-test-gw", noopLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	validator := oidcauth.NewTokenValidator(trust.NewSigner(km, "mcp-test-gw", time.Hour, 2*time.Hour), authz, nil, "mcp-test-gw", noopLogger())
	a := &Adapter{core: newFakeGatewayCore(t, "mcp-test-gw"), logger: noopLogger(), validator: validator}

	type outcome struct {
		Status    int
		Reached   bool
		EchoesKey bool
	}
	tests := []struct {
		name   string
		header string
	}{
		{"plain key is refused", "Bearer " + mcpTestKey},
		{"key after two spaces is refused", "Bearer  " + mcpTestKey},
		{"key after a tab is refused", "Bearer \t" + mcpTestKey},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", tc.header)
			rr := httptest.NewRecorder()

			a.authMiddleware(next).ServeHTTP(rr, req)

			got := outcome{Status: rr.Code, Reached: reached, EchoesKey: strings.Contains(rr.Body.String(), mcpTestKey)}
			if diff := cmp.Diff(outcome{Status: http.StatusUnauthorized}, got); diff != "" {
				t.Errorf("authMiddleware POST /mcp (Authorization %q) mismatch (-want +got):\n%s", tc.header, diff)
			}
		})
	}
}

func TestAuthMiddleware_NoValidatorPassesThrough(t *testing.T) {
	a := &Adapter{logger: noopLogger()}
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/mcp", nil)
	rr := httptest.NewRecorder()
	a.authMiddleware(next).ServeHTTP(rr, req)
	if !reached {
		t.Error("next handler not reached when validator is nil")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d (want 200)", rr.Code)
	}
}

func newFakeGatewayCore(t *testing.T, gatewayID string) *entrypoint.GatewayCore {
	t.Helper()
	return entrypoint.NewGatewayCore(nil, map[string]any{"gateway_id": gatewayID})
}

func TestResourceMetadataURL(t *testing.T) {
	const issuerAnchoredMetadataURL = "https://gateway.example.com/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp"

	tests := []struct {
		name    string
		core    *entrypoint.GatewayCore
		cfg     *Config
		target  string
		host    string
		headers map[string]string
		want    string
	}{
		{
			name:   "bare data path leaves no trailing slash",
			core:   newFakeGatewayCore(t, "go-mcp-gw"),
			target: "http://localhost:8000/",
			host:   "localhost:8000",
			want:   "http://localhost:8000/.well-known/oauth-protected-resource/gw/go-mcp-gw",
		},
		{
			name:    "X-Original-Path set by the proxy is used as the mount path",
			core:    newFakeGatewayCore(t, "go-mcp-gw"),
			target:  "http://localhost:8000/mcp",
			host:    "localhost:8000",
			headers: map[string]string{"X-Original-Path": "/gw/custom-mount/mcp"},
			want:    "http://localhost:8000/.well-known/oauth-protected-resource/gw/custom-mount/mcp",
		},
		{
			name:   "without X-Original-Path the mount path is rebuilt from the gateway id",
			core:   newFakeGatewayCore(t, "go-mcp-gw"),
			target: "http://localhost:8000/mcp",
			host:   "localhost:8000",
			want:   "http://localhost:8000/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp",
		},
		{
			name:    "X-Forwarded-Proto https advertises https",
			core:    newFakeGatewayCore(t, "go-mcp-gw"),
			target:  "http://localhost:8000/mcp",
			host:    "localhost:8000",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "https://localhost:8000/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp",
		},
		{
			name:    "configured issuer wins over a spoofed Host and scheme hint",
			core:    newFakeGatewayCore(t, "go-mcp-gw"),
			cfg:     &Config{Issuer: "https://gateway.example.com"},
			target:  "http://localhost:8000/mcp",
			host:    "evil.example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    issuerAnchoredMetadataURL,
		},
		{
			name:   "trailing slash on the issuer adds no double slash",
			core:   newFakeGatewayCore(t, "go-mcp-gw"),
			cfg:    &Config{Issuer: "https://gateway.example.com/"},
			target: "http://localhost:8000/mcp",
			host:   "localhost:8000",
			want:   issuerAnchoredMetadataURL,
		},
		{
			name:   "gateway path in the issuer is not duplicated and the well-known URL sits at the authority root",
			core:   newFakeGatewayCore(t, "mcp-gw-1"),
			cfg:    &Config{Issuer: "http://localhost:8000/gw/mcp-gw-1"},
			target: "http://localhost:8000/mcp",
			host:   "localhost:8000",
			want:   "http://localhost:8000/.well-known/oauth-protected-resource/gw/mcp-gw-1/mcp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{core: tc.core, logger: noopLogger(), cfg: tc.cfg}
			req := httptest.NewRequest("GET", tc.target, nil)
			req.Host = tc.host
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got := a.resourceMetadataURL(req)

			if got != tc.want {
				t.Errorf("resourceMetadataURL(GET %s, Host %q, headers %v) = %q, want %q", tc.target, tc.host, tc.headers, got, tc.want)
			}
		})
	}
}

func TestAdapter_MetadataHandler_LazyResolutionAfterInit(t *testing.T) {
	a := &Adapter{}
	h := a.MetadataHandler()
	if h == nil {
		t.Fatal("MetadataHandler returned nil before Init; want non-nil lazy handler")
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", mcpauth.PathProtectedResource, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("pre-Init status = %d; want 404", rr.Code)
	}

	a.mcpauthHandler = mcpauth.New(mcpauth.Config{Issuer: "http://localhost:8000"})

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", mcpauth.PathProtectedResource, nil))
	if rr.Code != http.StatusOK {
		t.Errorf("post-Init PR metadata status = %d; want 200", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", mcpauth.PathWellKnown, nil))
	if rr.Code != http.StatusOK {
		t.Errorf("post-Init AS metadata status = %d; want 200", rr.Code)
	}
}

func TestMCPSessionToUUID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"mcp-go format strips the prefix", "mcp-session-463e19da-6656-4576-80df-025535d2685f", "463e19da-6656-4576-80df-025535d2685f"},
		{"non-prefixed UUID passes through", "463e19da-6656-4576-80df-025535d2685f", "463e19da-6656-4576-80df-025535d2685f"},
		{"non-UUID falls back to a deterministic v5", "garbage-not-a-uuid", "5423d343-a7ec-5115-8eaa-81f146b066b5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpSessionToUUID(tc.in)

			if got != tc.want {
				t.Errorf("mcpSessionToUUID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToolCallCaller(t *testing.T) {
	t.Run("staged caller wins", func(t *testing.T) {
		staged := auth.NewTestSubject(auth.SubjectFields{UserID: "alice@example.com"})
		got := toolCallCaller(auth.ContextWithSubject(t.Context(), staged))
		if got != staged {
			t.Errorf("got %+v, want the staged caller itself", got)
		}
	})

	t.Run("no identity in the context is the sentinel without an identity", func(t *testing.T) {
		got := toolCallCaller(t.Context())
		if got.UserID() != "mcp_user" {
			t.Errorf("toolCallCaller(ctx without identity).UserID() = %q, want %q", got.UserID(), "mcp_user")
		}
		if got.FullIdentity() {
			t.Error("FullIdentity() = true; a call with no identity must not be authorized or signed as one")
		}
	})
}

func TestAuthMiddleware_NoAuth_PlantsDefaultIdentity(t *testing.T) {
	cases := []struct {
		name                string
		defaultUserIdentity string
		wantUserID          string
	}{
		{name: "configured default_user_identity", defaultUserIdentity: "grace.miller@example.org", wantUserID: "grace.miller@example.org"},
		{name: "empty falls back to the default system user", defaultUserIdentity: "", wantUserID: rbac.DefaultSystemUserSub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_DEV_USER_ID", "")
			a := &Adapter{
				cfg:    &Config{EnableAuth: false, DefaultUserIdentity: tc.defaultUserIdentity},
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}

			var got *auth.Subject
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				id, ok := auth.SubjectFromContext(r.Context())
				if ok {
					got = id
				}
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			a.authMiddleware(next).ServeHTTP(httptest.NewRecorder(), req)

			if got == nil {
				t.Fatal("no identity planted on context in no-auth mode")
			}
			if got.UserID() != tc.wantUserID {
				t.Errorf("planted UserID = %q, want %q", got.UserID(), tc.wantUserID)
			}
		})
	}
}

func TestAssembleHTTPHandler_NoAuth_PlantsIdentity(t *testing.T) {
	var got *auth.Subject
	capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			got = id
		}
	})
	a := &Adapter{
		cfg:    &Config{EnableAuth: false, DefaultUserIdentity: "grace.miller@example.org", Transport: "sse"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	h := a.assembleHTTPHandler(capture)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatal("no-auth handler did not plant an identity on the inner handler's context")
	}
	if got.UserID() != "grace.miller@example.org" {
		t.Errorf("planted UserID = %q, want grace.miller@example.org", got.UserID())
	}
}

func TestAssembleHTTPHandler_RootMount(t *testing.T) {
	type outcome struct {
		Reached string
		Status  int
	}
	tests := []struct {
		name   string
		method string
		path   string
		want   outcome
	}{
		{"POST to the bare root reaches the MCP handler", http.MethodPost, "/", outcome{Reached: "/", Status: http.StatusNoContent}},
		{"POST to a sub-path reaches the MCP handler", http.MethodPost, "/anything", outcome{Reached: "/anything", Status: http.StatusNoContent}},
		{"OAuth metadata is served, not swallowed by the catch-all", http.MethodGet, "/.well-known/oauth-authorization-server", outcome{Status: http.StatusOK}},
		{"client registration is served, not swallowed by the catch-all", http.MethodPost, mcpauth.PathRegister, outcome{Status: http.StatusCreated}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_DEV_USER_ID", "")
			var reached string
			capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			})
			a := &Adapter{
				cfg: &Config{
					EnableAuth:          true,
					DevMode:             true,
					DefaultUserIdentity: "dev@local",
					Issuer:              "https://mcp.example.com",
					Host:                "127.0.0.1",
					Port:                8090,
					RequirePKCE:         true,
				},
				logger: noopLogger(),
			}
			a.mcpauthHandler = newOAuthHandler()

			rr := httptest.NewRecorder()
			a.assembleHTTPHandler(capture).ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))

			got := outcome{Reached: reached, Status: rr.Code}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s %s through assembleHTTPHandler mismatch (-want +got):\n%s", tc.method, tc.path, diff)
			}
		})
	}
}

func TestAuthMiddleware_DevMode_BypassesValidatorAndPlantsIdentity(t *testing.T) {
	t.Setenv("SAM_DEV_USER_ID", "")
	a := &Adapter{
		cfg:    &Config{EnableAuth: true, DevMode: true, DefaultUserIdentity: "dev@local"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		validator: auth.TokenValidatorFunc(func(context.Context, string) (*auth.Subject, error) {
			t.Error("validator must not be called in dev_mode")
			return nil, errors.New("validator should be bypassed in dev_mode")
		}),
	}

	var got *auth.Subject
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			got = id
		}
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer should-be-ignored")
	a.authMiddleware(next).ServeHTTP(httptest.NewRecorder(), req)

	if got == nil || got.UserID() != "dev@local" {
		t.Fatalf("dev_mode planted identity = %v, want UserID dev@local", got)
	}
}

func newCoreResolvingScopes(t *testing.T, scopes []string) *entrypoint.GatewayCore {
	t.Helper()
	core := newFakeGatewayCore(t, "go-mcp-gw")
	core.SetScopeResolver(stubScopeResolver{scopes: scopes})
	return core
}

func newCoreEnforcingWithoutResolver(t *testing.T) *entrypoint.GatewayCore {
	t.Helper()
	core := newFakeGatewayCore(t, "go-mcp-gw")
	core.SetAuthzEnabled(true)
	return core
}

func TestDefaultIdentity(t *testing.T) {
	type identity struct {
		UserID          string
		Scopes          []string
		SystemPrincipal bool
		NonDelegatable  bool
	}
	tests := []struct {
		name string
		core *entrypoint.GatewayCore
		cfg  *Config
		want identity
	}{
		{
			name: "run_as names a system user",
			cfg:  &Config{EnableAuth: false, RunAs: "orders-bot"},
			want: identity{UserID: "system:orders-bot", Scopes: []string{"*"}, SystemPrincipal: true, NonDelegatable: true},
		},
		{
			name: "default_user_identity wins over run_as",
			cfg:  &Config{EnableAuth: false, RunAs: "orders-bot", DefaultUserIdentity: "dev@local"},
			want: identity{UserID: "dev@local", Scopes: []string{"*"}},
		},
		{
			name: "neither setting resolves to the default system user",
			cfg:  &Config{EnableAuth: false},
			want: identity{UserID: "system:default", Scopes: []string{"*"}, SystemPrincipal: true, NonDelegatable: true},
		},
		{
			name: "run_as resolves scopes through the resolver",
			core: newCoreResolvingScopes(t, []string{"agent:Orchestrator:invoke"}),
			cfg:  &Config{EnableAuth: false, RunAs: "orders-bot"},
			want: identity{UserID: "system:orders-bot", Scopes: []string{"agent:Orchestrator:invoke"}, SystemPrincipal: true, NonDelegatable: true},
		},
		{
			name: "resolver wired resolves scopes",
			core: newCoreResolvingScopes(t, []string{"agent:Orchestrator:invoke"}),
			cfg:  &Config{EnableAuth: false, DefaultUserIdentity: "dev@local"},
			want: identity{UserID: "dev@local", Scopes: []string{"agent:Orchestrator:invoke"}},
		},
		{
			name: "no resolver keeps the DevIdentity wildcard",
			cfg:  &Config{EnableAuth: false, DefaultUserIdentity: "dev@local"},
			want: identity{UserID: "dev@local", Scopes: []string{"*"}},
		},
		{
			name: "enforced without resolver fails closed",
			core: newCoreEnforcingWithoutResolver(t),
			cfg:  &Config{EnableAuth: false, DefaultUserIdentity: "dev@local"},
			want: identity{UserID: "dev@local", Scopes: []string{}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_DEV_USER_ID", "")
			a := &Adapter{core: tc.core, cfg: tc.cfg, logger: noopLogger()}

			id := a.defaultIdentity()

			got := identity{
				UserID:          id.UserID(),
				Scopes:          id.Scopes(),
				SystemPrincipal: rbac.IsSystemPrincipal(id.UserID()),
				NonDelegatable:  rbac.IsNonDelegatablePrincipal(id.UserID()),
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("defaultIdentity() with config %+v mismatch (-want +got):\n%s", tc.cfg, diff)
			}
		})
	}
}

type systemUserOnlyResolver struct{}

func (systemUserOnlyResolver) ResolveScopes(userID string, _ []string) []string {
	if userID == rbac.DefaultSystemUserSub {
		return []string{rbac.ScopeAgentInvokeAny}
	}
	return nil
}

func TestUnauthenticatedCaller_ReachesAgentsUnderEnforcedRBAC(t *testing.T) {
	newEnforcedAdapter := func(t *testing.T, transport string) *Adapter {
		t.Helper()
		t.Setenv("SAM_DEV_USER_ID", "")
		core := newFakeGatewayCore(t, "go-mcp-gw")
		core.SetAuthzEnabled(true)
		core.SetScopeResolver(systemUserOnlyResolver{})
		return &Adapter{
			core:   core,
			cfg:    &Config{EnableAuth: false, Transport: transport},
			logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
	}

	t.Run("http through authMiddleware", func(t *testing.T) {
		a := newEnforcedAdapter(t, "sse")

		var allowed bool
		handler := a.authMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			allowed = a.core.CanDelegate(r.Context(), contextCaller(r.Context()), "agent_a")
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "http://localhost:8000/mcp", nil))

		if !allowed {
			t.Error("an unauthenticated HTTP caller must reach agent_a as the default system user")
		}
	})

	t.Run("stdio through plantDefaultIdentity", func(t *testing.T) {
		a := newEnforcedAdapter(t, "stdio")

		var allowed bool
		handler := a.plantDefaultIdentity(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			allowed = a.core.CanDelegate(ctx, contextCaller(ctx), "agent_a")
			return nil, nil
		})
		if _, err := handler(context.Background(), mcp.CallToolRequest{}); err != nil {
			t.Fatalf("tool handler: %v", err)
		}

		if !allowed {
			t.Error("an unauthenticated stdio caller must reach agent_a as the default system user")
		}
	})
}

func TestAdapter_AuthDeps_RoundTrip(t *testing.T) {
	a, err := NewAdapter(map[string]any{"namespace": "test", "gateway_id": "g"})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if a.AuthDeps() != nil {
		t.Fatal("AuthDeps must be nil before SetAuthDeps")
	}
	deps := &MCPAuthDeps{}
	a.SetAuthDeps(deps)
	if a.AuthDeps() != deps {
		t.Fatal("AuthDeps must return the value passed to SetAuthDeps")
	}
}

func warnLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestAdapter_WarnIfAuthnOnly(t *testing.T) {
	tests := []struct {
		name      string
		core      *entrypoint.GatewayCore
		cfg       *Config
		wantWarn  bool
		wantInLog []string
	}{
		{
			name:      "authentication-only HTTP gateway warns",
			core:      newFakeGatewayCore(t, "go-mcp-gw"),
			cfg:       &Config{EnableAuth: true, Transport: "sse"},
			wantWarn:  true,
			wantInLog: []string{"authentication-only mode", "go-mcp-gw"},
		},
		{
			name: "enforced authz without a resolver is not authentication-only",
			core: newCoreEnforcingWithoutResolver(t),
			cfg:  &Config{EnableAuth: true, Transport: "sse"},
		},
		{
			name: "auth disabled does not warn",
			core: newFakeGatewayCore(t, "go-mcp-gw"),
			cfg:  &Config{EnableAuth: false, Transport: "sse"},
		},
		{
			name: "stdio does not warn",
			core: newFakeGatewayCore(t, "go-mcp-gw"),
			cfg:  &Config{EnableAuth: true, Transport: "stdio"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			a := &Adapter{core: tc.core, cfg: tc.cfg, logger: warnLogger(&buf)}

			a.warnIfAuthnOnly()

			if warned := buf.Len() != 0; warned != tc.wantWarn {
				t.Errorf("warnIfAuthnOnly() with config %+v warned = %v, want %v; output: %q", tc.cfg, warned, tc.wantWarn, buf.String())
			}
			for _, want := range tc.wantInLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("warnIfAuthnOnly() with config %+v log = %q, want it to contain %q", tc.cfg, buf.String(), want)
				}
			}
		})
	}
}

func TestAdapter_WarnIfDevMode(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantWarn  bool
		wantInLog []string
	}{
		{
			name:      "dev mode on HTTP warns",
			cfg:       &Config{EnableAuth: true, DevMode: true, Transport: "sse"},
			wantWarn:  true,
			wantInLog: []string{"DEV MODE", "Authentication is bypassed", "go-mcp-gw"},
		},
		{
			name: "dev mode off does not warn",
			cfg:  &Config{EnableAuth: true, DevMode: false, Transport: "sse"},
		},
		{
			name: "stdio does not warn",
			cfg:  &Config{EnableAuth: true, DevMode: true, Transport: "stdio"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			a := &Adapter{core: newFakeGatewayCore(t, "go-mcp-gw"), cfg: tc.cfg, logger: warnLogger(&buf)}

			a.warnIfDevMode()

			if warned := buf.Len() != 0; warned != tc.wantWarn {
				t.Errorf("warnIfDevMode() with config %+v warned = %v, want %v; output: %q", tc.cfg, warned, tc.wantWarn, buf.String())
			}
			for _, want := range tc.wantInLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("warnIfDevMode() with config %+v log = %q, want it to contain %q", tc.cfg, buf.String(), want)
				}
			}
		})
	}
}

func TestAdapter_WarnIfPermissiveSystemUser(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		enforced  bool
		wantWarn  bool
		wantInLog []string
	}{
		{"auth off, neither field, enforced", &Config{Transport: "sse"}, true, true, []string{"go-mcp-gw"}},
		{"stdio ignores enable_auth", &Config{EnableAuth: true, Transport: "stdio"}, true, true, []string{"go-mcp-gw"}},
		{"named run_as is a deliberate choice", &Config{Transport: "sse", RunAs: "orders-bot"}, true, false, nil},
		{"default_user_identity set", &Config{Transport: "sse", DefaultUserIdentity: "dev@local"}, true, false, nil},
		{"not enforced keeps the dev wildcard", &Config{Transport: "sse"}, false, false, nil},
		{"bearer identity can arrive", &Config{EnableAuth: true, Transport: "sse"}, true, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			core := newFakeGatewayCore(t, "go-mcp-gw")
			core.SetAuthzEnabled(tc.enforced)
			a := &Adapter{core: core, cfg: tc.cfg, logger: warnLogger(&buf)}

			a.warnIfPermissiveSystemUser()

			if warned := buf.Len() != 0; warned != tc.wantWarn {
				t.Errorf("warnIfPermissiveSystemUser() with config %+v warned = %v, want %v; output: %q", tc.cfg, warned, tc.wantWarn, buf.String())
			}
			for _, want := range tc.wantInLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("warnIfPermissiveSystemUser() with config %+v log = %q, want it to contain %q", tc.cfg, buf.String(), want)
				}
			}
		})
	}
}

func TestCallTool_RegisterFailureRefusesAndDropsTheTask(t *testing.T) {
	type refusal struct {
		Result       *mcp.CallToolResult
		Reason       string
		RecordFailed bool
		Tracked      int
		Removed      int
	}
	registerErr := errors.New("database unreachable")
	registry := &mockTaskRegistry{registerErr: registerErr}
	a := &Adapter{
		cfg:               mustParseConfig(t, nil),
		toolMap:           map[string]toolMapping{"test_agent_greet": {agentName: "test-agent"}},
		agentTools:        make(map[string][]string),
		tasks:             make(map[string]*taskState),
		logger:            noopLogger(),
		coreTasksOverride: registry,
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"message": "hello"}

	result, reason, err := a.callTool(context.Background(), "test_agent_greet", req)

	got := refusal{Result: result, Reason: reason, RecordFailed: errors.Is(err, registerErr), Tracked: len(a.tasks), Removed: len(registry.removed)}
	want := refusal{Reason: "internal", RecordFailed: true, Removed: 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("callTool(test_agent_greet) with a failing task registry mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractBearer_DropsBlanksAroundTheCredential(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"single space before the credential", "Bearer abc", "abc"},
		{"two spaces before the credential", "Bearer  abc", "abc"},
		{"tab before the credential", "Bearer \tabc", "abc"},
		{"blanks after the credential", "Bearer abc \t", "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", tc.header)

			got, err := extractBearer(req)

			if err != nil {
				t.Errorf("extractBearer(%q) error: %v", tc.header, err)
			}
			if got != tc.want {
				t.Errorf("extractBearer(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func contextCaller(ctx context.Context) *auth.Subject {
	id, _ := auth.SubjectFromContext(ctx)
	return id
}
