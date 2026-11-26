// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func agentResponseWithToolsetConfig(configValues map[string]any) wire.AgentResponse {
	return wire.AgentResponse{
		Name:         "x",
		Description:  "d",
		SystemPrompt: "p",
		Toolsets:     []string{"common"},
		ToolsetConfigs: []apiv2.ToolsetConfig{
			{ToolsetName: "common", ConfigValues: maps.Clone(configValues)},
		},
	}
}

func agentResolvedWithToolsetConfig(configValues map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"toolsets":     []string{"common"},
			"toolsetConfigs": []any{
				map[string]any{
					"toolsetName":  "common",
					"configValues": maps.Clone(configValues),
				},
			},
		},
	}
}

type fakeDeployer struct {
	failOnID map[string]error
	calls    []deployCall
}

type deployCall struct {
	ID     string
	Action string
}

func (f *fakeDeployer) DeployResource(_ context.Context, id string, action string) error {
	f.calls = append(f.calls, deployCall{ID: id, Action: action})
	if err, ok := f.failOnID[id]; ok {
		return err
	}
	return nil
}

func TestRunAgentDeployments_ContinuesAfterFailure(t *testing.T) {
	outcome := &agentKindOutcome{
		Kind: "agent",
		Created: []agentOpResult{
			{Op: agentOp{Name: "alpha", ID: "id-alpha-bad"}, Verb: "create"},
			{Op: agentOp{Name: "bravo", ID: "id-bravo"}, Verb: "create"},
		},
	}
	d := &fakeDeployer{failOnID: map[string]error{
		"id-alpha-bad": errors.New("deploy rejected: bad uuid"),
	}}

	runAgentDeployments(context.Background(), d, io.Discard, outcome)

	if got := len(d.calls); got != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", got)
	}
	if d.calls[0].Action != "deploy" || d.calls[1].Action != "deploy" {
		t.Errorf("expected action=deploy on creates, got %+v", d.calls)
	}
	if len(outcome.Deployments) != 2 {
		t.Fatalf("expected 2 deployment results, got %d", len(outcome.Deployments))
	}
	if outcome.Deployments[0].Err == nil {
		t.Errorf("alpha deployment should have Err set")
	}
	if outcome.Deployments[1].Err != nil {
		t.Errorf("bravo deployment should have succeeded: %v", outcome.Deployments[1].Err)
	}
	if len(outcome.Failures) != 1 {
		t.Errorf("expected 1 mirrored failure, got %+v", outcome.Failures)
	}
	if outcome.Failures[0].Verb != "deploy" {
		t.Errorf("mirrored failure Verb should be \"deploy\", got %q", outcome.Failures[0].Verb)
	}
	if outcome.Failures[0].Op.Name != "alpha" {
		t.Errorf("mirrored failure should target alpha, got %q", outcome.Failures[0].Op.Name)
	}
}

func TestRunAgentDeployments_UpdatesUseUpdateAction(t *testing.T) {
	outcome := &agentKindOutcome{
		Kind: "agent",
		Updated: []agentOpResult{
			{
				Op:               agentOp{Name: "alpha", ID: "id-alpha"},
				Verb:             "update",
				DeploymentStatus: entplatform.DisplayStatusDeployed,
			},
		},
	}
	d := &fakeDeployer{}

	runAgentDeployments(context.Background(), d, io.Discard, outcome)

	if len(d.calls) != 1 {
		t.Fatalf("expected 1 deploy call, got %d", len(d.calls))
	}
	if d.calls[0].Action != "update" {
		t.Errorf("update path must use action=update for deployed agent, got %q", d.calls[0].Action)
	}
	if outcome.Deployments[0].Action != "update" {
		t.Errorf("deployResult.Action mismatch: got %q want update", outcome.Deployments[0].Action)
	}
}

func TestRunAgentDeployments_UpdatesUseDeployActionWhenNotDeployed(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{"empty status (never had a deployment record)", ""},
		{"explicit not_deployed", entplatform.DisplayStatusNotDeployed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &agentKindOutcome{
				Kind: "agent",
				Updated: []agentOpResult{
					{
						Op:               agentOp{Name: "alpha", ID: "id-alpha"},
						Verb:             "update",
						DeploymentStatus: tc.status,
					},
				},
			}
			d := &fakeDeployer{}
			runAgentDeployments(context.Background(), d, io.Discard, outcome)

			if len(d.calls) != 1 {
				t.Fatalf("expected 1 deploy call, got %d", len(d.calls))
			}
			if d.calls[0].Action != "deploy" {
				t.Errorf("update of not-yet-deployed agent must use action=deploy, got %q", d.calls[0].Action)
			}
			if outcome.Deployments[0].Action != "deploy" {
				t.Errorf("deployResult.Action mismatch: got %q want deploy", outcome.Deployments[0].Action)
			}
		})
	}
}

func TestRunAgentDeployments_UpdatesUseUpdateActionForDeployFailed(t *testing.T) {
	outcome := &agentKindOutcome{
		Kind: "agent",
		Updated: []agentOpResult{
			{
				Op:               agentOp{Name: "alpha", ID: "id-alpha"},
				Verb:             "update",
				DeploymentStatus: entplatform.DisplayStatusDeployFailed,
			},
		},
	}
	d := &fakeDeployer{}
	runAgentDeployments(context.Background(), d, io.Discard, outcome)

	if len(d.calls) != 1 || d.calls[0].Action != "update" {
		t.Errorf("deploy_failed must take the update path, got %+v", d.calls)
	}
}

func TestRunGatewayDeployments_UpdateActionRespectsStatus(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"", "deploy"},
		{entplatform.DisplayStatusNotDeployed, "deploy"},
		{entplatform.DisplayStatusDeployed, "update"},
		{entplatform.DisplayStatusDeployFailed, "update"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			outcome := &gatewayKindOutcome{
				Kind: "gateway",
				Updated: []gatewayOpResult{
					{Op: gatewayOp{Name: "g", ID: "id-g"}, Verb: "update", DeploymentStatus: tc.status},
				},
			}
			d := &fakeDeployer{}
			runGatewayDeployments(context.Background(), d, io.Discard, outcome)
			if len(d.calls) != 1 || d.calls[0].Action != tc.want {
				t.Errorf("status=%q: want action=%q, got %+v", tc.status, tc.want, d.calls)
			}
		})
	}
}

func TestRunWorkflowDeployments_UpdateActionRespectsStatus(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"", "deploy"},
		{entplatform.DisplayStatusNotDeployed, "deploy"},
		{entplatform.DisplayStatusDeployed, "update"},
		{entplatform.DisplayStatusDeployFailed, "update"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			outcome := &workflowKindOutcome{
				Kind: "workflow",
				Updated: []workflowOpResult{
					{Op: workflowOp{Name: "w", ID: "id-w"}, Verb: "update", DeploymentStatus: tc.status},
				},
			}
			d := &fakeDeployer{}
			runWorkflowDeployments(context.Background(), d, io.Discard, outcome)
			if len(d.calls) != 1 || d.calls[0].Action != tc.want {
				t.Errorf("status=%q: want action=%q, got %+v", tc.status, tc.want, d.calls)
			}
		})
	}
}

func TestRunAgentDeployments_SkipsMissingID(t *testing.T) {
	outcome := &agentKindOutcome{
		Kind: "agent",
		Created: []agentOpResult{
			{Op: agentOp{Name: "ghost", ID: ""}, Verb: "create"},
		},
	}
	d := &fakeDeployer{}
	runAgentDeployments(context.Background(), d, io.Discard, outcome)

	if len(d.calls) != 0 {
		t.Errorf("expected zero deploy calls when ID is empty, got %+v", d.calls)
	}
	if len(outcome.Deployments) != 1 || outcome.Deployments[0].Err == nil {
		t.Errorf("expected one failed deployment record, got %+v", outcome.Deployments)
	}
	if len(outcome.Failures) != 1 {
		t.Errorf("expected mirrored failure, got %+v", outcome.Failures)
	}
}

func TestRunAgentDeployments_RedeploysOutOfSync(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{entplatform.DisplayStatusDeployed, "update"},
		{entplatform.DisplayStatusDeployFailed, "update"},
		{entplatform.DisplayStatusNotDeployed, "deploy"},
		{"", "deploy"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			outcome := &agentKindOutcome{
				Kind: "agent",
				OutOfSync: []agentOpResult{
					{Op: agentOp{Name: "drift", ID: "id-drift"}, Verb: "redeploy", DeploymentStatus: tc.status},
				},
			}
			d := &fakeDeployer{}
			runAgentDeployments(context.Background(), d, io.Discard, outcome)
			if len(d.calls) != 1 || d.calls[0].Action != tc.want {
				t.Fatalf("status=%q: want one %q call, got %+v", tc.status, tc.want, d.calls)
			}
			if len(outcome.Deployments) != 1 {
				t.Errorf("expected one deployment record, got %+v", outcome.Deployments)
			}
		})
	}
}

func TestRunAgentDeployments_OutOfSyncFailureMirrored(t *testing.T) {
	outcome := &agentKindOutcome{
		Kind: "agent",
		OutOfSync: []agentOpResult{
			{Op: agentOp{Name: "drift", ID: "id-drift"}, Verb: "redeploy", DeploymentStatus: entplatform.DisplayStatusDeployed},
		},
	}
	d := &fakeDeployer{failOnID: map[string]error{"id-drift": errors.New("boom")}}
	runAgentDeployments(context.Background(), d, io.Discard, outcome)
	if len(outcome.Failures) != 1 || outcome.Failures[0].Op.Name != "drift" {
		t.Fatalf("expected one mirrored failure for drift, got %+v", outcome.Failures)
	}
}

func TestApplyAgents_FlagsOutOfSyncUnchanged(t *testing.T) {
	outOfSync := syncStatusOutOfSync
	inSync := "in_sync"
	plan := &agentKindPlan{
		Kind: "agent",
		Unchanged: []agentOp{
			{Name: "stale", ID: "id-stale", Actual: &wire.AgentResponse{
				Name: "stale", SyncStatus: &outOfSync, DeploymentStatus: entplatform.DisplayStatusDeployed,
			}},
			{Name: "fine", ID: "id-fine", Actual: &wire.AgentResponse{
				Name: "fine", SyncStatus: &inSync, DeploymentStatus: entplatform.DisplayStatusDeployed,
			}},
		},
	}
	c := NewClient(target.Resolved{URL: "http://127.0.0.1:0", Token: "tok"})
	outcome := applyAgents(context.Background(), c, plan, ApplyOptions{})

	if len(outcome.OutOfSync) != 1 || outcome.OutOfSync[0].Op.Name != "stale" {
		t.Fatalf("expected only 'stale' flagged out-of-sync, got %+v", outcome.OutOfSync)
	}
	if outcome.OutOfSync[0].DeploymentStatus != entplatform.DisplayStatusDeployed {
		t.Errorf("expected carried deployment status, got %q", outcome.OutOfSync[0].DeploymentStatus)
	}
}

func TestIsToolsetNotReady(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"not ready 422", &HTTPError{StatusCode: 422, Body: `tool package "x" is not ready (status: "pending")`}, true},
		{"wait for STR 422", &HTTPError{StatusCode: 422, Body: "wait for STR to finish discovery"}, true},
		{"unrelated 422", &HTTPError{StatusCode: 422, Body: "validation failed"}, false},
		{"404", &HTTPError{StatusCode: 404, Body: "is not ready"}, false},
		{"plain error", errors.New("is not ready"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isToolsetNotReady(tc.err); got != tc.want {
				t.Errorf("isToolsetNotReady = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanAgents_PreservesNestedBuiltinToolConfig(t *testing.T) {
	nested := map[string]any{
		"deep_research": map[string]any{
			"max_iterations":          2,
			"max_runtime_seconds":     120,
			"sources":                 []any{"web"},
			"max_response_size_bytes": 5242880,
		},
	}
	desired := []resolve.ResolvedResource{
		{
			Name:        "x",
			Description: "d",
			SourceFile:  "agents/x.yaml",
			Spec: map[string]any{
				"systemPrompt": "p",
				"toolsets":     []string{"builtin_research_tools"},
				"toolsetConfigs": []any{
					map[string]any{
						"toolsetName":  "builtin_research_tools",
						"configValues": maps.Clone(nested),
					},
				},
			},
		},
	}

	plan, err := planAgents(t.Context(), nil, desired, nil, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %+v", plan.Creates)
	}
	got := plan.Creates[0].Create.ToolsetConfigs
	if len(got) != 1 || got[0].ToolsetName != "builtin_research_tools" {
		t.Fatalf("unexpected toolset config bindings: %+v", got)
	}
	dr, _ := got[0].ConfigValues["deep_research"].(map[string]any)
	if dr == nil {
		t.Fatalf("nested deep_research map lost in round-trip: %+v", got[0].ConfigValues)
	}
	if v, _ := dr["max_iterations"].(float64); v != 2 {
		t.Errorf("max_iterations = %v, want 2", dr["max_iterations"])
	}
	if v, _ := dr["max_runtime_seconds"].(float64); v != 120 {
		t.Errorf("max_runtime_seconds = %v, want 120", dr["max_runtime_seconds"])
	}
}

func TestPlanAgents_PreservesSecretsOnWire(t *testing.T) {
	desired := []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "myPlainSecret"}),
	}
	plan, err := planAgents(t.Context(), nil, desired, nil, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected 1 create, got %+v", plan.Creates)
	}
	got := plan.Creates[0].Create.ToolsetConfigs[0].ConfigValues
	if got["password"] != "myPlainSecret" {
		t.Errorf("password lost from wire payload: got %v, want myPlainSecret", got["password"])
	}
	if got["region"] != "us-east-1" {
		t.Errorf("region lost from wire payload: got %v", got["region"])
	}
}

func TestPlanAgents_AuthSecretNormalization(t *testing.T) {
	desired := []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "plain"}),
	}
	actual := []wire.AgentResponse{
		agentResponseWithToolsetConfig(map[string]any{"region": "us-east-1", "password": "<REDACTED>"}),
	}
	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates for redaction-equivalent inputs, got %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got %+v", plan.Unchanged)
	}

	desired = []resolve.ResolvedResource{
		agentResolvedWithToolsetConfig(map[string]any{"region": "eu-west-1", "password": "plain"}),
	}
	plan, err = planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("expected one update for non-secret drift, got %+v plan=%+v", plan.Updates, plan)
	}
}

func TestPlanAgents_RoundTripStableForTypeAndSkillIDs(t *testing.T) {
	desired := []resolve.ResolvedResource{{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"type":         "standard",
			"skillRefs":    []string{"summarize"},
		},
	}}
	actual := []wire.AgentResponse{{
		Name:         "x",
		Description:  "d",
		SystemPrompt: "p",
		Type:         "standard",
		SkillIDs:     []string{"11111111-1111-1111-1111-111111111111"},
	}}
	plan, err := planAgents(t.Context(), nil, desired, actual, summarizeSkillHooks())
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates, got %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got %+v", plan.Unchanged)
	}
}

func TestDeployResource_FailedStatusSurfaces(t *testing.T) {
	const hydrationError = `resolve connector refs: node "fetch": connector "pets-api" cannot be enumerated`

	cases := []struct {
		name     string
		resource platformResource
		idField  string
		deployer func(c *Client) Deployer
	}{
		{"workflow", workflowDeploymentsResource, "workflowId", func(c *Client) Deployer { return newWorkflowDeployer(c) }},
		{"agent", agentDeploymentsResource, "agentId", func(c *Client) Deployer { return newAgentDeployer(c) }},
		{"gateway", entrypointDeploymentsResource, "entrypointId", func(c *Client) Deployer { return newGatewayDeployer(c) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var status int
			var details map[string][]string
			mux := http.NewServeMux()
			mux.HandleFunc("POST "+platform.APIV2Prefix+tc.resource.path, func(w http.ResponseWriter, _ *http.Request) {
				if status != http.StatusCreated {
					writeV2Error(w, status, "Unable to deploy "+tc.name, details)
					return
				}
				writeV2Single(w, status, map[string]any{"id": "d1", "status": entplatform.DeployStatusSuccess, "action": "deploy", "failure": nil})
			})
			d := tc.deployer(newV2PlatformClient(t, mux))

			status, details = http.StatusUnprocessableEntity, map[string][]string{tc.idField: {hydrationError}}
			err := d.DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "deploy")
			if err == nil || !strings.Contains(err.Error(), strings.Trim(toJSON(t, hydrationError), `"`)) {
				t.Fatalf("a refused deployment must surface the platform's reason, got %v", err)
			}

			status, details = http.StatusBadGateway, nil
			err = d.DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "deploy")
			if err == nil {
				t.Fatal("a deployment that ran and failed must still error")
			}

			status, details = http.StatusCreated, nil
			if err := d.DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "deploy"); err != nil {
				t.Fatalf("successful deployment must not error: %v", err)
			}
		})
	}
}

func TestRunGatewayDeployments_OutOfSyncFailureMirrored(t *testing.T) {
	outcome := &gatewayKindOutcome{
		Kind: "entrypoint",
		OutOfSync: []gatewayOpResult{
			{Op: gatewayOp{Name: "drift", ID: "id-drift"}, Verb: "redeploy", DeploymentStatus: entplatform.DisplayStatusDeployed},
		},
	}
	d := &fakeDeployer{failOnID: map[string]error{"id-drift": errors.New("boom")}}
	runGatewayDeployments(context.Background(), d, io.Discard, outcome)
	if len(outcome.Failures) != 1 || outcome.Failures[0].Op.Name != "drift" {
		t.Fatalf("expected one mirrored failure for drift, got %+v", outcome.Failures)
	}
}

func TestDeployerOutlastsRequestTimeout(t *testing.T) {
	const deployDuration = 200 * time.Millisecond
	deployers := map[string]func(*Client) Deployer{
		"agent":      func(c *Client) Deployer { return newAgentDeployer(c) },
		"workflow":   func(c *Client) Deployer { return newWorkflowDeployer(c) },
		"entrypoint": func(c *Client) Deployer { return newGatewayDeployer(c) },
	}
	for kind, newDeployer := range deployers {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(deployDuration)
				writeV2Single(w, http.StatusCreated, map[string]string{"status": entplatform.DeployStatusSuccess})
			}))
			defer srv.Close()

			c := NewClient(target.Resolved{URL: srv.URL, Token: "tok"})
			c.http.Timeout = deployDuration / 4

			if err := c.do(t.Context(), http.MethodGet, platform.APIV2Prefix+entrypointsResource.path, nil, nil); err == nil {
				t.Fatal("ordinary request should time out; the test no longer proves the deploy client differs")
			}
			if err := newDeployer(c).DeployResource(t.Context(), "id-1", "update"); err != nil {
				t.Fatalf("deploy should outlast the ordinary request timeout: %v", err)
			}
		})
	}
}

func TestPlanAgents_ReadSideShapeIsUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{{
		Name:        "x",
		Description: "d",
		SourceFile:  "agents/x.yaml",
		Spec: map[string]any{
			"systemPrompt": "p",
			"type":         "standard",
			"skillRefs":    []string{"summarize"},
			"outputModes":  []string{"text", "file"},
			"toolsets":     []string{"zeta", "alpha"},
			"inputModes":   []string{"file", "text"},
			"connectors":   []string{"beta", "alpha"},
		},
	}}
	actual := []wire.AgentResponse{{
		Name:         "x",
		Description:  "d",
		SystemPrompt: "p",
		Type:         "standard",
		SkillIDs:     []string{"11111111-1111-1111-1111-111111111111"},
		OutputModes:  []string{"file", "text"},
		Toolsets:     []string{"alpha", "zeta"},
		InputModes:   []string{"text", "file"},
		ConnectorIDs: []string{"alpha", "beta"},
	}}
	plan, err := planAgents(t.Context(), nil, desired, actual, summarizeSkillHooks())
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("expected no updates, got %+v", plan.Updates)
	}
}

func TestPlanAgents_PlatformSkillSetChangeStillUpdates(t *testing.T) {
	const existingID = "11111111-1111-1111-1111-111111111111"
	cases := map[string]struct {
		skillIDs []string
		stored   []string
	}{
		"added":   {[]string{existingID, "22222222-2222-2222-2222-222222222222"}, []string{existingID}},
		"removed": {[]string{existingID}, []string{existingID, "22222222-2222-2222-2222-222222222222"}},
		"first":   {[]string{existingID}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{{
				Name: "x", Description: "d", SourceFile: "agents/x.yaml",
				Spec: map[string]any{"systemPrompt": "p", "type": "standard", "skillIds": tc.skillIDs, "skillRefs": tc.skillIDs},
			}}
			actual := []wire.AgentResponse{{
				Name: "x", Description: "d", SystemPrompt: "p", Type: "standard", SkillIDs: tc.stored,
			}}
			plan, err := planAgents(t.Context(), nil, desired, actual, summarizeSkillHooks())
			if err != nil {
				t.Fatalf("planAgents: %v", err)
			}
			if len(plan.Updates) != 1 {
				t.Errorf("a changed platform skill set must plan an update, got %+v", plan)
			}
		})
	}
}

func summarizeSkillHooks() translate.AgentHooks {
	return translate.AgentHooks{LookupSkillID: func(name string) (string, error) {
		if name == "summarize" {
			return "11111111-1111-1111-1111-111111111111", nil
		}
		return name, nil
	}}
}

func TestDeployers_V2FailedDeploymentIsAnErrorStatus(t *testing.T) {
	const errorID = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77"
	cases := []struct {
		name     string
		resource platformResource
		failure  string
		deployer func(c *Client) Deployer
	}{
		{"workflow", workflowDeploymentsResource, "Unable to deploy workflow", func(c *Client) Deployer { return newWorkflowDeployer(c) }},
		{"agent", agentDeploymentsResource, "Unable to deploy agent", func(c *Client) Deployer { return newAgentDeployer(c) }},
		{"entrypoint", entrypointDeploymentsResource, "Unable to deploy entrypoint", func(c *Client) Deployer { return newGatewayDeployer(c) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fail bool
			var sent map[string]any
			mux := http.NewServeMux()
			mux.HandleFunc("GET "+APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			mux.HandleFunc("POST "+platform.APIV2Prefix+tc.resource.path, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				if fail {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"message":"` + tc.failure + `","errorId":"` + errorID + `"}`))
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"data":{"status":"success"}}`))
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			d := tc.deployer(NewClient(target.Resolved{URL: srv.URL}))

			fail = true
			err := d.DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "update")
			if err == nil || !strings.Contains(err.Error(), tc.failure) || !strings.Contains(err.Error(), errorID) {
				t.Fatalf("a failed v2 deployment must surface the platform message and errorId, got %v", err)
			}
			if sent["action"] != "update" {
				t.Errorf("deployment request = %v, want action update", sent)
			}

			fail = false
			if err := d.DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "deploy"); err != nil {
				t.Fatalf("a successful v2 deployment must not error: %v", err)
			}
		})
	}
}

func TestAgentDeployer_V2RetriesWhileAToolsetIsNotReady(t *testing.T) {
	const notReady = `{"message":"Unable to deploy agent","errorId":"e-1","validationDetails":{"agentId":["tool package \"lookup\" is not ready (status: \"pending\"); wait for STR to finish discovery before deploying"]}}`
	var posts int
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	mux.HandleFunc("POST "+platform.APIV2Prefix+agentDeploymentsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		posts++
		w.Header().Set("Content-Type", "application/json")
		if posts == 1 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(notReady))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"status":"success"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	err := newAgentDeployer(NewClient(target.Resolved{URL: srv.URL})).DeployResource(t.Context(), "11111111-1111-1111-1111-111111111111", "deploy")
	if err != nil {
		t.Fatalf("deploy after the toolset became ready: %v", err)
	}
	if posts != 2 {
		t.Errorf("deployment requests = %d, want 2: the not-ready refusal must be retried", posts)
	}
}
