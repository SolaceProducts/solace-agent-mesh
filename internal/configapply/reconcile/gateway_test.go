// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestPlanWorkflowsGateways_LegacyGatewaysManifestKeyRejected(t *testing.T) {
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"gateways": {{Name: "webui"}}}}
	out := &PlanResult{}
	err := planAllWorkflowsGateways(t.Context(), nil, m, nil, out)
	if err == nil {
		t.Fatal("expected error for legacy manifest key \"gateways\"")
	}
	if !strings.Contains(err.Error(), "renamed") {
		t.Errorf("expected rename hint, got: %v", err)
	}
}

func gatewayFixture(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"type":      "webui",
		"publicUrl": "https://chat.example.com",
		"values": map[string]any{
			"fastapiHost": "0.0.0.0",
			"fastapiPort": 8080,
		},
	}
	if f != nil {
		f(m)
	}
	return m
}

func resolvedGateway(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "gateway",
		Name:        name,
		SourceFile:  "gateways/" + name + ".yaml",
		Spec:        spec,
		Description: "fixture gateway with a long-enough description",
	}
}

func liveWebuiGateway(slug string) wire.EntrypointResponse {
	return wire.EntrypointResponse{
		ID:             "id-1",
		Slug:           sp(slug),
		Name:           "webui",
		Description:    "fixture gateway with a long-enough description",
		EntrypointType: "webui",
		PublicURL:      sp("https://chat.example.com"),
		Values:         map[string]any{"fastapiHost": "0.0.0.0", "fastapiPort": float64(8080)},
	}
}

func TestPlanGateways_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}
	actual := []wire.EntrypointResponse{{ID: "id-old", Name: "obsolete", EntrypointType: "webui"}}

	plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "webui" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 0 {
		t.Errorf("expected no updates/unchanged, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestPlanGateways_NoOpRoundTrip(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}
	actual := []wire.EntrypointResponse{{
		ID:             "id-1",
		Name:           "webui",
		Description:    "fixture gateway with a long-enough description",
		EntrypointType: "webui",
		PublicURL:      sp("https://chat.example.com"),
		Values: map[string]any{
			"fastapiHost": "0.0.0.0",
			"fastapiPort": float64(8080),
		},
	}}

	plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "id-1" {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if got := len(plan.Creates) + len(plan.Updates) + len(plan.Deletes); got != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanGateways_UpdateOnValuesChange(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
		m["values"] = map[string]any{
			"fastapiHost": "0.0.0.0",
			"fastapiPort": 9090,
		}
	}))}
	actual := []wire.EntrypointResponse{{
		ID:             "id-1",
		Name:           "webui",
		Description:    "fixture gateway with a long-enough description",
		EntrypointType: "webui",
		PublicURL:      sp("https://chat.example.com"),
		Values: map[string]any{
			"fastapiHost": "0.0.0.0",
			"fastapiPort": float64(8080),
		},
	}}

	plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	op := plan.Updates[0]
	if !op.Update.Values.Set || !reflect.DeepEqual(op.Update.Values.Value, map[string]any{"fastapiPort": float64(9090)}) {
		t.Errorf("Update.Values = %+v; want only the changed fastapiPort, which the platform merges in", op.Update.Values)
	}
	if op.Update.Name.Set || op.Update.Description.Set || op.Update.PublicURL.Set {
		t.Errorf("Update sends unchanged fields: %+v", op.Update)
	}
}

func TestPlanGateways_ValuesTheYAMLLeavesOutAreKept(t *testing.T) {
	stored := map[string]any{"fastapiHost": "0.0.0.0", "fastapiPort": float64(8080), "run_as": "support-bot"}
	for _, tc := range []struct {
		name       string
		values     map[string]any
		wantUpdate map[string]any
	}{
		{name: "nothing else changed plans no update", values: map[string]any{"fastapiHost": "0.0.0.0", "fastapiPort": 8080}},
		{name: "a change sends only that setting", values: map[string]any{"fastapiHost": "0.0.0.0", "fastapiPort": 9090}, wantUpdate: map[string]any{"fastapiPort": float64(9090)}},
		{name: "an empty value clears it", values: map[string]any{"fastapiHost": "0.0.0.0", "fastapiPort": 8080, "run_as": ""}, wantUpdate: map[string]any{"run_as": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
				m["values"] = tc.values
			}))}
			live := liveWebuiGateway("")
			live.Slug = nil
			live.Values = maps.Clone(stored)

			plan, err := planGateways(t.Context(), nil, desired, []wire.EntrypointResponse{live}, translate.GatewayHooks{})
			if err != nil {
				t.Fatalf("planGateways: %v", err)
			}
			if tc.wantUpdate == nil {
				if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
					t.Fatalf("plan = %d unchanged, %d updates; want the entrypoint unchanged", len(plan.Unchanged), len(plan.Updates))
				}
				return
			}
			if len(plan.Updates) != 1 {
				t.Fatalf("Updates = %+v", plan.Updates)
			}
			if got := plan.Updates[0].Update.Values; !got.Set || !reflect.DeepEqual(got.Value, tc.wantUpdate) {
				t.Errorf("Update.Values = %+v; want %v", got, tc.wantUpdate)
			}
		})
	}
}

func TestPlanGateways_PublicURLRemovedFromYAMLIsCleared(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stored    *string
		wantClear bool
	}{
		{name: "stored URL is cleared", stored: sp("https://chat.example.com"), wantClear: true},
		{name: "no URL on either side is left out", stored: nil, wantClear: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
				delete(m, "publicUrl")
				m["values"] = map[string]any{"fastapiPort": 9090}
			}))}
			actual := []wire.EntrypointResponse{{
				ID:             "id-1",
				Name:           "webui",
				Description:    "fixture gateway with a long-enough description",
				EntrypointType: "webui",
				PublicURL:      tc.stored,
				Values:         map[string]any{"fastapiPort": float64(8080)},
			}}

			plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
			if err != nil {
				t.Fatalf("planGateways: %v", err)
			}
			if len(plan.Updates) != 1 {
				t.Fatalf("Updates = %+v", plan.Updates)
			}
			got := plan.Updates[0].Update.PublicURL
			if cleared := got.Set && got.Null; cleared != tc.wantClear || (!tc.wantClear && got.Set) {
				t.Errorf("Update.PublicURL = %+v; want cleared=%t", got, tc.wantClear)
			}
		})
	}
}

func TestPlanGateways_RejectsTypeMutation(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
		m["type"] = "slack"
	}))}
	actual := []wire.EntrypointResponse{{
		ID:             "id-1",
		Name:           "webui",
		EntrypointType: "webui",
	}}
	_, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err == nil {
		t.Fatal("expected error when type changes between desired and actual")
	}
	if !strings.Contains(err.Error(), "type change") {
		t.Errorf("error = %v; want one mentioning 'type change'", err)
	}
}

func TestPlanGateways_CreateCarriesNoDeployFlag(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
		m["deploy"] = true
	}))}

	plan, err := planGateways(t.Context(), nil, desired, nil, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("Creates = %+v", plan.Creates)
	}
	body, err := json.Marshal(plan.Creates[0].Create)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"deploy"`) {
		t.Errorf("create body %s carries a deploy flag", body)
	}
}

func TestPlanGateways_SlugRoundTrip(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
		m["slug"] = "my-mcp"
	}))}
	actual := []wire.EntrypointResponse{liveWebuiGateway("my-mcp")}

	plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("expected Unchanged=1, got Unchanged=%d Creates=%d Updates=%d Deletes=%d",
			len(plan.Unchanged), len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanGateways_SlugImmutable(t *testing.T) {
	tests := []struct {
		name        string
		desiredSlug string
		live        wire.EntrypointResponse
		wantErr     string
	}{
		{"differs from real live slug", "new-slug", liveWebuiGateway("old-slug"), "slug is immutable"},
		{"live slug is the id default", "id-1", liveWebuiGateway("id-1"), "cannot be added"},
		{"live slug is the id default, new slug authored", "helpdesk", liveWebuiGateway("id-1"), "cannot be added"},
		{"live slug is empty", "helpdesk", liveWebuiGateway(""), "cannot be added"},
		{"omitted desired slug", "", liveWebuiGateway("ui-set-slug"), ""},
		{"equals live slug", "my-mcp", liveWebuiGateway("my-mcp"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(func(m map[string]any) {
				if tt.desiredSlug != "" {
					m["slug"] = tt.desiredSlug
				}
			}))}
			plan, err := planGateways(t.Context(), nil, desired, []wire.EntrypointResponse{tt.live}, translate.GatewayHooks{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v; want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("planGateways: %v", err)
			}
			if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
				t.Errorf("Unchanged=%d Updates=%d; want 1 and 0", len(plan.Unchanged), len(plan.Updates))
			}
		})
	}
}

func TestPlanGateways_OmittedSlugIsNoOp(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}
	actual := []wire.EntrypointResponse{liveWebuiGateway("")}

	plan, err := planGateways(t.Context(), nil, desired, actual, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("expected omitted-slug gateway to be Unchanged=1, got Unchanged=%d Creates=%d Updates=%d Deletes=%d",
			len(plan.Unchanged), len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanGateways_DuplicateNameInDesired(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("dup", gatewayFixture(nil)),
		resolvedGateway("dup", gatewayFixture(nil)),
	}
	if _, err := planGateways(t.Context(), nil, desired, nil, translate.GatewayHooks{}); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func webhookGatewayFixture(targetAgent, targetWorkflow string) map[string]any {
	values := map[string]any{
		"routePath":       "/hook",
		"method":          "POST",
		"authMode":        "token",
		"authToken":       "dev-webhook-token",
		"inputExpression": "input.payload:message",
	}
	if targetAgent != "" {
		values["targetAgentName"] = targetAgent
	}
	if targetWorkflow != "" {
		values["targetWorkflowName"] = targetWorkflow
	}
	return map[string]any{"type": "webhook", "values": values}
}

func TestPlanGateways_WebhookWithoutTargetTypeConverges(t *testing.T) {
	for _, tt := range []struct {
		name           string
		targetAgent    string
		targetWorkflow string
		wantTargetType string
	}{
		{name: "agent target", targetAgent: "Orchestrator", wantTargetType: "agent"},
		{name: "workflow target", targetWorkflow: "OrderIntake", wantTargetType: "workflow"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := webhookGatewayFixture(tt.targetAgent, tt.targetWorkflow)
			authored := spec["values"].(map[string]any)
			if _, present := authored["targetType"]; present {
				t.Fatal("fixture must omit targetType — that is the case under test")
			}

			served := map[string]any{"targetType": tt.wantTargetType}
			for k, v := range authored {
				served[k] = v
			}

			plan, err := planGateways(t.Context(), nil,
				[]resolve.ResolvedResource{resolvedGateway("order-hook", spec)},
				[]wire.EntrypointResponse{{
					ID:             "id-1",
					Name:           "order-hook",
					Description:    "fixture gateway with a long-enough description",
					EntrypointType: "webhook",
					Values:         served,
				}},
				translate.GatewayHooks{})
			if err != nil {
				t.Fatalf("planGateways: %v", err)
			}

			if len(plan.Updates) != 0 {
				t.Errorf("Updates = %+v, want none — a no-op apply must not re-deploy the entrypoint", plan.Updates)
			}
			if len(plan.Unchanged) != 1 || plan.Unchanged[0].ID != "id-1" {
				t.Fatalf("Unchanged = %+v, want the served entrypoint", plan.Unchanged)
			}
		})
	}
}

func TestPlanGateways_ValidateWebhookAgentDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("webhook-ac10", webhookGatewayFixture("AgentThatDoesNotExist", "")),
	}
	hooks := translate.GatewayHooks{
		ValidateAgent: func(name string) error {
			return fmt.Errorf("agent %q not found on platform and not declared in the desired manifest", name)
		},
	}
	_, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling targetAgentName to fail plan, got nil")
	}
	for _, want := range []string{"webhook-ac10", "targetAgentName", "AgentThatDoesNotExist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestPlanGateways_ValidateWebhookWorkflowDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("webhook-wf", webhookGatewayFixture("", "MissingWorkflow")),
	}
	hooks := translate.GatewayHooks{
		ValidateWorkflow: func(name string) error {
			return fmt.Errorf("workflow %q not found", name)
		},
	}
	_, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling targetWorkflowName to fail plan, got nil")
	}
	if !strings.Contains(err.Error(), "targetWorkflowName") || !strings.Contains(err.Error(), "MissingWorkflow") {
		t.Errorf("error %q missing targetWorkflowName/MissingWorkflow", err)
	}
}

func TestPlanGateways_ValidateWebhookAgentResolves(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("webhook-ok", webhookGatewayFixture("Orchestrator", "")),
	}
	called := false
	hooks := translate.GatewayHooks{
		ValidateAgent: func(name string) error {
			called = true
			if name != "Orchestrator" {
				t.Errorf("ValidateAgent got %q, want Orchestrator", name)
			}
			return nil
		},
	}
	if _, err := planGateways(t.Context(), nil, desired, nil, hooks); err != nil {
		t.Fatalf("valid webhook target should pass plan: %v", err)
	}
	if !called {
		t.Error("ValidateAgent hook was never consulted for the webhook target")
	}
}

func TestGatewayOutOfSync_ReappliedConfigRedeploys(t *testing.T) {
	var deploys []apiv2.CreateEntrypointDeploymentRequest
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != platform.APIV2Prefix+entrypointDeploymentsResource.path {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			writeV2Error(w, http.StatusNotFound, "not found", nil)
			return
		}
		var req apiv2.CreateEntrypointDeploymentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode deploy request: %v", err)
		}
		deploys = append(deploys, req)
		writeV2Single(w, http.StatusCreated, apiv2.EntrypointDeployment{
			ID: "dep-1", Type: "entrypointDeployment", EntrypointID: req.EntrypointID, Status: entplatform.DeployStatusSuccess, Action: req.Action,
		})
	}))
	defer srv.Close()

	outOfSync := syncStatusOutOfSync
	live := liveWebuiGateway("")
	live.SyncStatus = &outOfSync
	live.DeploymentStatus = entplatform.DisplayStatusDeployed
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}

	plan, err := planGateways(t.Context(), nil, desired, []wire.EntrypointResponse{live}, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	ops := (&PlanResult{Gateways: plan}).Ops()
	if len(ops) != 1 || ops[0].Action != ActionRedeploy {
		t.Fatalf("plan ops = %+v, want one redeploy", ops)
	}

	c := NewClient(target.Resolved{URL: srv.URL, Token: "tok"})
	outcome := applyGatewayUpserts(t.Context(), c, plan)
	var progress strings.Builder
	runGatewayDeployments(t.Context(), newGatewayDeployer(c), &progress, outcome)

	want := []apiv2.CreateEntrypointDeploymentRequest{{EntrypointID: "id-1", Action: "update"}}
	if !reflect.DeepEqual(deploys, want) {
		t.Errorf("deploy requests = %+v, want %+v", deploys, want)
	}
	if got, wantLine := progress.String(), "deploying entrypoint/webui (update)...\n"; got != wantLine {
		t.Errorf("progress = %q, want %q", got, wantLine)
	}
	if len(outcome.Failures) != 0 {
		t.Errorf("unexpected failures: %+v", outcome.Failures)
	}
}

func TestGatewayInSync_UnchangedConfigDoesNotRedeploy(t *testing.T) {
	inSync := "in_sync"
	live := liveWebuiGateway("")
	live.SyncStatus = &inSync
	desired := []resolve.ResolvedResource{resolvedGateway("webui", gatewayFixture(nil))}

	plan, err := planGateways(t.Context(), nil, desired, []wire.EntrypointResponse{live}, translate.GatewayHooks{})
	if err != nil {
		t.Fatalf("planGateways: %v", err)
	}
	if ops := (&PlanResult{Gateways: plan}).Ops(); len(ops) != 1 || ops[0].Action != ActionUnchanged {
		t.Fatalf("plan ops = %+v, want one unchanged", ops)
	}
	c := NewClient(target.Resolved{URL: "http://127.0.0.1:0", Token: "tok"})
	if outcome := applyGatewayUpserts(t.Context(), c, plan); len(outcome.OutOfSync) != 0 {
		t.Errorf("OutOfSync = %+v, want none", outcome.OutOfSync)
	}
}
