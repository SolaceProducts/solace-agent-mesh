// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestConnectorSchemas_Paging(t *testing.T) {
	app := newConnectorAPI(t)

	all := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
	if len(all.Data) < 3 || all.Meta.Pagination.Count != len(all.Data) {
		t.Fatalf("schemas = %d items, pagination %+v", len(all.Data), all.Meta.Pagination)
	}

	second := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=2&pageNumber=2", nil))
	if len(second.Data) != 2 || second.Data[0].ID != all.Data[2].ID || second.Meta.Pagination.Count != len(all.Data) {
		t.Errorf("page 2 of size 2 = %v, pagination %+v", schemaIDs(second.Data), second.Meta.Pagination)
	}

	past := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=2&pageNumber=500", nil))
	if len(past.Data) != 0 {
		t.Errorf("page 500 = %d items, want none", len(past.Data))
	}
}

func TestConnectorSchemas_EveryListedIDIsFetchable(t *testing.T) {
	app := newConnectorAPI(t)

	all := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
	for _, listed := range all.Data {
		if listed.Type != "connectorSchema" || listed.ID == "" {
			t.Errorf("schema %+v lacks its id or type", listed)
		}
		rec := app.call(http.MethodGet, "/connectorSchemas/"+listed.ID, nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeData[ConnectorSchema](t, rec); !reflect.DeepEqual(got, listed) {
			t.Errorf("GET %s = %+v, want the listed %+v", listed.ID, got, listed)
		}
	}
}

func TestConnectorSchemas_BadKeyIs400AndUnknownIs404(t *testing.T) {
	app := newConnectorAPI(t)

	for _, badKey := range []string{"NotSnakeCase", "event-mesh", "Api"} {
		expectDetail(t, app.call(http.MethodGet, "/connectorSchemas/"+badKey, nil), http.StatusBadRequest, "id")
	}
	expectStatus(t, app.call(http.MethodGet, "/connectorSchemas/no_such_type", nil), http.StatusNotFound)
}

func TestConnectorSchemas_PathsExist(t *testing.T) {
	app := newConnectorAPI(t)
	for _, path := range []string{"/connectorSchemas", "/connectorSchemas/api"} {
		expectStatus(t, app.call(http.MethodGet, path, nil), http.StatusOK)
	}
}

func TestConnectorSchemas_GatedTypeIsLeftOutAndNotFetchable(t *testing.T) {
	app := newConnectorAPI(t)

	all := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
	if slices.Contains(schemaIDs(all.Data), gatedConnectorType) {
		t.Errorf("%s is switched off on this deployment but is listed", gatedConnectorType)
	}
	expectStatus(t, app.call(http.MethodGet, "/connectorSchemas/"+gatedConnectorType, nil), http.StatusNotFound)
}

func TestConnectorSchemas_EveryFieldIsAlwaysSent(t *testing.T) {
	app := newConnectorAPI(t)

	raw := string(app.callRaw(http.MethodGet, "/connectorSchemas/api", nil))
	for _, present := range []string{`"usageGuidance"`, `"connectionTemplate"`, `"warning"`, `"experimental"`,
		`"placeholder"`, `"hint"`, `"default"`, `"options"`, `"validation"`, `"conditionalDisplay"`} {
		if !strings.Contains(raw, present) {
			t.Errorf("%s must always be sent, never omitted: %s", present, raw)
		}
	}
}

func TestConnectorSchemas_NeedConnectorRead(t *testing.T) {
	app := newConnectorAPI(t)

	expectStatus(t, app.as(rbac.ScopeEntrypointRead).call(http.MethodGet, "/connectorSchemas", nil), http.StatusForbidden)
	expectStatus(t, app.as(rbac.ScopeEntrypointRead).call(http.MethodGet, "/connectorSchemas/api", nil), http.StatusForbidden)
	expectStatus(t, app.as(rbac.ScopeConnectorRead).call(http.MethodGet, "/connectorSchemas/api", nil), http.StatusOK)
}

func schemaIDs(schemas []ConnectorSchema) []string {
	ids := make([]string, len(schemas))
	for i, s := range schemas {
		ids[i] = s.ID
	}
	return ids
}
