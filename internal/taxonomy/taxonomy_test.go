// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestParseArrivingStringSet(t *testing.T) {
	for name, tc := range map[string]struct {
		in    any
		limit int
		want  []string
		ok    bool
	}{
		"same-process slice":       {in: []string{"a", "b"}, limit: -1, want: []string{"a", "b"}, ok: true},
		"decoded slice":            {in: []any{"a", "b"}, limit: -1, want: []string{"a", "b"}, ok: true},
		"same-process truncated":   {in: []string{"a", "b", "c"}, limit: 2, want: []string{"a", "b"}, ok: true},
		"decoded truncated":        {in: []any{"a", "b", "c"}, limit: 2, want: []string{"a", "b"}, ok: true},
		"limit at length":          {in: []string{"a", "b"}, limit: 2, want: []string{"a", "b"}, ok: true},
		"zero limit drops all":     {in: []string{"a"}, limit: 0, want: []string{}, ok: true},
		"empty stays empty":        {in: []any{}, limit: -1, want: []string{}, ok: true},
		"decoded non-string":       {in: []any{"a", 1}, limit: -1, ok: false},
		"non-string past limit":    {in: []any{"a", 1}, limit: 1, want: []string{"a"}, ok: true},
		"not a slice":              {in: "a", limit: -1, ok: false},
		"nil":                      {in: nil, limit: -1, ok: false},
		"slice of the wrong types": {in: []int{1}, limit: -1, ok: false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ParseArrivingStringSet(tc.in, tc.limit)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				if got != nil {
					t.Errorf("got %v on a rejected value, want nil", got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseArrivingStringSetAgreesAcrossATransport(t *testing.T) {
	direct := []string{"builtin", "connector", "custom"}

	encoded, err := json.Marshal(direct)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}

	fromProcess, okProcess := ParseArrivingStringSet(direct, 2)
	fromWire, okWire := ParseArrivingStringSet(decoded, 2)

	if !okProcess || !okWire {
		t.Fatalf("parse ok: in-process %v, over the wire %v — both must accept the same value", okProcess, okWire)
	}
	if !slices.Equal(fromProcess, fromWire) {
		t.Errorf("in-process %v, over the wire %v — a transport must not change the parse", fromProcess, fromWire)
	}
}
