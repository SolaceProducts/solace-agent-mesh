// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

func (f *inputCheckFixture) report(t *testing.T, nodes ...map[string]any) *ConnectorToolInputReport {
	t.Helper()
	list := make([]any, len(nodes))
	for i, n := range nodes {
		list[i] = n
	}
	return f.svc.CheckConnectorToolInputs(t.Context(), map[string]any{"nodes": list})
}

func connectorNode(id, connector, tool string, input map[string]any) map[string]any {
	node := map[string]any{"id": id, "type": "tool", "connector": connector, "tool": tool}
	if input != nil {
		node["input"] = input
	}
	return node
}

func TestWorkflowSave_ReportsEveryOffendingNode(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")

	_, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "two-bad-nodes", Description: "two nodes with a wrong key each",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{first.output}}"},
			"nodes": []any{
				connectorNode("first", "carrier-api", "get_shipment", map[string]any{"tracking_number": "x"}),
				connectorNode("second", "carrier-api", "create_shipment", map[string]any{"note": "x"}),
			},
		},
	}, "u")
	if err == nil {
		t.Fatal("save accepted two nodes that cannot run")
	}
	for _, want := range []string{`node "first"`, `node "second"`, "tracking_number", "destination"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Field != fieldAppConfig {
		t.Errorf("error = %#v, want a ValidationError on the appConfig field", err)
	}
}

func TestValidateWorkflowRequest_UnknownToolRefusalCarriesFieldAndDetails(t *testing.T) {
	t.Parallel()
	flat := map[string]any{
		"output_mapping": map[string]any{"result": "{{weather.output}}"},
		"tools":          []any{mcpEntryFor("http://127.0.0.1:1/mcp", map[string]any{"tool_name": "get_forecast"})},
		"nodes":          []any{map[string]any{"id": "weather", "type": "tool", "tool_name": "get_weather"}},
	}
	nested := map[string]any{"workflow": map[string]any{
		"output_mapping": flat["output_mapping"],
		"tools":          flat["tools"],
		"nodes":          flat["nodes"],
	}}
	for name, tc := range map[string]struct {
		appConfig map[string]any
		field     string
	}{
		"flat":   {flat, "appConfig.nodes"},
		"nested": {nested, "appConfig.workflow.nodes"},
	} {
		err := ValidateWorkflowRequest("weather-flow", tc.appConfig)
		var invalid *ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("%s: err = %v, want a *ValidationError", name, err)
		}
		if invalid.Field != tc.field || len(invalid.Details[tc.field]) != 1 || !strings.Contains(invalid.Details[tc.field][0], "get_weather") {
			t.Errorf("%s: Field = %q, Details = %v, want the refusal under %q", name, invalid.Field, invalid.Details, tc.field)
		}
	}
}

func TestWorkflowSave_AnUndeclaredKeyBesideEveryRequiredOneIsLoggedNotRefused(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")

	extra := map[string]any{"tracking": "{{workflow.input.t}}", "tracking_number": "x"}
	if err := f.create(t, shipNode("get_shipment", extra)); err != nil {
		t.Fatalf("save refused an undeclared key that the call survives: %v", err)
	}
	line := findLog(t, f.logs, "passes input keys its OpenAPI operation does not declare")
	assertLogFields(t, line, map[string]any{"level": "WARN", "nodeID": "ship", "connector": "carrier-api"})
	if keys, _ := line["undeclaredKeys"].([]any); len(keys) != 1 || keys[0] != "tracking_number" {
		t.Errorf("the log names undeclaredKeys %v, want [tracking_number]", line["undeclaredKeys"])
	}
	if name, _ := line["workflowName"].(string); name == "" {
		t.Errorf("the log names no workflow on a save: %v", line)
	}
	err := f.create(t, shipNode("get_shipment", map[string]any{"tracking_number": "x"}))
	if err == nil {
		t.Fatal("a missing required parameter fails every run that reaches the node and must be refused")
	}
	if !strings.Contains(err.Error(), "a failure of them can stop a run") {
		t.Errorf("refusal %q does not say the failure can stop a run", err)
	}

	report := f.report(t, connectorNode("ship", "carrier-api", "get_shipment", extra))
	if len(report.Problems) != 1 || report.Problems[0].Fatal {
		t.Errorf("problems = %+v, want one that is not Fatal, so the Builder warns instead of failing", report.Problems)
	}
}

func TestWorkflowSave_APanicInTheCheckLeavesTheNodesUnchecked(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.specs = panickingResourceStore{newMemResourceStore()}
	f.svc.SetResourceStore(f.specs)
	conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
	if err := f.repo.UpdateResourceKey(t.Context(), conn.ID, "connectors/spec.yaml", "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}

	if err := f.create(t, shipNode("get_shipment", map[string]any{"tracking_number": "x"})); err != nil {
		t.Fatalf("save refused a workflow because the check panicked: %v", err)
	}
	f.logs.Reset()
	report := f.report(t,
		connectorNode("ship", "carrier-api", "get_shipment", map[string]any{"tracking_number": "x"}),
		connectorNode("ship-again", "carrier-api", "get_shipment", map[string]any{"tracking_number": "y"}))
	if len(report.Problems) != 0 || len(report.Unchecked) != 1 || report.Unchecked[0].Reason != reasonCheckFailedUnexpected {
		t.Fatalf("report = %+v, want the connector unchecked with the check-failed reason", report)
	}
	line := findLog(t, f.logs, "check panicked")
	assertLogFields(t, line, map[string]any{"level": "ERROR", "panicType": "string"})
	if connectors, _ := line["connectors"].([]any); len(connectors) != 1 || connectors[0] != "carrier-api" {
		t.Errorf("the panic log names connectors %v, want [carrier-api] once", line["connectors"])
	}
	if stack, _ := line["stack"].(string); !strings.HasPrefix(stack, "goroutine ") {
		t.Errorf("the panic log carries no stack: %v", line["stack"])
	}
	if _, quoted := line["panicValue"]; quoted || strings.Contains(f.logs.String(), "simulated resource store bug") {
		t.Errorf("the panic log quotes the panic value, which can carry spec content:\n%s", f.logs.String())
	}
}

func TestWorkflowSave_APanicKeepsWhatWasAlreadyFound(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.specs = keyPanickingResourceStore{ResourceStore: newMemResourceStore(), key: "connectors/panics.yaml"}
	f.svc.SetResourceStore(f.specs)
	f.shipmentConnector(t, "carrier-api")
	bad := createHydrationConnector(t, f.repo, "bad-api", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
	if err := f.repo.UpdateResourceKey(t.Context(), bad.ID, "connectors/panics.yaml", "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}

	report := f.report(t,
		connectorNode("missing", "carrier-api", "get_shipment", map[string]any{"tracking_number": "x"}),
		connectorNode("boom", "bad-api", "get_shipment", map[string]any{"x": "y"}),
		connectorNode("after", "carrier-api", "get_shipment", map[string]any{"tracking_number": "z"}))
	if len(report.Problems) != 1 || report.Problems[0].NodeID != "missing" || !report.Problems[0].Fatal {
		t.Fatalf("problems = %+v, want the verdict reached before the panic kept", report.Problems)
	}
	if len(report.Unchecked) != 2 || report.Unchecked[0].Connector != "bad-api" || report.Unchecked[1].Connector != "carrier-api" ||
		report.Unchecked[0].Reason != reasonCheckFailedUnexpected {
		t.Fatalf("unchecked = %+v, want the panicking connector and the node never reached", report.Unchecked)
	}
}

type keyPanickingResourceStore struct {
	ResourceStore
	key string
}

func (s keyPanickingResourceStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == s.key {
		panic("simulated resource store bug")
	}
	return s.ResourceStore.Get(ctx, key)
}

func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func findLog(t *testing.T, logs *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range logLines(t, logs) {
		if m, _ := line["msg"].(string); strings.Contains(m, msg) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d log lines contain %q, want one:\n%s", len(found), msg, logs.String())
	}
	return found[0]
}

func assertLogFields(t *testing.T, line map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if line[key] != value {
			t.Errorf("log field %q = %v, want %v (line %v)", key, line[key], value, line)
		}
	}
}

func TestWorkflowUpdate_LogsTheUndeclaredKeyUnderTheWorkflowID(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	created, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "shipment-flow-update-log", Description: "starts valid",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}"})},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.svc.Update(t.Context(), created.ID, UpdateWorkflowRequest{
		Name: "shipment-flow-update-log", Description: "adds a key the operation does not declare",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}", "extra": "x"})},
		},
	}, "u"); err != nil {
		t.Fatalf("update refused an undeclared key that the call survives: %v", err)
	}
	line := findLog(t, f.logs, "passes input keys its OpenAPI operation does not declare")
	assertLogFields(t, line, map[string]any{"workflowID": created.ID, "nodeID": "ship"})
}

type panickingResourceStore struct{ ResourceStore }

func (panickingResourceStore) Get(context.Context, string) (io.ReadCloser, error) {
	panic("simulated resource store bug")
}

func TestWorkflowSave_ASpecWithAnEmptyRequestBodyMediaTypeIsGradedNotCrashed(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, conn, `openapi: 3.0.0
info: {title: Empty media, version: 1.0.0}
servers: [{url: "https://api.example.com"}]
paths:
  /upload:
    post:
      operationId: upload
      requestBody:
        content:
          application/octet-stream:
      responses:
        "200": {description: ok}
  /ping:
    get:
      operationId: ping
      responses:
        "200": {description: ok}
`)
	if err := f.create(t, shipNode("ping", map[string]any{})); err != nil {
		t.Fatalf("a valid node on a spec with an empty media type was refused: %v", err)
	}
	report := f.report(t, shipNode("ping", map[string]any{"stray": 1}))
	if len(report.Problems) != 1 || report.Problems[0].Fatal || !strings.Contains(report.Problems[0].Message, "stray") {
		t.Fatalf("report = %+v, want the spec graded: ping declares no parameters, so stray is reported", report)
	}
}

func TestControlAction_SaysWhyAConnectorWasNotChecked(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)

	createHydrationConnector(t, f.repo, "no-spec", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/s.json"`))
	createHydrationConnector(t, f.repo, "allow-list-only", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/s.json","allow_list":"getShipment"`))
	lost := createHydrationConnector(t, f.repo, "lost-spec", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
	if err := f.repo.UpdateResourceKey(t.Context(), lost.ID, "connectors/missing.yaml", "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}
	broken := createHydrationConnector(t, f.repo, "broken-spec", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
	storeStoredSpec(t, f.repo, f.specs, broken, "this is: [not an openapi document")
	mediaTypes := createHydrationConnector(t, f.repo, "media-types", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, mediaTypes, `openapi: 3.0.0
info: {title: Media types, version: 1.0.0}
servers: [{url: "https://api.example.com"}]
paths:
  /pets:
    post:
      operationId: getPet
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [name], properties: {name: {type: string}}}
          application/x-www-form-urlencoded:
            schema: {type: object, properties: {name: {type: string}}}
      responses:
        "200": {description: ok}
`)

	empty := createHydrationConnector(t, f.repo, "empty-spec", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, empty, `openapi: 3.0.0
info: {title: Empty, version: 1.0.0}
servers: [{url: "https://api.example.com"}]
paths: {}
`)
	createHydrationConnector(t, f.repo, "bad-values", "api", "openapi", `{not json`)

	input := map[string]any{"x": 1}
	report := f.report(t,
		connectorNode("a", "no-spec", "get_shipment", input),
		connectorNode("b", "allow-list-only", "get_shipment", input),
		connectorNode("c", "lost-spec", "get_shipment", input),
		connectorNode("d", "broken-spec", "get_shipment", input),
		connectorNode("e", "media-types", "get_pet", input),
		connectorNode("e2", "media-types", "get_pet", input),
		connectorNode("f", "not-created-yet", "", input),
		connectorNode("g", "empty-spec", "", input),
		connectorNode("h", "bad-values", "", input),
	)
	perConnector := map[string]int{}
	for _, u := range report.Unchecked {
		perConnector[u.Connector]++
	}
	if perConnector["media-types"] != 1 {
		t.Errorf("two nodes on one unsettled operation gave %d unchecked entries, want one", perConnector["media-types"])
	}
	if len(report.Problems) != 0 {
		t.Fatalf("problems = %+v, want none: nothing here could be graded", report.Problems)
	}
	reasons := map[string]string{}
	for _, u := range report.Unchecked {
		reasons[u.Connector] = u.Reason
	}
	want := map[string]string{
		"no-spec":         reasonNoSpecOrNames,
		"allow-list-only": reasonNoUploadedSpec,
		"lost-spec":       reasonSpecUnreadable,
		"broken-spec":     reasonSpecUnparseable,
		"media-types":     fmt.Sprintf(reasonOperationNotSettled, "get_pet"),
		"not-created-yet": reasonConnectorNotResolved,
		"empty-spec":      reasonSpecHasNoOperations,
		"bad-values":      reasonToolNamesUnreadable,
	}
	for connector, reason := range want {
		if reasons[connector] != reason {
			t.Errorf("%s: reason = %q, want %q", connector, reasons[connector], reason)
		}
	}

	const msg = "could not read a connector to check a workflow's tool-node inputs"
	byTarget := map[string]map[string]any{}
	for _, line := range logLines(t, f.logs) {
		if m, _ := line["msg"].(string); !strings.Contains(m, msg) {
			continue
		}
		target, _ := line["connector"].(string)
		if target == "" {
			target, _ = line["connectorID"].(string)
		}
		byTarget[target] = line
	}
	for target, want := range map[string]map[string]any{
		"not-created-yet": {"level": "DEBUG", "failureClass": failureNotFound},
		broken.ID:         {"level": "DEBUG", "failureClass": failureUnparseable, "reason": reasonSpecUnparseable},
		lost.ID:           {"level": "WARN", "failureClass": failureReadFailed, "reason": reasonSpecUnreadable},
	} {
		line, logged := byTarget[target]
		if !logged {
			t.Errorf("no ungraded-connector log for %s:\n%s", target, f.logs.String())
			continue
		}
		assertLogFields(t, line, want)
	}
	if len(byTarget) != 4 {
		t.Errorf("%d connectors were logged as ungraded, want 4 (the three above and bad-values):\n%s", len(byTarget), f.logs.String())
	}
	for _, leaked := range []string{"parse spec", "malformed values JSON"} {
		if strings.Contains(f.logs.String(), leaked) {
			t.Errorf("the log repeats the raw cause (%q), which can carry spec content:\n%s", leaked, f.logs.String())
		}
	}
}

func TestWorkflowSave_SaysInTheLogWhichConnectorsWentUnchecked(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	createHydrationConnector(t, f.repo, "allow-list-only", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/s.json","allow_list":"getShipment"`))

	node := connectorNode("ship", "allow-list-only", "get_shipment", map[string]any{"anything": "x"})
	if _, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "ungraded", Description: "binds a connector with no uploaded spec",
		AppConfig: map[string]any{"output_mapping": map[string]any{"result": "{{ship.output}}"}, "nodes": []any{node}},
	}, "u"); err != nil {
		t.Fatalf("save refused a node that cannot be graded: %v", err)
	}
	line := findLog(t, f.logs, "inputs not checked against a connector")
	assertLogFields(t, line, map[string]any{"level": "DEBUG", "connector": "allow-list-only", "reason": reasonNoUploadedSpec})
}

func TestControlAction_ClassifiesWhyAConnectorCouldNotBeResolved(t *testing.T) {
	t.Parallel()
	nodes := map[string]any{"nodes": []any{connectorNode("ship", "carrier-api", "get_shipment", map[string]any{"x": 1})}}

	t.Run("a cancelled request is interrupted and quiet", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		report := f.svc.CheckConnectorToolInputs(ctx, nodes)
		if len(report.Unchecked) != 1 || report.Unchecked[0].Reason != reasonCheckInterrupted {
			t.Fatalf("unchecked = %+v, want the interrupted reason", report.Unchecked)
		}
		assertLogFields(t, findLog(t, f.logs, "could not read a connector"), map[string]any{"level": "DEBUG", "failureClass": failureCanceled})
	})

	t.Run("a store that fails is a warning of its own class", func(t *testing.T) {
		t.Parallel()
		var logs bytes.Buffer
		check := &connectorInputCheck{
			resolve: resolveByName(store.NewConnectorRepo(closedTestDB(t))),
			logger:  slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		}
		report := check.check(t.Context(), nodes)
		if len(report.Unchecked) != 1 || report.Unchecked[0].Reason != reasonConnectorNotResolved {
			t.Fatalf("unchecked = %+v, want the connector unresolved", report.Unchecked)
		}
		assertLogFields(t, findLog(t, &logs, "could not read a connector"), map[string]any{"level": "WARN", "failureClass": failureReadFailed})
	})
}

func TestWorkflowSave_ANonAPIConnectorIsLeftAlone(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	createHydrationConnector(t, f.repo, "mailer", "email", "smtp", emailConnectorValues())

	node := map[string]any{"id": "send", "type": "tool", "connector": "mailer", "input": map[string]any{"anything": "x"}}
	if _, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "mail-flow", Description: "binds an email connector",
		AppConfig: map[string]any{"output_mapping": map[string]any{"result": "{{send.output}}"}, "nodes": []any{node}},
	}, "u"); err != nil {
		t.Fatalf("save refused a node bound to a non-api connector: %v", err)
	}
	if report := f.report(t, node); len(report.Problems) != 0 || len(report.Unchecked) != 0 {
		t.Fatalf("report = %+v, want nothing said about an email connector", report)
	}
}

func TestRepoSpecFixtures_EveryWorkflowThatRunsStillSaves(t *testing.T) {
	sourcetree.RequireInternal(t)
	t.Parallel()
	specs, err := filepath.Glob("../../../cmd/sam-e2e-harness/mocksrv/openapi/specs/*.json")
	if err != nil || len(specs) == 0 {
		t.Fatalf("no spec fixtures found: %v", err)
	}
	for _, path := range specs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			f := newInputCheckFixture(t)
			conn := createHydrationConnector(t, f.repo, "fixture-api", "api", "openapi", apiConnectorValues(""))
			storeStoredSpec(t, f.repo, f.specs, conn, string(raw))

			operations, err := openapi.EnumerateOperations(t.Context(), raw, nil, nil)
			if err != nil || len(operations) == 0 {
				t.Fatalf("enumerate %s: %v (%d operations)", path, err, len(operations))
			}
			for _, op := range operations {
				input := map[string]any{}
				for _, p := range op.Params {
					input[p.Name] = "{{workflow.input." + p.Name + "}}"
				}
				node := connectorNode("call", "fixture-api", op.ToolName, input)
				report := f.report(t, node)
				if len(report.Problems) != 0 {
					t.Errorf("%s: a node naming exactly the declared parameters was refused: %+v", op.ToolName, report.Problems)
				}
				input["not_a_declared_parameter_zz"] = "x"
				if report := f.report(t, node); len(report.Problems) != 1 || report.Problems[0].Fatal {
					t.Errorf("%s: an undeclared key was not reported as a warning: %+v", op.ToolName, report)
				}
			}
		})
	}
}

func TestConnectorInputCheck_NamesTheNodesOwnReferenceNotTheConnectorsCurrentName(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	conn := createHydrationConnector(t, f.repo, "renamed-api", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, conn, shipmentSpec)
	conn, err := f.repo.GetByID(t.Context(), conn.ID)
	if err != nil || conn == nil {
		t.Fatalf("reload connector: %v", err)
	}
	check := &connectorInputCheck{
		resolve: func(context.Context, string) (*store.Connector, error) { return conn, nil },
		specs:   f.specs,
	}

	report := check.check(t.Context(), map[string]any{"nodes": []any{
		connectorNode("ship", "old-name", "get_shipment", map[string]any{"tracking_number": "x"}),
	}})
	if len(report.Problems) != 1 || report.Problems[0].Connector != "old-name" || !strings.Contains(report.Problems[0].Message, `connector "old-name"`) {
		t.Fatalf("problems = %+v, want the node's own reference name", report.Problems)
	}
}

type failingResourceStore struct {
	ResourceStore
	err error
}

func (s failingResourceStore) Get(context.Context, string) (io.ReadCloser, error) { return nil, s.err }

func TestControlAction_ClassifiesEveryWayAStoredSpecCanFailToRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		level  string
		class  string
		reason string
	}{
		{"a store that times out is the platform failing", fmt.Errorf("get: %w", context.DeadlineExceeded), "WARN", failureTimeout, reasonCheckInterrupted},
		{"a request cancelled between resolve and read is expected", fmt.Errorf("get: %w", context.Canceled), "DEBUG", failureCanceled, reasonCheckInterrupted},
		{"any other store error is the platform failing", errors.New("connection refused"), "WARN", failureReadFailed, reasonSpecUnreadable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.shipmentConnector(t, "carrier-api")
			f.svc.SetResourceStore(failingResourceStore{ResourceStore: f.specs, err: tc.err})

			report := f.report(t, connectorNode("ship", "carrier-api", "get_shipment", map[string]any{"x": 1}))
			if len(report.Unchecked) != 1 || report.Unchecked[0].Reason != tc.reason {
				t.Fatalf("unchecked = %+v, want the reason %q", report.Unchecked, tc.reason)
			}
			assertLogFields(t, findLog(t, f.logs, "could not read a connector"), map[string]any{"level": tc.level, "failureClass": tc.class})
		})
	}
}

func TestConnectorInputCheck_ClassifiesWhyAReferenceDidNotResolve(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	gone := []store.WorkflowConnectorBinding{{ConnectorID: "00000000-0000-0000-0000-000000000000", RefName: "carrier-api"}}
	cases := []struct {
		name    string
		resolve connectorResolver
		class   string
	}{
		{"no binding for the reference", resolveThroughBindings(nil, f.repo), failureNotFound},
		{"a binding whose connector no longer exists", resolveThroughBindings(gone, f.repo), failureNotFound},
		{"a name two connectors answer to", func(context.Context, string) (*store.Connector, error) { return nil, errConnectorAmbiguous }, failureAmbiguous},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			check := &connectorInputCheck{resolve: tc.resolve, specs: f.specs, logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
			report := check.check(t.Context(), map[string]any{"nodes": []any{connectorNode("ship", "carrier-api", "get_shipment", map[string]any{"x": 1})}})
			if len(report.Unchecked) != 1 || report.Unchecked[0].Reason != reasonConnectorNotResolved {
				t.Fatalf("unchecked = %+v, want the connector unresolved", report.Unchecked)
			}
			assertLogFields(t, findLog(t, &logs, "could not read a connector"), map[string]any{"level": "DEBUG", "failureClass": tc.class})
		})
	}
}

func TestConnectorInputCheck_ResolvesAConnectorOncePerCheck(t *testing.T) {
	t.Parallel()
	var calls int
	var logs bytes.Buffer
	check := &connectorInputCheck{
		resolve: func(context.Context, string) (*store.Connector, error) {
			calls++
			return nil, errConnectorNotFound
		},
		logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	check.check(t.Context(), map[string]any{"nodes": []any{
		connectorNode("a", "carrier-api", "get_shipment", map[string]any{"x": 1}),
		connectorNode("b", "carrier-api", "get_shipment", map[string]any{"x": 2}),
		connectorNode("c", "carrier-api", "get_shipment", map[string]any{"x": 3}),
	}})
	if calls != 1 || len(logLines(t, &logs)) != 1 {
		t.Fatalf("resolved %d times and logged %d lines for three nodes on one connector, want once each", calls, len(logLines(t, &logs)))
	}
}

func TestWorkflowSave_TheUndeclaredKeyWarnIsOnlyForTheGateAndOnlyWhenNotFatal(t *testing.T) {
	t.Parallel()
	const msg = "passes input keys its OpenAPI operation does not declare"
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")

	report := f.report(t, connectorNode("ship", "carrier-api", "get_shipment", map[string]any{"tracking": "t", "tracking_number": "x"}))
	if len(report.Problems) != 1 || report.Problems[0].Fatal {
		t.Fatalf("problems = %+v, want the undeclared key reported", report.Problems)
	}
	for _, line := range logLines(t, f.logs) {
		if m, _ := line["msg"].(string); strings.Contains(m, msg) {
			t.Fatalf("the control action logged the undeclared key: %v", line)
		}
	}

	if err := f.create(t, shipNode("get_shipment", map[string]any{"tracking_number": "x"})); err == nil {
		t.Fatal("a node missing a required parameter must be refused")
	}
	for _, line := range logLines(t, f.logs) {
		if m, _ := line["msg"].(string); strings.Contains(m, msg) {
			t.Fatalf("a refused node was also logged as an undeclared key: %v", line)
		}
	}
}
