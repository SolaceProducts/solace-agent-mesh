// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const plainWebTools = "        - tool_type: builtin-group\n          group_name: web_tools\n"

func ownHIL(tool, hil string) string {
	return "        - tool_type: builtin\n          tool_name: " + tool + "\n          hil: " + hil + "\n"
}

func applyOnCreateAndUpdate(t *testing.T, name, tools string, check func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, svc *Service)) {
	t.Helper()
	for _, stage := range []string{"create", "update"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			svc, ctx := newTestService(t), t.Context()
			if stage == "update" {
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent(name, plainWebTools+researchGroup)}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{builtinSettingsAgent(name, tools)}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			check(t, stage, agentNamed(ctx, t, svc, name), opFor(t, resp, kindAgent, name).Warnings, svc)
		})
	}
}

func TestApply_ANullMemberOverrideIsRefused(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	build := []BuildComponentInput{builtinSettingsAgent("null_override",
		plainWebTools+"          hil:\n            require_approval: true\n            tools:\n              web_search_google: null\n")}
	const path = "tools[0].hil.tools.web_search_google"
	for _, dryRun := range []bool{true, false} {
		_, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
		invalid, ok := errors.AsType[*entplatform.ValidationError](err)
		if !ok || len(invalid.Details[path]) == 0 {
			t.Errorf("dryRun=%v: error = %v, want a validation error with a detail at %s", dryRun, err, path)
		}
	}
}

func TestApply_AToolsOwnHILCannotCancelItsGroupsGate(t *testing.T) {
	t.Parallel()
	gate := map[string]any{"require_approval": true}
	const stillGated = "tools[%d] (web_request): the tool is still gated by the hil on tools[%d] (web_tools); a tool's own hil cannot turn that gate off or make an always-on gate conditional, so remove the group's hil to change that"
	group := groupWithHIL("web_tools", "{require_approval: true}")
	for _, tc := range []struct {
		name, own string
		want      any
		warns     bool
	}{
		{"require_approval false", "{require_approval: false}", gate, true},
		{"an empty block", "{}", gate, true},
		{"null", "null", gate, true},
		{"false with a message", `{require_approval: false, approval_message: "Why?"}`, map[string]any{"require_approval": true, "approval_message": "Why?"}, true},
		{"fields that add to it", `{approval_message: "Why?", show_args: false}`, map[string]any{"require_approval": true, "approval_message": "Why?", "show_args": false}, false},
	} {
		for _, order := range []string{"group first", "tool first"} {
			t.Run(tc.name+"/"+order, func(t *testing.T) {
				t.Parallel()
				tools, groupAt, toolAt := group+ownHIL("web_request", tc.own), 0, 1
				if order == "tool first" {
					tools, groupAt, toolAt = ownHIL("web_request", tc.own)+group, 1, 0
				}
				applyOnCreateAndUpdate(t, "own_hil_"+strings.ReplaceAll(tc.name+order, " ", "_"), tools, func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, _ *Service) {
					if got := gateFor(t, a, builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(got, tc.want) {
						t.Errorf("%s: web_request = %v, want %v", stage, got, tc.want)
					}
					got := warningsContaining(warnings, "still gated")
					switch {
					case tc.warns && !slices.Equal(got, []string{fmt.Sprintf(stillGated, toolAt, groupAt)}):
						t.Errorf("%s: warnings = %q, want the still-gated warning", stage, got)
					case !tc.warns && len(got) != 0:
						t.Errorf("%s: warnings = %q, want none", stage, got)
					}
				})
			})
		}
	}
}

func TestApply_AMemberOverrideCannotCancelItsGroupsGate(t *testing.T) {
	t.Parallel()
	tools := plainWebTools + "          hil:\n            require_approval: true\n            tools:\n              web_request: {require_approval: false}\n"
	applyOnCreateAndUpdate(t, "member_override", tools, func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, _ *Service) {
		if got := gateFor(t, a, builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(got, any(map[string]any{"require_approval": true})) {
			t.Errorf("%s: web_request = %v, want the gate kept", stage, got)
		}
		const want = "tools[0] (web_tools): web_request is still gated by the group's hil; hil.tools.web_request cannot turn that gate off or make an always-on gate conditional, so remove the group's hil to change that"
		if got := warningsContaining(warnings, "still gated"); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: warnings = %q, want [%q]", stage, got, want)
		}
	})
}

func TestApply_AnUnconditionalGateBeatsAConditionalOneBetweenGroups(t *testing.T) {
	t.Parallel()
	always := groupWithHIL("web_tools", "{require_approval: true}")
	when := groupWithHIL("research", "{require_approval_when: [{arg: query, exists: true}]}")
	for _, order := range []string{"always first", "conditional first"} {
		tools, alwaysAt, whenAt := always+when, 0, 1
		if order == "conditional first" {
			tools, alwaysAt, whenAt = when+always, 1, 0
		}
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			applyOnCreateAndUpdate(t, "ranked_"+strings.ReplaceAll(order, " ", "_"), tools, func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, _ *Service) {
				if got := gateFor(t, a, researchToolset, "web_search_google"); !reflect.DeepEqual(got, any(map[string]any{"require_approval": true})) {
					t.Errorf("%s: web_search_google = %v, want the unconditional gate", stage, got)
				}
				want := fmt.Sprintf("tools[%d] (web_tools) and tools[%d] (research) state different hil for web_search_google; kept the block from tools[%d] (web_tools) because it requires approval on every call", alwaysAt, whenAt, alwaysAt)
				if got := warningsContaining(warnings, "state different hil"); !slices.Equal(got, []string{want}) {
					t.Errorf("%s: warnings = %q, want [%q]", stage, got, want)
				}
			})
		})
	}
}

func TestHILKeptBecause(t *testing.T) {
	t.Parallel()
	always := hilCandidate{value: map[string]any{"require_approval": true}}
	when := hilCandidate{value: map[string]any{"require_approval_when": []any{map[string]any{"arg": "a", "eq": "b"}}}}
	open := hilCandidate{value: map[string]any{"require_approval": false}}
	for _, tc := range []struct {
		name        string
		kept, other hilCandidate
		want        string
	}{
		{"always over rules", always, when, "it requires approval on every call"},
		{"always over open", always, open, "it requires approval on every call"},
		{"rules over open", when, open, "it requires approval when its rules match and the other never does"},
		{"a block over a null", open, hilCandidate{}, "a null never overrides a block"},
		{"two of a kind", always, always, "neither outranks the other, so the first by group name wins"},
	} {
		if got := hilKeptBecause(tc.kept, tc.other); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApply_ARestatedStoredValueBelowItsMinimumIsKept(t *testing.T) {
	t.Parallel()
	const warning = "additionalConfigurations: maxLlmCallsPerTask: value 0 is below minimum 1; kept because this save does not change the stored value, and it will be refused once changed"
	for _, tc := range []struct {
		name, seed string
	}{
		{"stored in camelCase", `{"maxLlmCallsPerTask":0}`},
		{"stored in snake_case", `{"max_llm_calls_per_task":0}`},
	} {
		for _, dryRun := range []bool{true, false} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				svc, ctx := newTestService(t), t.Context()
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("below_min")}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
				seedRawAdditional(ctx, t, svc, "below_min", tc.seed)

				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: []BuildComponentInput{agentComponentWithKey(t, "below_min", "max_llm_calls_per_task: 0")}}, testActor)
				if err != nil {
					t.Fatalf("restating the stored 0: %v", err)
				}
				if got := warningsContaining(opFor(t, resp, kindAgent, "below_min").Warnings, "below minimum"); !slices.Equal(got, []string{warning}) {
					t.Errorf("warnings = %q, want [%q]", got, warning)
				}
				if _, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: []BuildComponentInput{agentComponentWithKey(t, "below_min", "max_llm_calls_per_task: -1")}}, testActor); err == nil {
					t.Error("changing the stored 0 to -1 was accepted")
				}
			})
		}
	}
}

func TestApply_ChangingAStoredValueToOneBelowItsMinimumIsRefused(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "was_five", "max_llm_calls_per_task: 5")}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "was_five", "max_llm_calls_per_task: 0")}}, testActor)
	invalid, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok || !strings.Contains(invalid.Message, "maxLlmCallsPerTask: value 0 is below minimum 1") {
		t.Errorf("error = %v, want the minimum named", err)
	}
}

func TestApply_AnUnconditionalGateIsNotWeakenedToAConditionalOne(t *testing.T) {
	t.Parallel()
	rules := []any{map[string]any{"arg": "url", "eq": "x"}}
	want := map[string]any{"require_approval": true, "require_approval_when": rules}
	t.Run("own entry", func(t *testing.T) {
		t.Parallel()
		tools := groupWithHIL("web_tools", "{require_approval: true}") + ownHIL("web_request", "{require_approval: false, require_approval_when: [{arg: url, eq: x}]}")
		applyOnCreateAndUpdate(t, "weaken_own", tools, func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, _ *Service) {
			if got := gateFor(t, a, builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(got, any(want)) {
				t.Errorf("%s: web_request = %v, want %v", stage, got, want)
			}
			const wantWarning = "tools[1] (web_request): the tool is still gated by the hil on tools[0] (web_tools); a tool's own hil cannot turn that gate off or make an always-on gate conditional, so remove the group's hil to change that"
			if got := warningsContaining(warnings, "still gated"); !slices.Equal(got, []string{wantWarning}) {
				t.Errorf("%s: warnings = %q, want [%q]", stage, got, wantWarning)
			}
		})
	})
	t.Run("hil.tools form", func(t *testing.T) {
		t.Parallel()
		tools := plainWebTools + "          hil:\n            require_approval: true\n            tools:\n              web_request: {require_approval: false, require_approval_when: [{arg: url, eq: x}]}\n"
		applyOnCreateAndUpdate(t, "weaken_member", tools, func(t *testing.T, stage string, a entplatform.AgentResponse, warnings []string, _ *Service) {
			if got := gateFor(t, a, builtinWebRequestToolset, "web_request"); !reflect.DeepEqual(got, any(want)) {
				t.Errorf("%s: web_request = %v, want %v", stage, got, want)
			}
			if got := warningsContaining(warnings, "still gated"); len(got) != 1 {
				t.Errorf("%s: warnings = %q, want one still-gated warning", stage, got)
			}
		})
	})
}

func TestApply_TheMinimumRefusalNamesTheAgentOnce(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "reporter", "max_llm_calls_per_task: 5")}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "reporter", "max_llm_calls_per_task: 0")}}, testActor)
	if err == nil || strings.Count(err.Error(), `agent "reporter"`) != 1 {
		t.Errorf("error = %v, want the agent named exactly once", err)
	}
}
