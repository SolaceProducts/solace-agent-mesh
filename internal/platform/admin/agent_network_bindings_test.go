// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func TestNetworkPolicyNamesFromConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "no networks key", raw: `{"model":"x"}`, want: nil},
		{name: "malformed", raw: `{not json`, want: nil},
		{
			name: "two policies",
			raw:  `{"networks":[{"name":"egress","allowlist":["api.anthropic.com"]},{"name":"model"}]}`,
			want: []string{"egress", "model"},
		},
		{
			name: "skips unnamed",
			raw:  `{"networks":[{"name":"egress"},{"allowlist":["x"]}]}`,
			want: []string{"egress"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := networkPolicyNamesFromConfig(json.RawMessage(tc.raw))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Errorf("missing policy %q in %v", w, got)
				}
			}
		})
	}
}

func TestNetworkBindingsMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()
	if got, err := marshalNetworkBindings("ts", nil); err != nil || got != nil {
		t.Fatalf("marshal(nil) = %v, %v; want nil, nil", got, err)
	}
	if got := unmarshalStringBindings(nil); got != nil {
		t.Errorf("unmarshal(nil) = %v, want nil", got)
	}

	in := map[string]string{"anthropic_api": "egress"}
	raw, err := marshalNetworkBindings("ts", in)
	if err != nil || raw == nil {
		t.Fatalf("marshal = %v, %v", raw, err)
	}
	out := unmarshalStringBindings(raw)
	if len(out) != 1 || out["anthropic_api"] != "egress" {
		t.Errorf("round-trip = %v, want {anthropic_api: egress}", out)
	}
}
