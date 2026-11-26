// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func a2aComponent(name string, mutate func(map[string]any)) BuildComponentInput {
	body := map[string]any{
		"name":              name,
		"agentUrl":          "https://example.com/a2a",
		"agentCardLocation": "well_known",
	}
	if mutate != nil {
		mutate(body)
	}
	raw, _ := json.Marshal(body)
	return BuildComponentInput{Kind: kindA2AAgent, Name: name, Content: string(raw)}
}

func connectorWith(name string, mutate func(map[string]any)) BuildComponentInput {
	body := map[string]any{
		"name":        name,
		"description": "A connector used in tests.",
		"type":        "sql",
		"subtype":     "postgres",
		"values":      map[string]any{"hostname": "db"},
	}
	if mutate != nil {
		mutate(body)
	}
	raw, _ := json.Marshal(body)
	return BuildComponentInput{Kind: kindConnector, Name: name, Content: string(raw)}
}

func agentWithDescription(name, description string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content, "An agent created by the plan integration test.", description, 1)
	return c
}

func agentWithInstruction(name, instruction string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content, "You are a test agent that answers questions.", instruction, 1)
	return c
}

func agentWithoutInstruction(name string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content, "      instruction: You are a test agent that answers questions.\n", "", 1)
	return c
}

func gatewayWithDuplicateHandler(name string) BuildComponentInput {
	c := gatewayComponent(name, "test_agent")
	c.Content = strings.Replace(c.Content, "      event_handlers:\n",
		"      event_handlers:\n        - name: ON_EVENT\n          target_agent_name: test_agent\n          subscriptions:\n            - topic: events/other\n", 1)
	return c
}

func gatewayWithAckMode(name, mode string) BuildComponentInput {
	c := gatewayComponent(name, "test_agent")
	c.Content = strings.Replace(c.Content, "      gateway_id: "+name+"\n", "      gateway_id: "+name+"\n      acknowledgment_policy:\n        mode: "+mode+"\n", 1)
	return c
}

const validBrokerBlock = `      event_mesh_broker_config:
        broker_url: tcp://broker.example.com:55555
        broker_vpn: default
        broker_username: gw
        broker_password: real-password-1
`

func gatewayWithBroker(name, brokerBlock string) BuildComponentInput {
	c := gatewayComponent(name, "test_agent")
	c.Content = strings.Replace(c.Content, "      gateway_id: "+name+"\n", "      gateway_id: "+name+"\n"+brokerBlock, 1)
	return c
}

func gatewayWithoutSubscriptions(name string) BuildComponentInput {
	c := gatewayComponent(name, "test_agent")
	c.Content = strings.Replace(c.Content, "          subscriptions:\n            - topic: events/incoming\n", "          subscriptions: []\n", 1)
	return c
}

func gatewayTargetingAgentAndWorkflow(name string) BuildComponentInput {
	c := gatewayComponent(name, "test_agent")
	c.Content = strings.Replace(c.Content, "target_agent_name: test_agent", "target_workflow_name: wf\n          target_agent_name: test_agent", 1)
	return c
}

func workflowWith(name, extraTop, extraBody string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	c.Content = strings.Replace(c.Content, "      agent_type: workflow\n", "      agent_type: workflow\n"+extraTop, 1)
	c.Content = strings.Replace(c.Content, "        nodes:\n", extraBody+"        nodes:\n", 1)
	return c
}

func workflowWithDecisionNode(name string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	c.Content = strings.Replace(c.Content, "        nodes:\n", "        nodes:\n          - id: dec\n            type: decision\n            depends_on: []\n", 1)
	return c
}

func workflowWithConnectorOnAgentNode(name string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	c.Content = strings.Replace(c.Content, "            agent_name: test_agent\n", "            agent_name: test_agent\n            connector: db\n", 1)
	return c
}

func workflowWithDecisionAndAlias(name, targetAgent string) BuildComponentInput {
	c := workflowWithDecisionNode(name)
	c.Content = strings.Replace(c.Content, "      agent_type: workflow\n", "      agent_type: workflow\n      decision:\n        model_alias: judge\n", 1)
	c.Content = strings.Replace(c.Content, "agent_name: test_agent", "agent_name: "+targetAgent, 1)
	return c
}

func workflowWithDescription(name, description string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	c.Content = strings.Replace(c.Content, "A workflow exercised by the apply tests.", description, 1)
	return c
}

func platformSnapshot(t *testing.T, svc *Service) string {
	t.Helper()
	ctx := t.Context()
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	connectors, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	workflows, err := svc.deps.listWorkflows(ctx)
	if err != nil {
		t.Fatalf("listWorkflows: %v", err)
	}
	gateways, err := svc.deps.listGateways(ctx)
	if err != nil {
		t.Fatalf("listGateways: %v", err)
	}
	remotes, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	raw, err := json.Marshal([]any{agents, connectors, workflows, gateways, remotes})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return string(raw)
}

func applyBoth(t *testing.T, svc *Service, components ...BuildComponentInput) (dry, applied *ApplyBuildResponse) {
	t.Helper()
	before := platformSnapshot(t, svc)

	dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: components}, testActor)
	if err != nil {
		t.Fatalf("dry-run Apply: %v", err)
	}
	if after := platformSnapshot(t, svc); after != before {
		t.Fatalf("the dry run wrote to the platform:\nbefore %s\nafter  %s", before, after)
	}

	applied, err = svc.Apply(t.Context(), ApplyBuildRequest{Components: components}, testActor)
	if err != nil {
		t.Fatalf("real Apply: %v", err)
	}
	return dry, applied
}

func requireSameFailure(t *testing.T, dry, applied BuildOperationResult, wantMessage string) {
	t.Helper()
	if applied.Status != statusFailed || applied.Error == nil {
		t.Fatalf("the real apply did not reject this component, so the case proves nothing: %+v", applied)
	}
	if !strings.Contains(applied.Error.Message, wantMessage) {
		t.Fatalf("real apply failed for another reason: %q, want it to contain %q", applied.Error.Message, wantMessage)
	}
	if dry.Status != statusFailed || dry.Error == nil {
		t.Fatalf("dry run says %q for a component the real apply rejects with %q", dry.Status, applied.Error.Message)
	}
	if dry.Action != applied.Action {
		t.Errorf("action: dry run %q, real apply %q", dry.Action, applied.Action)
	}
	if dry.Error.Message != applied.Error.Message {
		t.Errorf("message: dry run %q, real apply %q", dry.Error.Message, applied.Error.Message)
	}
	if dry.Error.HTTPStatus != applied.Error.HTTPStatus {
		t.Errorf("httpStatus: dry run %d, real apply %d", dry.Error.HTTPStatus, applied.Error.HTTPStatus)
	}
	if !reflect.DeepEqual(dry.Error.ValidationDetails, applied.Error.ValidationDetails) {
		t.Errorf("validationDetails: dry run %v, real apply %v", dry.Error.ValidationDetails, applied.Error.ValidationDetails)
	}
	if dry.Error.ErrorID == "" {
		t.Error("a failed dry-run row must carry an errorId like any other failure")
	}
}

type parityCase struct {
	name        string
	existing    *BuildComponentInput
	seed        func(t *testing.T, svc *Service)
	component   BuildComponentInput
	wantMessage string
}

func parityCases() []parityCase {
	long := func(n int) string { return strings.Repeat("d", n) }
	existingAgent := agentComponent("edited_agent")
	existingConnector := connectorWith("edited-db", nil)
	existingRemote := a2aComponent("edited_remote", nil)
	existingTrustedRemote := a2aComponent("trusted_remote", func(b map[string]any) {
		b["trustedCardJwksUrls"] = []any{"https://keys.example.com/jwks.json"}
		b["requireSignedCard"] = true
	})
	existingKeySetsOnlyRemote := a2aComponent("keysets_remote", func(b map[string]any) {
		b["trustedCardJwksUrls"] = []any{"https://keys.example.com/jwks.json"}
	})
	existingWorkflow := workflowComponent("edited_flow", "edited_flow", "test_agent")
	existingGateway := gatewayComponent("edited_gw", "test_agent")
	existingBrokerGateway := gatewayWithBroker("edited_broker_gw", validBrokerBlock)
	existingAPIConnector := connectorWith("edited-api", func(b map[string]any) {
		b["type"], b["subtype"] = "api", "openapi"
		b["values"] = map[string]any{"base_url": "https://example.com"}
	})

	return []parityCase{
		{name: "connector name under 3 characters", component: connectorWith("ab", nil), wantMessage: "name must be at least 3"},
		{name: "connector description under 10 characters", component: connectorWith("short-desc", func(b map[string]any) { b["description"] = "short" }), wantMessage: "description must be at least 10"},
		{name: "connector description over 1000 characters", component: connectorWith("long-desc", func(b map[string]any) { b["description"] = long(3000) }), wantMessage: "description must be at most 1000"},
		{name: "connector values rejected by the connector type", component: connectorWith("bad-url", func(b map[string]any) {
			b["type"], b["subtype"] = "api", "openapi"
			b["values"] = map[string]any{"base_url": "not a url"}
		}), wantMessage: "base_url"},
		{name: "connector with an unknown subtype", component: connectorWith("odd-sub", func(b map[string]any) { b["subtype"] = "no_such_subtype" }), wantMessage: "no_such_subtype"},
		{name: "connector of a type disabled on this deployment", component: searchConnector("finder"), wantMessage: "not enabled"},
		{name: "agent description over 1000 characters", component: agentWithDescription("long_desc_agent", long(3000)), wantMessage: "description must be at most 1000"},
		{name: "agent description under 10 characters", component: agentWithDescription("short_desc_agent", "abc"), wantMessage: "description must be at least 10"},
		{name: "agent description with a control character", component: agentWithDescription("ctrl_agent", `bad\atext in the description`), wantMessage: "unsafe characters"},
		{name: "agent instruction over the deployment bound", component: agentWithInstruction("verbose_agent", strings.Repeat("a", 40000)), wantMessage: "systemPrompt must be 32000 characters or fewer"},
		{name: "workflow description under 10 characters", component: workflowWithDescription("short_flow", "abc"), wantMessage: "description must be at least 10"},
		{name: "a2a name under 3 characters", component: a2aComponent("ab", nil), wantMessage: "name must be at least 3"},
		{name: "a2a name over 40 characters", component: a2aComponent(strings.Repeat("n", 50), nil), wantMessage: "name must be at most 40"},
		{name: "a2a requestTimeoutSeconds under 10", component: a2aComponent("fast_remote", func(b map[string]any) { b["requestTimeoutSeconds"] = 5 }), wantMessage: "must be at least 10"},
		{name: "a2a agentAuthType outside the allowed set", component: a2aComponent("magic_remote", func(b map[string]any) { b["agentAuthType"] = "magic" }), wantMessage: "agentAuthType must be one of"},
		{name: "a2a agentUrl that is not http", component: a2aComponent("ftp_remote", func(b map[string]any) { b["agentUrl"] = "ftp://x" }), wantMessage: "agentUrl must start with http:// or https://"},
		{name: "agent without an instruction", component: agentWithoutInstruction("no_prompt_agent"), wantMessage: "system prompt is required"},
		{name: "workflow decision node without a model alias", component: workflowWithDecisionNode("no_alias_flow"), wantMessage: "no decision.model_alias"},
		{name: "workflow with an inline decision model", component: workflowWith("inline_model_flow", "      decision:\n        model: gpt-4\n", ""), wantMessage: "decision.model is for file-loaded configs only"},
		{name: "workflow listing a toolset twice", component: workflowWith("dup_toolset_flow", "      toolsets: [artifact_management, artifact_management]\n", ""), wantMessage: "more than once"},
		{name: "workflow with an empty toolset name", component: workflowWith("empty_toolset_flow", "      toolsets: [\"\"]\n", ""), wantMessage: "non-empty toolset name"},
		{name: "workflow connector on a node that is not a tool node", component: workflowWithConnectorOnAgentNode("agent_connector_flow"), wantMessage: "connector is only valid on tool nodes"},
		{name: "workflow with connector_refs", component: workflowWith("refs_flow", "", "        connector_refs: [x]\n"), wantMessage: "connector_refs is no longer supported"},
		{name: "entrypoint with duplicate handler names", component: gatewayWithDuplicateHandler("dup_gateway"), wantMessage: "duplicate rule name"},
		{name: "entrypoint handler targeting an agent and a workflow", component: gatewayTargetingAgentAndWorkflow("both_gateway"), wantMessage: "mutually exclusive"},
		{name: "a2a card-less agent without skills", component: a2aComponent("bare_remote", func(b map[string]any) { b["agentCardLocation"] = "none" }), wantMessage: "skills are required when agentCardLocation is 'none'"},
		{name: "workflow decision alias that names no model", component: workflowWithDecisionAndAlias("missing_alias_flow", "test_agent"), wantMessage: `decision.model_alias "judge"`},
		{
			name:        "workflow tool node on a stored spec-URL connector with no allow_list",
			seed:        func(t *testing.T, svc *Service) { seedStoredURLOnlyConnector(t, svc, "petstore") },
			component:   workflowCallingConnector("ship_flow", "petstore", "listPets", ""),
			wantMessage: "set allow_list",
		},
		{name: "workflow built-in tool node with an argument the tool rejects", component: workflowFetching("fetch_flow", badFetchInput, producedFetchField), wantMessage: "output_filename"},
		{name: "a2a custom card location without a card url", component: a2aComponent("custom_remote", func(b map[string]any) { b["agentCardLocation"] = "custom" }), wantMessage: "agentCardUrl is required"},

		{
			name:     "update of an agent to a description under 10 characters",
			existing: &existingAgent, component: agentWithDescription("edited_agent", "abc"),
			wantMessage: "description must be at least 10",
		},
		{
			name:     "update of a connector to a description under 10 characters",
			existing: &existingConnector, component: connectorWith("edited-db", func(b map[string]any) { b["description"] = "short" }),
			wantMessage: "description must be at least 10",
		},
		{
			name:     "update of a connector to values that are a list",
			existing: &existingConnector, component: connectorWith("edited-db", func(b map[string]any) { b["values"] = []any{} }),
			wantMessage: "values must be a JSON object",
		},
		{
			name:     "update of a connector to values that are a string",
			existing: &existingConnector, component: connectorWith("edited-db", func(b map[string]any) { b["values"] = "hostname" }),
			wantMessage: "values must be a JSON object",
		},
		{
			name:     "update of an agent to an empty instruction",
			existing: &existingAgent, component: agentWithInstruction("edited_agent", `""`),
			wantMessage: "system prompt is required",
		},
		{
			name:     "update of a connector to a base_url that is too short",
			existing: &existingAPIConnector, component: connectorWith("edited-api", func(b map[string]any) {
				b["type"], b["subtype"] = "api", "openapi"
				b["values"] = map[string]any{"base_url": "not a url"}
			}),
			wantMessage: "base_url",
		},
		{
			name:     "update of a connector to a hostname that is not one",
			existing: &existingConnector, component: connectorWith("edited-db", func(b map[string]any) { b["values"] = map[string]any{"hostname": "bad host name!"} }),
			wantMessage: "hostname",
		},
		{
			name:     "update of a connector to a port that is not a number",
			existing: &existingConnector, component: connectorWith("edited-db", func(b map[string]any) { b["values"] = map[string]any{"hostname": "db", "port": "notnumber"} }),
			wantMessage: "port",
		},
		{
			name:     "update of an entrypoint to a broker url that is not one",
			existing: &existingBrokerGateway, component: gatewayWithBroker("edited_broker_gw", strings.Replace(validBrokerBlock, "tcp://broker.example.com:55555", "not-a-url", 1)),
			wantMessage: "broker_url",
		},
		{
			name:     "update of an entrypoint to a tls_skip_verify that is not a choice",
			existing: &existingBrokerGateway, component: gatewayWithBroker("edited_broker_gw", validBrokerBlock+"        tls_skip_verify: maybe\n"),
			wantMessage: "tls_skip_verify",
		},
		{
			name:     "update of an entrypoint to duplicate handler names",
			existing: &existingGateway, component: gatewayWithDuplicateHandler("edited_gw"),
			wantMessage: "duplicate rule name",
		},
		{
			name:     "update of an entrypoint with a handler targeting an agent and a workflow",
			existing: &existingGateway, component: gatewayTargetingAgentAndWorkflow("edited_gw"),
			wantMessage: "mutually exclusive",
		},
		{
			name:     "update of an entrypoint to a handler with no subscriptions",
			existing: &existingGateway, component: gatewayWithoutSubscriptions("edited_gw"),
			wantMessage: "subscriptions",
		},
		{
			name:     "update of an entrypoint to an acknowledgment mode that does not exist",
			existing: &existingGateway, component: gatewayWithAckMode("edited_gw", "bogus"),
			wantMessage: "on_completion",
		},
		{
			name:     "update of a remote agent to a card-less agent with no skills",
			existing: &existingRemote, component: a2aComponent("edited_remote", func(b map[string]any) { b["agentCardLocation"] = "none" }),
			wantMessage: "skills are required when agentCardLocation is 'none'",
		},
		{
			name:     "update of a trusted remote agent to a card-less one, card trust left unstated",
			existing: &existingTrustedRemote, component: a2aComponent("trusted_remote", func(b map[string]any) {
				b["agentCardLocation"] = "none"
				b["skills"] = []any{}
			}),
			wantMessage: "cannot be set when agentCardLocation is 'none'",
		},
		{
			name:     "update of a remote agent with trusted key sets only to a card-less one",
			existing: &existingKeySetsOnlyRemote, component: a2aComponent("keysets_remote", func(b map[string]any) {
				b["agentCardLocation"] = "none"
				b["skills"] = []any{}
			}),
			wantMessage: "cannot be set when agentCardLocation is 'none'",
		},
		{
			name:     "update of a remote agent to require a signed card with no trusted key sets",
			existing: &existingRemote, component: a2aComponent("edited_remote", func(b map[string]any) { b["requireSignedCard"] = true }),
			wantMessage: "requireSignedCard needs at least one trustedCardJwksUrls entry",
		},
		{
			name:     "update of a remote agent to a plain-http key set off the loopback host",
			existing: &existingRemote, component: a2aComponent("edited_remote", func(b map[string]any) {
				b["trustedCardJwksUrls"] = []any{"http://keys.example.com/jwks.json"}
			}),
			wantMessage: "must be an absolute https URL",
		},
		{
			name:     "update of a workflow listing a toolset twice",
			existing: &existingWorkflow, component: workflowWith("edited_flow", "      toolsets: [artifact_management, artifact_management]\n", ""),
			wantMessage: "more than once",
		},
		{
			name:     "update of an a2a agent to an agentUrl that is not http",
			existing: &existingRemote, component: a2aComponent("edited_remote", func(b map[string]any) { b["agentUrl"] = "ftp://x" }),
			wantMessage: "agentUrl must start with http:// or https://",
		},
		{
			name:     "update of an a2a agent to requestTimeoutSeconds under 10",
			existing: &existingRemote, component: a2aComponent("edited_remote", func(b map[string]any) { b["requestTimeoutSeconds"] = 5 }),
			wantMessage: "must be at least 10",
		},
	}
}

func TestDryRun_ReportsTheVerdictARealApplyReturns(t *testing.T) {
	t.Parallel()
	for _, tc := range parityCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
				t.Fatalf("seed agent: %v", err)
			}
			if tc.seed != nil {
				tc.seed(t, svc)
			}
			if tc.existing != nil {
				resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{*tc.existing}}, testActor)
				if err != nil || resp.Summary.Failed != 0 {
					t.Fatalf("seed %s %q: %v %+v", tc.existing.Kind, tc.existing.Name, err, resp)
				}
			}

			dry, applied := applyBoth(t, svc, tc.component)

			wantAction := actionCreate
			if tc.existing != nil {
				wantAction = actionUpdate
			}
			dryOp := opFor(t, dry, tc.component.Kind, tc.component.Name)
			realOp := opFor(t, applied, tc.component.Kind, tc.component.Name)
			if realOp.Action != wantAction {
				t.Fatalf("action = %q, want %q", realOp.Action, wantAction)
			}
			requireSameFailure(t, dryOp, realOp, tc.wantMessage)

			if dry.Summary != applied.Summary {
				t.Errorf("summary: dry run %+v, real apply %+v", dry.Summary, applied.Summary)
			}
		})
	}
}

func TestDryRun_CollectsEveryFieldErrorOfAComponent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	bad := connectorWith("ab", func(b map[string]any) { b["description"] = "short" })

	dry, applied := applyBoth(t, svc, bad)

	dryOp, realOp := opFor(t, dry, kindConnector, "ab"), opFor(t, applied, kindConnector, "ab")
	requireSameFailure(t, dryOp, realOp, "name must be at least 3")
	for _, field := range []string{"name", "description"} {
		if len(dryOp.Error.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails misses %q: %v", field, dryOp.Error.ValidationDetails)
		}
	}
}

func TestDryRun_FlagsTheComponentThatWouldFailTheBuild(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	components := []BuildComponentInput{
		connectorWith("good-db", nil),
		agentWithDescription("bad_agent", "abc"),
		workflowComponent("good_flow", "good_flow", "test_agent"),
	}

	before := platformSnapshot(t, svc)

	dry, applied := applyBoth(t, svc, components...)

	if after := platformSnapshot(t, svc); after != before {
		t.Fatalf("a refused build wrote to the platform:\nbefore %s\nafter  %s", before, after)
	}
	if dry.Summary.Failed != 1 {
		t.Fatalf("dry-run summary = %+v, want the agent reported failed", dry.Summary)
	}
	requireSameFailure(t, opFor(t, dry, kindAgent, "bad_agent"), opFor(t, applied, kindAgent, "bad_agent"), "description must be at least 10")
	for _, valid := range []struct{ kind, name string }{{kindConnector, "good-db"}, {kindWorkflow, "good_flow"}} {
		if op := opFor(t, dry, valid.kind, valid.name); op.Status != statusPlanned {
			t.Errorf("%s %q = %q in the dry run, want it still planned", valid.kind, valid.name, op.Status)
		}
	}
	if got := opFor(t, applied, kindConnector, "good-db").Status; got != statusSkipped {
		t.Errorf("real apply connector = %q, want skipped: nothing is written once the build is refused", got)
	}
}

func TestDryRun_ReportsEveryRejectedComponentNotJustTheFirst(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	components := []BuildComponentInput{
		connectorWith("ab", nil),
		connectorWith("xy", nil),
		connectorWith("good-db", nil),
		a2aComponent("fast_remote", func(b map[string]any) { b["requestTimeoutSeconds"] = 5 }),
	}

	dry, applied := applyBoth(t, svc, components...)

	if dry.Summary.Failed != 3 {
		t.Fatalf("dry-run summary = %+v, want all three rejected components reported failed", dry.Summary)
	}
	for _, name := range []string{"ab", "xy"} {
		requireSameFailure(t, opFor(t, dry, kindConnector, name), opFor(t, applied, kindConnector, name), "name must be at least 3")
	}
	requireSameFailure(t, opFor(t, dry, kindA2AAgent, "fast_remote"), opFor(t, applied, kindA2AAgent, "fast_remote"), "must be at least 10")
	if got := opFor(t, dry, kindConnector, "good-db").Status; got != statusPlanned {
		t.Errorf("valid connector = %q, want planned", got)
	}
	if got := opFor(t, applied, kindConnector, "good-db").Status; got != statusSkipped {
		t.Errorf("valid connector = %q in the real apply, want skipped: nothing is written once the build is refused", got)
	}
}

func TestDryRun_PlansAValidBuildExactlyAsBefore(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedModel(t, svc, "judge", store.ModelTypeDecision)
	components := []BuildComponentInput{
		connectorWith("good-db", nil),
		a2aComponent("good_remote", nil),
		a2aComponent("cardless_remote", func(b map[string]any) {
			b["agentCardLocation"] = "none"
			b["skills"] = []any{}
		}),
		agentComponent("good_agent"),
		workflowComponent("good_flow", "good_flow", "good_agent"),
		workflowWithDecisionAndAlias("decision_flow", "good_agent"),
		gatewayComponent("good_gateway", "good_agent"),
	}

	dry, applied := applyBoth(t, svc, components...)

	if dry.Summary.Failed != 0 || applied.Summary.Failed != 0 {
		t.Fatalf("a valid build was rejected: dry %+v, real %+v", dry.Summary, applied.Summary)
	}
	if len(dry.Operations) != len(applied.Operations) {
		t.Fatalf("dry run planned %d operations, real apply ran %d", len(dry.Operations), len(applied.Operations))
	}
	for i, want := range applied.Operations {
		got := dry.Operations[i]
		if got.Status != statusPlanned {
			t.Errorf("%s %q = %q, want planned", got.Kind, got.Name, got.Status)
		}
		if got.Kind != want.Kind || got.Name != want.Name || got.Action != want.Action {
			t.Errorf("operation %d: dry run %s %q %s, real apply %s %q %s", i, got.Kind, got.Name, got.Action, want.Kind, want.Name, want.Action)
		}
		if got.Error != nil {
			t.Errorf("%s %q carries an error on a valid build: %+v", got.Kind, got.Name, got.Error)
		}
	}
	if dry.Summary != applied.Summary {
		t.Errorf("summary: dry run %+v, real apply %+v", dry.Summary, applied.Summary)
	}
}

func TestDryRun_LeavesAnUnchangedComponentPlanned(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{a2aComponent("steady_remote", nil)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const staleURL = "ftp://stale.example.com/a2a"
	repo := store.NewA2AAgentRepo(svc.deps.DB)
	row, err := repo.GetByName(t.Context(), "steady_remote")
	if err != nil || row == nil {
		t.Fatalf("read seed: %v %v", row, err)
	}
	row.AgentURL = staleURL
	if err := repo.Update(t.Context(), row); err != nil {
		t.Fatalf("rewrite seed: %v", err)
	}
	remote := a2aComponent("steady_remote", func(b map[string]any) { b["agentUrl"] = staleURL })

	dry, applied := applyBoth(t, svc, remote)

	dryOp, realOp := opFor(t, dry, kindA2AAgent, "steady_remote"), opFor(t, applied, kindA2AAgent, "steady_remote")
	if realOp.Action != actionUnchanged || realOp.Status != statusSucceeded {
		t.Fatalf("real apply = %+v, want an unchanged row that succeeds, or the case proves nothing", realOp)
	}
	if dryOp.Action != actionUnchanged || dryOp.Status != statusPlanned {
		t.Errorf("dry run = %+v, want the unchanged row planned: a real apply does not validate it", dryOp)
	}
}

func TestDryRun_DoesNotHoldAnUpdateToCreateOnlyChecks(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	const seeded = `{"name":"steady_remote","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"original","password":"pw"}}`
	const edited = `{"name":"steady_remote","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"edited","password":"<<__SAM_REQUIRED__>>"}}`
	remote := func(content string) BuildComponentInput {
		return BuildComponentInput{Kind: kindA2AAgent, Name: "steady_remote", Content: content}
	}
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{remote(seeded)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dry, applied := applyBoth(t, svc, remote(edited))

	if got := opFor(t, applied, kindA2AAgent, "steady_remote"); got.Action != actionUpdate || got.Status != statusSucceeded {
		t.Fatalf("real apply = %+v, want a succeeded update", got)
	}
	if got := opFor(t, dry, kindA2AAgent, "steady_remote"); got.Action != actionUpdate || got.Status != statusPlanned {
		t.Errorf("dry run = %+v, want the update planned", got)
	}
}

func TestDryRun_PlansValidEntrypointAndRemoteAgentUpdates(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		agentComponent("test_agent"), gatewayComponent("steady_gw", "test_agent"),
		gatewayWithBroker("steady_broker_gw", validBrokerBlock),
		connectorWith("steady-db", func(b map[string]any) { b["values"] = map[string]any{"hostname": "db", "password": "stored-secret"} }),
		a2aComponent("steady_remote", func(b map[string]any) {
			b["agentCardLocation"] = "none"
			b["skills"] = []any{map[string]any{"id": "s", "name": "skill"}}
		}),
	}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	editedGateway := gatewayComponent("steady_gw", "test_agent")
	editedGateway.Content = strings.Replace(editedGateway.Content, "events/incoming", "events/>/edited", 1)
	editedRemote := a2aComponent("steady_remote", func(b map[string]any) {
		b["agentCardLocation"] = "none"
		b["agentUrl"] = "https://example.com/a2a-edited"
	})

	editedBrokerGateway := gatewayWithBroker("steady_broker_gw", strings.Replace(validBrokerBlock, "real-password-1", "<<__SAM_REQUIRED__>>", 1))
	editedBrokerGateway.Content = strings.Replace(editedBrokerGateway.Content, "Routes inbound events to an agent, used in tests.", "An edited description of the routing.", 1)
	editedConnector := connectorWith("steady-db", func(b map[string]any) {
		b["values"] = map[string]any{"hostname": "edited-db", "password": "<<__SAM_REQUIRED__>>"}
	})

	dry, applied := applyBoth(t, svc, editedGateway, editedRemote, editedBrokerGateway, editedConnector)

	for _, want := range []struct{ kind, name string }{
		{kindGateway, "steady_gw"}, {kindGateway, "steady_broker_gw"}, {kindA2AAgent, "steady_remote"}, {kindConnector, "steady-db"},
	} {
		if got := opFor(t, applied, want.kind, want.name); got.Action != actionUpdate || got.Status != statusSucceeded {
			t.Fatalf("real apply %s = %+v, want a succeeded update", want.kind, got)
		}
		if got := opFor(t, dry, want.kind, want.name); got.Action != actionUpdate || got.Status != statusPlanned {
			t.Errorf("dry run %s = %+v, want the update planned", want.kind, got)
		}
	}
}

func TestDryRun_APlaceholderCredentialIsCompletedByTheStoredRow(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcp://system-broker.example.com:55555")
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		agentComponent("test_agent"), gatewayWithBroker("steady_broker_gw", validBrokerBlock),
	}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	edited := gatewayWithBroker("steady_broker_gw", strings.Replace(validBrokerBlock, "real-password-1", "<<__SAM_REQUIRED__>>", 1))
	edited.Content = strings.Replace(edited.Content, "Routes inbound events to an agent, used in tests.", "An edited description of the routing.", 1)

	dry, applied := applyBoth(t, svc, edited)

	if got := opFor(t, applied, kindGateway, "steady_broker_gw"); got.Action != actionUpdate || got.Status != statusSucceeded {
		t.Fatalf("real apply = %+v, want a succeeded update, or the case proves nothing", got)
	}
	if got := opFor(t, dry, kindGateway, "steady_broker_gw"); got.Action != actionUpdate || got.Status != statusPlanned {
		t.Errorf("dry run = %+v, want the update planned", got)
	}
}

func TestDryRun_AFailedStoredReadIsNotARejection(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	missing := BuildOperationResult{Kind: kindConnector, Name: "gone", Action: actionUpdate, ResourceID: "no-such-id"}
	desired := desiredComponent{create: entplatform.CreateConnectorRequest{
		Name: "gone", Description: "A connector used in tests.", Type: "sql", Subtype: "postgres",
		Values: map[string]any{"hostname": "db"},
	}}

	if _, _, err := svc.rejectInvalidAgainstStored(t.Context(), desired, missing); err != nil {
		t.Errorf("an unreadable stored row was reported as a rejection: %v", err)
	}
}

func TestDryRun_DoesNotApplyTheConnectorTypeGateToAnUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.features = registryEnabling(t, "search_connector")
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{searchConnector("finder")}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.features = nil
	edited := searchConnector("finder")
	edited.Content = strings.Replace(edited.Content, "A feature-gated connector type.", "An edited feature-gated connector.", 1)

	dry, applied := applyBoth(t, svc, edited)

	if got := opFor(t, applied, kindConnector, "finder"); got.Action != actionUpdate || got.Status != statusSucceeded {
		t.Fatalf("real apply = %+v, want a succeeded update", got)
	}
	if got := opFor(t, dry, kindConnector, "finder"); got.Action != actionUpdate || got.Status != statusPlanned {
		t.Errorf("dry run = %+v, want the update planned", got)
	}
}

func TestDryRun_LogsARejectionAsAPreviewAndARealOneAsNot(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var logged strings.Builder
	svc.logger = slog.New(slog.NewTextHandler(&logged, nil))

	applyBoth(t, svc, connectorWith("ab", nil))

	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "dryRun=true") || !strings.Contains(lines[1], "dryRun=false") {
		t.Errorf("log = %q, want the preview's rejection logged as dryRun=true, then the write's as dryRun=false", logged.String())
	}
}

func TestDryRun_HonoursTheConnectorTypeGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		reg        *features.Registry
		wantStatus string
	}{
		{"gate off", nil, statusFailed},
		{"gate on", registryEnabling(t, "search_connector"), statusPlanned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			svc.features = tc.reg

			dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{searchConnector("finder")}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := opFor(t, dry, kindConnector, "finder").Status; got != tc.wantStatus {
				t.Errorf("status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}
