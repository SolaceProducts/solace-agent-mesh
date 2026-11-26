// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newConnectorUpdateFixture(t *testing.T) (*ConnectorService, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	svc.SetRedeployDeps(db, store.NewAgentRepo(db), store.NewWorkflowRepo(db), store.NewOutboxRepo(db))
	return svc, db
}

func seedGatedEventMeshConnector(t *testing.T, svc *ConnectorService) string {
	t.Helper()
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders", Description: "Event mesh orders", Type: "event_mesh", Subtype: "solace",
		Values: eventMeshOpValues(placeOrderTool, gateOn(placeOrderTool), true),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed event mesh connector: %v", err)
	}
	return created.ID
}

func seedSpeclessOpenAPIConnector(t *testing.T, svc *ConnectorService) string {
	t.Helper()
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "petstore", Description: "OpenAPI connector without a specification", Type: "api", Subtype: "openapi",
		Values: petValues(nil),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed OpenAPI connector: %v", err)
	}
	return created.ID
}

func cloneUpdateRequest(t *testing.T, req UpdateConnectorRequest) UpdateConnectorRequest {
	t.Helper()
	encoded, err := json.Marshal(req.Values)
	if err != nil {
		t.Fatalf("marshal request values: %v", err)
	}
	cloned := req
	cloned.Values = nil
	if err := json.Unmarshal(encoded, &cloned.Values); err != nil {
		t.Fatalf("unmarshal request values: %v", err)
	}
	return cloned
}

const (
	gatedEventMeshKind = "event mesh connector with a gated operation"
	speclessAPIKind    = "OpenAPI connector without a specification"
)

func seedConnectorOfKind(t *testing.T, svc *ConnectorService, kind string) string {
	t.Helper()
	switch kind {
	case gatedEventMeshKind:
		return seedGatedEventMeshConnector(t, svc)
	case speclessAPIKind:
		return seedSpeclessOpenAPIConnector(t, svc)
	}
	t.Fatalf("unknown connector kind %q", kind)
	return ""
}

func TestConnectorUpdate_TransactionalPathsReturnTheGateWarningsOfTheUpdate(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		req     UpdateConnectorRequest
		warning string
	}{
		{
			name:    "a renamed operation prunes its inherited gate",
			kind:    gatedEventMeshKind,
			req:     UpdateConnectorRequest{Values: eventMeshOpValues("submit_order", nil, false)},
			warning: removedGateWarning(placeOrderTool),
		},
		{
			name:    "a gate on an operation that cannot be checked",
			kind:    speclessAPIKind,
			req:     gateTypoUpdate(),
			warning: noSpecGateWarning,
		},
	}
	updates := map[string]func(t *testing.T, svc *ConnectorService, db *store.DB, id string, req UpdateConnectorRequest) (*ConnectorResponse, error){
		"UpdateInTx": func(t *testing.T, svc *ConnectorService, db *store.DB, id string, req UpdateConnectorRequest) (*ConnectorResponse, error) {
			var resp *ConnectorResponse
			err := db.WithTx(t.Context(), func(tx *dbutil.Tx) error {
				var inErr error
				resp, inErr = svc.UpdateInTx(t.Context(), tx, id, req, "user-1", ConnectorUpdateOptions{})
				return inErr
			})
			return resp, err
		},
		"UpdateConnectorWithRedeploy": func(t *testing.T, svc *ConnectorService, db *store.DB, id string, req UpdateConnectorRequest) (*ConnectorResponse, error) {
			return UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: id, Request: req, UserID: "user-1"}, discardLogger())
		},
	}

	for _, tc := range cases {
		for path, update := range updates {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				svc, db := newConnectorUpdateFixture(t)
				id := seedConnectorOfKind(t, svc, tc.kind)

				resp, err := update(t, svc, db, id, cloneUpdateRequest(t, tc.req))
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				if diff := cmp.Diff([]string{tc.warning}, resp.Warnings); diff != "" {
					t.Errorf("%s warnings mismatch (-want +got):\n%s", path, diff)
				}
			})
		}
	}
}

func TestConnectorUpdate_RestoresTheSpecDisplayPathInTheStoredValues(t *testing.T) {
	t.Parallel()
	svc, _ := newConnectorUpdateFixture(t)
	id := seedSpeclessOpenAPIConnector(t, svc)
	if _, err := svc.UploadResource(t.Context(), id, "spec.json", strings.NewReader(petSpecWithDelete)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}

	for name, specURL := range map[string]any{
		"the field omitted": nil,
		"the field emptied": "",
	} {
		t.Run(name, func(t *testing.T) {
			values := petValues(map[string]any{"allow_list": "listPets"})
			if specURL != nil {
				values["specification_url"] = specURL
			}
			if _, err := svc.Update(t.Context(), id, UpdateConnectorRequest{Values: values}, "user-1"); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if got := storedConnectorValues(t, svc, id)["specification_url"]; got != SpecResourcePath(id) {
				t.Errorf("stored specification_url = %v, want the managed spec path %q", got, SpecResourcePath(id))
			}
		})
	}
}

func TestDecodeStoredValues(t *testing.T) {
	t.Parallel()
	raw := func(s string) *string { return &s }
	cases := []struct {
		name   string
		stored *string
		want   map[string]any
		wantOK bool
	}{
		{"no stored values", nil, nil, false},
		{"stored values", raw(`{"host":"db.example.com","port":5432}`), map[string]any{"host": "db.example.com", "port": 5432.0}, true},
		{"stored null", raw(`null`), nil, true},
		{"stored values that do not decode", raw(`{"host":`), nil, false},
		{"stored values of another shape", raw(`["host"]`), nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := decodeStoredValues(&store.Connector{Values: tc.stored})

			if ok != tc.wantOK {
				t.Errorf("decodeStoredValues ok = %v, want %v", ok, tc.wantOK)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("decodeStoredValues values mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
