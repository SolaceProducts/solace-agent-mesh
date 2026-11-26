// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const ruleAgentYAML = `
apps:
  - name: PetAgent
    app_config:
      agent_name: PetAgent
      instruction: hi
      input_schema:
        type: object
        properties:
          city: {type: string}
        required: [city]
`

const ruleWorkflowYAML = `
apps:
  - name: PetFlow
    app_config:
      agent_type: workflow
      output_mapping: {pets: "{{fetch_pets.output.result}}", answer: "{{ask.output.answer}}"}
      nodes:
        - id: fetch_pets
          type: tool
          connector: PetAPI
          tool: findPetsByStatus
          input: {status: "available"}
        - id: ask
          type: agent
          agent_name: PetAgent
          input: {}
`

type ruleCase struct {
	agentBlocked, workflowBlocked, connectorBlocked, drafted, bareOnPlatform bool
}

func (c ruleCase) String() string {
	return fmt.Sprintf("agent_blocked=%t/workflow_blocked=%t/connector_blocked=%t/drafted=%t/bare_on_platform=%t", c.agentBlocked, c.workflowBlocked, c.connectorBlocked, c.drafted, c.bareOnPlatform)
}

func (c ruleCase) run(t *testing.T) (valid bool, errs []string) {
	t.Helper()
	status := func(blocked bool) string {
		if blocked {
			return "blocked\n    blocked_reason: the thing only the user has"
		}
		return "validated"
	}
	manifest := fmt.Sprintf(`name: pets
components:
  - filename: pet_api.json
    type: connector
    name: PetAPI
    origin: session
    status: %s
  - filename: pet_agent.yaml
    type: agent
    name: PetAgent
    origin: session
    status: %s
    connectors: [PetAPI]
  - filename: pet_flow.yaml
    type: workflow
    name: PetFlow
    origin: session
    status: %s
    depends_on: [PetAgent]
    nodes:
      - id: fetch_pets
        type: tool
        connector: PetAPI
        tool: findPetsByStatus
      - id: ask
        type: agent
        agent_name: PetAgent
`, status(c.connectorBlocked), status(c.agentBlocked), status(c.workflowBlocked))

	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ControlQuerier = namedLookups{}
	if c.bareOnPlatform {
		toolCtx.ControlQuerier = namedLookups{byName: map[string]ConnectorLookup{"PetAPI": {Exists: true, Type: "api"}}}
	}
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
	if c.drafted {
		if !c.bareOnPlatform {
			saveTestArtifact(ctx, svc, toolCtx, "pet_api.json", petConnector(t, "PetAPI", "api", map[string]any{"specification_url": "TODO"}), SAMConnectorConfigMIMEType)
		}
		saveTestArtifact(ctx, svc, toolCtx, "pet_agent.yaml", ruleAgentYAML, "application/x-yaml")
		saveTestArtifact(ctx, svc, toolCtx, "pet_flow.yaml", ruleWorkflowYAML, "application/x-yaml")
	}

	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	valid, _ = result.Data["valid"].(bool)
	return valid, validationErrorsWithPath(result.Data)
}

func containsAny(errs []string, needles ...string) bool {
	for _, e := range errs {
		for _, n := range needles {
			if strings.Contains(e, n) {
				return true
			}
		}
	}
	return false
}

func TestFullValidation_ABlockedComponentAndItsBindingsAreNeverChecked(t *testing.T) {
	for mask := range 32 {
		c := ruleCase{agentBlocked: mask&1 != 0, workflowBlocked: mask&2 != 0, connectorBlocked: mask&4 != 0, drafted: mask&8 != 0, bareOnPlatform: mask&16 != 0}
		t.Run(c.String(), func(t *testing.T) {
			valid, errs := c.run(t)

			if c.agentBlocked && containsAny(errs, "pet_agent.yaml", "missing required input", "PetAgent input_schema") {
				t.Errorf("a blocked agent was checked: %v", errs)
			}
			if c.workflowBlocked && containsAny(errs, "pet_flow.yaml", "could not load config", "binds API connector", "missing required input") {
				t.Errorf("a blocked workflow was checked: %v", errs)
			}
			if c.connectorBlocked && containsAny(errs, "pet_api.json", "binds API connector", "allow_list", "specification_url") {
				t.Errorf("a blocked connector was checked: %v", errs)
			}
			if c.agentBlocked && c.workflowBlocked && c.connectorBlocked && !valid {
				t.Errorf("with every component blocked and nothing written the manifest is valid, got %v", errs)
			}
		})
	}
}

func TestFullValidation_TheRuleFixtureIsRefusedWhenNothingIsBlocked(t *testing.T) {
	valid, errs := ruleCase{drafted: true}.run(t)
	if valid {
		t.Fatalf("the fixture must be invalid when nothing is blocked")
	}
	for _, want := range []string{bindingErrorTag, "specification_url", "missing required input"} {
		if !containsAny(errs, want) {
			t.Errorf("no error mentions %q: %v", want, errs)
		}
	}
	_, onPlatform := ruleCase{drafted: true, bareOnPlatform: true}.run(t)
	if !containsAny(onPlatform, bindingErrorTag) {
		t.Errorf("a binding to a connector that sits bare on the platform was not reported: %v", onPlatform)
	}
	_, missing := ruleCase{}.run(t)
	if !containsAny(missing, "was not found") {
		t.Errorf("a component with no config was not reported: %v", missing)
	}
}

func TestFullValidation_ABindingByTheDraftedNameOfABlockedConnectorIsNotChecked(t *testing.T) {
	run := func(connectorStatus string) (bool, []string) {
		manifest := fmt.Sprintf(`name: pets
components:
  - filename: pet_api.json
    type: connector
    name: PetStore
    origin: session
    status: %s
  - filename: pet_flow.yaml
    type: workflow
    name: PetFlow
    origin: session
    status: validated
    nodes:
      - id: fetch_pets
        type: tool
        connector: pet-api
        tool: findPetsByStatus
`, connectorStatus)
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		toolCtx.ControlQuerier = namedLookups{}
		ctx := context.Background()
		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "pet_api.json", petConnector(t, "pet-api", "api", nil), SAMConnectorConfigMIMEType)
		saveTestArtifact(ctx, svc, toolCtx, "pet_flow.yaml", strings.ReplaceAll(petWorkflowYAML, "connector: PetAPI", "connector: pet-api"), "application/x-yaml")
		result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		valid, _ := result.Data["valid"].(bool)
		return valid, validationErrorsWithPath(result.Data)
	}

	if valid, errs := run("blocked\n    blocked_reason: the spec URL"); !valid || containsAny(errs, bindingErrorTag) {
		t.Errorf("a binding by the drafted name of a blocked connector was checked: valid=%t %v", valid, errs)
	}
	if _, errs := run("validated"); !containsAny(errs, bindingErrorTag) {
		t.Errorf("the same binding must be refused once the connector is not blocked: %v", errs)
	}
}
