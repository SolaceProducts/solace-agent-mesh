// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	petSpecWithDelete = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}},"delete":{"operationId":"deletePet","responses":{"200":{"description":"ok"}}}}}}`
	petSpecWithRemove = `{"openapi":"3.0.0","info":{"title":"t","version":"2"},"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}},"delete":{"operationId":"removePet","responses":{"200":{"description":"ok"}}}}}}`
	petSpecListOnly   = `{"openapi":"3.0.0","info":{"title":"t","version":"3"},"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`
	deletePetTool     = "delete_pet"
	placeOrderTool    = "place_order"
)

func gateOn(tools ...string) map[string]any {
	entries := make(map[string]any, len(tools))
	for _, name := range tools {
		entries[name] = map[string]any{"require_approval": true}
	}
	return map[string]any{"tools": entries}
}

func petValues(extra map[string]any) map[string]any {
	values := map[string]any{"base_url": "https://api.example.com", "auth_type": "none"}
	for k, v := range extra {
		values[k] = v
	}
	return values
}

func storedConnectorValues(t *testing.T, svc *ConnectorService, id string) map[string]any {
	t.Helper()
	_, _, values, err := svc.GetValuesCleartext(t.Context(), id)
	if err != nil {
		t.Fatalf("read stored values: %v", err)
	}
	return values
}

func storedGateKeys(t *testing.T, svc *ConnectorService, id string) []string {
	t.Helper()
	return ConnectorHILToolKeys(storedConnectorValues(t, svc, id)["hil"])
}

func removedGateWarning(tool string) string {
	return `approval gate on "` + tool + `" was removed: the connector no longer provides that tool`
}

func gatedPetConnector(t *testing.T, tools ...string) (*ConnectorService, string) {
	t.Helper()
	svc, _, created := newOpenAPIConnectorFixture(t)
	if _, err := svc.UploadResource(t.Context(), created.ID, "spec.json", strings.NewReader(petSpecWithDelete)); err != nil {
		t.Fatalf("seed UploadResource: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn(tools...)})}, "user-1"); err != nil {
		t.Fatalf("seed gate: %v", err)
	}
	return svc, created.ID
}

func withHIL(values map[string]any, hil any, present bool) map[string]any {
	if present {
		values["hil"] = hil
	}
	return values
}

func TestConnectorService_Update_APIDeselectingGatedOperationKeepsGate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		hil     any
		present bool
	}{
		"hil omitted": {nil, false},
		"hil null":    {nil, true},
		"hil echoed":  {gateOn(deletePetTool), true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, id := gatedPetConnector(t, deletePetTool)
			values := withHIL(petValues(map[string]any{"allow_list": "listPets"}), tc.hil, tc.present)

			resp, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: values}, "user-1")
			if err != nil {
				t.Fatalf("narrowing the allow_list was refused: %v", err)
			}
			if len(resp.Warnings) != 0 {
				t.Errorf("warnings = %v, want none", resp.Warnings)
			}
			if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{deletePetTool}) {
				t.Errorf("stored gate keys = %v, want [delete_pet]", got)
			}
		})
	}
}

func TestConnectorService_Update_APIAuthoredUnknownGateKeyIsRefused(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	_, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn("delet_pet")})}, "user-1")

	valErr, ok := errors.AsType[*ValidationError](err)
	if !ok || !strings.Contains(valErr.Message, "hil.tools.delet_pet") {
		t.Fatalf("Update = %v, want a ValidationError naming hil.tools.delet_pet", err)
	}
	if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{deletePetTool}) {
		t.Errorf("stored gate keys = %v after the refused update, want [delete_pet]", got)
	}
}

func TestConnectorService_UploadResource_PrunesGateOnRemovedOperation(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	resp, err := svc.UploadResource(t.Context(), id, "spec.json", strings.NewReader(petSpecListOnly))
	if err != nil {
		t.Fatalf("upload of a spec lacking the gated operation was refused: %v", err)
	}

	if !slices.Equal(resp.Warnings, []string{removedGateWarning(deletePetTool)}) {
		t.Errorf("warnings = %v, want the removed-gate warning", resp.Warnings)
	}
	values := storedConnectorValues(t, svc, id)
	if _, present := values["hil"]; present {
		t.Errorf("stored hil = %v, want the key absent", values["hil"])
	}
	if values["specification_url"] != SpecResourcePath(id) {
		t.Errorf("specification_url = %v, want the managed spec path", values["specification_url"])
	}
}

const petSpecWithExternalRef = `openapi: 3.0.0
info:
  title: Refs
  version: 1.0.0
paths:
  /pets:
    $ref: "http://192.0.2.1/path-item.yaml#/pets"
`

func TestConnectorService_UploadResource_WarnsWhenGatesCannotBeCheckedAgainstUnparseableSpec(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	resp, err := svc.UploadResource(t.Context(), id, "spec.yaml", strings.NewReader(petSpecWithExternalRef))
	if err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	want := `approval gates on "delete_pet" could not be checked: ` + unparseableSpecHILReason
	if !slices.Equal(resp.Warnings, []string{want}) {
		t.Errorf("warnings = %v, want [%s]", resp.Warnings, want)
	}
	if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{deletePetTool}) {
		t.Errorf("stored gate keys = %v, want the gate kept", got)
	}
}

func TestConnectorService_UploadResource_UnparseableSpecWithoutGatesWarnsNothing(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)

	resp, err := svc.UploadResource(t.Context(), created.ID, "spec.yaml", strings.NewReader(petSpecWithExternalRef))
	if err != nil {
		t.Fatalf("UploadResource: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", resp.Warnings)
	}
}

const (
	unparsedGateWarning = `approval gates on "delete_pett" could not be checked: ` + unparseableSpecHILReason
	unreadGateWarning   = `approval gates on "delete_pett" could not be checked: ` + unreadableSpecHILReason
	noSpecGateWarning   = `approval gates on "delete_pett" could not be checked: ` + uncheckedAPIHILReason
	gateTypo            = "delete_pett"
)

func gateTypoUpdate() UpdateConnectorRequest {
	return UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn(gateTypo)})}
}

func TestConnectorService_Update_WarnsWhenGatesCannotBeCheckedAgainstTheStoredSpec(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		breakSpec func(t *testing.T, svc *ConnectorService, id string)
		want      string
	}{
		"an unparseable stored spec": {
			breakSpec: func(t *testing.T, svc *ConnectorService, id string) {
				t.Helper()
				if _, err := svc.UploadResource(t.Context(), id, "spec.yaml", strings.NewReader(petSpecWithExternalRef)); err != nil {
					t.Fatalf("UploadResource: %v", err)
				}
			},
			want: unparsedGateWarning,
		},
		"an unreadable stored spec": {
			breakSpec: func(t *testing.T, svc *ConnectorService, id string) {
				t.Helper()
				if err := svc.resourceStore.Delete(t.Context(), connectorResourceKey(svc.namespace, id, "spec.json")); err != nil {
					t.Fatalf("delete the stored spec: %v", err)
				}
			},
			want: unreadGateWarning,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, id := gatedPetConnector(t, deletePetTool)
			tc.breakSpec(t, svc, id)
			req := gateTypoUpdate()

			preflight, err := svc.PreflightUpdate(t.Context(), id, req)
			if err != nil || !slices.Equal(preflight, []string{tc.want}) {
				t.Errorf("PreflightUpdate = %q, %v; want [%s]", preflight, err, tc.want)
			}
			validated, err := svc.ValidateUpdate(t.Context(), id, req)
			if err != nil || !slices.Equal(validated, []string{tc.want}) {
				t.Errorf("ValidateUpdate = %q, %v; want [%s]", validated, err, tc.want)
			}
			resp, err := svc.Update(t.Context(), id, req, "user-1")
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !slices.Equal(resp.Warnings, []string{tc.want}) {
				t.Errorf("Update warnings = %q, want [%s]", resp.Warnings, tc.want)
			}
			if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{gateTypo}) {
				t.Errorf("stored gate keys = %v, want the gate kept", got)
			}
		})
	}
}

func TestConnectorService_UpdateWithRedeploy_WarnsWhenGatesCannotBeCheckedAgainstTheStoredSpec(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	svc.SetRedeployDeps(db, store.NewAgentRepo(db), store.NewWorkflowRepo(db), store.NewOutboxRepo(db))
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "petstore", Description: "OpenAPI connector for the gate warning test", Type: "api", Subtype: "openapi",
		Values: petValues(nil),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if _, err = svc.UploadResource(t.Context(), created.ID, "spec.yaml", strings.NewReader(petSpecWithExternalRef)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, gateTypoUpdate(), "user-1", ConnectorUpdateOptions{})

	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	if !slices.Equal(resp.Warnings, []string{unparsedGateWarning}) {
		t.Errorf("warnings = %q, want [%s]", resp.Warnings, unparsedGateWarning)
	}
}

func TestConnectorService_ValidateUpdate_WarnsWhenTheConnectorHasNoSpecification(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)

	warnings, err := svc.ValidateUpdate(t.Context(), created.ID, gateTypoUpdate())

	if err != nil || !slices.Equal(warnings, []string{noSpecGateWarning}) {
		t.Errorf("ValidateUpdate = %q, %v; want [%s]", warnings, err, noSpecGateWarning)
	}
}

func TestConnectorService_Update_UnparseableStoredSpecWithoutGatesWarnsNothing(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)
	if _, err := svc.UploadResource(t.Context(), created.ID, "spec.yaml", strings.NewReader(petSpecWithExternalRef)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}
	req := UpdateConnectorRequest{Values: petValues(nil)}

	preflight, err := svc.PreflightUpdate(t.Context(), created.ID, req)
	if err != nil || len(preflight) != 0 {
		t.Errorf("PreflightUpdate = %q, %v; want no warnings", preflight, err)
	}
	validated, err := svc.ValidateUpdate(t.Context(), created.ID, req)
	if err != nil || len(validated) != 0 {
		t.Errorf("ValidateUpdate = %q, %v; want no warnings", validated, err)
	}
}

func TestConnectorService_UploadResource_KeepsGatesOnSurvivingOperations(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool, "list_pets")

	resp, err := svc.UploadResource(t.Context(), id, "spec.json", strings.NewReader(petSpecWithRemove))
	if err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	if !slices.Equal(resp.Warnings, []string{removedGateWarning(deletePetTool)}) {
		t.Errorf("warnings = %v, want the removed-gate warning", resp.Warnings)
	}
	if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{"list_pets"}) {
		t.Errorf("stored gate keys = %v, want [list_pets]", got)
	}
}

func TestConnectorService_UploadResource_RenameThenPatchConverges(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	if _, err := svc.UploadResource(t.Context(), id, "spec.json", strings.NewReader(petSpecWithRemove)); err != nil {
		t.Fatalf("upload of the renamed spec: %v", err)
	}
	if _, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn("remove_pet")})}, "user-1"); err != nil {
		t.Fatalf("authoring the gate on the renamed operation: %v", err)
	}
	if got := storedGateKeys(t, svc, id); !slices.Equal(got, []string{"remove_pet"}) {
		t.Errorf("stored gate keys = %v, want [remove_pet]", got)
	}
}

func eventMeshOpValues(toolName string, hil any, withHIL bool) map[string]any {
	values := emValues(map[string]any{
		"broker_connection_type": "custom",
		"operations":             []any{emOperation(map[string]any{"tool_name": toolName})},
	})
	if withHIL {
		values["hil"] = hil
	}
	return values
}

func gatedEventMeshConnector(t *testing.T) (*ConnectorService, string) {
	t.Helper()
	svc, _, _ := newOpenAPIConnectorFixture(t)
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders", Description: "Event mesh orders", Type: "event_mesh", Subtype: "solace",
		Values: eventMeshOpValues(placeOrderTool, gateOn(placeOrderTool), true),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	return svc, created.ID
}

func TestConnectorService_Update_EventMeshRenamePrunesInheritedGate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		hil     any
		present bool
	}{
		"hil omitted": {nil, false},
		"hil null":    {nil, true},
		"hil echoed":  {gateOn(placeOrderTool), true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, id := gatedEventMeshConnector(t)

			resp, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: eventMeshOpValues("submit_order", tc.hil, tc.present)}, "user-1")
			if err != nil {
				t.Fatalf("renaming the gated operation was refused: %v", err)
			}

			if !slices.Equal(resp.Warnings, []string{removedGateWarning(placeOrderTool)}) {
				t.Errorf("warnings = %v, want the removed-gate warning", resp.Warnings)
			}
			if stored := storedConnectorValues(t, svc, id); stored["hil"] != nil {
				t.Errorf("stored hil = %v, want the key absent", stored["hil"])
			}
		})
	}
}

func TestConnectorService_Update_EventMeshAuthoredUnknownGateKeyIsRefused(t *testing.T) {
	t.Parallel()
	svc, id := gatedEventMeshConnector(t)

	_, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: eventMeshOpValues(placeOrderTool, gateOn("plce_order"), true)}, "user-1")

	valErr, ok := errors.AsType[*ValidationError](err)
	if !ok || !strings.Contains(valErr.Message, "hil.tools.plce_order") {
		t.Fatalf("Update = %v, want a ValidationError naming hil.tools.plce_order", err)
	}
}

func TestConnectorService_CreateAndUpdate_APIGateWithoutSpecWarns(t *testing.T) {
	t.Parallel()
	svc, _, _ := newOpenAPIConnectorFixture(t)
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "external", Description: "spec by url", Type: "api", Subtype: "openapi",
		Values: petValues(map[string]any{"hil": gateOn(deletePetTool)}),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertOneWarningContaining(t, created.Warnings, `"delete_pet"`, "none could be read from its specification URL")

	updated, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn("remove_pet")})}, "user-1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertOneWarningContaining(t, updated.Warnings, `"remove_pet"`, "none could be read from its specification URL")

	kept, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: petValues(nil)}, "user-1")
	if err != nil {
		t.Fatalf("Update that keeps the stored gate: %v", err)
	}
	assertOneWarningContaining(t, kept.Warnings, `"remove_pet"`)
}

func assertOneWarningContaining(t *testing.T, warnings []string, fragments ...string) {
	t.Helper()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	for _, fragment := range fragments {
		if !strings.Contains(warnings[0], fragment) {
			t.Errorf("warning %q does not contain %q", warnings[0], fragment)
		}
	}
}

func TestConnectorService_Update_APIGateWithStoredSpecDoesNotWarn(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	resp, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: petValues(nil)}, "user-1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a connector with a stored spec", resp.Warnings)
	}
}

func TestConnectorService_EmptyHILBlockIsNotStored(t *testing.T) {
	t.Parallel()
	svc, id := gatedPetConnector(t, deletePetTool)

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "plain", Description: "connector with no gate", Type: "api", Subtype: "openapi",
		Values: petValues(map[string]any{"hil": map[string]any{}}),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stored := storedConnectorValues(t, svc, created.ID); stored["hil"] != nil {
		t.Errorf("created hil = %v, want the key absent", stored["hil"])
	}

	for name, empty := range map[string]any{"empty map": map[string]any{}, "empty tools": map[string]any{"tools": map[string]any{}}} {
		if _, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": empty})}, "user-1"); err != nil {
			t.Fatalf("%s: Update: %v", name, err)
		}
		if stored := storedConnectorValues(t, svc, id); stored["hil"] != nil {
			t.Errorf("%s: stored hil = %v, want the key absent", name, stored["hil"])
		}
		if _, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: petValues(map[string]any{"hil": gateOn(deletePetTool)})}, "user-1"); err != nil {
			t.Fatalf("%s: re-gating: %v", name, err)
		}
	}
}

func bindAgentToConnector(t *testing.T, db *store.DB, connectorID string) string {
	t.Helper()
	agentSvc := NewAgentService(store.NewAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), nil, nil, discardLogger())
	agent, err := agentSvc.Create(t.Context(), CreateAgentRequest{
		Name:         "pet-agent",
		Description:  "Agent bound to the connector under test",
		SystemPrompt: "You help with the pet store and answer user questions about pets.",
		Toolsets:     []string{},
		InputModes:   []string{"text"},
		OutputModes:  []string{"text"},
		Connectors:   []string{connectorID},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return agent.ID
}

func TestConnectorService_UploadResource_QueuesTheConnectorRedeployOnlyWhenTheSpecChanges(t *testing.T) {
	t.Parallel()
	svc, db, outbox := urlSpecService(t)
	created := createURLSpecConnector(t, svc, petValues(map[string]any{"hil": gateOn(deletePetTool)}))
	agentID := bindAgentToConnector(t, db, created.ID)
	upload := func(spec string) *ConnectorResponse {
		t.Helper()
		resp, err := svc.UploadResource(t.Context(), created.ID, "spec.json", strings.NewReader(spec))
		if err != nil {
			t.Fatalf("UploadResource: %v", err)
		}
		return resp
	}
	events := func() []redeployPayload { return outboxPayloads(t, outbox, "connector.updated") }

	upload(petSpecWithDelete)
	if got := events(); len(got) != 1 || !slices.Equal(got[0].AgentIDs, []string{agentID}) {
		t.Fatalf("first upload queued %+v, want one connector.updated covering the bound agent", got)
	}

	upload(petSpecWithDelete)
	if got := events(); len(got) != 1 {
		t.Errorf("an identical re-upload queued %d events in all, want still 1", len(got))
	}

	resp := upload(petSpecListOnly)
	if !slices.Equal(resp.Warnings, []string{removedGateWarning(deletePetTool)}) {
		t.Errorf("warnings = %v, want the removed-gate warning", resp.Warnings)
	}
	if got := events(); len(got) != 2 || !slices.Equal(got[1].AgentIDs, []string{agentID}) {
		t.Errorf("a spec change queued %+v in all, want a second connector.updated covering the bound agent", got)
	}
	if got := storedGateKeys(t, svc, created.ID); len(got) != 0 {
		t.Errorf("stored gate keys = %v, want the pruned gate gone", got)
	}
}

func TestConnectorService_UploadResource_FailedRedeployWriteRollsBackThePrune(t *testing.T) {
	t.Parallel()
	svc, db, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, petValues(map[string]any{"hil": gateOn(deletePetTool)}))
	bindAgentToConnector(t, db, created.ID)
	if _, err := db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
		t.Skipf("cannot break the outbox table: %v", err)
	}

	if _, err := svc.UploadResource(t.Context(), created.ID, "spec.json", strings.NewReader(petSpecListOnly)); err == nil {
		t.Fatal("UploadResource succeeded although the redeploy event could not be written")
	}

	if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{deletePetTool}) {
		t.Errorf("stored gate keys = %v, want the gate kept when the upload failed", got)
	}
}
