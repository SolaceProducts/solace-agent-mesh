// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestInstant_Marshal(t *testing.T) {
	for _, tc := range []struct {
		in   Instant
		want string
	}{
		{Instant(1_700_000_000_123), `"2023-11-14T22:13:20.123Z"`},
		{Instant(1_700_000_000_000), `"2023-11-14T22:13:20.000Z"`},
		{0, `null`},
	} {
		got, err := json.Marshal(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%d) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestInstant_Unmarshal(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Instant
	}{
		{`"2023-11-14T22:13:20.123Z"`, 1_700_000_000_123},
		{`"2023-11-14T23:13:20.123+01:00"`, 1_700_000_000_123},
		{`null`, 0},
	} {
		var got Instant
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Unmarshal(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestInstant_UnmarshalRejectsNonDateTimes(t *testing.T) {
	var body struct {
		CreatedTime Instant `json:"createdTime"`
	}
	for _, in := range []string{`""`, `"yesterday"`, `1700000000123`, `true`} {
		err := json.Unmarshal([]byte(`{"createdTime":`+in+`}`), &body)
		typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err)
		if !ok {
			t.Errorf("%s: err = %v, want *json.UnmarshalTypeError", in, err)
			continue
		}
		if typeErr.Type != InstantType {
			t.Errorf("%s: Type = %v, want Instant", in, typeErr.Type)
		}
	}
}

func TestPatch_TellsAbsentFromNullFromValue(t *testing.T) {
	type body struct {
		Description Patch[string] `json:"description"`
	}
	for _, tc := range []struct {
		in   string
		want Patch[string]
	}{
		{`{}`, Patch[string]{}},
		{`{"description":null}`, Patch[string]{Set: true, Null: true}},
		{`{"description":""}`, Patch[string]{Set: true}},
		{`{"description":"new"}`, Patch[string]{Set: true, Value: "new"}},
	} {
		var got body
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got.Description != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.in, got.Description, tc.want)
		}
	}
}

func TestPatch_DecodesStrictly(t *testing.T) {
	type spec struct {
		Name string `json:"name"`
	}
	var body struct {
		Spec Patch[spec] `json:"spec"`
	}

	err := json.Unmarshal([]byte(`{"spec":{"name":1}}`), &body)
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); !ok {
		t.Errorf("wrong type: err = %v, want a type error", err)
	}

	err = json.Unmarshal([]byte(`{"spec":{"extra":"x"}}`), &body)
	if err == nil || err.Error() != `json: unknown field "extra"` {
		t.Errorf("unknown field: err = %v, want unknown field \"extra\"", err)
	}
}
