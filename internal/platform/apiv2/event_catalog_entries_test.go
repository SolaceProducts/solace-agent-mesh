// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eventcatalog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
)

type eventCatalogAPI struct {
	*apiClient
	deps *entplatform.RouteDeps
	mesh string
}

func newEventCatalogAPI(t *testing.T, enabled bool) *eventCatalogAPI {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyEventCatalog), strconv.FormatBool(enabled))
	core, deps := newTestCore(t)
	api := &eventCatalogAPI{apiClient: newAPIClient(t, core), deps: deps}
	api.mesh = api.addEntrypoint("orders-mesh", "event_mesh")
	return api
}

func (a *eventCatalogAPI) addEntrypoint(name, entrypointType string) string {
	a.t.Helper()
	values := map[string]any{
		"broker_url": "tcps://broker.example.com:55443", "broker_vpn": "orders",
		"broker_username": "sam", "broker_password": "placeholder-password",
		"event_rules": []any{map[string]any{
			"name": "placed", "subscriptions": []any{map[string]any{"topic": "acme/orders/placed"}},
			"promptTemplate": "New order {payload}", "targetAgent": "OrderAgent",
		}},
	}
	if entrypointType != "event_mesh" {
		gw := &store.Gateway{Name: name, Type: entrypointType, CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
		if err := store.NewGatewayRepo(a.core.DB()).Create(a.t.Context(), gw); err != nil {
			a.t.Fatalf("create %s entrypoint: %v", entrypointType, err)
		}
		return gw.ID
	}
	created, err := a.deps.GatewaySvc.Create(a.t.Context(), entplatform.CreateGatewayRequest{
		Name: name, Description: "Entrypoint an event catalog test needs.", Type: entrypointType, Values: values,
	}, "alice@example.com")
	if err != nil {
		a.t.Fatalf("create %s entrypoint: %v", entrypointType, err)
	}
	return created.ID
}

func (a *eventCatalogAPI) entryBody(name, key string) map[string]any {
	return map[string]any{
		"name": name, "key": key, "description": "A carrier reports that a shipment will arrive late.",
		"entrypointId": a.mesh, "topicPattern": "acme/logistics/shipment/*/delayed",
		"payloadSchema": map[string]any{
			"type": "object", "required": []any{"shipmentId"},
			"properties": map[string]any{"shipmentId": map[string]any{"type": "string"}, "delayMinutes": map[string]any{"type": "integer"}},
		},
		"examplePayload": map[string]any{"shipmentId": "SHP-1001", "delayMinutes": 90},
	}
}

func (a *eventCatalogAPI) create(name, key string) EventCatalogEntry {
	a.t.Helper()
	rec := a.call(http.MethodPost, eventCatalogEntriesPath, a.entryBody(name, key))
	expectStatus(a.t, rec, http.StatusCreated)
	return decodeData[EventCatalogEntry](a.t, rec)
}

func entryPath(id string) string { return eventCatalogEntriesPath + "/" + id }

var curatorScopes = []string{rbac.ScopeEventCatalogRead, rbac.ScopeEventCatalogUpdate}

func TestEventCatalog_FlagOffServesNothing(t *testing.T) {
	api := newEventCatalogAPI(t, false)
	expectStatus(t, api.call(http.MethodGet, eventCatalogEntriesPath, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPost, eventCatalogEntriesPath, api.entryBody("Shipment delayed", "shipment_delayed")), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, entryPath(idgen.New()), nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPatch, entryPath(idgen.New()), map[string]any{"enabled": false}), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, entryPath(idgen.New()), nil), http.StatusNotFound)
}

func TestEventCatalog_Lifecycle(t *testing.T) {
	api := newEventCatalogAPI(t, true)

	rec := api.call(http.MethodPost, eventCatalogEntriesPath, api.entryBody("Shipment delayed", "shipment_delayed"))
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[EventCatalogEntry](t, rec)
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, entryPath(created.ID)) {
		t.Errorf("Location = %q, want the new entry", loc)
	}
	if created.Type != "eventCatalogEntry" || created.Key != "shipment_delayed" || !created.Enabled {
		t.Errorf("created = %+v", created)
	}
	if created.TopicPattern == nil || *created.TopicPattern != "acme/logistics/shipment/*/delayed" || created.EntrypointID == nil || *created.EntrypointID != api.mesh {
		t.Errorf("a curator sees topicPattern %v and entrypointId %v, want both", created.TopicPattern, created.EntrypointID)
	}

	got := decodeData[EventCatalogEntry](t, api.call(http.MethodGet, entryPath(created.ID), nil))
	if got.Name != "Shipment delayed" || got.ExamplePayload["shipmentId"] != "SHP-1001" {
		t.Errorf("got = %+v", got)
	}

	rec = api.call(http.MethodPatch, entryPath(created.ID), map[string]any{
		"name": "Shipment running late", "enabled": false, "payloadSchema": nil, "examplePayload": nil,
	})
	expectStatus(t, rec, http.StatusOK)
	updated := decodeData[EventCatalogEntry](t, rec)
	if updated.Name != "Shipment running late" || updated.Enabled || updated.PayloadSchema != nil || updated.ExamplePayload != nil {
		t.Errorf("updated = %+v, want renamed, disabled and both documents cleared", updated)
	}
	if updated.Key != created.Key || *updated.TopicPattern != *created.TopicPattern {
		t.Errorf("updated = %+v, want the fields left out kept", updated)
	}

	expectStatus(t, api.call(http.MethodDelete, entryPath(created.ID), nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, entryPath(created.ID), nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, entryPath(created.ID), nil), http.StatusNotFound)
}

func TestEventCatalog_EveryVerbNeedsItsScope(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	entry := api.create("Shipment delayed", "shipment_delayed")
	path := entryPath(entry.ID)
	all := []string{rbac.ScopeEventCatalogRead, rbac.ScopeEventCatalogCreate, rbac.ScopeEventCatalogUpdate, rbac.ScopeEventCatalogDelete}

	for _, tc := range []struct {
		name, method, path string
		body               any
		scope              string
		allowed            int
	}{
		{"list", http.MethodGet, eventCatalogEntriesPath, nil, rbac.ScopeEventCatalogRead, http.StatusOK},
		{"get", http.MethodGet, path, nil, rbac.ScopeEventCatalogRead, http.StatusOK},
		{"create", http.MethodPost, eventCatalogEntriesPath, api.entryBody("Order placed", "order_placed"), rbac.ScopeEventCatalogCreate, http.StatusCreated},
		{"update", http.MethodPatch, path, map[string]any{"description": "A shipment will arrive late."}, rbac.ScopeEventCatalogUpdate, http.StatusOK},
		{"delete", http.MethodDelete, path, nil, rbac.ScopeEventCatalogDelete, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			others := slices.DeleteFunc(slices.Clone(all), func(s string) bool { return s == tc.scope })
			expectStatus(t, api.call(tc.method, tc.path, tc.body, others...), http.StatusForbidden)
			expectStatus(t, api.call(tc.method, tc.path, tc.body, tc.scope), tc.allowed)
		})
	}
}

func TestEventCatalog_AReaderSeesNoTopologyAndNoDisabledEntries(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	shown := api.create("Shipment delayed", "shipment_delayed")
	hidden := api.create("Invoice overdue", "invoice_overdue")
	expectStatus(t, api.call(http.MethodPatch, entryPath(hidden.ID), map[string]any{"enabled": false}), http.StatusOK)
	reader := api.as(rbac.ScopeEventCatalogRead)

	raw := reader.callRaw(http.MethodGet, entryPath(shown.ID), nil)
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"topicPattern", "entrypointId"} {
		if got, ok := body.Data[field]; !ok || string(got) != "null" {
			t.Errorf("a reader's %s = %s (present %v), want null", field, got, ok)
		}
	}
	if strings.Contains(string(raw), "acme/logistics") || strings.Contains(string(raw), api.mesh) {
		t.Errorf("the topology reached a reader: %s", raw)
	}
	if string(body.Data["payloadSchema"]) == "null" || string(body.Data["name"]) != `"Shipment delayed"` {
		t.Errorf("a reader must still see the name and schema: %s", raw)
	}

	expectStatus(t, reader.call(http.MethodGet, entryPath(hidden.ID), nil), http.StatusNotFound)
	page := decodeList[EventCatalogEntry](t, reader.call(http.MethodGet, eventCatalogEntriesPath, nil))
	if len(page.Data) != 1 || page.Data[0].ID != shown.ID || page.Meta.Pagination.Count != 1 {
		t.Errorf("a reader's list = %+v, want only the enabled entry", page)
	}
	for _, e := range page.Data {
		if e.TopicPattern != nil || e.EntrypointID != nil {
			t.Errorf("a reader's list shows the topology of %s", e.Key)
		}
	}
	if got := decodeList[EventCatalogEntry](t, reader.call(http.MethodGet, eventCatalogEntriesPath+"?enabled=false", nil)); len(got.Data) != 0 {
		t.Errorf("a reader's disabled list = %+v, want empty", got.Data)
	}
	expectStatus(t, reader.call(http.MethodGet, eventCatalogEntriesPath+"?entrypointId="+api.mesh, nil), http.StatusForbidden)

	curator := api.as(curatorScopes...)
	if got := decodeData[EventCatalogEntry](t, curator.call(http.MethodGet, entryPath(hidden.ID), nil)); got.TopicPattern == nil || got.Enabled {
		t.Errorf("a curator's view of the disabled entry = %+v", got)
	}
	if got := decodeList[EventCatalogEntry](t, curator.call(http.MethodGet, eventCatalogEntriesPath, nil)); len(got.Data) != 2 {
		t.Errorf("a curator lists %d entries, want both", len(got.Data))
	}
}

func TestEventCatalog_EachCuratorScopeGivesTheCuratorView(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	api.create("Shipment delayed", "shipment_delayed")
	hidden := api.create("Invoice overdue", "invoice_overdue")
	expectStatus(t, api.call(http.MethodPatch, entryPath(hidden.ID), map[string]any{"enabled": false}), http.StatusOK)

	for _, scope := range []string{rbac.ScopeEventCatalogCreate, rbac.ScopeEventCatalogUpdate, rbac.ScopeEventCatalogDelete} {
		t.Run(scope, func(t *testing.T) {
			holder := api.as(rbac.ScopeEventCatalogRead, scope)

			got := decodeData[EventCatalogEntry](t, holder.call(http.MethodGet, entryPath(hidden.ID), nil))
			if got.Enabled || got.TopicPattern == nil || got.EntrypointID == nil {
				t.Errorf("a holder of read and %s sees the disabled entry as %+v, want the curator view with its topic and entrypoint", scope, got)
			}
			page := decodeList[EventCatalogEntry](t, holder.call(http.MethodGet, eventCatalogEntriesPath, nil))
			if len(page.Data) != 2 {
				t.Errorf("a holder of read and %s lists %d entries, want both the enabled and the disabled one", scope, len(page.Data))
			}
			expectStatus(t, holder.call(http.MethodGet, eventCatalogEntriesPath+"?entrypointId="+api.mesh, nil), http.StatusOK)
		})
	}
}

func TestEventCatalog_ACreateWithEnabledFalseIsStoredDisabledAndHiddenFromReaders(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	body := api.entryBody("Shipment delayed", "shipment_delayed")
	body["enabled"] = false

	rec := api.call(http.MethodPost, eventCatalogEntriesPath, body)

	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[EventCatalogEntry](t, rec)
	if created.Enabled {
		t.Errorf("created = %+v, want it disabled", created)
	}
	if stored := decodeData[EventCatalogEntry](t, api.call(http.MethodGet, entryPath(created.ID), nil)); stored.Enabled {
		t.Errorf("stored = %+v, want it disabled", stored)
	}
	reader := api.as(rbac.ScopeEventCatalogRead)
	expectStatus(t, reader.call(http.MethodGet, entryPath(created.ID), nil), http.StatusNotFound)
	if page := decodeList[EventCatalogEntry](t, reader.call(http.MethodGet, eventCatalogEntriesPath, nil)); len(page.Data) != 0 {
		t.Errorf("a reader lists %d entries, want none while the only entry is disabled", len(page.Data))
	}
}

func TestEventCatalog_CreateReportsEveryProblem(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	webhook := api.addEntrypoint("orders-webhook", "webhook")

	rec := api.call(http.MethodPost, eventCatalogEntriesPath, map[string]any{
		"name": " ", "key": "Shipment-Delayed", "description": "",
		"entrypointId": webhook, "topicPattern": "acme/>/delayed",
		"payloadSchema": map[string]any{"type": "not-a-type"},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "key", "description", "entrypointId", "topicPattern", "payloadSchema"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, details)
		}
	}
}

func TestEventCatalog_RefusesInvalidDocumentsAndTopics(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	for name, tc := range map[string]struct {
		field  string
		change func(map[string]any)
	}{
		"bad topic pattern": {"topicPattern", func(b map[string]any) { b["topicPattern"] = "acme//delayed" }},
		"reserved topic":    {"topicPattern", func(b map[string]any) { b["topicPattern"] = "#share/group/acme/>" }},
		"invalid schema":    {"payloadSchema", func(b map[string]any) { b["payloadSchema"] = map[string]any{"type": 7} }},
		"external reference": {"payloadSchema", func(b map[string]any) {
			b["payloadSchema"] = map[string]any{"$ref": "https://schemas.example.com/shipment.json"}
		}},
		"oversized schema": {"payloadSchema", func(b map[string]any) {
			b["payloadSchema"] = map[string]any{"type": "object", "description": strings.Repeat("x", eventcatalog.MaxDocumentBytes)}
		}},
		"oversized example": {"examplePayload", func(b map[string]any) {
			b["examplePayload"] = map[string]any{"note": strings.Repeat("x", eventcatalog.MaxExampleBytes)}
		}},
		"unknown entrypoint": {"entrypointId", func(b map[string]any) { b["entrypointId"] = idgen.New() }},
		"key not snake_case": {"key", func(b map[string]any) { b["key"] = "shipment__delayed" }},
	} {
		t.Run(name, func(t *testing.T) {
			body := api.entryBody("Shipment delayed", "shipment_delayed")
			tc.change(body)
			rec := api.call(http.MethodPost, eventCatalogEntriesPath, body)
			expectDetail(t, rec, http.StatusUnprocessableEntity, tc.field)
			if details := decodeError(t, rec).ValidationDetails; len(details) != 1 {
				t.Errorf("validationDetails = %v, want only %s", details, tc.field)
			}
		})
	}
	if got := decodeList[EventCatalogEntry](t, api.call(http.MethodGet, eventCatalogEntriesPath, nil)); len(got.Data) != 0 {
		t.Errorf("refused creates stored %d entries", len(got.Data))
	}
}

func TestEventCatalog_ATakenNameOrKeyIsAConflict(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	api.create("Shipment delayed", "shipment_delayed")

	rec := api.call(http.MethodPost, eventCatalogEntriesPath, api.entryBody("SHIPMENT DELAYED", "order_placed"))
	expectDetail(t, rec, http.StatusConflict, "name")

	rec = api.call(http.MethodPost, eventCatalogEntriesPath, api.entryBody("Order placed", "shipment_delayed"))
	expectDetail(t, rec, http.StatusConflict, "key")

	body := api.entryBody("Shipment delayed", "shipment_delayed")
	body["topicPattern"] = "acme/>/delayed"
	rec = api.call(http.MethodPost, eventCatalogEntriesPath, body)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "key", "topicPattern"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s beside the other problem: %v", field, details)
		}
	}

	other := api.create("Order placed", "order_placed")
	expectDetail(t, api.call(http.MethodPatch, entryPath(other.ID), map[string]any{"name": "shipment delayed"}), http.StatusConflict, "name")
}

func TestEventCatalog_UpdateReportsEveryProblem(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	entry := api.create("Shipment delayed", "shipment_delayed")

	rec := api.call(http.MethodPatch, entryPath(entry.ID), map[string]any{
		"key": "shipment_late", "name": nil, "description": "", "topicPattern": "acme/ship*ments",
		"enabled": nil, "payloadSchema": map[string]any{"type": 7},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"key", "name", "description", "topicPattern", "enabled", "payloadSchema"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, details)
		}
	}
	if !slices.Equal(details["key"], []string{"cannot be changed"}) {
		t.Errorf("validationDetails.key = %v, want the key refused as fixed", details["key"])
	}
	if got := decodeData[EventCatalogEntry](t, api.call(http.MethodGet, entryPath(entry.ID), nil)); got.Name != entry.Name || got.Key != entry.Key {
		t.Errorf("the refused update changed the entry: %+v", got)
	}
}

func TestEventCatalog_ListIsAnEnvelopeThatPagesAndSorts(t *testing.T) {
	api := newEventCatalogAPI(t, true)

	raw := api.callRaw(http.MethodGet, eventCatalogEntriesPath, nil)
	var empty struct {
		Data json.RawMessage `json:"data"`
		Meta json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(raw, &empty); err != nil {
		t.Fatal(err)
	}
	if string(empty.Data) != "[]" || len(empty.Meta) == 0 {
		t.Errorf("empty list = %s, want data [] and a meta", raw)
	}

	other := api.addEntrypoint("billing-mesh", "event_mesh")
	for _, e := range [][2]string{{"Order placed", "order_placed"}, {"invoice overdue", "invoice_overdue"}, {"Shipment delayed", "shipment_delayed"}} {
		api.create(e[0], e[1])
	}
	moved := api.create("Payment failed", "payment_failed")
	expectStatus(t, api.call(http.MethodPatch, entryPath(moved.ID), map[string]any{"entrypointId": other}), http.StatusOK)

	keys := func(query string) ([]string, int) {
		t.Helper()
		page := decodeList[EventCatalogEntry](t, api.call(http.MethodGet, eventCatalogEntriesPath+query, nil))
		out := make([]string, len(page.Data))
		for i, e := range page.Data {
			out[i] = e.Key
		}
		return out, page.Meta.Pagination.Count
	}
	if got, total := keys(""); !slices.Equal(got, []string{"invoice_overdue", "order_placed", "payment_failed", "shipment_delayed"}) || total != 4 {
		t.Errorf("default order = %v (%d), want name order ignoring case", got, total)
	}
	if got, total := keys("?sort=key:desc&pageSize=2&pageNumber=2"); !slices.Equal(got, []string{"order_placed", "invoice_overdue"}) || total != 4 {
		t.Errorf("page 2 by key descending = %v (%d)", got, total)
	}
	if got, _ := keys("?entrypointId=" + other); !slices.Equal(got, []string{"payment_failed"}) {
		t.Errorf("entries on the billing mesh = %v", got)
	}
	expectStatus(t, api.call(http.MethodGet, eventCatalogEntriesPath+"?enabled=yes", nil), http.StatusBadRequest)
	expectStatus(t, api.call(http.MethodGet, eventCatalogEntriesPath+"?entrypointId=not-a-uuid", nil), http.StatusBadRequest)
	expectStatus(t, api.call(http.MethodGet, eventCatalogEntriesPath+"?sort=description", nil), http.StatusUnprocessableEntity)
}

func TestEventCatalog_DeleteRouteDocPromisesNoRefusalNothingCanProduceYet(t *testing.T) {
	api := newEventCatalogAPI(t, true)
	var found bool
	for _, route := range eventCatalogRoutes(api.deps) {
		if route.Doc.ID != "deleteEventCatalogEntry" {
			continue
		}
		found = true
		if strings.Contains(route.Doc.Text, "409") || strings.Contains(route.Doc.Text, "in use") {
			t.Errorf("the delete route promises a refusal no input can produce yet: %q", route.Doc.Text)
		}
		if slices.Contains(route.Doc.Errors, http.StatusConflict) {
			t.Errorf("the delete route lists 409 among its errors, which no input can produce yet")
		}
	}
	if !found {
		t.Fatal("the delete route is not registered")
	}
}
