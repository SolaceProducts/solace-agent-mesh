// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "testing"

func TestTaskMessageText(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"bare string unquoted", `"https://x"`, "https://x"},
		{"parts array joins texts", `[{"kind":"text","text":"a"},{"kind":"text","text":"b"}]`, "a\nb"},
		{"parts array with no text renders empty", `[{"kind":"data","data":{"a":1}}]`, ``},
		{"empty stays empty", ``, ``},
		{"non-JSON preserved", `hello`, `hello`},
		{"malformed preserved", `[{`, `[{`},
		{"null renders empty", `null`, ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskMessageText(tt.raw); got != tt.want {
				t.Errorf("taskMessageText(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
