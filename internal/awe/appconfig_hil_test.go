// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestParseApprovalRules_EachOperator(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		yaml []any
		want []agent.ApprovalRule
	}{
		{
			name: "eq",
			yaml: []any{map[string]any{"arg": "method", "eq": "POST"}},
			want: []agent.ApprovalRule{{Arg: "method", Op: agent.OpEq, Value: "POST"}},
		},
		{
			name: "in",
			yaml: []any{map[string]any{"arg": "method", "in": []any{"POST", "PUT"}}},
			want: []agent.ApprovalRule{{Arg: "method", Op: agent.OpIn, Value: []any{"POST", "PUT"}}},
		},
		{
			name: "not_in",
			yaml: []any{map[string]any{"arg": "method", "not_in": []any{"GET"}}},
			want: []agent.ApprovalRule{{Arg: "method", Op: agent.OpNotIn, Value: []any{"GET"}}},
		},
		{
			name: "exists",
			yaml: []any{map[string]any{"arg": "token", "exists": true}},
			want: []agent.ApprovalRule{{Arg: "token", Op: agent.OpExists, Value: true}},
		},
		{
			name: "not_exists",
			yaml: []any{map[string]any{"arg": "confirmation_token", "not_exists": true}},
			want: []agent.ApprovalRule{{Arg: "confirmation_token", Op: agent.OpNotExists, Value: true}},
		},
		{
			name: "gt int",
			yaml: []any{map[string]any{"arg": "amount", "gt": 100}},
			want: []agent.ApprovalRule{{Arg: "amount", Op: agent.OpGt, Value: 100}},
		},
		{
			name: "gt float",
			yaml: []any{map[string]any{"arg": "amount", "gt": 99.5}},
			want: []agent.ApprovalRule{{Arg: "amount", Op: agent.OpGt, Value: 99.5}},
		},
		{
			name: "lt",
			yaml: []any{map[string]any{"arg": "n", "lt": 10}},
			want: []agent.ApprovalRule{{Arg: "n", Op: agent.OpLt, Value: 10}},
		},
		{
			name: "gte",
			yaml: []any{map[string]any{"arg": "n", "gte": 5}},
			want: []agent.ApprovalRule{{Arg: "n", Op: agent.OpGte, Value: 5}},
		},
		{
			name: "lte",
			yaml: []any{map[string]any{"arg": "n", "lte": 5}},
			want: []agent.ApprovalRule{{Arg: "n", Op: agent.OpLte, Value: 5}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseApprovalRules(tc.yaml, "tool")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d rules, want %d", len(got), len(tc.want))
			}
			for i, r := range got {
				if r.Arg != tc.want[i].Arg || r.Op != tc.want[i].Op {
					t.Errorf("rule[%d] = %+v, want %+v", i, r, tc.want[i])
				}
			}
		})
	}
}

func TestParseApprovalRules_DottedPath(t *testing.T) {
	t.Parallel()
	rules, err := parseApprovalRules([]any{
		map[string]any{"arg": "body.priority", "eq": "critical"},
	}, "tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rules[0].Arg != "body.priority" {
		t.Errorf("Arg = %q, want body.priority", rules[0].Arg)
	}
}

func TestParseApprovalRules_EmptyList(t *testing.T) {
	t.Parallel()
	rules, err := parseApprovalRules([]any{}, "tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected zero rules, got %d", len(rules))
	}
}

func TestParseApprovalRules_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		yaml        any
		wantSubstr  string
		wantToolCtx bool
	}{
		{
			name:       "not a list",
			yaml:       "not a list",
			wantSubstr: "must be a list",
		},
		{
			name:       "rule not a map",
			yaml:       []any{"not a map"},
			wantSubstr: "must be a map",
		},
		{
			name:       "missing arg",
			yaml:       []any{map[string]any{"eq": "x"}},
			wantSubstr: "missing required field 'arg'",
		},
		{
			name:       "arg not string",
			yaml:       []any{map[string]any{"arg": 5, "eq": "x"}},
			wantSubstr: "must be a non-empty string",
		},
		{
			name:       "arg empty string",
			yaml:       []any{map[string]any{"arg": "", "eq": "x"}},
			wantSubstr: "must be a non-empty string",
		},
		{
			name:       "missing operator",
			yaml:       []any{map[string]any{"arg": "method"}},
			wantSubstr: "missing operator",
		},
		{
			name:       "unknown operator",
			yaml:       []any{map[string]any{"arg": "method", "matches": "POST"}},
			wantSubstr: "unknown operator",
		},
		{
			name:       "multiple operators on one rule",
			yaml:       []any{map[string]any{"arg": "method", "eq": "POST", "in": []any{"GET"}}},
			wantSubstr: "multiple operators",
		},
		{
			name:       "in value not a list",
			yaml:       []any{map[string]any{"arg": "method", "in": "POST"}},
			wantSubstr: "value must be a list",
		},
		{
			name:       "in value empty list",
			yaml:       []any{map[string]any{"arg": "method", "in": []any{}}},
			wantSubstr: "list must be non-empty",
		},
		{
			name:       "in list contains a map",
			yaml:       []any{map[string]any{"arg": "method", "in": []any{"POST", map[string]any{"x": 1}}}},
			wantSubstr: "must be a string, number, or bool",
		},
		{
			name:       "gt value not numeric",
			yaml:       []any{map[string]any{"arg": "amount", "gt": "lots"}},
			wantSubstr: "value must be a number",
		},
		{
			name:       "eq value is a map",
			yaml:       []any{map[string]any{"arg": "x", "eq": map[string]any{"y": 1}}},
			wantSubstr: "value must be a string, number, or bool",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseApprovalRules(tc.yaml, "my_tool")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), "my_tool") {
				t.Errorf("error %q does not mention tool name", err.Error())
			}
		})
	}
}

func TestParseHILConfigsFromEntries_RequireApprovalWhen(t *testing.T) {
	t.Parallel()

	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval_when": []any{
						map[string]any{"arg": "method", "in": []any{"POST", "PUT", "DELETE"}},
					},
				},
			},
			toolNames: []string{"http_call"},
		},
	}
	got, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, ok := got["http_call"]
	if !ok {
		t.Fatal("missing http_call config — rules-only entries must produce a config")
	}
	if cfg.RequireApproval {
		t.Error("RequireApproval should remain false")
	}
	if len(cfg.ApprovalWhen) != 1 || cfg.ApprovalWhen[0].Op != agent.OpIn {
		t.Errorf("ApprovalWhen = %+v", cfg.ApprovalWhen)
	}
}

func TestParseHILConfigsFromEntries_PerToolReplaceRules(t *testing.T) {
	t.Parallel()

	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval_when": []any{
						map[string]any{"arg": "method", "eq": "POST"},
					},
					"tools": map[string]any{
						"tool_b": map[string]any{
							"require_approval_when": []any{
								map[string]any{"arg": "force", "eq": true},
							},
						},
					},
				},
			},
			toolNames: []string{"tool_a", "tool_b"},
		},
	}
	got, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["tool_a"].ApprovalWhen) != 1 || got["tool_a"].ApprovalWhen[0].Arg != "method" {
		t.Errorf("tool_a rules = %+v", got["tool_a"].ApprovalWhen)
	}
	if len(got["tool_b"].ApprovalWhen) != 1 || got["tool_b"].ApprovalWhen[0].Arg != "force" {
		t.Errorf("tool_b rules = %+v (should be replaced, not appended)", got["tool_b"].ApprovalWhen)
	}
}

func TestParseHILConfigsFromEntries_PropagatesParseError(t *testing.T) {
	t.Parallel()

	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval_when": []any{
						map[string]any{"arg": "method", "matches": "POST"},
					},
				},
			},
			toolNames: []string{"http_call"},
		},
	}
	_, err := parseHILConfigsFromEntries(entries)
	if err == nil {
		t.Fatal("expected error from unknown operator, got nil")
	}
	if !strings.Contains(err.Error(), "unknown operator") {
		t.Errorf("error = %q, want 'unknown operator'", err.Error())
	}
}

func TestParseHILConfigsFromEntries_PerToolOverrideError(t *testing.T) {
	t.Parallel()

	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"tools": map[string]any{
						"tool_b": map[string]any{
							"require_approval_when": []any{
								map[string]any{"arg": "force"},
							},
						},
					},
				},
			},
			toolNames: []string{"tool_a", "tool_b"},
		},
	}
	_, err := parseHILConfigsFromEntries(entries)
	if err == nil {
		t.Fatal("expected error from missing operator in per-tool override, got nil")
	}
	if !strings.Contains(err.Error(), "tool_b") {
		t.Errorf("error = %q, expected to mention 'tool_b'", err.Error())
	}
}

func TestRequireApprovalWhen_GatesOnMatchingArgs(t *testing.T) {
	t.Parallel()

	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval_when": []any{
						map[string]any{"arg": "method", "in": []any{"POST", "PUT", "PATCH", "DELETE"}},
					},
				},
			},
			toolNames: []string{"atlassian_rest_request"},
		},
	}
	hilConfigs, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg, ok := hilConfigs["atlassian_rest_request"]
	if !ok {
		t.Fatal("config missing — rules-only entries must produce a HILToolConfig")
	}

	cases := []struct {
		name string
		args map[string]any
		want bool
	}{
		{"POST gates", map[string]any{"method": "POST", "path": "/issue"}, true},
		{"DELETE gates", map[string]any{"method": "DELETE", "path": "/issue/1"}, true},
		{"GET runs unattended", map[string]any{"method": "GET", "path": "/issue/1"}, false},
		{"missing method fails open", map[string]any{"path": "/issue"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cfg.ShouldGate(tc.args)
			if got != tc.want {
				t.Errorf("ShouldGate(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseHILConfigsFromEntries_RulesAndRequireApproval(t *testing.T) {
	t.Parallel()
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"require_approval_when": []any{
						map[string]any{"arg": "method", "eq": "DELETE"},
					},
				},
			},
			toolNames: []string{"tool_x"},
		},
	}
	got, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := got["tool_x"]
	if !cfg.RequireApproval {
		t.Error("RequireApproval should be true")
	}
	if len(cfg.ApprovalWhen) != 1 {
		t.Errorf("ApprovalWhen = %+v, want one rule", cfg.ApprovalWhen)
	}
}

func TestParseAgentConfig_AgentLevelHILWarns(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	cfg := config.FromMap(map[string]any{
		"agent_name": "hil_at_agent_level",
		"model":      "general",
		"hil":        map[string]any{"require_approval": true},
	})
	if _, err := ParseAgentConfig(cfg); err != nil {
		t.Fatal(err)
	}

	logged := buf.String()
	var hilLine string
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "agent-level 'hil' block ignored") {
			hilLine = line
		}
	}
	if hilLine == "" {
		t.Fatalf("no warning for an agent-level hil block; logged:\n%s", logged)
	}
	if !strings.Contains(hilLine, "level=WARN") {
		t.Errorf("agent-level hil must warn, not inform; got: %s", hilLine)
	}
}

func TestParseAgentConfig_NoAgentLevelHILNoWarning(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	cfg := config.FromMap(map[string]any{
		"agent_name":     "hil-per-tool-only",
		"model_provider": []any{"general"},
		"tools": []any{map[string]any{
			"tool_type": "builtin",
			"tool_name": "web_request",
			"hil":       map[string]any{"require_approval": true},
		}},
	})
	if _, err := ParseAgentConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if logged := buf.String(); strings.Contains(logged, "agent-level 'hil' block ignored") {
		t.Errorf("per-tool hil must not trigger the agent-level warning; logged:\n%s", logged)
	}
}
