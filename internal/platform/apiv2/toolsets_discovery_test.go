// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (a *toolsetAPI) toolsetsSetDiscoveryStatus(id, status string) {
	a.t.Helper()
	if err := store.NewToolPackageRepo(a.deps.DB).UpdateDiscoveryStatus(a.t.Context(), id, status, nil); err != nil {
		a.t.Fatalf("set discoveryStatus %s: %v", status, err)
	}
}

func toolsetsIDs(list []Toolset) []string {
	ids := make([]string, len(list))
	for i, ts := range list {
		ids[i] = ts.ID
	}
	return ids
}

func TestToolsets_DiscoveryStatusFilterListsOnlyMatchingCustomToolsets(t *testing.T) {
	api := newToolsetAPI(t)
	ready := api.create("ready_tools")
	failed := api.create("failed_tools")
	api.create("pending_tools")
	api.toolsetsSetDiscoveryStatus(ready.ID, "ready")
	api.toolsetsSetDiscoveryStatus(failed.ID, "failed")

	for status, want := range map[string]string{"ready": ready.ID, "failed": failed.ID} {
		got := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?discoveryStatus="+status, nil)).Data
		if len(got) != 1 || got[0].ID != want || got[0].DiscoveryStatus == nil || *got[0].DiscoveryStatus != status {
			t.Errorf("discoveryStatus=%s listed %v, want only %s", status, toolsetsIDs(got), want)
		}
	}
	if customs := decodeList[Toolset](t, api.call(http.MethodGet, "/toolsets?origin=custom", nil)).Data; len(customs) != 3 {
		t.Errorf("origin=custom listed %d, want all 3", len(customs))
	}
}

func TestToolsets_CapabilitiesAreAlwaysAnObject(t *testing.T) {
	api := newToolsetAPI(t)
	plain := api.create("plain_tools")
	declaring := api.create("declaring_tools")
	expectStatus(t, api.upload(http.MethodPut, "/toolsets/"+declaring.ID+"/bundle", "declaring_tools.zip", zipOf(t, map[string]string{"tool.py": "print(1)"})), http.StatusOK)
	params, caps := `{}`, `{"profileProvider":true,"foo":"bar"}`
	api.deps.ToolsetSvc.HandleToolInit(t.Context(), "declaring_tools__do", "desc", entplatform.ToolInitMeta{Params: &params, Capabilities: &caps})

	listed := map[string]string{}
	for _, item := range decodeList[map[string]json.RawMessage](t, api.call(http.MethodGet, "/toolsets?pageSize=100", nil)).Data {
		var id string
		_ = json.Unmarshal(item["id"], &id)
		raw, ok := item["capabilities"]
		if !ok || len(raw) == 0 || raw[0] != '{' {
			t.Errorf("toolset %s capabilities = %s, want an object", id, raw)
		}
		listed[id] = string(raw)
	}
	if listed[plain.ID] != "{}" {
		t.Errorf("undeclared capabilities = %s, want {}", listed[plain.ID])
	}

	got := decodeData[Toolset](t, api.call(http.MethodGet, "/toolsets/"+declaring.ID, nil)).Capabilities
	if got["profileProvider"] != true || got["foo"] != "bar" {
		t.Errorf("declared capabilities = %v, want profileProvider true and foo bar", got)
	}
	if listed[declaring.ID] == "{}" {
		t.Error("the list dropped the declared capabilities")
	}
}

func TestToolsets_ConfigOfAMissingOrMalformedID(t *testing.T) {
	api := newToolsetAPI(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		body := map[string]any{"values": map[string]any{}}
		expectStatus(t, api.call(method, "/toolsets/"+missingID+"/config", body), http.StatusNotFound)
		expectDetail(t, api.call(method, "/toolsets/Not-A-UUID/config", body), http.StatusBadRequest, "id")
	}
}

func TestToolsets_ConfigPatchRejectsAnUnknownSetting(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.createReady("order_tools")
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID+"/config", map[string]any{"values": map[string]any{"lookup": map[string]any{"not_a_field": "oops"}}}),
		http.StatusUnprocessableEntity, "lookup.not_a_field")
}

func TestToolsets_ConfigPatchRejectsAnUnknownEnvelopeField(t *testing.T) {
	api := newToolsetAPI(t)
	ts := api.createReady("order_tools")
	expectDetail(t, api.call(http.MethodPatch, "/toolsets/"+ts.ID+"/config", map[string]any{
		"values":     map[string]any{"lookup": map[string]any{"api_key": "sk-1"}},
		"extraField": "bad",
	}), http.StatusBadRequest, "extraField")
}
