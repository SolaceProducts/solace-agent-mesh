// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func TestValidateBuiltinToolsetVolumeBindings(t *testing.T) {
	t.Parallel()
	slots := map[string]struct{}{"ws": {}, "data": {}}

	tests := []struct {
		name      string
		toolset   string
		bindings  map[string]string
		wantErr   bool
		wantField string
	}{
		{name: "nil bindings ok", toolset: "builtin_volume_tools", bindings: nil},
		{name: "valid", toolset: "builtin_volume_tools", bindings: map[string]string{"workspace": "ws"}},
		{
			name: "unknown slot", toolset: "builtin_volume_tools",
			bindings: map[string]string{"workspace": "nope"}, wantErr: true, wantField: "volumeBindings.workspace",
		},
		{
			name: "unknown param", toolset: "builtin_volume_tools",
			bindings: map[string]string{"sidecar": "ws"}, wantErr: true, wantField: "volumeBindings.sidecar",
		},
		{
			name: "empty slot", toolset: "builtin_volume_tools",
			bindings: map[string]string{"workspace": ""}, wantErr: true, wantField: "volumeBindings.workspace",
		},
		{
			name: "toolset without volume params", toolset: "builtin_artifact_tools",
			bindings: map[string]string{"workspace": "ws"}, wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBuiltinToolsetVolumeBindings(tc.toolset, tc.bindings, slots)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr || tc.wantField == "" {
				return
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("err type = %T, want *ValidationError", err)
			}
			if _, ok := ve.Details[tc.wantField]; !ok {
				t.Errorf("Details = %v, want key %q", ve.Details, tc.wantField)
			}
		})
	}
}

func TestValidateBuiltinToolsetVolumeBindings_BatchesAllErrors(t *testing.T) {
	t.Parallel()
	err := validateBuiltinToolsetVolumeBindings(
		"builtin_volume_tools",
		map[string]string{"workspace": "missing", "bogus": "ws"},
		map[string]struct{}{},
	)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err type = %T, want *ValidationError", err)
	}
	if len(ve.Details) != 2 {
		t.Errorf("Details = %v, want 2 field errors", ve.Details)
	}
}

func TestVolumeSlotNamesFromConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "no volumes key", raw: `{"model":"x"}`, want: nil},
		{name: "malformed", raw: `{not json`, want: nil},
		{
			name: "two slots",
			raw:  `{"volumes":[{"name":"ws","provision":"auto_create"},{"name":"data"}]}`,
			want: []string{"ws", "data"},
		},
		{
			name: "skips unnamed",
			raw:  `{"volumes":[{"name":"ws"},{"provision":"static"}]}`,
			want: []string{"ws"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := volumeSlotNamesFromConfig(json.RawMessage(tc.raw))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Errorf("missing slot %q in %v", w, got)
				}
			}
		})
	}
}

func TestVolumeBindingsMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()
	if got, err := marshalVolumeBindings("ts", nil); err != nil || got != nil {
		t.Fatalf("marshal(nil) = %v, %v; want nil, nil", got, err)
	}
	if got := unmarshalStringBindings(nil); got != nil {
		t.Errorf("unmarshal(nil) = %v, want nil", got)
	}

	in := map[string]string{"workspace": "ws"}
	raw, err := marshalVolumeBindings("ts", in)
	if err != nil || raw == nil {
		t.Fatalf("marshal = %v, %v", raw, err)
	}
	out := unmarshalStringBindings(raw)
	if len(out) != 1 || out["workspace"] != "ws" {
		t.Errorf("round-trip = %v, want {workspace: ws}", out)
	}
}

func TestBuiltinToolsetAcceptsVolumeBindings(t *testing.T) {
	t.Parallel()
	if !builtinToolsetAcceptsVolumeBindings("builtin_volume_tools") {
		t.Error("builtin_volume_tools should accept volume bindings")
	}
	if builtinToolsetAcceptsVolumeBindings("builtin_artifact_tools") {
		t.Error("builtin_artifact_tools should not accept volume bindings")
	}
}
