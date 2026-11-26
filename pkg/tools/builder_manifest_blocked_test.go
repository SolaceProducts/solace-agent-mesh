// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const orderFulfillmentPlan = `name: order-fulfillment
components:
  - filename: inventory_api.json
    type: connector
    name: inventory_api
    status: draft
    origin: session
  - filename: order_intake.yaml
    type: agent
    name: OrderIntake
    status: validated
    origin: session
    connectors: []
  - filename: inventory_checker.yaml
    type: agent
    name: InventoryChecker
    status: validated
    origin: session
    connectors: [inventory_api]
    depends_on: [inventory_api]
  - filename: order_fulfillment.yaml
    type: workflow
    name: OrderFulfillment
    status: validated
    origin: session
    depends_on: [InventoryChecker, OrderIntake]
    nodes:
      - id: check
        type: agent
        agent_name: InventoryChecker
  - filename: orders_entrypoint.yaml
    type: gateway
    name: OrdersEntrypoint
    status: validated
    origin: session
    gateway_type: event_mesh
    depends_on: [OrderFulfillment]
`

const orderFulfillmentBlocked = `name: order-fulfillment
components:
  - filename: inventory_api.json
    type: connector
    name: inventory_api
    status: blocked
    blocked_reason: the http(s) URL of the Inventory API's OpenAPI document (specification_url)
    origin: session
  - filename: order_intake.yaml
    type: agent
    name: OrderIntake
    status: validated
    origin: session
    connectors: []
  - filename: inventory_checker.yaml
    type: agent
    name: InventoryChecker
    status: validated
    origin: session
    connectors: [inventory_api]
    depends_on: [inventory_api]
  - filename: order_fulfillment.yaml
    type: workflow
    name: OrderFulfillment
    status: validated
    origin: session
    depends_on: [InventoryChecker, OrderIntake]
    nodes:
      - id: check
        type: agent
        agent_name: InventoryChecker
  - filename: orders_entrypoint.yaml
    type: gateway
    name: OrdersEntrypoint
    status: validated
    origin: session
    gateway_type: event_mesh
    depends_on: [OrderFulfillment]
`

const withoutInventoryConnector = `name: order-fulfillment
components:
  - filename: order_intake.yaml
    type: agent
    name: OrderIntake
    status: validated
    origin: session
    connectors: []
  - filename: inventory_checker.yaml
    type: agent
    name: InventoryChecker
    status: validated
    origin: session
    connectors: []
  - filename: order_fulfillment.yaml
    type: workflow
    name: OrderFulfillment
    status: validated
    origin: session
    depends_on: [InventoryChecker, OrderIntake]
    nodes:
      - id: check
        type: agent
        agent_name: InventoryChecker
  - filename: orders_entrypoint.yaml
    type: gateway
    name: OrdersEntrypoint
    status: validated
    origin: session
    gateway_type: event_mesh
    depends_on: [OrderFulfillment]
`

const orderFulfillmentConfig = `
apps:
  - name: OrderFulfillment
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{check.output}}"}
      input_schema:
        type: object
        properties:
          order:
            type: string
      nodes:
        - id: check
          type: agent
          agent_name: InventoryChecker
          input:
            order: "{{workflow.input.order}}"
          depends_on: []
`

const agentConfigWithConnector = "apps:\n  - name: %s\n    connectors: [inventory_api]\n    app_config:\n      agent_name: %s\n      instruction: hi\n"

func seedOrderFulfillmentConfigs(t *testing.T, toolCtx *tool.Context, svc *mockArtifactService) {
	t.Helper()
	ctx := context.Background()
	for _, f := range []string{"order_intake.yaml", "orders_entrypoint.yaml"} {
		saveTestArtifact(ctx, svc, toolCtx, f, "apps:\n  - name: x\n    app_config:\n      agent_name: x\n      instruction: hi\n", "application/x-yaml")
	}
	saveTestArtifact(ctx, svc, toolCtx, "inventory_checker.yaml", strings.ReplaceAll(agentConfigWithConnector, "%s", "InventoryChecker"), "application/x-yaml")
	saveTestArtifact(ctx, svc, toolCtx, "order_fulfillment.yaml", orderFulfillmentConfig, "application/x-yaml")
}

func storedManifest(t *testing.T, toolCtx *tool.Context, svc *mockArtifactService) string {
	t.Helper()
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
	content, _, err := svc.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load stored manifest: %v", err)
	}
	return string(content)
}

func validateFull(t *testing.T, toolCtx *tool.Context, manifest string) *tool.Result {
	t.Helper()
	result, err := (&ValidateBuildManifestTool{}).Execute(context.Background(), map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return result
}

func TestValidateBuildManifest_BlockedComponentIsValidButNeverStamped(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)

	result := validateFull(t, toolCtx, orderFulfillmentBlocked)

	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("a blocked component with a reason is a valid manifest, got errors %v", extractValidationErrors(result.Data))
	}
	if strings.Contains(storedManifest(t, toolCtx, svc), "full_validation_passed") {
		t.Errorf("a manifest with a blocked component must not be stamped deployable:\n%s", storedManifest(t, toolCtx, svc))
	}
	blocked, _ := result.Data["blocked"].([]blockedComponent)
	if len(blocked) != 1 || blocked[0].Name != "inventory_api" {
		t.Fatalf("blocked = %+v, want inventory_api", blocked)
	}
	if !strings.Contains(blocked[0].Reason, "OpenAPI document") {
		t.Errorf("reason lost: %q", blocked[0].Reason)
	}
	wantHeld := []string{"InventoryChecker", "OrderFulfillment", "OrdersEntrypoint"}
	got := slices.Clone(blocked[0].HeldBack)
	slices.Sort(got)
	if !slices.Equal(got, wantHeld) {
		t.Errorf("held back = %v, want %v (OrderIntake does not use the connector)", got, wantHeld)
	}
	for _, want := range []string{"NOT deployable", "inventory_api", "nothing new has been deployed"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("the message to the Builder lost %q: %s", want, result.Message)
		}
	}
}

func TestValidateBuildManifest_UnblockedManifestIsStampedAgain(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "inventory_api.json", `{"type":"api","values":{"specification_url":"https://inventory.example.invalid/openapi.json","base_url":"https://inventory.example.invalid/v2"}}`, "application/vnd.sam-connector-config+json")

	resolved := strings.Replace(orderFulfillmentBlocked, "status: blocked", "status: validated", 1)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", resolved, "application/vnd.sam-build-manifest+yaml")
	result := validateFull(t, toolCtx, resolved)

	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("resolved manifest invalid: %v", extractValidationErrors(result.Data))
	}
	if _, has := result.Data["blocked"]; has {
		t.Errorf("a resolved manifest still reports blocked components")
	}
	if !strings.Contains(storedManifest(t, toolCtx, svc), "full_validation_passed: true") {
		t.Errorf("a manifest with nothing blocked must be stamped:\n%s", storedManifest(t, toolCtx, svc))
	}
}

func TestValidateBuildManifest_BlockedComponentNeedsAReasonItsOwnerCanActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		want     string
	}{
		"no reason": {
			manifest: strings.Replace(orderFulfillmentBlocked, "    blocked_reason: the http(s) URL of the Inventory API's OpenAPI document (specification_url)\n", "", 1),
			want:     "must say why",
		},
		"blank reason": {
			manifest: strings.Replace(orderFulfillmentBlocked, "the http(s) URL of the Inventory API's OpenAPI document (specification_url)", `"   "`, 1),
			want:     "must say why",
		},
		"reason that is not text": {
			manifest: strings.Replace(orderFulfillmentBlocked, "the http(s) URL of the Inventory API's OpenAPI document (specification_url)", "[a, b]", 1),
			want:     "not text",
		},
		"platform component": {
			manifest: orderFulfillmentBlocked + "  - filename: \"\"\n    type: agent\n    name: Existing\n    status: blocked\n    blocked_reason: x\n    origin: platform\n",
			want:     "already exists",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := (&ValidateBuildManifestTool{}).Execute(context.Background(), map[string]any{"manifest_yaml": tc.manifest}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if valid, _ := result.Data["valid"].(bool); valid {
				t.Fatalf("expected the manifest to be refused")
			}
			if !slices.ContainsFunc(extractValidationErrors(result.Data), func(m string) bool { return strings.Contains(m, tc.want) }) {
				t.Errorf("errors %v do not mention %q", extractValidationErrors(result.Data), tc.want)
			}
		})
	}
}

func TestValidateBuildManifest_BlockedOpenAPIConnectorMayHoldAPlaceholderSpec(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "inventory_api.json", `{"type":"api","subtype":"openapi","values":{"specification_url":"TODO","base_url":"https://inventory.example.invalid/v2"}}`, "application/vnd.sam-connector-config+json")

	result := validateFull(t, toolCtx, orderFulfillmentBlocked)
	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("a blocked connector with a placeholder spec must still be recorded as blocked: %v", extractValidationErrors(result.Data))
	}

	unblocked := strings.Replace(orderFulfillmentBlocked, "status: blocked", "status: validated", 1)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", unblocked, "application/vnd.sam-build-manifest+yaml")
	if valid, _ := validateFull(t, toolCtx, unblocked).Data["valid"].(bool); valid {
		t.Errorf("the same connector, no longer blocked, must be refused for its placeholder spec")
	}
}

func TestValidateBuildManifest_BlockedWorkflowNeedsNoConfig(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	manifest := strings.Replace(orderFulfillmentPlan, "name: OrderFulfillment\n    status: validated", "name: OrderFulfillment\n    status: blocked\n    blocked_reason: the workflow it calls does not exist yet", 1)
	manifest = strings.Replace(manifest, "status: draft", "status: validated", 1)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
	for _, f := range []string{"order_intake.yaml", "orders_entrypoint.yaml"} {
		saveTestArtifact(context.Background(), svc, toolCtx, f, "apps:\n  - name: x\n    app_config:\n      agent_name: x\n      instruction: hi\n", "application/x-yaml")
	}
	saveTestArtifact(context.Background(), svc, toolCtx, "inventory_checker.yaml", strings.ReplaceAll(agentConfigWithConnector, "%s", "InventoryChecker"), "application/x-yaml")
	saveTestArtifact(context.Background(), svc, toolCtx, "inventory_api.json", `{"type":"api","subtype":"openapi","values":{"specification_url":"https://x.example.invalid/o.json","base_url":"https://x.example.invalid"}}`, "application/vnd.sam-connector-config+json")

	result := validateFull(t, toolCtx, manifest)
	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("a blocked workflow has no config to load, got %v", extractValidationErrors(result.Data))
	}
}

func TestValidateBuildManifest_HeldBackDoesNotDependOnListingOrder(t *testing.T) {
	reversed := []map[string]any{
		{"name": "OrdersEntrypoint", "type": "gateway", "depends_on": []any{"OrderFulfillment"}},
		{"name": "OrderFulfillment", "type": "workflow", "nodes": []any{map[string]any{"agent_name": "InventoryChecker"}}},
		{"name": "InventoryChecker", "type": "agent", "connectors": []any{"inventory_api"}},
		{"name": "OrderIntake", "type": "agent"},
		{"name": "StockFlow", "type": "workflow", "nodes": []any{map[string]any{"connector": "inventory_api"}}},
		{"name": "inventory_api", "type": "connector", "status": "blocked", "blocked_reason": "x"},
	}
	blocked := blockedComponents(reversed)
	if len(blocked) != 1 {
		t.Fatalf("blocked = %+v", blocked)
	}
	got := slices.Clone(blocked[0].HeldBack)
	slices.Sort(got)
	if want := []string{"InventoryChecker", "OrderFulfillment", "OrdersEntrypoint", "StockFlow"}; !slices.Equal(got, want) {
		t.Errorf("held back = %v, want %v (a workflow tool node that binds the connector uses it)", got, want)
	}
}

func TestValidateBuildManifest_BlockedManifestStillRepairsLostDeployEvidence(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	stored := strings.Replace(orderFulfillmentBlocked, "name: OrderFulfillment\n    status: validated", "name: OrderFulfillment\n    status: deployed\n    id: 01a020e7-6397-78dd-bed8-e3c20314a9c2\n    deployed_version: 0", 1)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", stored, "application/vnd.sam-build-manifest+yaml")
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)

	result := validateFull(t, toolCtx, orderFulfillmentBlocked)

	if valid, _ := result.Data["valid"].(bool); !valid {
		t.Fatalf("the manifest is valid, got %v", extractValidationErrors(result.Data))
	}
	got := storedManifest(t, toolCtx, svc)
	if !strings.Contains(got, "id: 01a020e7-6397-78dd-bed8-e3c20314a9c2") || !strings.Contains(got, "deployed_version: 0") {
		t.Errorf("the lost deploy evidence was not put back:\n%s", got)
	}
	if strings.Contains(got, "full_validation_passed") {
		t.Errorf("a blocked manifest must not be stamped:\n%s", got)
	}
}

func TestValidateBuildManifest_RefusedDropIsNotPersistedByAnEvidenceRepair(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	stored := strings.Replace(orderFulfillmentBlocked, "name: OrderFulfillment\n    status: validated", "name: OrderFulfillment\n    status: deployed\n    id: 01a020e7-6397-78dd-bed8-e3c20314a9c2\n    deployed_version: 0", 1)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", stored, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)

	result := validateFull(t, toolCtx, withoutInventoryConnector)

	if valid, _ := result.Data["valid"].(bool); valid {
		t.Fatalf("the drop must be refused")
	}
	if got := storedManifest(t, toolCtx, svc); got != stored {
		t.Errorf("a refused validation replaced the stored manifest:\n%s", got)
	}
}

func TestValidateBuildManifest_BlockedComponentNeedsNoConfigArtifact(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)

	result := validateFull(t, toolCtx, orderFulfillmentBlocked)

	for _, msg := range extractValidationErrors(result.Data) {
		if strings.Contains(msg, "inventory_api.json") {
			t.Errorf("a blocked connector has no config to find, but validation asked for it: %s", msg)
		}
	}
}

func TestValidateBuildManifest_RefusesToDropAPlannedComponent(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentPlan, "application/vnd.sam-build-manifest+yaml")
	seedOrderFulfillmentConfigs(t, toolCtx, svc)

	result := validateFull(t, toolCtx, withoutInventoryConnector)

	if valid, _ := result.Data["valid"].(bool); valid {
		t.Fatalf("a manifest that drops a planned component was validated")
	}
	errs := strings.Join(extractValidationErrors(result.Data), "\n")
	for _, want := range []string{"inventory_api", "status: blocked", "blocked_reason"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errs)
		}
	}
	stored := storedManifest(t, toolCtx, svc)
	if stored != orderFulfillmentPlan {
		t.Errorf("a refused validation must not replace the stored manifest, got:\n%s", stored)
	}
}

func TestValidateBuildManifest_DroppingIsAllowedOnceTheRemovalIsASavedEdit(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentPlan, "application/vnd.sam-build-manifest+yaml")
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", withoutInventoryConnector, "application/vnd.sam-build-manifest+yaml")

	result := validateFull(t, toolCtx, withoutInventoryConnector)

	for _, msg := range extractValidationErrors(result.Data) {
		if strings.Contains(msg, "missing from this one") {
			t.Errorf("a removal the user asked for and the Builder saved is not a drop: %s", msg)
		}
	}
}

func TestValidateBuildManifest_RenamedComponentKeepingItsFileIsNotADrop(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentPlan, "application/vnd.sam-build-manifest+yaml")

	renamed := strings.ReplaceAll(orderFulfillmentPlan, "inventory_api", "inventory_api_v2")
	renamed = strings.Replace(renamed, "filename: inventory_api_v2.json", "filename: inventory_api.json", 1)
	result := validateFull(t, toolCtx, renamed)

	for _, msg := range extractValidationErrors(result.Data) {
		if strings.Contains(msg, "missing from this one") {
			t.Errorf("a rename that keeps the config file is not a drop: %s", msg)
		}
	}
}

func TestValidateBuildManifest_DroppingABlockedComponentIsRefusedToo(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentBlocked, "application/vnd.sam-build-manifest+yaml")

	result := validateFull(t, toolCtx, withoutInventoryConnector)

	if !slices.ContainsFunc(extractValidationErrors(result.Data), func(m string) bool {
		return strings.Contains(m, "inventory_api") && strings.Contains(m, "missing from this one")
	}) {
		t.Errorf("dropping the blocked connector went unnoticed: %v", extractValidationErrors(result.Data))
	}
}

func TestValidateBuildManifest_UnparsableManifestIsNotReportedAsEveryComponentDropped(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "build_manifest.yaml", orderFulfillmentPlan, "application/vnd.sam-build-manifest+yaml")

	result := validateFull(t, toolCtx, "name: x\ncomponents: [")

	for _, msg := range extractValidationErrors(result.Data) {
		if strings.Contains(msg, "missing from this one") {
			t.Errorf("a manifest that does not parse said every component was dropped: %s", msg)
		}
	}
}

func TestBlockedStatusIsPartOfTheManifestVocabulary(t *testing.T) {
	if !validStatuses[componentStatusBlocked] {
		t.Fatalf("%q is not an accepted status", componentStatusBlocked)
	}
}

func TestHeldBack_OnlyTextEntriesAreNames(t *testing.T) {
	odd := []any{nil, 42, map[string]any{"name": "inventory_api"}, map[string]any{}, []any{"inventory_api"}, ""}
	connector := map[string]any{"name": "inventory_api", "type": "connector", "status": "blocked", "blocked_reason": "x"}
	for _, field := range []string{"skills", "connectors", "depends_on"} {
		t.Run(field, func(t *testing.T) {
			user := map[string]any{"name": "StockFlow", "type": "agent", field: odd}
			if got := blockedComponents([]map[string]any{connector, user})[0].HeldBack; len(got) != 0 {
				t.Errorf("odd entries held back %v", got)
			}
			user[field] = append([]any{nil}, "inventory_api")
			if got := blockedComponents([]map[string]any{connector, user})[0].HeldBack; len(got) != 1 {
				t.Errorf("a text entry did not hold the component back: %v", got)
			}
		})
	}
	for name, nodes := range map[string]any{
		"not a list":     "inventory_api",
		"null and nums":  []any{nil, 42},
		"names not text": []any{map[string]any{"agent_name": 42, "workflow_name": map[string]any{}, "connector": []any{"inventory_api"}}},
	} {
		t.Run("nodes "+name, func(t *testing.T) {
			wf := map[string]any{"name": "StockFlow", "type": "workflow", "nodes": nodes}
			if got := blockedComponents([]map[string]any{connector, wf})[0].HeldBack; len(got) != 0 {
				t.Errorf("odd nodes held back %v", got)
			}
		})
	}
}
