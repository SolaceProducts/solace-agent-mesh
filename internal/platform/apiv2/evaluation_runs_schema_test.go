// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"testing"
)

func TestStoredInputSchema_NullForNoUsableSchema(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		want   string
	}{
		{"declared schema", `{"type":"object","required":["orderId"]}`, `{"type":"object","required":["orderId"]}`},
		{"empty object", ` {} `, ""},
		{"boolean schema", `true`, ""},
		{"not recorded", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := storedInputSchema(json.RawMessage(tc.stored))
			if string(got) != tc.want {
				t.Fatalf("storedInputSchema(%q) = %q, want %q", tc.stored, got, tc.want)
			}
		})
	}
}
