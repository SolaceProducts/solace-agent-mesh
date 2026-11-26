// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const keptHILWarning = "kept these stored values"

func applyConnectorHIL(t *testing.T, svc *Service, hil any, set bool) ApplyBuildResponse {
	t.Helper()
	values := fullSQLValues()
	if set {
		values[hilSetting] = hil
	}
	res, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", values)}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return *res
}

func sqlQueryGate(message string) map[string]any {
	return map[string]any{"tools": map[string]any{"sql_query": map[string]any{"require_approval": true, "approval_message": message}}}
}

func storedConnectorValues(t *testing.T, svc *Service, name string) map[string]any {
	t.Helper()
	values, _ := connectorNamed(t, svc, name).Values.(map[string]any)
	return values
}

func storedGateMessage(t *testing.T, svc *Service, name string) string {
	t.Helper()
	hil, _ := storedConnectorValues(t, svc, name)[hilSetting].(map[string]any)
	tools, _ := hil["tools"].(map[string]any)
	entry, _ := tools["sql_query"].(map[string]any)
	message, _ := entry["approval_message"].(string)
	return message
}

func TestApply_ConnectorHIL_OmittedKeepsExplicitReplacesNullRemoves(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	applyConnectorHIL(t, svc, sqlQueryGate("first"), true)
	res := applyConnectorHIL(t, svc, nil, false)
	if storedGateMessage(t, svc, "db_conn") != "first" {
		t.Errorf("an omitted hil dropped the stored gate")
	}
	if w := strings.Join(res.Operations[0].Warnings, "\n"); !strings.Contains(w, keptHILWarning) || !strings.Contains(w, hilSetting) {
		t.Errorf("warnings %q do not report the kept hil", w)
	}

	applyConnectorHIL(t, svc, sqlQueryGate("second"), true)
	if storedGateMessage(t, svc, "db_conn") != "second" {
		t.Errorf("an explicit hil did not replace the stored one")
	}

	applyConnectorHIL(t, svc, nil, true)
	if storedGateMessage(t, svc, "db_conn") != "" {
		t.Errorf("hil: null left the gate in place")
	}
	if again := applyConnectorHIL(t, svc, nil, true); again.Operations[0].Action != actionUnchanged {
		t.Errorf("re-saving hil: null plans %q, want unchanged", again.Operations[0].Action)
	}
}

func TestApply_ConnectorHIL_NullWithNoStoredGateIsUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	applyConnectorHIL(t, svc, nil, false)
	res := applyConnectorHIL(t, svc, nil, true)

	if got := res.Operations[0].Action; got != actionUnchanged {
		t.Errorf("hil: null on a connector with no gate plans %q, want unchanged", got)
	}
	if _, stored := storedConnectorValues(t, svc, "db_conn")[hilSetting]; stored {
		t.Errorf("hil: null stored a hil key on a connector that had none")
	}
}

func TestApply_ConnectorHIL_OmittedAfterRemovalWarnsNothingAndIsUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	applyConnectorHIL(t, svc, sqlQueryGate("first"), true)
	applyConnectorHIL(t, svc, nil, true)
	res := applyConnectorHIL(t, svc, nil, false)

	if w := strings.Join(res.Operations[0].Warnings, "\n"); strings.Contains(w, keptHILWarning) {
		t.Errorf("an omitted hil after removal still warns: %q", w)
	}
	if got := res.Operations[0].Action; got != actionUnchanged {
		t.Errorf("an omitted hil after removal plans %q, want unchanged", got)
	}
}

func apiConnectorWithHIL(name string, hilTool string) BuildComponentInput {
	return connectorWith(name, func(b map[string]any) {
		b["type"], b["subtype"] = "api", "openapi"
		b["values"] = map[string]any{
			"base_url":  "https://api.example.com",
			"auth_type": "none",
			hilSetting:  map[string]any{"tools": map[string]any{hilTool: map[string]any{"require_approval": true}}},
		}
	})
}

func TestApply_APIConnectorHILOutsideStoredSpec_PreviewAgreesWithWrite(t *testing.T) {
	t.Parallel()
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/users":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok"}}}}}}`
	svc := newTestService(t)
	svc.deps.Connectors = entplatform.NewConnectorService(
		store.NewConnectorRepo(svc.deps.DB), objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	ctx := t.Context()

	runBuild(ctx, t, svc, apiConnectorWithHIL("pets_api", "list_users"), false)
	stored := connectorNamed(t, svc, "pets_api")
	if _, err := svc.deps.Connectors.UploadResource(ctx, stored.ID, "spec.json", strings.NewReader(spec)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	for _, dryRun := range []bool{true, false} {
		got := runBuild(ctx, t, svc, apiConnectorWithHIL("pets_api", "delete_user"), dryRun)
		if !got.refused || !strings.Contains(got.message, "hil.tools.delete_user") {
			t.Errorf("dryRun=%v: outcome %+v, want a refusal naming hil.tools.delete_user", dryRun, got)
		}
	}
}

func eventMeshWithOperation(toolName string, extra map[string]any) map[string]any {
	operation := eventMeshOperation()
	operation["tool_name"] = toolName
	return eventMeshValues(mergeMaps(map[string]any{"operations": []any{operation}}, extra))
}

func mergeMaps(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func TestApply_RenamingAGatedEventMeshOperationReportsTheRemovedGate(t *testing.T) {
	t.Parallel()
	f := eventMeshFixture()
	svc := serviceFor(t, f, true)
	const name = "orders_mesh"
	gate := map[string]any{"tools": map[string]any{"create_order": map[string]any{"require_approval": true}}}
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		connectorArtifactFor(name, f, eventMeshWithOperation("create_order", map[string]any{hilSetting: gate})),
	}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dry, applied := applyBoth(t, svc, connectorArtifactFor(name, f, eventMeshWithOperation("place_order", nil)))

	op := opFor(t, applied, kindConnector, name)
	if op.Status != statusSucceeded {
		t.Fatalf("rename failed: %+v", op.Error)
	}
	const removed = `approval gate on "create_order" was removed: the connector no longer provides that tool`
	for label, row := range map[string]BuildOperationResult{"dry run": opFor(t, dry, kindConnector, name), "save": op} {
		var removals []string
		for _, w := range row.Warnings {
			if strings.Contains(w, keptHILWarning) {
				t.Errorf("%s warns that hil was kept although the gate was removed: %q", label, w)
			}
			if strings.Contains(w, "was removed") {
				removals = append(removals, w)
			}
		}
		if len(removals) != 1 || removals[0] != removed {
			t.Errorf("%s removal warnings = %q, want exactly [%q]", label, removals, removed)
		}
	}
	if _, stored := storedValues(t, svc, name)[hilSetting]; stored {
		t.Errorf("the gate on the renamed-away operation is still stored")
	}
}

func TestWithSaveWarnings_RemovedGateTakesHILOutOfTheKeptNoteOnlyWhenNoHILSurvives(t *testing.T) {
	t.Parallel()
	const removed = `approval gate on "create_order" was removed: the connector no longer provides that tool`
	kept := func(keys string) string { return keptUnsetValuesWarning + keys }
	gate := map[string]any{"require_approval": true}
	onlyGate := map[string]any{"tools": map[string]any{"create_order": gate}}
	twoGates := map[string]any{"tools": map[string]any{"create_order": gate, "cancel_order": gate}}
	gateAndDefault := map[string]any{"tools": map[string]any{"create_order": gate}, "require_approval_when": "true"}

	for name, tc := range map[string]struct {
		planned, saved, want []string
		plannedHIL           any
	}{
		"hil is the only kept key":        {[]string{kept("hil")}, []string{removed}, []string{removed}, onlyGate},
		"other kept keys stay":            {[]string{kept("hil, port")}, []string{removed}, []string{kept("port"), removed}, onlyGate},
		"no removal keeps the note":       {[]string{kept("hil")}, nil, []string{kept("hil")}, onlyGate},
		"unrelated warnings are kept":     {[]string{"other", kept("hil")}, []string{removed}, []string{"other", removed}, onlyGate},
		"another gate survives":           {[]string{kept("hil")}, []string{removed}, []string{kept("hil"), removed}, twoGates},
		"a hil default survives":          {[]string{kept("hil")}, []string{removed}, []string{kept("hil"), removed}, gateAndDefault},
		"the same hil in its string form": {[]string{kept("hil")}, []string{removed}, []string{removed}, "tools:\n  create_order:\n    require_approval: true\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := withSaveWarnings(tc.planned, tc.saved, tc.plannedHIL); !slices.Equal(got, tc.want) {
				t.Errorf("withSaveWarnings = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApply_APIConnectorGatesOnAnUnparseableStoredSpec_PreviewAndWriteWarn(t *testing.T) {
	t.Parallel()
	const (
		specWithExternalRef = "openapi: 3.0.0\ninfo:\n  title: Refs\n  version: 1.0.0\npaths:\n  /pets:\n    $ref: \"http://192.0.2.1/path-item.yaml#/pets\"\n"
		unparsedConsequence = "so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list"
		unparsed            = `approval gates on "delete_pett" could not be checked: the uploaded specification could not be parsed, ` + unparsedConsequence
	)
	svc := newTestService(t)
	svc.deps.Connectors = entplatform.NewConnectorService(
		store.NewConnectorRepo(svc.deps.DB), objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	ctx := t.Context()
	runBuild(ctx, t, svc, apiConnectorWithHIL("pets_api", "delete_pet"), false)
	stored := connectorNamed(t, svc, "pets_api")
	if _, err := svc.deps.Connectors.UploadResource(ctx, stored.ID, "spec.yaml", strings.NewReader(specWithExternalRef)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	dry, applied := applyBoth(t, svc, apiConnectorWithHIL("pets_api", "delete_pett"))

	for label, res := range map[string]*ApplyBuildResponse{"dry run": dry, "save": applied} {
		var got []string
		for _, w := range opFor(t, res, kindConnector, "pets_api").Warnings {
			if strings.Contains(w, "could not be checked") {
				got = append(got, w)
			}
		}
		if !slices.Equal(got, []string{unparsed}) {
			t.Errorf("%s unchecked-gate warnings = %q, want exactly [%s]", label, got, unparsed)
		}
	}
}

func TestApply_RenamingOneOfTwoGatedOperationsKeepsHILInTheKeptNote(t *testing.T) {
	t.Parallel()
	f := eventMeshFixture()
	svc := serviceFor(t, f, true)
	const name = "orders_mesh"
	gates := map[string]any{"tools": map[string]any{
		"create_order": map[string]any{"require_approval": true},
		"cancel_order": map[string]any{"require_approval": true},
	}}
	withOperations := func(names ...string) map[string]any {
		operations := make([]any, 0, len(names))
		for _, toolName := range names {
			operation := eventMeshOperation()
			operation["tool_name"] = toolName
			operations = append(operations, operation)
		}
		return eventMeshValues(map[string]any{"operations": operations})
	}
	seeded := withOperations("create_order", "cancel_order")
	seeded[hilSetting] = gates
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor(name, f, seeded)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dry, applied := applyBoth(t, svc, connectorArtifactFor(name, f, withOperations("place_order", "cancel_order")))

	for label, res := range map[string]*ApplyBuildResponse{"dry run": dry, "save": applied} {
		warnings := opFor(t, res, kindConnector, name).Warnings
		if !slices.ContainsFunc(warnings, func(w string) bool {
			return strings.HasPrefix(w, keptUnsetValuesWarning) && strings.Contains(w, hilSetting)
		}) {
			t.Errorf("%s warnings %q dropped hil from the kept note although the cancel_order gate stays", label, warnings)
		}
	}
	if got := entplatform.ConnectorHILToolKeys(storedValues(t, svc, name)[hilSetting]); !slices.Equal(got, []string{"cancel_order"}) {
		t.Errorf("stored gate keys = %v, want [cancel_order]", got)
	}
}

func TestApply_ARefusedApplyReportsTheSaveWarningsItsDryRunDoes(t *testing.T) {
	t.Parallel()
	f := eventMeshFixture()
	svc := serviceFor(t, f, true)
	const name = "orders_mesh"
	gate := map[string]any{"tools": map[string]any{"create_order": map[string]any{"require_approval": true}}}
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		connectorArtifactFor(name, f, eventMeshWithOperation("create_order", map[string]any{hilSetting: gate})),
	}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dry, applied := applyBoth(t, svc,
		connectorArtifactFor(name, f, eventMeshWithOperation("place_order", nil)),
		workflowCallingConnector("ship_flow", "ghost", "anything", ""))

	want := opFor(t, dry, kindConnector, name).Warnings
	got := opFor(t, applied, kindConnector, name)
	if got.Status != statusSkipped {
		t.Fatalf("connector = %q, want skipped: the build was refused before its first write", got.Status)
	}
	if !slices.ContainsFunc(want, func(w string) bool { return strings.Contains(w, "was removed") }) {
		t.Fatalf("the dry run does not forecast the removed gate, so the case proves nothing: %q", want)
	}
	if !slices.Equal(got.Warnings, want) {
		t.Errorf("refused apply warnings = %q, want the dry run's %q", got.Warnings, want)
	}
}
