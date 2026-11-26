// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func updateOpFixture(name string, mutateDesired, mutateActual func(*modelOpFixtureData)) modelOp {
	d := modelOpFixtureData{
		Alias:      name,
		Provider:   "anthropic",
		ModelName:  "claude-sonnet-4-6",
		APIBase:    "https://api.anthropic.com",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "k1"},
		ModelParams: map[string]any{
			"temperature": 0.2,
		},
		Description: "fixture",
		AuthType:    "apikey",
	}
	a := d
	a.AuthConfig = map[string]any{"type": "apikey", "api_key": "k1"}
	a.ModelParams = map[string]any{"temperature": 0.2}
	if mutateDesired != nil {
		mutateDesired(&d)
	}
	if mutateActual != nil {
		mutateActual(&a)
	}
	apiBase := a.APIBase
	desc := a.Description
	return modelOp{
		Name: name,
		Create: wire.CreateModelRequest{
			Alias:       d.Alias,
			Provider:    d.Provider,
			ModelName:   d.ModelName,
			APIBase:     d.APIBase,
			AuthConfig:  d.AuthConfig,
			ModelParams: d.ModelParams,
			Description: d.Description,
		},
		Actual: &wire.ModelResponse{
			ID:          "id-1",
			Alias:       a.Alias,
			Provider:    sp(a.Provider),
			ModelName:   sp(a.ModelName),
			APIBase:     &apiBase,
			AuthType:    a.AuthType,
			AuthConfig:  a.AuthConfig,
			ModelParams: a.ModelParams,
			Description: &desc,
		},
	}
}

type modelOpFixtureData struct {
	Alias       string
	Provider    string
	ModelName   string
	APIBase     string
	AuthConfig  map[string]any
	ModelParams map[string]any
	Description string
	AuthType    string
}

func planWithUpdate(op modelOp) *PlanResult {
	return &PlanResult{Models: &kindPlan{Kind: "model", Updates: []modelOp{op}}}
}

func renderVerbose(t *testing.T, plan *PlanResult) string {
	t.Helper()
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true, Verbose: true})
	return buf.String()
}

func TestRenderPlanVerbose_FieldDiff(t *testing.T) {
	op := updateOpFixture("general", func(d *modelOpFixtureData) {
		d.ModelName = "claude-sonnet-4-6"
	}, func(a *modelOpFixtureData) {
		a.ModelName = "claude-sonnet-4-5"
	})
	out := renderVerbose(t, planWithUpdate(op))

	if !strings.Contains(out, "modelName:") {
		t.Errorf("expected modelName diff, got:\n%s", out)
	}
	if !strings.Contains(out, "claude-sonnet-4-5") || !strings.Contains(out, "claude-sonnet-4-6") {
		t.Errorf("expected old and new modelName values, got:\n%s", out)
	}
	if !strings.Contains(out, "→") {
		t.Errorf("expected arrow separator, got:\n%s", out)
	}
	if !strings.Contains(out, "    modelName:") {
		t.Errorf("expected 4-space indent before field name, got:\n%s", out)
	}
}

func TestRenderPlanVerbose_NoUnchanged(t *testing.T) {
	op := updateOpFixture("general", nil, func(a *modelOpFixtureData) {
		a.ModelName = "claude-sonnet-4-5"
	})
	out := renderVerbose(t, planWithUpdate(op))

	for _, unchanged := range []string{"alias", "provider", "apiBase", "description"} {
		needle := "    " + unchanged + ":"
		if strings.Contains(out, needle) {
			t.Errorf("expected no diff line for unchanged field %q, got:\n%s", unchanged, out)
		}
	}
	if !strings.Contains(out, "    modelName:") {
		t.Errorf("expected modelName diff line, got:\n%s", out)
	}
}

func TestRenderPlanVerbose_SecretsRedacted(t *testing.T) {
	op := updateOpFixture("general", func(d *modelOpFixtureData) {
		d.AuthConfig = map[string]any{"type": "apikey", "api_key": "shiny-new-secret"}
	}, func(a *modelOpFixtureData) {
		a.AuthConfig = map[string]any{"type": "apikey", "api_key": "<REDACTED>"}
	})
	out := renderVerbose(t, planWithUpdate(op))

	if !strings.Contains(out, "authConfig.api_key: (changed)") {
		t.Errorf("expected redacted secret diff line, got:\n%s", out)
	}
	if strings.Contains(out, "shiny-new-secret") {
		t.Errorf("plaintext secret leaked into verbose output:\n%s", out)
	}
}

func TestRenderPlanVerbose_TruncatesLongValues(t *testing.T) {
	long := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	op := updateOpFixture("general", func(d *modelOpFixtureData) {
		d.APIBase = long
	}, func(a *modelOpFixtureData) {
		a.APIBase = "https://api.anthropic.com"
	})
	out := renderVerbose(t, planWithUpdate(op))

	if !strings.Contains(out, "    apiBase:") {
		t.Errorf("expected apiBase diff line, got:\n%s", out)
	}
	if strings.Contains(out, long) {
		t.Errorf("expected long value to be truncated, got full value in output:\n%s", out)
	}
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation ellipsis '...', got:\n%s", out)
	}
}

func TestRenderPlanVerbose_NestedPath(t *testing.T) {
	op := updateOpFixture("general", func(d *modelOpFixtureData) {
		d.AuthConfig = map[string]any{"type": "apikey", "api_key": "k1", "client_id": "client-A"}
	}, func(a *modelOpFixtureData) {
		a.AuthConfig = map[string]any{"type": "apikey", "api_key": "k1", "client_id": "client-B"}
	})
	out := renderVerbose(t, planWithUpdate(op))

	if !strings.Contains(out, "authConfig.client_id:") {
		t.Errorf("expected dot-notated nested path 'authConfig.client_id:', got:\n%s", out)
	}
	if strings.Contains(out, "    client_id:") {
		t.Errorf("nested field rendered without parent path; got:\n%s", out)
	}
}

func TestRenderPlanVerbose_NoActualSkipsDiff(t *testing.T) {
	op := modelOp{
		Name: "general",
		Create: wire.CreateModelRequest{
			Alias:    "general",
			Provider: "anthropic",
		},
	}
	out := renderVerbose(t, planWithUpdate(op))

	if !strings.Contains(out, "~ general") {
		t.Errorf("expected op summary line, got:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "    ") && strings.Contains(line, ":") {
			t.Errorf("unexpected diff line for op without Actual: %q\n--full output--\n%s", line, out)
		}
	}
}

func TestRenderPlanVerbose_OffByDefault(t *testing.T) {
	op := updateOpFixture("general", nil, func(a *modelOpFixtureData) {
		a.ModelName = "claude-sonnet-4-5"
	})
	var buf bytes.Buffer
	RenderPlan(&buf, planWithUpdate(op), FormatOptions{NoColor: true, Verbose: false})
	out := buf.String()

	if strings.Contains(out, "    modelName:") {
		t.Errorf("expected no field diff with Verbose=false, got:\n%s", out)
	}
	if !strings.Contains(out, "~ general") {
		t.Errorf("expected op summary line, got:\n%s", out)
	}
}

func TestRenderToolsetPlan_FailedDiscoveryAnnotated(t *testing.T) {
	plan := &PlanResult{Toolsets: &toolsetPlan{
		Kind: "toolset",
		Unchanged: []toolsetOp{{
			Name:            "bamboohr",
			ID:              "id-1",
			Verb:            "noop",
			DiscoveryStatus: "failed",
			DiscoveryErrors: []string{"running tool --schema: exit status 2 (stderr: panic: dial tcp 1.2.3.4:443: i/o timeout)"},
		}},
	}}
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "= bamboohr") {
		t.Fatalf("expected unchanged row for bamboohr; got:\n%s", out)
	}
	if !strings.Contains(out, "[DISCOVERY: failed —") {
		t.Errorf("expected failed-discovery annotation; got:\n%s", out)
	}
	if !strings.Contains(out, "dial tcp 1.2.3.4:443") {
		t.Errorf("expected stderr diagnostic to appear inline so users can debug without Datadog; got:\n%s", out)
	}
}

func TestRenderToolsetPlan_ReadyHasNoAnnotation(t *testing.T) {
	plan := &PlanResult{Toolsets: &toolsetPlan{
		Kind: "toolset",
		Unchanged: []toolsetOp{{
			Name:            "salesforce",
			ID:              "id-2",
			Verb:            "noop",
			DiscoveryStatus: "ready",
		}},
	}}
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	if strings.Contains(out, "DISCOVERY") {
		t.Errorf("expected no discovery annotation on ready row; got:\n%s", out)
	}
}

func TestRenderApplyConnectorKind_PrintsWarnings(t *testing.T) {
	var buf bytes.Buffer
	outcome := &connectorOutcome{
		Kind: "connector",
		Created: []connectorOpResult{{
			Op:   connectorOp{Name: "figma-api"},
			Verb: "create",
			Warnings: []string{
				"specification_url is not reachable from the platform: HTTP 404 — agents may still be able to reach it; if not, upload the spec file (specification_file) instead",
			},
		}},
		Updated: []connectorOpResult{{
			Op:   connectorOp{Name: "clean-api"},
			Verb: "update",
		}},
	}
	renderApplyConnectorKind(&buf, outcome, "connectors", FormatOptions{NoColor: true})

	out := buf.String()
	if !strings.Contains(out, "figma-api") || !strings.Contains(out, "created") {
		t.Fatalf("output missing the created row:\n%s", out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "not reachable from the platform") {
		t.Errorf("output missing the warning line:\n%s", out)
	}
	if strings.Count(out, "warning:") != 1 {
		t.Errorf("warning must print only under ops that carry one:\n%s", out)
	}
}

func TestRenderPlanAndApply_EvaluatorsAndExperiments(t *testing.T) {
	res := &ApplyResult{
		Plan: &PlanResult{
			Evaluators: &evaluatorKindPlan{Kind: "evaluator",
				Creates: []evaluatorOp{{Name: "new-eval"}}, Unchanged: []evaluatorOp{{Name: "same-eval"}}},
			Experiments: &experimentKindPlan{Kind: "experiment",
				Updates: []experimentOp{{Name: "tuned-exp"}}, Deletes: []experimentOp{{Name: "old-exp"}}},
		},
		Evaluators: &evaluatorOutcome{Kind: "evaluator",
			Created: []evaluatorOpResult{{Op: evaluatorOp{Name: "new-eval"}, Verb: "create"}}},
		Experiments: &experimentOutcome{Kind: "experiment",
			Failures: []experimentOpResult{{Op: experimentOp{Name: "tuned-exp"}, Verb: "update", Err: errors.New("boom")}}},
	}
	var buf bytes.Buffer
	RenderApply(&buf, res, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{
		"evaluators/", "+ new-eval", "= same-eval",
		"2 evaluators: 1 create, 0 update, 0 delete, 1 unchanged",
		"experiments/", "~ tuned-exp", "- old-exp",
		"2 experiments: 0 create, 1 update, 1 delete, 0 unchanged",
		"Applied evaluators:", "created",
		"Applied experiments:", "! tuned-exp", "update failed", "boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output\n%s", want, out)
		}
	}
}

func TestRenderPlan_DeleteRowNamesPrune(t *testing.T) {
	plan := &PlanResult{Experiments: &experimentKindPlan{Kind: "experiment", Deletes: []experimentOp{{Name: "old-exp"}}}}
	for _, tc := range []struct {
		prune bool
		want  bool
	}{{prune: false, want: true}, {prune: true, want: false}} {
		var buf bytes.Buffer
		RenderPlan(&buf, plan, FormatOptions{NoColor: true, Prune: tc.prune})
		if got := strings.Contains(buf.String(), needsPruneSuffix); got != tc.want {
			t.Errorf("prune=%v: has %q = %v, want %v\n%s", tc.prune, needsPruneSuffix, got, tc.want, buf.String())
		}
	}
}

func renderedRow(out, name string) string {
	for line := range strings.Lines(out) {
		if strings.Contains(line, " "+name+" ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestRenderPlan_OutOfSyncRowsSayWillRedeploy(t *testing.T) {
	outOfSync, inSync := syncStatusOutOfSync, "in_sync"
	failed := entplatform.DisplayStatusDeployFailed
	plan := &PlanResult{
		Agents: &agentKindPlan{Kind: "agent", Unchanged: []agentOp{
			{Name: "agent-drift", Actual: &wire.AgentResponse{SyncStatus: &outOfSync}},
			{Name: "agent-failed", Actual: &wire.AgentResponse{SyncStatus: &outOfSync, DeploymentStatus: failed}},
			{Name: "agent-same", Actual: &wire.AgentResponse{SyncStatus: &inSync, DeploymentStatus: failed}},
		}},
		Gateways: &gatewayKindPlan{Kind: "entrypoint", Unchanged: []gatewayOp{
			{Name: "entry-drift", Actual: &wire.EntrypointResponse{SyncStatus: &outOfSync}},
			{Name: "entry-failed", Actual: &wire.EntrypointResponse{SyncStatus: &outOfSync, DeploymentStatus: failed}},
			{Name: "entry-same", Actual: &wire.EntrypointResponse{SyncStatus: &inSync, DeploymentStatus: failed}},
		}},
	}
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()

	wantRows := map[string]string{
		"agent-drift":  "! agent-drift " + outOfSyncLabel,
		"agent-failed": "! agent-failed " + outOfSyncLabel + lastDeployFailedSuffix,
		"agent-same":   "= agent-same " + noChangeLabel,
		"entry-drift":  "! entry-drift " + outOfSyncLabel,
		"entry-failed": "! entry-failed " + outOfSyncLabel + lastDeployFailedSuffix,
		"entry-same":   "= entry-same " + noChangeLabel,
	}
	for name, want := range wantRows {
		if row := strings.Join(strings.Fields(renderedRow(out, name)), " "); row != want {
			t.Errorf("%s row = %q, want %q\n%s", name, row, want, out)
		}
	}
}

func TestRenderPlan_LinkAgents(t *testing.T) {
	plan := &PlanResult{LinkAgents: &linkAgentKindPlan{Kind: linkAgentKind,
		Creates: []linkAgentOp{{Name: "Claims Desk"}},
		Updates: []linkAgentOp{{Name: "Billing Desk"}},
		Deletes: []linkAgentOp{{Name: "Policy Desk"}},
	}}
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()

	for _, want := range []string{"linkAgents/\n", "3 linkAgents: 1 create, 1 update, 1 delete, 0 unchanged"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	wantRows := map[string]string{
		"Claims":  "+ Claims Desk " + createPlusDeploy,
		"Billing": "~ Billing Desk " + updatePlusDeploy,
		"Policy":  "- Policy Desk delete",
	}
	for name, want := range wantRows {
		if row := strings.Join(strings.Fields(renderedRow(out, name)), " "); !strings.HasPrefix(row, want) {
			t.Errorf("%s row = %q, want it to start with %q\n%s", name, row, want, out)
		}
	}
}

func TestRenderApply_DeployedKinds(t *testing.T) {
	boom := errors.New("boom")
	res := &ApplyResult{
		Agents: &agentKindOutcome{
			Created:     []agentOpResult{{Op: agentOp{Name: "agent-new"}, Verb: "create"}},
			Skipped:     []agentOpResult{{Op: agentOp{Name: "agent-old"}, Verb: "delete"}},
			Failures:    []agentOpResult{{Op: agentOp{Name: "agent-bad"}, Verb: "update", Err: boom}},
			Deployments: []deployResult{{Kind: "agent", Name: "agent-new", Action: "deploy"}},
		},
		Gateways: &gatewayKindOutcome{
			Deployments: []deployResult{
				{Kind: "entrypoint", Name: "entry-ok", Action: "update"},
				{Kind: "entrypoint", Name: "entry-fail", Action: "update", Err: boom},
			},
		},
		Workflows: &workflowKindOutcome{},
		LinkAgents: &linkAgentKindOutcome{
			Created:     []linkAgentOpResult{{Op: linkAgentOp{Name: "Claims Desk"}, Verb: "create"}},
			Failures:    []linkAgentOpResult{{Op: linkAgentOp{Name: "Billing Desk"}, Verb: "update", Err: boom}},
			Deployments: []deployResult{{Kind: linkAgentKind, Name: "Policy Desk", Action: "deploy"}},
		},
	}
	var buf bytes.Buffer
	RenderApply(&buf, res, FormatOptions{NoColor: true})
	out := buf.String()

	for _, want := range []string{"Applied agents", "Applied entrypoints", "Applied linkAgents:", "Deployments:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Applied workflows") {
		t.Errorf("an empty workflow outcome must print nothing:\n%s", out)
	}
	wantRows := map[string]string{
		"agent-new":  "created",
		"agent-old":  skipPruneLabel,
		"agent-bad":  "update failed: boom",
		"entry-ok":   "deploy (update) completed",
		"entry-fail": "deploy failed: boom",
		"Claims":     "created",
		"Billing":    "update failed: boom",
		"Policy":     "deploy (deploy) completed",
	}
	for name, want := range wantRows {
		if row := renderedRow(out, name); !strings.Contains(row, want) {
			t.Errorf("%s row = %q, want it to contain %q\n%s", name, row, want, out)
		}
	}
}
