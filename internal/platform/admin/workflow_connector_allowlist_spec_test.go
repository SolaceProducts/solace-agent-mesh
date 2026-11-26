// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

const (
	allowListNamesGhost       = `,"allow_list":"ghostOp"`
	allowListNamesGhostAndOne = `,"allow_list":"getShipment,ghostOp"`
	allowListNamesRealOps     = `,"allow_list":"getShipment,ping"`
	allowListSnakeSpelling    = `,"allow_list":"get_shipment"`
	specURLOnlyNamesGhost     = `,"specification_url":"https://specs.example.com/s.yaml","allow_list":"ghostOp"`
)

func (f *inputCheckFixture) allowListConnector(t *testing.T, allowList string) {
	t.Helper()
	conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(allowList))
	storeStoredSpec(t, f.repo, f.specs, conn, shipmentSpec)
}

func TestWorkflowSave_RefusesANodeBoundToAnAllowListEntryTheSpecLacks(t *testing.T) {
	t.Parallel()
	offered := []string{"create_shipment", "get_shipment", "ping"}
	cases := []struct {
		name      string
		allowList string
		node      map[string]any
		wants     []string
	}{
		{
			name:      "the only entry, selected by tool name",
			allowList: allowListNamesGhost,
			node:      shipNode("ghost_op", map[string]any{"tracking": "x"}),
			wants:     []string{`"ghost_op"`, `allow_list entry "ghostOp"`},
		},
		{
			name:      "the node has no input",
			allowList: allowListNamesGhost,
			node:      shipNode("ghost_op", nil),
			wants:     []string{`allow_list entry "ghostOp"`},
		},
		{
			name:      "the selector spelled as the entry",
			allowList: allowListNamesGhost,
			node:      shipNode("ghostOp", map[string]any{"tracking": "x"}),
			wants:     []string{`"ghost_op"`, `allow_list entry "ghostOp"`},
		},
		{
			name:      "no selector, the single entry is the one bound",
			allowList: allowListNamesGhost,
			node:      shipNode("", nil),
			wants:     []string{`allow_list entry "ghostOp"`},
		},
		{
			name:      "one entry of several is missing and is the one selected",
			allowList: allowListNamesGhostAndOne,
			node:      shipNode("ghost_op", nil),
			wants:     []string{`allow_list entry "ghostOp"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.allowListConnector(t, tc.allowList)

			err := f.create(t, tc.node)
			if err == nil {
				t.Fatal("save accepted a node bound to an operation the spec does not have")
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err is %T, want *ValidationError so the caller answers 422 rather than 500", err)
			}
			wants := slices.Concat(tc.wants, []string{`connector "carrier-api"`, `node "ship"`, "references unknown tool", strings.Join(offered, ", ")})
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func TestWorkflowSave_AcceptsEveryAllowListNodeThatRuns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		build func(t *testing.T, f *inputCheckFixture)
		node  map[string]any
	}{
		{"every entry is an operation of the spec", func(t *testing.T, f *inputCheckFixture) {
			f.allowListConnector(t, allowListNamesRealOps)
		}, shipNode("get_shipment", map[string]any{"tracking": "x"})},
		{"a stale entry the node does not bind", func(t *testing.T, f *inputCheckFixture) {
			f.allowListConnector(t, allowListNamesGhostAndOne)
		}, shipNode("get_shipment", map[string]any{"tracking": "x"})},
		{"an entry spelled in snake_case for a camelCase operationId", func(t *testing.T, f *inputCheckFixture) {
			f.allowListConnector(t, allowListSnakeSpelling)
		}, shipNode("get_shipment", map[string]any{"tracking": "x"})},
		{"an entry spelled as the operationId, selected by operationId", func(t *testing.T, f *inputCheckFixture) {
			f.allowListConnector(t, allowListNamesRealOps)
		}, shipNode("getShipment", map[string]any{"tracking": "x"})},
		{"no allow_list at all", func(t *testing.T, f *inputCheckFixture) {
			f.allowListConnector(t, "")
		}, shipNode("ping", nil)},
		{"a spec URL the platform does not fetch, so the entry cannot be checked", func(t *testing.T, f *inputCheckFixture) {
			createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(specURLOnlyNamesGhost))
		}, shipNode("ghost_op", map[string]any{"tracking": "x"})},
		{"a stored spec that does not parse", func(t *testing.T, f *inputCheckFixture) {
			conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(allowListNamesGhost))
			storeStoredSpec(t, f.repo, f.specs, conn, "this is: [not an openapi document")
		}, shipNode("ghost_op", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			tc.build(t, f)

			if err := f.create(t, tc.node); err != nil {
				t.Fatalf("save refused a node that runs: %v", err)
			}
		})
	}
}

func TestControlAction_ReportsAnAllowListEntryTheSpecLacksAsFatal(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.allowListConnector(t, allowListNamesGhost)

	report := f.report(t, connectorNode("ship", "carrier-api", "ghost_op", nil))
	if len(report.Problems) != 1 {
		t.Fatalf("problems = %+v, want exactly one", report.Problems)
	}
	problem := report.Problems[0]
	if !problem.Fatal || problem.NodeID != "ship" || problem.Connector != "carrier-api" {
		t.Errorf("problem = %+v, want a fatal one on node ship of connector carrier-api", problem)
	}
	for _, want := range []string{`allow_list entry "ghostOp"`, "create_shipment, get_shipment, ping"} {
		if !strings.Contains(problem.Message, want) {
			t.Errorf("message %q is missing %q", problem.Message, want)
		}
	}
	if len(report.Unchecked) != 0 {
		t.Errorf("unchecked = %+v, want none: the spec was read", report.Unchecked)
	}
}

func TestControlProtocol_ReportsAnAllowListEntryTheSpecLacksAsFatalOnTheWire(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.allowListConnector(t, allowListNamesGhost)
	env := setupControlTestWithDeps(t, func(deps *RouteDeps) { deps.WorkflowSvc = f.svc })

	body, err := json.Marshal(map[string]any{"appConfig": map[string]any{
		"nodes": []any{connectorNode("ship", "carrier-api", "ghost_op", nil)},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw, err := env.Client.Custom(t.Context(), "platform", "post", tools.WorkflowConnectorToolInputsPath, body)
	if err != nil {
		t.Fatalf("Custom: %v", err)
	}
	var report tools.ConnectorToolInputReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if len(report.Problems) != 1 || !report.Problems[0].Fatal || report.Problems[0].NodeID != "ship" ||
		!strings.Contains(report.Problems[0].Message, `allow_list entry "ghostOp"`) {
		t.Fatalf("problems = %+v, want one fatal problem on node ship naming the allow_list entry", report.Problems)
	}
}

func TestControlAction_ALongSpecIsListedUpToTheCap(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(allowListNamesGhost))
	const operations = maxOfferedOperations + 5
	var paths strings.Builder
	for i := range operations {
		fmt.Fprintf(&paths, "  /r%02d:\n    get:\n      operationId: op%02d\n      responses:\n        \"200\":\n          description: ok\n", i, i)
	}
	storeStoredSpec(t, f.repo, f.specs, conn, "openapi: 3.0.0\ninfo:\n  title: Big\n  version: 1.0.0\npaths:\n"+paths.String())

	report := f.report(t, connectorNode("ship", "carrier-api", "ghost_op", nil))
	if len(report.Problems) != 1 {
		t.Fatalf("problems = %+v, want exactly one", report.Problems)
	}
	message := report.Problems[0].Message
	if !strings.Contains(message, "op00") || !strings.Contains(message, fmt.Sprintf("op%02d", maxOfferedOperations-1)) {
		t.Errorf("message %q does not list the first %d operations", message, maxOfferedOperations)
	}
	if strings.Contains(message, fmt.Sprintf("op%02d", maxOfferedOperations)) {
		t.Errorf("message %q lists past the cap", message)
	}
	if !strings.Contains(message, "and 5 more") {
		t.Errorf("message %q does not say how many were left out", message)
	}
}

func TestWorkflowDeploy_RefusesAnAllowListEntryTheSpecLacks(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	specs := newMemResourceStore()
	conn := createHydrationConnector(t, h.connectorRepo, "carrier-api", "api", "openapi", apiConnectorValues(allowListNamesGhostAndOne))
	storeStoredSpec(t, h.connectorRepo, specs, conn, shipmentSpec)
	binding := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "carrier-api"}}
	workflowFor := func(tool string) map[string]any {
		return map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode(tool, map[string]any{"tracking": "{{workflow.input.t}}"})},
		}
	}

	badID := storeWithoutSaveGate(t, h, "binds-ghost", workflowFor("ghost_op"), binding)
	resp := deployThrough(h, specs, badID)
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q for a node bound to an operation the spec lacks", resp.Status, DeployStatusFailed)
	}
	for _, want := range []string{`allow_list entry "ghostOp"`, "create_shipment, get_shipment, ping"} {
		if !strings.Contains(resp.ErrorMessage, want) {
			t.Errorf("deploy error %q is missing %q", resp.ErrorMessage, want)
		}
	}

	goodID := storeWithoutSaveGate(t, h, "binds-real", workflowFor("get_shipment"), binding)
	if resp = deployThrough(h, specs, goodID); resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q (%s), want a node bound to a real operation to deploy beside the stale entry", resp.Status, resp.ErrorMessage)
	}
}
