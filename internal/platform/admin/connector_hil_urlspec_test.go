// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	unknownOperationMessage = "hil.tools.delete_pett is not a tool this connector provides; use one of: delete_pet, list_pets"
	urlSpecGateTypo         = "delete_pett"
)

type urlSpecServer struct {
	*httptest.Server
	status atomic.Int32
	hits   atomic.Int32
	body   atomic.Pointer[string]
}

func newURLSpecServer(t *testing.T) *urlSpecServer {
	t.Helper()
	installPlainProbeClient(t)
	s := &urlSpecServer{}
	s.status.Store(http.StatusOK)
	spec := petSpecWithDelete
	s.body.Store(&spec)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		if status := int(s.status.Load()); status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, *s.body.Load())
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *urlSpecServer) serve(body string) { s.body.Store(&body) }

func (s *urlSpecServer) values(extra map[string]any) map[string]any {
	values := map[string]any{"specification_url": s.URL + "/openapi.json"}
	maps.Copy(values, extra)
	return petValues(values)
}

func urlSpecService(t *testing.T) (*ConnectorService, *store.DB, *store.OutboxRepo) {
	t.Helper()
	db := testOpenDB(t)
	outbox := store.NewOutboxRepo(db)
	svc := NewConnectorService(store.NewConnectorRepo(db), objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	svc.SetRedeployDeps(db, store.NewAgentRepo(db), store.NewWorkflowRepo(db), outbox)
	return svc, db, outbox
}

func createURLSpecConnector(t *testing.T, svc *ConnectorService, values map[string]any) *ConnectorResponse {
	t.Helper()
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "petstore", Description: "OpenAPI connector read from a specification URL", Type: "api", Subtype: "openapi", Values: values,
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	return created
}

func wantUnknownOperationRefusal(t *testing.T, err error) {
	t.Helper()
	valErr, ok := errors.AsType[*ValidationError](err)
	if !ok || valErr.Message != unknownOperationMessage {
		t.Fatalf("err = %v, want a ValidationError %q", err, unknownOperationMessage)
	}
}

func TestConnectorService_Create_APIGateOnOperationMissingFromSpecURLIsRefused(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, _, _ := urlSpecService(t)

	_, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "petstore", Description: "OpenAPI connector read from a specification URL", Type: "api", Subtype: "openapi",
		Values: srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)}),
	}, "user-1")

	wantUnknownOperationRefusal(t, err)
	if got := srv.hits.Load(); got != 1 {
		t.Errorf("the platform fetched the spec %d times, want the probe's one fetch reused", got)
	}
}

func TestConnectorService_Create_APIGateOnOperationInSpecURLSavesWithoutWarning(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, _, _ := urlSpecService(t)

	created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(deletePetTool)}))

	if len(created.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a gate the spec confirms", created.Warnings)
	}
	if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{deletePetTool}) {
		t.Errorf("stored gate keys = %v, want [delete_pet]", got)
	}
}

func TestConnectorService_Create_APIGateWithUnreadableSpecURLSavesAndWarns(t *testing.T) {
	srv := newURLSpecServer(t)
	srv.status.Store(http.StatusInternalServerError)
	svc, _, _ := urlSpecService(t)

	created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)}))

	assertSomeWarningContains(t, created.Warnings, `"delete_pett"`, uncheckedAPIHILReason)
	assertSomeWarningContains(t, created.Warnings, "specification_url is not reachable")
	if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{urlSpecGateTypo}) {
		t.Errorf("stored gate keys = %v, want the gate kept", got)
	}
}

func TestConnectorService_UpdatePaths_APIAuthoredGateOnOperationMissingFromSpecURLIsRefused(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, db, outbox := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(nil))
	req := UpdateConnectorRequest{Values: srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)})}

	_, err := svc.PreflightUpdate(t.Context(), created.ID, req)
	wantUnknownOperationRefusal(t, err)
	_, err = svc.ValidateUpdate(t.Context(), created.ID, req)
	wantUnknownOperationRefusal(t, err)
	_, err = svc.ReadURLSpec(t.Context(), created.ID, req)
	wantUnknownOperationRefusal(t, err)
	_, err = svc.Update(t.Context(), created.ID, req, "user-1")
	wantUnknownOperationRefusal(t, err)
	_, err = svc.UpdateWithRedeploy(t.Context(), created.ID, req, "user-1", ConnectorUpdateOptions{})
	wantUnknownOperationRefusal(t, err)
	_, err = UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: created.ID, Request: req, UserID: "user-1"}, discardLogger())
	wantUnknownOperationRefusal(t, err)

	if got := storedGateKeys(t, svc, created.ID); len(got) != 0 {
		t.Errorf("stored gate keys = %v after the refused updates, want none", got)
	}
	if events, _ := outbox.GetDispatchable(t.Context(), 10); len(events) != 0 {
		t.Errorf("a refused update enqueued %d outbox events", len(events))
	}
}

func TestConnectorService_UpdatePaths_APIAuthoredGateOnOperationInSpecURLSavesWithoutWarning(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, db, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(nil))
	req := UpdateConnectorRequest{Values: srv.values(map[string]any{"hil": gateOn(deletePetTool)})}

	preflight, err := svc.PreflightUpdate(t.Context(), created.ID, req)
	if err != nil || len(preflight) != 0 {
		t.Errorf("PreflightUpdate = %q, %v; want no warnings", preflight, err)
	}
	validated, err := svc.ValidateUpdate(t.Context(), created.ID, req)
	if err != nil || len(validated) != 0 {
		t.Errorf("ValidateUpdate = %q, %v; want no warnings", validated, err)
	}
	resp, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: created.ID, Request: req, UserID: "user-1"}, discardLogger())
	if err != nil || len(resp.Warnings) != 0 {
		t.Errorf("UpdateConnectorWithRedeploy = %v, %v; want no warnings", resp, err)
	}
}

func TestConnectorService_Update_APIStoredGateTheRequestDidNotAuthorIsLeftToTheRuntime(t *testing.T) {
	srv := newURLSpecServer(t)
	srv.status.Store(http.StatusInternalServerError)
	svc, _, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)}))
	srv.status.Store(http.StatusOK)

	for name, hil := range map[string]struct {
		value   any
		present bool
	}{"hil omitted": {nil, false}, "hil null": {nil, true}, "hil echoed": {gateOn(urlSpecGateTypo), true}} {
		t.Run(name, func(t *testing.T) {
			values := withHIL(srv.values(map[string]any{"base_url": "https://api2.example.com"}), hil.value, hil.present)

			if _, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{Values: values}, "user-1", ConnectorUpdateOptions{}); err != nil {
				t.Fatalf("an update that left the stored gate alone was refused: %v", err)
			}
			if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{urlSpecGateTypo}) {
				t.Errorf("stored gate keys = %v, want the stored gate kept", got)
			}
		})
	}
}

func TestConnectorService_Update_APIGateWithUnreadableSpecURLSavesAndWarns(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, _, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(nil))
	srv.status.Store(http.StatusNotFound)

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{Values: srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)})}, "user-1", ConnectorUpdateOptions{})

	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	assertSomeWarningContains(t, resp.Warnings, `"delete_pett"`, uncheckedAPIHILReason)
}

func TestConnectorService_ValidateCreate_APIGateAgainstSpecURL(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, _, _ := urlSpecService(t)
	create := func(hil map[string]any) CreateConnectorRequest {
		return CreateConnectorRequest{Name: "petstore", Type: "api", Subtype: "openapi", Values: srv.values(map[string]any{"hil": hil})}
	}

	_, err := svc.ValidateCreate(t.Context(), create(gateOn(urlSpecGateTypo)))
	wantUnknownOperationRefusal(t, err)

	warnings, err := svc.ValidateCreate(t.Context(), create(gateOn(deletePetTool)))
	if err != nil || len(warnings) != 0 {
		t.Errorf("ValidateCreate of a gate the spec confirms = %q, %v; want no warnings", warnings, err)
	}

	srv.status.Store(http.StatusInternalServerError)
	warnings, err = svc.ValidateCreate(t.Context(), create(gateOn(urlSpecGateTypo)))
	if err != nil {
		t.Fatalf("ValidateCreate with an unreadable spec must not refuse: %v", err)
	}
	assertSomeWarningContains(t, warnings, `"delete_pett"`, uncheckedAPIHILReason)
}

func assertSomeWarningContains(t *testing.T, warnings []string, fragments ...string) {
	t.Helper()
	if !slices.ContainsFunc(warnings, func(w string) bool {
		return !slices.ContainsFunc(fragments, func(f string) bool { return !strings.Contains(w, f) })
	}) {
		t.Errorf("warnings = %q, want one containing every one of %q", warnings, fragments)
	}
}

func TestConnectorService_Create_APIGateWithABodyThatIsNotAnOpenAPIDocumentSavesAndWarns(t *testing.T) {
	for name, body := range map[string]string{
		"valid JSON that is not a spec":       `{"status":"ok"}`,
		"a spec version but no operations":    `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{}}`,
		"a document that does not parse":      "<html>not a spec</html>",
		"an operation list without a version": `{"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newURLSpecServer(t)
			srv.serve(body)
			svc, _, _ := urlSpecService(t)

			created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(deletePetTool)}))

			assertOneWarningContaining(t, created.Warnings, `"delete_pet"`, uncheckedAPIHILReason)
			if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{deletePetTool}) {
				t.Errorf("stored gate keys = %v, want the gate kept", got)
			}
		})
	}
}

func TestConnectorService_Create_APIGateWithAnOversizedSpecIsNotReportedUnreachable(t *testing.T) {
	srv := newURLSpecServer(t)
	srv.serve(`{"openapi":"3.0.0","info":{"title":"` + strings.Repeat("x", maxOpenAPIProbeSpecSize) + `","version":"1"},"paths":{}}`)
	svc, _, _ := urlSpecService(t)

	created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(deletePetTool)}))

	assertOneWarningContaining(t, created.Warnings, `"delete_pet"`, uncheckedAPIHILReason)
	if strings.Contains(strings.Join(created.Warnings, "\n"), "not reachable") {
		t.Errorf("warnings = %q, an oversized spec is not an unreachable one", created.Warnings)
	}
}

func TestConnectorService_Update_APIStoredGateTheReadableURLSpecLacksWarnsAndIsKept(t *testing.T) {
	srv := newURLSpecServer(t)
	srv.status.Store(http.StatusInternalServerError)
	svc, _, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)}))
	srv.status.Store(http.StatusOK)
	req := UpdateConnectorRequest{Values: srv.values(map[string]any{"base_url": "https://api2.example.com"})}

	preflight, err := svc.PreflightUpdate(t.Context(), created.ID, req)
	if err != nil {
		t.Fatalf("PreflightUpdate refused an update that left the stored gate alone: %v", err)
	}
	assertOneWarningContaining(t, preflight, `"delete_pett"`, "fail to start until the key is removed or corrected")
	if !IsUncheckedGatesWarning(preflight[0]) {
		t.Errorf("warning %q is not recognised as an unchecked-gate warning, so a save that uploads the spec would keep it", preflight[0])
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, req, "user-1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	assertOneWarningContaining(t, resp.Warnings, `"delete_pett"`)
	if got := storedGateKeys(t, svc, created.ID); !slices.Equal(got, []string{urlSpecGateTypo}) {
		t.Errorf("stored gate keys = %v, want the stale key kept, not pruned", got)
	}
}

func TestConnectorService_UploadResource_IdenticalReuploadThatPrunesAGateQueuesTheRedeploy(t *testing.T) {
	t.Parallel()
	svc, db, outbox := urlSpecService(t)
	created := createURLSpecConnector(t, svc, petValues(nil))
	agentID := bindAgentToConnector(t, db, created.ID)
	upload := func() *ConnectorResponse {
		t.Helper()
		resp, err := svc.UploadResource(t.Context(), created.ID, "spec.json", strings.NewReader(petSpecListOnly))
		if err != nil {
			t.Fatalf("UploadResource: %v", err)
		}
		return resp
	}
	upload()
	before := len(outboxPayloads(t, outbox, "connector.updated"))
	values := storedConnectorValues(t, svc, created.ID)
	values["hil"] = gateOn(deletePetTool)
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.NewConnectorRepo(db).UpdateValues(t.Context(), created.ID, string(encoded)); err != nil {
		t.Fatalf("seed a gate the spec lacks: %v", err)
	}

	resp := upload()

	if !slices.Equal(resp.Warnings, []string{removedGateWarning(deletePetTool)}) {
		t.Errorf("warnings = %v, want the removed-gate warning", resp.Warnings)
	}
	got := outboxPayloads(t, outbox, "connector.updated")
	if len(got) != before+1 || !slices.Equal(got[before].AgentIDs, []string{agentID}) {
		t.Errorf("an identical re-upload that pruned a gate queued %+v after %d earlier events, want one more covering the bound agent", got, before)
	}
}

func TestConnectorService_CreateWithSpecRead_ReusesTheValidationFetch(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, _, _ := urlSpecService(t)
	req := CreateConnectorRequest{
		Name: "petstore", Description: "OpenAPI connector read from a specification URL", Type: "api", Subtype: "openapi",
		Values: srv.values(map[string]any{"hil": gateOn(deletePetTool)}),
	}

	_, read, err := svc.ValidateCreateReading(t.Context(), req)
	if err != nil || read == nil || !read.Spec.Read {
		t.Fatalf("ValidateCreateReading = %+v, %v; want the spec it read", read, err)
	}
	if _, err := svc.CreateWithSpecRead(t.Context(), req, "user-1", read); err != nil {
		t.Fatalf("CreateWithSpecRead: %v", err)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Errorf("the platform fetched the spec %d times for a validation and a create, want 1", got)
	}
}

func TestConnectorService_UpdateConnectorWithRedeploy_DecidesOnTheSpecItWasGiven(t *testing.T) {
	srv := newURLSpecServer(t)
	svc, db, _ := urlSpecService(t)
	created := createURLSpecConnector(t, svc, srv.values(nil))
	req := UpdateConnectorRequest{Values: srv.values(map[string]any{"hil": gateOn(urlSpecGateTypo)})}
	srv.hits.Store(0)

	known := URLSpec{Read: true, Tools: []string{"delete_pet", "list_pets"}}
	_, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: created.ID, Request: req, UserID: "user-1", Read: &UpdateRead{Spec: known}}, discardLogger())

	wantUnknownOperationRefusal(t, err)
	if got := srv.hits.Load(); got != 0 {
		t.Errorf("the update fetched the spec %d times although it was given one", got)
	}
}
