// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"fmt"
	"testing"
)

type coerceFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type coerceParams struct {
	ObjectName string          `json:"object_name"`
	Filters    *[]coerceFilter `json:"filters,omitempty"`
	Tags       map[string]any  `json:"tags"`
	Note       string          `json:"note"`
}

func coerceHandler(_ context.Context, p coerceParams, _ *ToolContext) (*Result, error) {
	n, first := 0, ""
	if p.Filters != nil {
		n = len(*p.Filters)
		if n > 0 {
			first = (*p.Filters)[0].Field
		}
	}
	return OK(fmt.Sprintf("filters=%d first=%q tags=%d note=%q", n, first, len(p.Tags), p.Note)), nil
}

func TestExecute_CoercesStringifiedComposites(t *testing.T) {
	tool := NewTool("query", "query", coerceHandler)
	ctx, tc := context.Background(), &ToolContext{UserID: "u"}

	t.Run("stringified array filters decode and run", func(t *testing.T) {
		args := map[string]any{
			"object_name": "Contact",
			"filters":     `[{"field": "AccountId", "operator": "IN", "value": "0013"}]`,
		}
		res, err := tool.execute(ctx, args, nil, tc)
		if err != nil {
			t.Fatalf("execute errored on stringified filters: %v", err)
		}
		if want := `filters=1 first="AccountId" tags=0 note=""`; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("stringified object map decodes", func(t *testing.T) {
		args := map[string]any{"object_name": "Contact", "tags": `{"a": 1, "b": 2}`}
		res, err := tool.execute(ctx, args, nil, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := `filters=0 first="" tags=2 note=""`; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("native array still works (no regression)", func(t *testing.T) {
		args := map[string]any{
			"object_name": "Contact",
			"filters":     []any{map[string]any{"field": "Name", "operator": "=", "value": "x"}},
		}
		res, err := tool.execute(ctx, args, nil, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := `filters=1 first="Name" tags=0 note=""`; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("JSON-looking string field is NOT coerced", func(t *testing.T) {
		args := map[string]any{"object_name": "Contact", "note": "[1,2,3]"}
		res, err := tool.execute(ctx, args, nil, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := `filters=0 first="" tags=0 note="[1,2,3]"`; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})
}
