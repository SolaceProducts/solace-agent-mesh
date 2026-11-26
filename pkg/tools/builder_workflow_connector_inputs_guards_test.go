// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const twoConnectorWorkflowYAML = `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{ship.output}}"}
      nodes:
        - id: ship
          type: tool
          connector: carrier-api
          tool: get_shipment
          input: {tracking_number: "{{workflow.input.tracking}}"}
        - id: audit
          type: tool
          connector: audit-api
          tool: log_event
          depends_on: [ship]
          input: {event: "{{ship.output}}"}
`

func runManifest(t *testing.T, manifest string, artifacts map[string]string, querier tool.ControlQuerier) (valid bool, errs, warnings []string) {
	t.Helper()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ControlQuerier = querier
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
	for name, content := range artifacts {
		mime := "application/x-yaml"
		if strings.HasSuffix(name, ".json") {
			mime = SAMConnectorConfigMIMEType
		}
		saveTestArtifact(ctx, svc, toolCtx, name, content, mime)
	}
	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	valid, _ = result.Data["valid"].(bool)
	found, _ := result.Data["warnings"].([]schema.ValidationError)
	for _, w := range found {
		warnings = append(warnings, w.Path+": "+w.Message)
	}
	return valid, validationErrorsWithPath(result.Data), warnings
}

const shipmentManifest = `
name: shipment-project
components:
  - filename: shipment.yaml
    type: workflow
    name: ShipmentFlow
    origin: session
`

func TestFullValidation_TellsAuthorsTheKeySpellingDispatchNeeds(t *testing.T) {
	report := `{"problems":[],"unchecked":[{"connector":"carrier-api","reason":"it has no uploaded spec"}]}`
	_, _, warnings := inputsRun(t, shipmentWorkflowYAML, inputsQuerier{report: report})
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"snake_case", "`petId` is `pet_id`", "`from`", "`param_from`"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning %q does not say %q", joined, want)
		}
	}
	if strings.Contains(joined, "parameter names in the connector's OpenAPI spec") {
		t.Errorf("warning %q still sends the author to the spec's own spelling", joined)
	}
}

func TestFullValidation_NamesOnlyTheNodesBoundToTheConnectorThatWentUnchecked(t *testing.T) {
	report := `{"problems":[],"unchecked":[{"connector":"carrier-api","reason":"it has no uploaded spec"}]}`
	files := map[string]string{"shipment.yaml": twoConnectorWorkflowYAML}
	_, _, warnings := runManifest(t, shipmentManifest, files, inputsQuerier{report: report})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly the one for carrier-api", warnings)
	}
	if !strings.Contains(warnings[0], "'ship'") || strings.Contains(warnings[0], "'audit'") {
		t.Errorf("warning %q must name the node bound to carrier-api and not the one bound to audit-api", warnings[0])
	}

	_, _, warnings = runManifest(t, shipmentManifest, files, inputsQuerier{err: errors.New("boom")})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "'ship'") || !strings.Contains(warnings[0], "'audit'") {
		t.Errorf("warnings = %v, want the could-not-run warning to name every node that spells out an input", warnings)
	}
}

const noInputShipmentWorkflowYAML = `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{ship.output.result}}"}
      nodes:
        - id: ship
          type: tool
          connector: carrier-api
          tool: ghost_op
`

func TestFullValidation_WarnsWhenItCannotAskAboutAWorkflowWhoseNodesTakeNoInput(t *testing.T) {
	var bodies []json.RawMessage
	valid, errs, warnings := inputsRun(t, noInputShipmentWorkflowYAML, inputsQuerier{err: errors.New("boom"), bodies: &bodies})
	if !valid || len(bodies) != 1 {
		t.Fatalf("valid = %v (%v), asked %d times; want one question and a valid build", valid, errs, len(bodies))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one saying the check could not run", warnings)
	}
	for _, want := range []string{"workflow 'ShipmentFlow'", "'ship'", "operations", "could not run", "Saving may still refuse a node bound to an operation its connector's spec lacks"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q is missing %q", warnings[0], want)
		}
	}
	if strings.Contains(warnings[0], "input keys") {
		t.Errorf("warning %q talks about input keys of a workflow that has none", warnings[0])
	}
}

const mixedInputShipmentWorkflowYAML = `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{ship.output.result}}"}
      nodes:
        - id: lookup
          type: tool
          connector: carrier-api
          tool: get_shipment
          input: {tracking: "{{workflow.input.tracking}}"}
        - id: ship
          type: tool
          connector: carrier-api
          tool: ghost_op
          depends_on: [lookup]
`

func TestFullValidation_LocatesEachFindingOnItsOwnNode(t *testing.T) {
	report := `{"problems":[
{"nodeId":"lookup","connector":"carrier-api","fatal":true,"message":"a missing parameter"},
{"nodeId":"ship","connector":"carrier-api","fatal":true,"message":"a missing operation"}],"unchecked":[]}`
	_, errs, _ := inputsRun(t, mixedInputShipmentWorkflowYAML, inputsQuerier{report: report})
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want one per problem", errs)
	}
	for _, e := range errs {
		switch {
		case strings.Contains(e, "a missing parameter"):
			if !strings.Contains(e, "workflow 'ShipmentFlow'.node 'lookup'.input") {
				t.Errorf("%q: want the node that has an input located at that input", e)
			}
		case strings.Contains(e, "a missing operation"):
			if !strings.Contains(e, "workflow 'ShipmentFlow'.node 'ship'") || strings.Contains(e, ".node 'ship'.input") {
				t.Errorf("%q: want the node with no input located at the node", e)
			}
		}
	}
}

func TestFullValidation_RefusesANodeWithNoInputThatBindsAnOperationTheSpecLacks(t *testing.T) {
	report := `{"problems":[{"nodeId":"ship","connector":"carrier-api","fatal":true,"message":"tool node \"ship\" calls \"ghost_op\" of connector \"carrier-api\", but the connector's allow_list entry \"ghostOp\" is not an operation of its uploaded spec"}],"unchecked":[]}`
	valid, errs, _ := inputsRun(t, noInputShipmentWorkflowYAML, inputsQuerier{report: report})
	if valid {
		t.Fatal("the build was reported valid for a node the platform refuses")
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "allow_list entry") || !strings.Contains(errs[0], "workflow 'ShipmentFlow'.node 'ship'") || strings.Contains(errs[0], ".input") {
		t.Errorf("errors = %v, want the platform's message on the node's own path", errs)
	}
}

func TestFullValidation_SaysNothingAboutAConnectorTheBuildHasBlocked(t *testing.T) {
	manifest := shipmentManifest + `  - filename: carrier.json
    type: connector
    name: carrier-api
    status: blocked
    blocked_reason: the http(s) URL of the carrier's OpenAPI document (specification_url)
    origin: session
`
	files := map[string]string{
		"shipment.yaml": shipmentWorkflowYAML,
		"carrier.json":  `{"type":"api","values":{"specification_url":"https://carrier.example.invalid/openapi.json","base_url":"https://carrier.example.invalid/v1"}}`,
	}
	report := `{"problems":[{"nodeId":"ship","connector":"carrier-api","fatal":true,"message":"a connector with this name on the platform"}],
"unchecked":[{"connector":"carrier-api","reason":"it has no uploaded spec"}]}`
	valid, errs, warnings := runManifest(t, manifest, files, inputsQuerier{report: report})
	for _, got := range append(errs, warnings...) {
		if strings.Contains(got, "a connector with this name on the platform") || strings.Contains(got, "not checked against connector") {
			t.Errorf("%q: a blocked connector must not be graded or reported unchecked (valid = %v)", got, valid)
		}
	}
}
