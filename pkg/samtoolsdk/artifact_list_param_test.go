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

type optionalListParams struct {
	Name        string      `json:"name"`
	DataSources *[]Artifact `json:"data_sources"`
	Required    []Artifact  `json:"required"`
}

func optionalListHandler(_ context.Context, p optionalListParams, _ *ToolContext) (*Result, error) {
	n := -1
	if p.DataSources != nil {
		n = len(*p.DataSources)
	}
	return OK(fmt.Sprintf("data_sources=%d required=%d", n, len(p.Required))), nil
}

func TestExecute_OptionalArtifactList(t *testing.T) {
	tool := NewTool("write", "write", optionalListHandler)
	ctx, tc := context.Background(), &ToolContext{UserID: "u"}

	t.Run("optional list populated", func(t *testing.T) {
		artObjs := map[string]any{
			"data_sources": []*Artifact{
				{Filename: "a.csv", Content: []byte("x")},
				{Filename: "b.csv", Content: []byte("y")},
			},
		}
		res, err := tool.execute(ctx, map[string]any{"name": "w"}, artObjs, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := "data_sources=2 required=0"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("optional list omitted stays nil", func(t *testing.T) {
		res, err := tool.execute(ctx, map[string]any{"name": "w"}, nil, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := "data_sources=-1 required=0"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})

	t.Run("required (non-pointer) list still works", func(t *testing.T) {
		artObjs := map[string]any{
			"required": []*Artifact{{Filename: "r.csv", Content: []byte("z")}},
		}
		res, err := tool.execute(ctx, map[string]any{"name": "w"}, artObjs, tc)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if want := "data_sources=-1 required=1"; res.Message != want {
			t.Errorf("message = %q, want %q", res.Message, want)
		}
	})
}
