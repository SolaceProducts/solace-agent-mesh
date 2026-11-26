// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type argumentHintingTool struct {
	*mockTool
}

func (h argumentHintingTool) ArgumentErrorHint(args map[string]any) string {
	return fmt.Sprintf("hinted takes path; it got %d argument(s)", len(args))
}

func TestDispatchAddsAToolsArgumentHintToARejection(t *testing.T) {
	schema := &JSONSchema{
		Type:       "object",
		Properties: map[string]any{"path": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer"}},
		Required:   []string{"path"},
	}
	ok := func(context.Context, map[string]any, *Context) (*Result, error) { return OK("ok"), nil }
	ts := NewSet()
	if err := ts.Register(argumentHintingTool{&mockTool{name: "hinted", params: schema, execFn: ok}}); err != nil {
		t.Fatal(err)
	}
	if err := ts.Register(&mockTool{name: "plain", params: schema, execFn: ok}); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, arguments string }{
		{"a missing required argument", `{"file": "a.md"}`},
		{"a mistyped argument", `{"path": "a.md", "line": "3"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain, err := ts.Dispatch(t.Context(), Call{Name: "plain", Arguments: tc.arguments}, &Context{})
			if err != nil {
				t.Fatal(err)
			}
			hinted, err := ts.Dispatch(t.Context(), Call{Name: "hinted", Arguments: tc.arguments}, &Context{})
			if err != nil {
				t.Fatal(err)
			}
			if plain.ValidationError == "" || hinted.ValidationError == "" {
				t.Fatalf("plain = %+v, hinted = %+v, want both rejected", plain, hinted)
			}
			if strings.Contains(plain.ValidationError, "hinted takes path") {
				t.Errorf("a tool without a hint got one: %s", plain.ValidationError)
			}
			wantPrefix := strings.Replace(plain.ValidationError, "`plain()`", "`hinted()`", 1) + "\nhinted takes path; it got"
			if !strings.HasPrefix(hinted.ValidationError, wantPrefix) {
				t.Errorf("hinted rejection = %q, want the plain rejection followed by the hint", hinted.ValidationError)
			}
		})
	}

	accepted, err := ts.Dispatch(t.Context(), Call{Name: "hinted", Arguments: `{"path": "a.md"}`}, &Context{})
	if err != nil || accepted.Status == StatusError {
		t.Errorf("a valid call to a hinting tool = %+v, %v; want it dispatched", accepted, err)
	}
}
