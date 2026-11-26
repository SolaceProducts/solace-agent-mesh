// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const shipmentWorkflowYAML = `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{ship.output.result}}"}
      nodes:
        - id: ship
          type: tool
          connector: carrier-api
          tool: get_shipment
          input: {tracking_number: "{{workflow.input.tracking}}"}
`

type inputsQuerier struct {
	report string
	err    error
	bodies *[]json.RawMessage
}

func (q inputsQuerier) Custom(_ context.Context, appName, method, path string, body json.RawMessage) (json.RawMessage, error) {
	if appName != "platform" || method != "post" || path != WorkflowConnectorToolInputsPath {
		return nil, fmt.Errorf("unknown resource: %s %s %s", appName, method, path)
	}
	if q.bodies != nil {
		*q.bodies = append(*q.bodies, body)
	}
	if q.err != nil {
		return nil, q.err
	}
	return json.RawMessage(q.report), nil
}

func inputsRun(t *testing.T, workflowYAML string, querier tool.ControlQuerier) (valid bool, errs, warnings []string) {
	t.Helper()
	manifest := `
name: shipment-project
components:
  - filename: shipment.yaml
    type: workflow
    name: ShipmentFlow
    origin: session
`
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ControlQuerier = querier
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest, "application/vnd.sam-build-manifest+yaml")
	saveTestArtifact(ctx, svc, toolCtx, "shipment.yaml", workflowYAML, "application/x-yaml")

	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	valid, _ = result.Data["valid"].(bool)
	errs = validationErrorsWithPath(result.Data)
	found, _ := result.Data["warnings"].([]schema.ValidationError)
	for _, w := range found {
		warnings = append(warnings, w.Path+": "+w.Message)
	}
	return valid, errs, warnings
}

const fatalInputReport = `{"problems":[{"nodeId":"ship","connector":"carrier-api","fatal":true,
"message":"tool node \"ship\" calls operation \"get_shipment\" of connector \"carrier-api\" without its required input parameter(s) tracking"}],"unchecked":[]}`

func TestFullValidation_FailsAConnectorNodeThePlatformSaysCannotRun(t *testing.T) {
	var bodies []json.RawMessage
	valid, errs, _ := inputsRun(t, shipmentWorkflowYAML, inputsQuerier{report: fatalInputReport, bodies: &bodies})

	if valid {
		t.Fatal("valid = true for a node that fails on every run")
	}
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"workflow 'ShipmentFlow'.node 'ship'.input", "required input parameter(s) tracking"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q are missing %q", joined, want)
		}
	}
	if len(bodies) != 1 {
		t.Fatalf("the platform was asked %d times, want once per workflow", len(bodies))
	}
	var sent struct {
		AppConfig struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"appConfig"`
	}
	if err := json.Unmarshal(bodies[0], &sent); err != nil || len(sent.AppConfig.Nodes) != 1 || sent.AppConfig.Nodes[0]["connector"] != "carrier-api" {
		t.Errorf("request body %s must carry the workflow's app_config with its connector-bound node (%v)", bodies[0], err)
	}
}

func TestFullValidation_WarnsWhenAConnectorsInputKeysCannotBeChecked(t *testing.T) {
	report := `{"problems":[],"unchecked":[{"connector":"carrier-api","reason":"it has no uploaded spec, so its operations' parameters are not known"}]}`
	valid, errs, warnings := inputsRun(t, shipmentWorkflowYAML, inputsQuerier{report: report})

	if !valid {
		t.Fatalf("valid = false, errors = %v: an unchecked connector must not refuse the build", errs)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"workflow 'ShipmentFlow'.connector 'carrier-api'", "'ship'", "not checked", "no uploaded spec"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q are missing %q", joined, want)
		}
	}
}

func TestFullValidation_StaysQuietAboutAnUncheckedConnectorWhoseNodesTakeNoInput(t *testing.T) {
	report := `{"problems":[],"unchecked":[{"connector":"carrier-api","reason":"it has no uploaded spec"}]}`
	noInput := strings.Replace(shipmentWorkflowYAML, `          input: {tracking_number: "{{workflow.input.tracking}}"}`+"\n", "", 1)
	valid, errs, warnings := inputsRun(t, noInput, inputsQuerier{report: report})
	if !valid || len(warnings) != 0 {
		t.Fatalf("valid = %v, errors = %v, warnings = %v; want nothing to say about a node with no input", valid, errs, warnings)
	}
}

func TestFullValidation_NeverFailsBecauseTheCheckCouldNotRun(t *testing.T) {
	cases := map[string]tool.ControlQuerier{
		"an older platform that does not know the action": inputsQuerier{err: errors.New("unknown resource: post workflows/validate/connectorToolInputs")},
		"a platform that errors":                          inputsQuerier{err: errors.New("boom")},
		"a user without connector read":                   inputsQuerier{err: errors.New("insufficient permissions: requires connector:_:read")},
		"a reply that is not a report":                    inputsQuerier{report: `[1,2]`},
	}
	for name, querier := range cases {
		t.Run(name, func(t *testing.T) {
			valid, errs, warnings := inputsRun(t, shipmentWorkflowYAML, querier)
			if !valid {
				t.Fatalf("valid = false, errors = %v; a check that could not run must not refuse the build", errs)
			}
			joined := strings.Join(warnings, "\n")
			for _, want := range []string{"workflow 'ShipmentFlow'", "'ship'", "could not run", "Saving may still refuse"} {
				if !strings.Contains(joined, want) {
					t.Errorf("warnings %q are missing %q", joined, want)
				}
			}
		})
	}
}

func TestFullValidation_SaysNothingWhenThereIsNothingToCheck(t *testing.T) {
	cases := map[string]tool.ControlQuerier{
		"a clean report":          inputsQuerier{report: `{"problems":[],"unchecked":[]}`},
		"no control plane at all": nil,
	}
	for name, querier := range cases {
		t.Run(name, func(t *testing.T) {
			valid, errs, warnings := inputsRun(t, shipmentWorkflowYAML, querier)
			if !valid || len(warnings) != 0 {
				t.Fatalf("valid = %v, errors = %v, warnings = %v; want the build untouched", valid, errs, warnings)
			}
		})
	}
}

func TestFullValidation_ANonFatalProblemIsAWarning(t *testing.T) {
	report := `{"problems":[{"nodeId":"ship","connector":"carrier-api","fatal":false,"message":"tool node \"ship\" calls operation \"get_shipment\" of connector \"carrier-api\" with input key(s) tracking_number that it does not declare"}],"unchecked":[]}`
	valid, errs, warnings := inputsRun(t, shipmentWorkflowYAML, inputsQuerier{report: report})
	if !valid {
		t.Fatalf("valid = false, errors = %v; a key the platform lets through must not refuse the build", errs)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "tracking_number") {
		t.Fatalf("warnings = %v, want the dropped key reported", warnings)
	}
}

func TestFullValidation_SaysNothingAboutAnUnresolvedSessionConnectorThatIsNotAPI(t *testing.T) {
	report := `{"problems":[],"unchecked":[{"connector":"carrier-api","reason":"it could not be resolved to one connector"}]}`
	files := []buildFile{
		{filename: "shipment.yaml", typ: "workflow", name: "ShipmentFlow", origin: "session", content: shipmentWorkflowYAML},
		{filename: "carrier.json", typ: "connector", name: "carrier-api", origin: "session", content: `{"name":"carrier-api","type":"sql","subtype":"postgres","values":{}}`},
	}
	_, _, warnings := bindingRunWarnings(t, files, inputsQuerier{report: report})
	for _, w := range warnings {
		if strings.Contains(w, "not checked against connector") {
			t.Errorf("warning %q tells the author a non-api connector's keys are unchecked", w)
		}
	}

	files[1].content = `{"name":"carrier-api","type":"api","subtype":"openapi","values":{"base_url":"https://api.example.com","specification_url":"https://specs.example.com/s.json"}}`
	_, _, warnings = bindingRunWarnings(t, files, inputsQuerier{report: report})
	if !strings.Contains(strings.Join(warnings, "\n"), "not checked against connector 'carrier-api'") {
		t.Errorf("warnings = %v, want the api connector's unchecked keys reported", warnings)
	}
}

func bindingRunWarnings(t *testing.T, files []buildFile, querier tool.ControlQuerier) (valid bool, errs, warnings []string) {
	t.Helper()
	var manifest strings.Builder
	manifest.WriteString("\nname: shipment-project\ncomponents:\n")
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ControlQuerier = querier
	ctx := t.Context()
	for _, f := range files {
		fmt.Fprintf(&manifest, "  - filename: %q\n    type: %s\n    name: %s\n    origin: %s\n", f.filename, f.typ, f.name, f.origin)
	}
	saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", manifest.String(), "application/vnd.sam-build-manifest+yaml")
	for _, f := range files {
		if f.typ == "connector" {
			saveTestArtifact(ctx, svc, toolCtx, f.filename, f.content, SAMConnectorConfigMIMEType)
		} else {
			saveTestArtifact(ctx, svc, toolCtx, f.filename, f.content, "application/x-yaml")
		}
	}
	result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest.String(), "full_validation": true}, toolCtx)
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

func TestFullValidation_AsksNothingWhenNoNodeBindsAConnector(t *testing.T) {
	var bodies []json.RawMessage
	plain := `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{stamp.output}}"}
      tools:
        - tool_type: builtin
          tool_name: get_current_time
      nodes:
        - id: stamp
          type: tool
          tool_name: get_current_time
`
	valid, errs, _ := inputsRun(t, plain, inputsQuerier{report: fatalInputReport, bodies: &bodies})
	if !valid || len(bodies) != 0 {
		t.Fatalf("valid = %v (%v), platform asked %d times; want no question for a workflow with no connector node", valid, errs, len(bodies))
	}
}

const pinnedMCPWorkflowYAML = `
apps:
  - name: ShipmentFlow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{ship.output}}"}
      tools:
        - tool_type: mcp
          tool_name: get_shipment
          connection_params: {type: streamable-http, url: "http://localhost:9999/mcp"}
      nodes:
        - id: ship
          type: tool
          tool_name: %s
`

func TestFullValidation_FailsAToolNodeNoPinnedSourceRegisters(t *testing.T) {
	valid, errs, _ := inputsRun(t, fmt.Sprintf(pinnedMCPWorkflowYAML, "track_package"), nil)
	if valid {
		t.Fatal("valid = true for a node calling a tool no source registers")
	}
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"would be refused at save and deploy", `"track_package"`, "references unknown tool", "get_shipment"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q are missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "would fail to load") {
		t.Errorf("errors %q call a workflow that loads a load failure", joined)
	}

	valid, errs, _ = inputsRun(t, fmt.Sprintf(pinnedMCPWorkflowYAML, "get_shipment"), nil)
	if !valid {
		t.Fatalf("valid = false, errors = %v, want the corrected workflow to pass", errs)
	}
}
