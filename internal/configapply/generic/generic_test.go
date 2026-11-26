// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package generic

import (
	"encoding/json"
	"reflect"
	"testing"
)

type sampleJSONObjectDTO struct {
	Name   string          `json:"name"`
	Extras json.RawMessage `json:"extras,omitempty" cfgapply:"json-object"`
}

type sampleDefaultedCreate struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty" cfgapply:"default=chat"`
}

type sampleDefaultedResponse struct {
	Name string  `json:"name"`
	Kind *string `json:"kind" cfgapply:"default=chat"`
}

func TestHash_DefaultTagTreatsEmptyAsTheDefault(t *testing.T) {
	chat, decision := "chat", "decision"
	omitted := mustHash(t, sampleDefaultedCreate{Name: "m"})
	reportedDefault := mustHash(t, sampleDefaultedResponse{Name: "m", Kind: &chat})
	nilPointer := mustHash(t, sampleDefaultedResponse{Name: "m"})
	different := mustHash(t, sampleDefaultedResponse{Name: "m", Kind: &decision})

	if omitted != reportedDefault || omitted != nilPointer {
		t.Error("an omitted defaulted field must hash the same as the reported default")
	}
	if omitted == different {
		t.Error("a non-default value must still change the hash")
	}
}

func mustHash(t *testing.T, dto any) string {
	t.Helper()
	h, err := Hash(dto, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHash_JSONObjectCanonicalises(t *testing.T) {
	a := sampleJSONObjectDTO{Name: "a", Extras: []byte(`{"a":1,"b":2}`)}
	b := sampleJSONObjectDTO{Name: "a", Extras: []byte(`  {  "b" : 2 ,  "a" : 1  }  `)}
	ha, err := Hash(a, nil)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	hb, err := Hash(b, nil)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if ha != hb {
		t.Errorf("expected canonicalised hash to match, got %s vs %s", ha, hb)
	}
}

func TestHash_JSONObjectDifferentContent(t *testing.T) {
	a := sampleJSONObjectDTO{Name: "a", Extras: []byte(`{"a":1}`)}
	b := sampleJSONObjectDTO{Name: "a", Extras: []byte(`{"a":2}`)}
	ha, err := Hash(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Hash(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Errorf("expected different hashes for different blobs, got identical %s", ha)
	}
}

func TestHash_JSONObjectEmptyAndUnparseable(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("{not-json}")} {
		dto := sampleJSONObjectDTO{Name: "a", Extras: raw}
		if _, err := Hash(dto, nil); err != nil {
			t.Errorf("Hash(%q): %v", string(raw), err)
		}
	}
}

type sampleCreate struct {
	Alias       string         `json:"alias"`
	APIBase     string         `json:"apiBase,omitempty"`
	AuthConfig  map[string]any `json:"authConfig" cfgapply:"secret"`
	Description string         `json:"description,omitempty"`
	MaxTokens   *int           `json:"maxTokens,omitempty"`
	Deploy      bool           `json:"deploy" cfgapply:"omit-hash"`
}

type sampleResponse struct {
	ID          string         `json:"id" cfgapply:"read-only"`
	Alias       string         `json:"alias"`
	APIBase     *string        `json:"apiBase"`
	AuthType    string         `json:"authType" cfgapply:"read-only"`
	AuthConfig  map[string]any `json:"authConfig" cfgapply:"secret"`
	Description *string        `json:"description"`
	MaxTokens   *int           `json:"maxTokens"`
	CreatedTime int64          `json:"createdTime" cfgapply:"read-only"`
}

type sampleUpdate struct {
	Alias       *string        `json:"alias,omitempty"`
	APIBase     *string        `json:"apiBase,omitempty"`
	AuthConfig  map[string]any `json:"authConfig,omitempty" cfgapply:"secret"`
	Description *string        `json:"description,omitempty"`
	MaxTokens   *int           `json:"maxTokens,omitempty"`
}

func TestHash_DesiredEqualsActual(t *testing.T) {
	desc := "hello"
	apiBase := "https://api.example.com"
	desired := sampleCreate{
		Alias:       "planning",
		APIBase:     apiBase,
		AuthConfig:  map[string]any{"apiKey": "sk-real"},
		Description: desc,
	}
	actual := sampleResponse{
		ID:          "id-1",
		Alias:       "planning",
		APIBase:     &apiBase,
		AuthType:    "none",
		AuthConfig:  map[string]any{"apiKey": "sk-real"},
		Description: &desc,
		CreatedTime: 1234,
	}
	dh, err := Hash(desired, nil)
	if err != nil {
		t.Fatalf("Hash desired: %v", err)
	}
	ah, err := Hash(actual, nil)
	if err != nil {
		t.Fatalf("Hash actual: %v", err)
	}
	if dh != ah {
		t.Errorf("expected equal hashes, got desired=%s actual=%s", dh, ah)
	}
}

func TestHash_DropsRedactedSecretKeys(t *testing.T) {
	desired := sampleCreate{
		Alias:      "planning",
		AuthConfig: map[string]any{"apiKey": "sk-real", "region": "us-east-1"},
	}
	apiBase := ""
	desc := ""
	actual := sampleResponse{
		Alias:       "planning",
		APIBase:     &apiBase,
		AuthConfig:  map[string]any{"apiKey": RedactedSentinel, "region": "us-east-1"},
		Description: &desc,
	}
	redacted := RedactedKeysFromSecretField(actual)
	if _, ok := redacted["apiKey"]; !ok {
		t.Fatalf("expected apiKey to be detected as redacted, got %+v", redacted)
	}
	dh, err := Hash(desired, redacted)
	if err != nil {
		t.Fatal(err)
	}
	ah, err := Hash(actual, redacted)
	if err != nil {
		t.Fatal(err)
	}
	if dh != ah {
		t.Errorf("redacted-key normalisation failed: desired=%s actual=%s", dh, ah)
	}
}

func TestHash_OmitsReadOnlyAndOmitHash(t *testing.T) {
	a := sampleCreate{Alias: "x", Deploy: false}
	b := sampleCreate{Alias: "x", Deploy: true}
	ha, err := Hash(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Hash(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("Deploy omit-hash failed: %s vs %s", ha, hb)
	}
}

func TestToUpdate_WrapsScalarsInPointers(t *testing.T) {
	c := sampleCreate{
		Alias:       "planning",
		APIBase:     "https://api.example.com",
		AuthConfig:  map[string]any{"apiKey": "sk-real"},
		Description: "hello",
	}
	upd := ToUpdate(c, sampleUpdate{}).(sampleUpdate)
	if upd.Alias == nil || *upd.Alias != "planning" {
		t.Errorf("Alias not wrapped, got %+v", upd.Alias)
	}
	if upd.APIBase == nil || *upd.APIBase != c.APIBase {
		t.Errorf("APIBase not wrapped, got %+v", upd.APIBase)
	}
	if upd.AuthConfig["apiKey"] != "sk-real" {
		t.Errorf("AuthConfig not copied: %+v", upd.AuthConfig)
	}
	if upd.Description == nil || *upd.Description != "hello" {
		t.Errorf("Description not wrapped, got %+v", upd.Description)
	}
}

type xrefHolder struct {
	Skills    []string `json:"skills" cfgapply:"xref=skill"`
	Connector string   `json:"connector" cfgapply:"xref=connector"`
	Other     string   `json:"other"`
}

type fakeReg struct {
	idByName map[string]map[string]string
	nameByID map[string]map[string]string
}

func (f *fakeReg) LookupID(kind, name string) (string, error) {
	return f.idByName[kind][name], nil
}
func (f *fakeReg) LookupName(kind, id string) (string, error) {
	return f.nameByID[kind][id], nil
}

func TestResolveForwardReverse(t *testing.T) {
	reg := &fakeReg{
		idByName: map[string]map[string]string{
			"skill":     {"summarize": "skill-uuid-1", "translate": "skill-uuid-2"},
			"connector": {"slack": "conn-uuid-1"},
		},
		nameByID: map[string]map[string]string{
			"skill":     {"skill-uuid-1": "summarize", "skill-uuid-2": "translate"},
			"connector": {"conn-uuid-1": "slack"},
		},
	}
	h := xrefHolder{Skills: []string{"summarize", "translate"}, Connector: "slack", Other: "preserved"}
	if err := ResolveForward(&h, reg); err != nil {
		t.Fatalf("ResolveForward: %v", err)
	}
	want := []string{"skill-uuid-1", "skill-uuid-2"}
	if !reflect.DeepEqual(h.Skills, want) {
		t.Errorf("Skills not resolved: %+v", h.Skills)
	}
	if h.Connector != "conn-uuid-1" {
		t.Errorf("Connector not resolved: %s", h.Connector)
	}
	if h.Other != "preserved" {
		t.Errorf("Untagged field mutated: %s", h.Other)
	}
	if err := ResolveReverse(&h, reg); err != nil {
		t.Fatalf("ResolveReverse: %v", err)
	}
	if !reflect.DeepEqual(h.Skills, []string{"summarize", "translate"}) {
		t.Errorf("ResolveReverse skills: %+v", h.Skills)
	}
	if h.Connector != "slack" {
		t.Errorf("ResolveReverse connector: %s", h.Connector)
	}
}

func TestRegistry_OrderedKinds(t *testing.T) {
	Register(Kind{Name: "zzz_test_late", Order: 999})
	Register(Kind{Name: "zzz_test_early", Order: 1})
	t.Cleanup(func() {
		Register(Kind{Name: "zzz_test_late"})
		Register(Kind{Name: "zzz_test_early"})
	})

	ordered := OrderedKinds()
	earlyIdx, lateIdx := -1, -1
	for i, k := range ordered {
		switch k.Name {
		case "zzz_test_early":
			earlyIdx = i
		case "zzz_test_late":
			lateIdx = i
		}
	}
	if earlyIdx < 0 || lateIdx < 0 {
		t.Fatalf("test kinds missing from OrderedKinds: early=%d late=%d", earlyIdx, lateIdx)
	}
	if earlyIdx >= lateIdx {
		t.Errorf("OrderedKinds order wrong: early=%d late=%d (early should sort first)", earlyIdx, lateIdx)
	}
}

type fakePlanCreate struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type fakePlanResponse struct {
	ID    string `json:"id" cfgapply:"read-only"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type fakePlanUpdate struct {
	Value *string `json:"value,omitempty"`
}

func TestPlan_EndToEnd(t *testing.T) {
	k := Kind{
		Name:          "fakeplan",
		Plural:        "fakeplans",
		Order:         50,
		CreateProto:   fakePlanCreate{},
		UpdateProto:   fakePlanUpdate{},
		ResponseProto: fakePlanResponse{},
		NameFromCreate: func(v any) string {
			if c, ok := v.(fakePlanCreate); ok {
				return c.Name
			}
			return ""
		},
		NameFromResponse: func(v any) string {
			if r, ok := v.(fakePlanResponse); ok {
				return r.Name
			}
			return ""
		},
		IDFromResponse: func(v any) string {
			if r, ok := v.(fakePlanResponse); ok {
				return r.ID
			}
			return ""
		},
	}

	desired := []DesiredEntry{
		{Name: "alpha", Source: "alpha.yaml", Create: fakePlanCreate{Name: "alpha", Value: "v1"}},
		{Name: "bravo", Source: "bravo.yaml", Create: fakePlanCreate{Name: "bravo", Value: "v2"}},
		{Name: "delta", Source: "delta.yaml", Create: fakePlanCreate{Name: "delta", Value: "v4"}},
	}
	actual := []reflect.Value{
		reflect.ValueOf(fakePlanResponse{ID: "id-bravo", Name: "bravo", Value: "v2"}),
		reflect.ValueOf(fakePlanResponse{ID: "id-delta", Name: "delta", Value: "old"}),
		reflect.ValueOf(fakePlanResponse{ID: "id-charlie", Name: "charlie"}),
	}

	plan, err := Plan(desired, actual, k)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "alpha" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].Name != "delta" || plan.Updates[0].ID != "id-delta" {
		t.Errorf("Updates = %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 || plan.Unchanged[0].Name != "bravo" {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].Name != "charlie" || plan.Deletes[0].ID != "id-charlie" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	upd, ok := plan.Updates[0].Patch.Interface().(fakePlanUpdate)
	if !ok || upd.Value == nil || *upd.Value != "v4" {
		t.Errorf("Update Patch projection wrong: %+v", upd)
	}
}

func TestPlan_DuplicateDesiredName(t *testing.T) {
	k := Kind{
		Name:             "fakedup",
		ResponseProto:    fakePlanResponse{},
		NameFromResponse: func(v any) string { return "" },
		IDFromResponse:   func(v any) string { return "" },
	}
	desired := []DesiredEntry{
		{Name: "x", Create: fakePlanCreate{Name: "x"}},
		{Name: "x", Create: fakePlanCreate{Name: "x"}},
	}
	if _, err := Plan(desired, nil, k); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}
