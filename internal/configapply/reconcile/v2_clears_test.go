// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const (
	agentWithOverlaysYAML = `kind: agent
name: triage
description: Triages incoming support tickets.
spec:
  systemPrompt: You triage tickets.
  toolsets: [web_tools]
  toolsetConfigs:
    - toolsetName: web_tools
      configValues:
        hil:
          web_request:
            require_approval: true
`
	agentWithoutOverlaysYAML = `kind: agent
name: triage
description: Triages incoming support tickets.
spec:
  systemPrompt: You triage tickets.
  toolsets: [web_tools]
`
	entrypointWithPublicURLYAML = `kind: entrypoint
name: support-chat
description: Answers support questions in Google Chat.
spec:
  type: google_chat
  values:
    pubsub_subscription: projects/p/subscriptions/s
  publicUrl: https://bot:pw-secret@support.example.com/chat?token=tok-secret
`
	entrypointWithoutPublicURLYAML = `kind: entrypoint
name: support-chat
description: Answers support questions in Google Chat.
spec:
  type: google_chat
  values:
    pubsub_subscription: projects/p/subscriptions/s
`
)

var overlaysManifest = &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
	"agents":      {{Name: "triage"}},
	"entrypoints": {{Name: "support-chat"}},
}}

func TestPlan_SaysWhatAnUpdateClears(t *testing.T) {
	c := newWorkflowEvalPlatform(t, features.KeyGoogleChatGateway)
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{
		"agents/triage.yaml":            agentWithOverlaysYAML,
		"entrypoints/support-chat.yaml": entrypointWithPublicURLYAML,
	})
	applyRepo(t, c, repo)

	writeRepoFiles(t, repo, map[string]string{
		"agents/triage.yaml":            agentWithoutOverlaysYAML,
		"entrypoints/support-chat.yaml": entrypointWithoutPublicURLYAML,
	})
	plan := planRepo(t, c, repo)
	if len(plan.Agents.Updates) != 1 || len(plan.Gateways.Updates) != 1 {
		t.Fatalf("plan updates: agents %d, entrypoints %d; want one of each", len(plan.Agents.Updates), len(plan.Gateways.Updates))
	}
	var text bytes.Buffer
	RenderPlan(&text, plan, FormatOptions{})
	for _, row := range []string{"update + deploy (clears toolsetConfigs)", "update + deploy (clears publicUrl)"} {
		if !strings.Contains(text.String(), row) {
			t.Errorf("plan does not show %q; plan:\n%s", row, text.String())
		}
	}
	var verbose bytes.Buffer
	RenderPlan(&verbose, plan, FormatOptions{Verbose: true})
	for _, line := range []string{
		"toolsetConfigs: [builtin_web_request_tools]  →  <unset>",
		"publicUrl: https://support.example.com/chat  →  <unset>",
	} {
		if !strings.Contains(verbose.String(), line) {
			t.Errorf("verbose plan does not show %q; plan:\n%s", line, verbose.String())
		}
	}
	for _, secret := range []string{"pw-secret", "tok-secret", "projects/p/subscriptions/s"} {
		if strings.Contains(verbose.String(), secret) {
			t.Errorf("verbose plan prints %q; plan:\n%s", secret, verbose.String())
		}
	}
	want := []string{
		`agent "triage": apply clears the stored toolsetConfigs, which the YAML no longer sets`,
		`entrypoint "support-chat": apply clears the stored publicUrl, which the YAML no longer sets`,
	}
	if got := clearsDiagnosticMessages(plan.Diagnostics()); !slices.Equal(got, want) {
		t.Errorf("clears diagnostics = %q, want %q", got, want)
	}
}

func TestApply_ClearsWhatThePlanSaid(t *testing.T) {
	c := newWorkflowEvalPlatform(t, features.KeyGoogleChatGateway)
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{
		"agents/triage.yaml":            agentWithOverlaysYAML,
		"entrypoints/support-chat.yaml": entrypointWithPublicURLYAML,
	})
	applyRepo(t, c, repo)
	writeRepoFiles(t, repo, map[string]string{
		"agents/triage.yaml":            agentWithoutOverlaysYAML,
		"entrypoints/support-chat.yaml": entrypointWithoutPublicURLYAML,
	})
	applyRepo(t, c, repo)

	agents, err := c.ListAgentsWithSkillConfigs(t.Context())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	triage, found := findByName(agents, "triage", func(a wire.AgentResponse) string { return a.Name })
	if !found {
		t.Fatal("triage not found after apply")
	}
	if len(triage.ToolsetConfigs) > 0 {
		t.Errorf("triage toolset settings after apply = %+v, want none", triage.ToolsetConfigs)
	}
	entrypoints, err := c.ListGateways(t.Context())
	if err != nil {
		t.Fatalf("list entrypoints: %v", err)
	}
	chat, found := findByName(entrypoints, "support-chat", func(e wire.EntrypointResponse) string { return e.Name })
	if !found {
		t.Fatal("support-chat not found after apply")
	}
	if chat.PublicURL != nil {
		t.Errorf("support-chat publicUrl after apply = %v, want none", ptrString(chat.PublicURL))
	}
}

func TestNoteClears_EntrypointKeepsUnnamedValues(t *testing.T) {
	plan := &gatewayKindPlan{Updates: []gatewayOp{{
		Name:   "support-chat",
		Update: wire.UpdateEntrypointRequest{Values: wire.Patch[map[string]any]{Set: true, Value: map[string]any{"a": 1}}},
	}}}
	plan.noteClears()
	if got := plan.Updates[0].Clears; got != nil {
		t.Errorf("clears = %v, want none", got)
	}
}

func TestAgentClears_OnlyFieldsTheYAMLDropsAndThePlatformStores(t *testing.T) {
	stored := &wire.AgentResponse{
		ToolsetConfigs: []apiv2.ToolsetConfig{{ToolsetName: "web_tools"}},
		SkillConfigs:   []apiv2.SkillConfig{{SkillID: "skill-1"}},
	}
	tests := []struct {
		name string
		op   agentOp
		want []string
	}{
		{"both dropped", agentOp{Actual: stored}, []string{clearedToolsetConfigs, clearedSkillConfigs}},
		{"skill settings dropped", agentOp{Actual: stored, Create: wire.CreateAgentRequest{ToolsetConfigs: stored.ToolsetConfigs}}, []string{clearedSkillConfigs}},
		{"both still declared", agentOp{Actual: stored, Create: wire.CreateAgentRequest{ToolsetConfigs: stored.ToolsetConfigs, SkillConfigs: stored.SkillConfigs}}, nil},
		{"nothing stored", agentOp{Actual: &wire.AgentResponse{}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentClears(tt.op); !slicesEqual(got, tt.want) {
				t.Errorf("agentClears = %v, want %v", got, tt.want)
			}
		})
	}
}

func applyRepo(t *testing.T, c *Client, repo string) {
	t.Helper()
	plan := planRepo(t, c, repo)
	out := &ApplyResult{Plan: plan}
	if err := applyPlan(t.Context(), c, plan, ApplyOptions{NoDeploy: true}, out); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, f := range out.Agents.Failures {
		t.Errorf("agent %s %s: %v", f.Op.Name, f.Verb, f.Err)
	}
	for _, f := range out.Gateways.Failures {
		t.Errorf("entrypoint %s %s: %v", f.Op.Name, f.Verb, f.Err)
	}
	if anyFailures(out) {
		t.FailNow()
	}
}

func planRepo(t *testing.T, c *Client, repo string) *PlanResult {
	t.Helper()
	plan, err := planAll(t.Context(), c, repo, overlaysManifest, resolve.New(repo, overlaysManifest, resolve.Options{}), BuildOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func findByName[T any](items []T, name string, nameOf func(T) string) (T, bool) {
	for _, item := range items {
		if nameOf(item) == name {
			return item, true
		}
	}
	var zero T
	return zero, false
}

func clearsDiagnosticMessages(ds []Diagnostic) []string {
	var out []string
	for _, d := range ds {
		if strings.Contains(d.Message, "apply clears the stored") {
			out = append(out, d.Message)
		}
	}
	return out
}

func slicesEqual(a, b []string) bool {
	return strings.Join(a, ",") == strings.Join(b, ",")
}

func TestRenderAgentUpdateVerbose_NamesDroppedBindingsWithoutValues(t *testing.T) {
	kept := apiv2.ToolsetConfig{ToolsetName: "builtin_web_request_tools", ConfigValues: map[string]any{"token": "sk-kept"}}
	dropped := apiv2.ToolsetConfig{ToolsetName: "builtin_hil_tools", ConfigValues: map[string]any{"token": "sk-dropped"}}
	op := agentOp{
		Create: wire.CreateAgentRequest{ToolsetConfigs: []apiv2.ToolsetConfig{kept}},
		Actual: &wire.AgentResponse{ToolsetConfigs: []apiv2.ToolsetConfig{kept, dropped}},
	}
	var out bytes.Buffer
	renderAgentUpdateVerbose(&out, op, FormatOptions{})

	if want := "toolsetConfigs: [builtin_hil_tools builtin_web_request_tools]  →  [builtin_web_request_tools]"; !strings.Contains(out.String(), want) {
		t.Errorf("verbose diff = %q, want it to contain %q", out.String(), want)
	}
	if strings.Contains(out.String(), "sk-") {
		t.Errorf("verbose diff prints a binding value: %q", out.String())
	}
}

func TestRenderAgentUpdateVerbose_SkillBoundByIDMatchesStoredName(t *testing.T) {
	name := "render_skill"
	op := agentOp{
		Create: wire.CreateAgentRequest{SkillConfigs: []apiv2.SkillConfig{{SkillID: "skill-uuid-1"}}},
		Actual: &wire.AgentResponse{SkillConfigs: []apiv2.SkillConfig{{SkillID: "skill-uuid-1", SkillName: &name}}},
	}
	var out bytes.Buffer
	renderAgentUpdateVerbose(&out, op, FormatOptions{})

	if strings.Contains(out.String(), "skillConfigs") {
		t.Errorf("verbose diff = %q, want no skillConfigs change", out.String())
	}
}

func TestPublicURLForDisplay_DropsUserinfoQueryAndFragment(t *testing.T) {
	tests := map[string]string{
		"":                            "",
		"https://support.example.com": "https://support.example.com",
		"https://u:p@support.example.com/a?t=x#frag": "https://support.example.com/a",
		"https://support.example.com/%zz":            redactedPublicURL,
	}
	for raw, want := range tests {
		if got := publicURLForDisplay(raw); got != want {
			t.Errorf("publicURLForDisplay(%q) = %q, want %q", raw, got, want)
		}
	}
}
