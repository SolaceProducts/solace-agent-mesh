// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func gatewayComponent(name, targetAgent string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindGateway,
		Name: name,
		Content: `
apps:
  - name: ` + name + `
    app_config:
      gateway_id: ` + name + `
      agent_card:
        description: "Routes inbound events to an agent, used in tests."
      event_handlers:
        - name: on_event
          target_agent_name: ` + targetAgent + `
          subscriptions:
            - topic: events/incoming
`,
	}
}

func TestApply_CreatesEventMeshGatewayThroughAdapterCrossCheck(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{gatewayComponent("order_events", "Fulfilment")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindGateway, "order_events")
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}
	if op.ResolvedName != "OrderEvents" {
		t.Errorf("ResolvedName = %q, want OrderEvents", op.ResolvedName)
	}

	list, err := svc.deps.listGateways(ctx)
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	if !slices.ContainsFunc(list, func(g entplatform.GatewayResponse) bool { return g.Name == "OrderEvents" }) {
		t.Fatalf("gateway not created: %+v", list)
	}
}

func TestApply_GatewayIsStoredUnderItsManifestNameNotTheAppName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comp := gatewayComponent("ent_orders", "Fulfilment")
	comp.Name = "EntOrders"
	comp.Content = strings.Replace(strings.Replace(comp.Content, "name: ent_orders", "name: ent_orders_gateway", 1), "      gateway_id: ent_orders\n", "", 1)

	if strings.Contains(comp.Content, "gateway_id") || !strings.Contains(comp.Content, "name: ent_orders_gateway") {
		t.Fatalf("fixture no longer has an app name that differs from the manifest name and no gateway_id:\n%s", comp.Content)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindGateway, "EntOrders")
	if op.Status != statusSucceeded || op.ResolvedName != "EntOrders" {
		t.Fatalf("op = %+v, want success resolved to EntOrders", op)
	}
	list, err := svc.deps.listGateways(ctx)
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	if !slices.ContainsFunc(list, func(g entplatform.GatewayResponse) bool { return g.Name == "EntOrders" }) {
		t.Fatalf("gateway not stored as EntOrders: %+v", list)
	}
	if slices.ContainsFunc(list, func(g entplatform.GatewayResponse) bool { return g.Name == "EntOrdersGateway" }) {
		t.Fatalf("gateway stored under the app name: %+v", list)
	}
}

func TestApply_GatewayWithCamelCaseHandlerTargetNamesTheKey(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	_, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{{
		Kind: kindGateway,
		Name: "mule_events",
		Content: `
apps:
  - name: mule_events
    app_config:
      agent_card:
        description: "Routes transactions to the mule detection workflow."
      event_handlers:
        - name: on_txn
          subscriptions:
            - topic: txn/>
          inputExpression: "input.payload:"
          targetWorkflowName: MuleDetection
`,
	}}}, testActor)

	ve, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		t.Fatalf("Apply err = %v, want *ValidationError", err)
	}
	if got := ve.Details["event_handlers[0].targetWorkflowName"]; len(got) == 0 || !strings.Contains(got[0], "use target_workflow_name") {
		t.Errorf("Details = %v, want targetWorkflowName → target_workflow_name", ve.Details)
	}
	if strings.Contains(ve.Message, "promptTemplate") {
		t.Errorf("Message = %q, must not steer toward promptTemplate", ve.Message)
	}
}

func TestApply_UpdatesGateway(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{gatewayComponent("order_events", "Fulfilment")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	edited := gatewayComponent("order_events", "Shipping")
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	op := opFor(t, resp, kindGateway, "order_events")
	if op.Action != actionUpdate || op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want a succeeded update", op)
	}

	list, err := svc.deps.listGateways(ctx)
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want one gateway, got %d — the update created a duplicate", len(list))
	}
	values, _ := list[0].Values.(map[string]any)
	if !strings.Contains(mustJSON(t, values), "Shipping") {
		t.Errorf("stored values do not carry the retarget: %v", values)
	}
}

func TestDesiredGateway_TypedPassthroughReachesTheValidator(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: typed_expr
    app_config:
      gateway_id: typed_expr
      agent_card:
        description: "Uses a typed payload passthrough expression."
      event_handlers:
        - name: on_event
          target_agent_name: SomeAgent
          input_expression: "input.payload:text"
          subscriptions:
            - topic: typed/events
`
	got, err := desiredGateway("typed_expr", y)
	if err != nil {
		t.Fatalf("conversion should succeed and leave validation to the platform: %v", err)
	}
	values := got.create.(entplatform.CreateGatewayRequest).Values.(map[string]any)
	rules, _ := values["event_rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected one migrated rule, got %v", values["event_rules"])
	}
	rule, _ := rules[0].(map[string]any)
	if _, hasPrompt := rule["promptTemplate"]; hasPrompt {
		t.Error("a promptTemplate appeared — #3675 may be fixed; drop this test and enable the typed form in the flow checks")
	}
}

func TestApply_GatewayIsIdempotent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comp := gatewayComponent("order_events", "Fulfilment")

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if op := opFor(t, resp, kindGateway, "order_events"); op.Action != actionUnchanged {
		t.Errorf("Action = %q (changed=%v), want unchanged", op.Action, op.ChangedFields)
	}
}

func TestApply_GatewayEntrypointAckPolicyRoundTrips(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comp := gatewayComponent("ent_ack", "Fulfilment")
	comp.Content = strings.Replace(comp.Content, "      event_handlers:", "      acknowledgment_policy:\n        mode: on_completion\n        timeout_seconds: 90\n      event_handlers:", 1)
	req := ApplyBuildRequest{Components: []BuildComponentInput{comp}}

	resp, err := svc.Apply(ctx, req, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindGateway, "ent_ack")
	if op.Status != statusSucceeded || len(op.Warnings) != 0 {
		t.Fatalf("op = %+v, want a clean success", op)
	}
	list, err := svc.deps.listGateways(ctx)
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	var stored string
	for _, g := range list {
		if g.Name == "EntAck" {
			stored = mustJSON(t, g.Values)
		}
	}
	for _, want := range []string{`"acknowledgmentPolicy"`, `"mode":"on_completion"`, `"timeoutSeconds":90`} {
		if !strings.Contains(stored, want) {
			t.Fatalf("stored values lack %s: %s", want, stored)
		}
	}

	again, err := svc.Apply(ctx, req, testActor)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if op := opFor(t, again, kindGateway, "ent_ack"); op.Action != actionUnchanged {
		t.Errorf("second apply action = %q, want unchanged", op.Action)
	}
}
