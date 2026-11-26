// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"encoding/json"
	"testing"
)

func TestProfile_UnmarshalSplitsNamedAndExtra(t *testing.T) {
	t.Parallel()
	const raw = `{
		"id": "alice@dev.local",
		"displayName": "Alice",
		"workEmail": "alice@dev.local",
		"mobilePhone": "+1-555-0100",
		"costCenter": "CC-42",
		"nested": {"k": "v"}
	}`
	var p Profile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.ID != "alice@dev.local" || p.DisplayName != "Alice" || p.WorkEmail != "alice@dev.local" {
		t.Errorf("named fields wrong: %+v", p)
	}
	if p.Extra["mobilePhone"] != "+1-555-0100" || p.Extra["costCenter"] != "CC-42" {
		t.Errorf("Extra missing pass-through fields: %+v", p.Extra)
	}
	if _, ok := p.Extra["nested"].(map[string]any); !ok {
		t.Errorf("nested object did not survive in Extra: %+v", p.Extra["nested"])
	}
	for _, k := range []string{"id", "displayName", "workEmail"} {
		if _, present := p.Extra[k]; present {
			t.Errorf("named key %q leaked into Extra", k)
		}
	}
}

func TestProfile_MarshalFlattensExtra(t *testing.T) {
	t.Parallel()
	p := Profile{
		ID:          "u-1",
		DisplayName: "Alice",
		Extra:       map[string]any{"costCenter": "CC-42", "mobilePhone": "+1-555-0100"},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, nested := got["extra"]; nested {
		t.Errorf("Extra was nested under an \"extra\" key: %s", b)
	}
	for k, want := range map[string]any{
		"id": "u-1", "displayName": "Alice", "costCenter": "CC-42", "mobilePhone": "+1-555-0100",
	} {
		if got[k] != want {
			t.Errorf("marshalled[%s] = %v, want %v (full payload: %s)", k, got[k], want, b)
		}
	}
	if _, present := got["jobTitle"]; present {
		t.Errorf("empty optional field jobTitle should be omitted: %s", b)
	}
}

func TestProfile_NamedFieldWinsOverExtra(t *testing.T) {
	t.Parallel()
	p := Profile{ID: "real-id", Extra: map[string]any{"id": "spoofed"}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if got["id"] != "real-id" {
		t.Errorf("Extra shadowed the named id field: got %v", got["id"])
	}
}

func TestProfile_RoundTripPreservesEverything(t *testing.T) {
	t.Parallel()
	orig := Profile{
		ID:          "u-1",
		DisplayName: "Alice",
		JobTitle:    "Engineer",
		Extra:       map[string]any{"costCenter": "CC-42"},
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Profile
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.ID != orig.ID || back.DisplayName != orig.DisplayName || back.JobTitle != orig.JobTitle {
		t.Errorf("named fields lost in round trip: %+v", back)
	}
	if back.Extra["costCenter"] != "CC-42" {
		t.Errorf("Extra lost in round trip: %+v", back.Extra)
	}
}

func TestProfile_UnmarshalNullIsNoOp(t *testing.T) {
	t.Parallel()
	p := Profile{ID: "keep"}
	if err := json.Unmarshal([]byte("null"), &p); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if p.ID != "keep" {
		t.Errorf("null Unmarshal mutated the value: %+v", p)
	}
}

func TestProfile_NonStringNamedFieldPreservedNotDropped(t *testing.T) {
	t.Parallel()
	const raw = `{"id":"u-1","displayName":"Alice","jobTitle":123}`
	var p Profile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.ID != "u-1" || p.DisplayName != "Alice" {
		t.Errorf("string named fields lost: %+v", p)
	}
	if p.JobTitle != "" {
		t.Errorf("non-string jobTitle should not populate the typed field: %q", p.JobTitle)
	}
	if got, ok := p.Extra["jobTitle"].(float64); !ok || got != 123 {
		t.Errorf("non-string jobTitle should survive in Extra, got %v (%T)", p.Extra["jobTitle"], p.Extra["jobTitle"])
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if back["jobTitle"] != float64(123) {
		t.Errorf("non-string jobTitle dropped on marshal: %s", b)
	}
}

func TestProfile_UnmarshalRejectsNonStringID(t *testing.T) {
	t.Parallel()
	var p Profile
	if err := json.Unmarshal([]byte(`{"id":42,"displayName":"Alice"}`), &p); err == nil {
		t.Error("expected error when id is not a string")
	}
}

func TestProfile_UnmarshalRequiresID(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"displayName": "Alice"}`,
		`{"id": "", "displayName": "Alice"}`,
	} {
		var p Profile
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			t.Errorf("expected error for profile without id: %s", raw)
		}
	}
}
