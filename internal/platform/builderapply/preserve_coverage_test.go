// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestApply_EntrypointWithPublicURLConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := gatewayComponent("order_events", "Fulfilment")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	stored := gatewayNamed(t, svc, "OrderEvents")
	publicURL := "https://entrypoint.example.com/hook"
	if _, err := svc.deps.Gateways.Update(ctx, stored.ID, entplatform.UpdateGatewayRequest{
		Name:        stored.Name,
		Description: stored.Description,
		PublicURL:   &publicURL,
		Values:      stored.Values,
	}, "entrypoint-page"); err != nil {
		t.Fatalf("seed publicUrl: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if op := opFor(t, resp, kindGateway, "order_events"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — publicUrl is not something the Builder authors",
			op.Action, op.ChangedFields)
	}
	if got := gatewayNamed(t, svc, "OrderEvents").PublicURL; got != publicURL {
		t.Errorf("publicUrl = %q, want %q preserved", got, publicURL)
	}
}

func TestApply_EntrypointKeepsStoredValuesTheArtifactOmits(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := gatewayComponent("order_events", "Fulfilment")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	stored := gatewayNamed(t, svc, "OrderEvents")
	values, _ := stored.Values.(map[string]any)
	values["default_user_identity"] = "operator@example.com"
	if _, err := svc.deps.Gateways.Update(ctx, stored.ID, entplatform.UpdateGatewayRequest{
		Name: stored.Name, Description: stored.Description, Values: values,
	}, "entrypoint-page"); err != nil {
		t.Fatalf("seed values: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if op := opFor(t, resp, kindGateway, "order_events"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged", op.Action, op.ChangedFields)
	}

	after, _ := gatewayNamed(t, svc, "OrderEvents").Values.(map[string]any)
	if after["default_user_identity"] != "operator@example.com" {
		t.Errorf("default_user_identity = %v, want the operator's value kept", after["default_user_identity"])
	}
}

func TestApply_NonAlphabeticalModesConverge(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := BuildComponentInput{
		Kind: kindAgent,
		Name: "multimodal_agent",
		Content: `
apps:
  - name: multimodal_agent
    app_config:
      agent_name: multimodal_agent
      instruction: You are a test agent that answers questions.
      agent_card:
        description: "An agent whose modes are not authored alphabetically."
        defaultInputModes: [text, image, file]
        defaultOutputModes: [text, image]
`,
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "multimodal_agent"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — modes were authored in a different order than they read back",
			op.Action, op.ChangedFields)
	}

	got := agentNamed(ctx, t, svc, "multimodal_agent")
	if len(got.InputModes) != 3 || len(got.OutputModes) != 2 {
		t.Errorf("modes = in:%v out:%v, want all of them stored", got.InputModes, got.OutputModes)
	}
}

func TestApply_WarnsWhenAValuesRemovalIsIgnored(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	trimmed := map[string]any{"hostname": "db", "port": 5432, "username": "svc", "password": "real-password"}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", trimmed)},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	op := opFor(t, resp, kindConnector, "db_conn")
	if !strings.Contains(strings.Join(op.Warnings, " "), "database") {
		t.Errorf("warnings = %v, want one naming the key that was kept despite being dropped from the config",
			op.Warnings)
	}
}

func gatewayNamed(t *testing.T, svc *Service, name string) entplatform.GatewayResponse {
	t.Helper()
	list, err := svc.deps.listGateways(t.Context())
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	for _, g := range list {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("entrypoint %q not found", name)
	return entplatform.GatewayResponse{}
}
