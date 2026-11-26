// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

type patchCreate struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Endpoint    string         `json:"endpoint"`
	Description string         `json:"description"`
	Title       string         `json:"title"`
	Limit       *int           `json:"limit"`
	Auth        map[string]any `json:"auth" cfgapply:"secret"`
	Params      map[string]any `json:"params"`
	Tools       map[string]any `json:"tools"`
	Runs        *int           `json:"runs"`
	Config      map[string]any `json:"config" cfgapply:"secret"`
}

type patchResponse struct {
	ID          string         `json:"id" cfgapply:"read-only"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Endpoint    *string        `json:"endpoint"`
	Description *string        `json:"description"`
	Title       *string        `json:"title"`
	Limit       *int           `json:"limit"`
	Auth        map[string]any `json:"auth" cfgapply:"secret"`
	Params      map[string]any `json:"params"`
	Tools       map[string]any `json:"tools"`
	Runs        int            `json:"runs"`
	Config      map[string]any `json:"config" cfgapply:"secret"`
}

type patchUpdate struct {
	Name        wire.Patch[string]         `json:"name,omitzero"`
	Kind        wire.Patch[string]         `json:"kind,omitzero" cfgapply:"read-only"`
	Endpoint    wire.Patch[string]         `json:"endpoint,omitzero" cfgapply:"clear-empty"`
	Description wire.Patch[string]         `json:"description,omitzero" clearable:"true"`
	Title       wire.Patch[string]         `json:"title,omitzero"`
	Limit       wire.Patch[int]            `json:"limit,omitzero" clearable:"true"`
	Auth        wire.Patch[map[string]any] `json:"auth,omitzero" cfgapply:"secret,merge,switch=type"`
	Params      wire.Patch[map[string]any] `json:"params,omitzero" cfgapply:"clear-empty"`
	Tools       wire.Patch[map[string]any] `json:"tools,omitzero" cfgapply:"merge=2"`
	Runs        wire.Patch[int]            `json:"runs,omitzero"`
	Config      wire.Patch[map[string]any] `json:"config,omitzero" cfgapply:"secret,merge=2"`
}

func ptr[T any](v T) *T { return &v }

func stored() patchResponse {
	return patchResponse{
		ID:          "id-1",
		Name:        "orders",
		Kind:        "chat",
		Endpoint:    ptr("https://custom.example.com"),
		Description: ptr("Order lookups"),
		Title:       ptr("Orders"),
		Limit:       ptr(1000),
		Auth:        map[string]any{"type": "oauth2", "client_id": "app", "client_secret": generic.RedactedSentinel},
		Params:      map[string]any{"temperature": 0.2, "top_p": 0.9},
		Tools:       map[string]any{"lookup": map[string]any{"region": "eu", "retries": 3}},
		Runs:        4,
		Config:      map[string]any{"lookup": map[string]any{"region": "eu", "token": generic.RedactedSentinel}},
	}
}

func desiredFromStored() patchCreate {
	return patchCreate{
		Name:        "orders",
		Kind:        "chat",
		Endpoint:    "https://custom.example.com",
		Description: "Order lookups",
		Title:       "Orders",
		Limit:       ptr(1000),
		Auth:        map[string]any{"type": "oauth2", "client_id": "app", "client_secret": "s3cret"},
		Params:      map[string]any{"temperature": 0.2, "top_p": 0.9},
		Tools:       map[string]any{"lookup": map[string]any{"region": "eu", "retries": 3}},
		Config:      map[string]any{"lookup": map[string]any{"region": "eu", "token": "t0ken"}},
	}
}

const ridingSecrets = `"auth":{"client_secret":"s3cret"},"config":{"lookup":{"token":"t0ken"}}`

func patchBody(t *testing.T, desired patchCreate, actual patchResponse) string {
	t.Helper()
	got, err := generic.ToPatch(desired, actual, patchUpdate{})
	if err != nil {
		t.Fatalf("ToPatch: %v", err)
	}
	if _, ok := got.(patchUpdate); !ok {
		t.Fatalf("ToPatch returned %T, want patchUpdate", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func assertJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %s", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("PATCH body\n got: %s\nwant: %s", got, want)
	}
}

func TestToPatch_SendsOnlyWhatChanged(t *testing.T) {
	desired := desiredFromStored()
	desired.Title = "Order desk"
	desired.Kind = "decision"

	assertJSON(t, patchBody(t, desired, stored()),
		`{"title":"Order desk",`+ridingSecrets+`}`)
}

func TestToPatch_ClearsRemovedFieldsTheWayTheServerAccepts(t *testing.T) {
	desired := desiredFromStored()
	desired.Description = ""
	desired.Limit = nil
	desired.Endpoint = ""

	assertJSON(t, patchBody(t, desired, stored()),
		`{"description":null,"limit":null,"endpoint":"",`+ridingSecrets+`}`)
}

func TestToPatch_RefusesToDropAFieldTheServerCannotClear(t *testing.T) {
	desired := desiredFromStored()
	desired.Title = ""

	_, err := generic.ToPatch(desired, stored(), patchUpdate{})
	var notClearable *generic.NotClearableError
	if !errors.As(err, &notClearable) || notClearable.Field != "title" {
		t.Fatalf("err = %v, want a NotClearableError for title", err)
	}
}

func TestToPatch_MergedObjectSendsChangedMembersAndNullsRemovedOnes(t *testing.T) {
	desired := desiredFromStored()
	desired.Auth = map[string]any{"type": "apikey", "api_key": "k-1"}

	assertJSON(t, patchBody(t, desired, stored()),
		`{"auth":{"type":"apikey","api_key":"k-1","client_id":null},"config":{"lookup":{"token":"t0ken"}}}`)
}

func TestToPatch_MergeDepthReachesNestedMembers(t *testing.T) {
	desired := desiredFromStored()
	desired.Tools = map[string]any{
		"lookup": map[string]any{"region": "us"},
		"cancel": map[string]any{"reason": "dup"},
	}

	assertJSON(t, patchBody(t, desired, stored()),
		`{"tools":{"lookup":{"region":"us","retries":null},"cancel":{"reason":"dup"}},`+ridingSecrets+`}`)
}

func TestToPatch_ReplacedObjectIsSentWhole(t *testing.T) {
	desired := desiredFromStored()
	desired.Params = map[string]any{"temperature": 0.5}

	assertJSON(t, patchBody(t, desired, stored()),
		`{"params":{"temperature":0.5},`+ridingSecrets+`}`)
}

func TestToPatch_NullMemberOnTheServerMatchesAMissingOne(t *testing.T) {
	actual := stored()
	actual.Auth = map[string]any{"type": "oauth2", "client_id": "app", "scope": nil}
	desired := desiredFromStored()
	desired.Auth = map[string]any{"type": "oauth2", "client_id": "app"}

	assertJSON(t, patchBody(t, desired, actual), `{"config":{"lookup":{"token":"t0ken"}}}`)
}

func TestToPatch_NeverSendsTheRedactedSentinel(t *testing.T) {
	desired := desiredFromStored()
	desired.Auth = map[string]any{"type": "oauth2", "client_id": "app"}
	desired.Title = "Order desk"

	body := patchBody(t, desired, stored())
	if strings.Contains(body, generic.RedactedSentinel) {
		t.Fatalf("body carries the sentinel: %s", body)
	}
	assertJSON(t, body, `{"title":"Order desk","config":{"lookup":{"token":"t0ken"}}}`)
}

func TestToPatch_DropsTheSentinelAtAnyDepth(t *testing.T) {
	desired := desiredFromStored()
	desired.Config = map[string]any{"lookup": map[string]any{"region": "eu", "token": generic.RedactedSentinel}}
	actual := stored()
	actual.Config = map[string]any{"lookup": map[string]any{"region": "us"}}

	body := patchBody(t, desired, actual)
	if strings.Contains(body, generic.RedactedSentinel) {
		t.Fatalf("a <REDACTED> the YAML echoed back reached the body: %s", body)
	}
	assertJSON(t, body, `{"config":{"lookup":{"region":"eu"}},"auth":{"client_secret":"s3cret"}}`)
}

func TestToPatch_RemovedSecretIsLeftAsStored(t *testing.T) {
	desired := desiredFromStored()
	desired.Auth = map[string]any{"type": "oauth2", "client_id": "app"}
	desired.Config = nil
	actual := stored()
	actual.Config = map[string]any{"lookup": map[string]any{"token": generic.RedactedSentinel}}

	assertJSON(t, patchBody(t, desired, actual), `{}`)
}

func TestToPatch_ClearsAnObjectWithAnEmptyOneWhereTaggedClearEmpty(t *testing.T) {
	desired := desiredFromStored()
	desired.Params = nil

	assertJSON(t, patchBody(t, desired, stored()), `{"params":{},`+ridingSecrets+`}`)
}

func TestToPatch_NeverClearsTheSwitchMember(t *testing.T) {
	cases := map[string]map[string]any{
		"switch member removed": {"client_id": "app", "client_secret": "s3cret"},
		"whole object removed":  nil,
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			desired := desiredFromStored()
			desired.Auth = auth

			_, err := generic.ToPatch(desired, stored(), patchUpdate{})
			var notClearable *generic.NotClearableError
			if !errors.As(err, &notClearable) || notClearable.Field != "auth.type" {
				t.Fatalf("err = %v, want a NotClearableError for auth.type", err)
			}
		})
	}
}

func TestToPatch_SwitchSendsTheWholeObject(t *testing.T) {
	desired := desiredFromStored()
	desired.Auth = map[string]any{"type": "azure_workload_identity", "client_id": "app", "tenant_id": "t-1"}

	assertJSON(t, patchBody(t, desired, stored()),
		`{"auth":{"type":"azure_workload_identity","client_id":"app","tenant_id":"t-1"},"config":{"lookup":{"token":"t0ken"}}}`)
}

func TestToPatch_PointerTheYAMLLeavesOutIsNotManagedWhenTheServerCannotClearIt(t *testing.T) {
	desired := desiredFromStored()
	desired.Title = "Order desk"

	assertJSON(t, patchBody(t, desired, stored()), `{"title":"Order desk",`+ridingSecrets+`}`)

	desired.Runs = ptr(2)
	assertJSON(t, patchBody(t, desired, stored()), `{"title":"Order desk","runs":2,`+ridingSecrets+`}`)
}

func TestToPatch_FieldTheYAMLDoesNotDescribeIsLeftOut(t *testing.T) {
	type narrowCreate struct {
		Name string `json:"name"`
	}
	got, err := generic.ToPatch(narrowCreate{Name: "orders"}, stored(), patchUpdate{})
	if err != nil {
		t.Fatalf("ToPatch: %v", err)
	}
	raw, _ := json.Marshal(got)
	assertJSON(t, string(raw), `{}`)
}

func TestPlan_BuildsPatchBodiesWithToPatch(t *testing.T) {
	k := generic.Kind{
		Name:             "sample",
		UpdateProto:      patchUpdate{},
		NameFromResponse: func(v any) string { return v.(patchResponse).Name },
		IDFromResponse:   func(v any) string { return v.(patchResponse).ID },
	}
	desired := desiredFromStored()
	desired.Description = ""

	plan, err := generic.Plan(
		[]generic.DesiredEntry{{Name: "orders", Create: desired}},
		[]reflect.Value{reflect.ValueOf(stored())},
		k,
	)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(plan.Updates))
	}
	raw, _ := json.Marshal(plan.Updates[0].Patch.Interface())
	assertJSON(t, string(raw), `{"description":null,`+ridingSecrets+`}`)

	desired.Title = ""
	_, err = generic.Plan([]generic.DesiredEntry{{Name: "orders", Create: desired}}, []reflect.Value{reflect.ValueOf(stored())}, k)
	if err == nil || !strings.Contains(err.Error(), `sample "orders": title was removed`) {
		t.Fatalf("err = %v, want one naming the kind, the resource and the field", err)
	}
}

func TestPlan_KeepsUnchangedWhatOnlyTheHashSeesDiffer(t *testing.T) {
	k := generic.Kind{
		Name:             "sample",
		UpdateProto:      patchUpdate{},
		NameFromResponse: func(v any) string { return v.(patchResponse).Name },
		IDFromResponse:   func(v any) string { return v.(patchResponse).ID },
	}
	cases := map[string]func(*patchResponse){
		"null member read back":      func(a *patchResponse) { a.Params["seed"] = nil },
		"field the update can't set": func(a *patchResponse) { a.Kind = "decision" },
		"server default":             func(a *patchResponse) { a.Runs = 4 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			actual := stored()
			change(&actual)
			plan, err := generic.Plan(
				[]generic.DesiredEntry{{Name: "orders", Create: desiredFromStored()}},
				[]reflect.Value{reflect.ValueOf(actual)},
				k,
			)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(plan.Unchanged) != 1 {
				raw, _ := json.Marshal(plan.Updates[0].Patch.Interface())
				t.Errorf("planned an update with body %s; the server would see no change", raw)
			}
		})
	}
}

func TestIsPatchBody(t *testing.T) {
	if !generic.IsPatchBody(patchUpdate{}) || !generic.IsPatchBody(&patchUpdate{}) {
		t.Error("an update type with wire.Patch fields is a PATCH body")
	}
	if generic.IsPatchBody(patchCreate{}) {
		t.Error("an update type without wire.Patch fields is built by ToUpdate")
	}
}

func TestToUpdate_PanicsOnAPatchBody(t *testing.T) {
	for _, proto := range []any{patchUpdate{}, &patchUpdate{}} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, "ToPatch") {
					t.Errorf("ToUpdate(%T) recovered %q, want a panic naming ToPatch", proto, msg)
				}
			}()
			generic.ToUpdate(patchCreate{}, proto)
		}()
	}
}

func TestRegister_AcceptsEveryPatchTag(t *testing.T) {
	generic.Register(generic.Kind{Name: "patch-tag-check", UpdateProto: patchUpdate{}})
}

func TestRegister_RejectsPatchTagsToPatchWouldIgnore(t *testing.T) {
	cases := []struct {
		name  string
		proto any
		want  string
	}{
		{"merge on a plain field", struct {
			Auth map[string]any `json:"auth" cfgapply:"merge"`
		}{}, "apply only to PATCH fields"},
		{"clear-empty on a plain field", struct {
			Name string `json:"name" cfgapply:"clear-empty"`
		}{}, "apply only to PATCH fields"},
		{"merge on a string", struct {
			Name wire.Patch[string] `json:"name,omitzero" cfgapply:"merge"`
		}{}, "merge applies only to objects"},
		{"merge depth below one", struct {
			Auth wire.Patch[map[string]any] `json:"auth,omitzero" cfgapply:"merge=0"`
		}{}, "merge=0 is not a depth"},
		{"merge depth not a number", struct {
			Auth wire.Patch[map[string]any] `json:"auth,omitzero" cfgapply:"merge=abc"`
		}{}, "merge=abc is not a depth"},
		{"clear-empty on a number", struct {
			Limit wire.Patch[int] `json:"limit,omitzero" cfgapply:"clear-empty"`
		}{}, "clear-empty applies only to strings and objects"},
		{"switch without merge", struct {
			Auth wire.Patch[map[string]any] `json:"auth,omitzero" cfgapply:"switch=type"`
		}{}, "switch names a member of a merged object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				got := fmt.Sprint(recover())
				if !strings.Contains(got, c.want) {
					t.Errorf("Register panicked with %q, want it to say %q", got, c.want)
				}
			}()
			generic.Register(generic.Kind{Name: "patch-tag-check", UpdateProto: c.proto})
		})
	}
}
