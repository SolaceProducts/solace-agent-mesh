// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestLoadArtifact_SchemaRequiresOnlyFilename(t *testing.T) {
	lt := &LoadArtifactTool{}
	params := lt.Parameters()
	if params == nil {
		t.Fatal("Parameters() is nil")
	}
	if !slices.Contains(params.Required, "filename") {
		t.Errorf("Parameters.Required = %v, want to contain \"filename\"", params.Required)
	}
	if slices.Contains(params.Required, "version") {
		t.Errorf("Parameters.Required = %v, want \"version\" omitted (defaults to latest)", params.Required)
	}
}

func TestLoadArtifact_DispatcherRejectsNonNumericVersion(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "data.txt", "v0 content", "text/plain")

	ts := tool.NewSet()
	if err := ts.Register(&LoadArtifactTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	cases := []struct {
		name string
		args string
	}{
		{"string_latest", `{"filename":"data.txt","version":"latest"}`},
		{"string_numeric", `{"filename":"data.txt","version":"0"}`},
		{"bool", `{"filename":"data.txt","version":true}`},
		{"array", `{"filename":"data.txt","version":[1,2]}`},
		{"object", `{"filename":"data.txt","version":{"v":1}}`},
		{"fractional_number", `{"filename":"data.txt","version":0.5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ts.Dispatch(ctx, tool.Call{Name: "load_artifact", Arguments: tc.args}, toolCtx)
			if err != nil {
				t.Fatalf("Dispatch error: %v", err)
			}
			if res.Status != tool.StatusError {
				t.Fatalf("status = %q, want error; ValidationError = %q", res.Status, res.ValidationError)
			}
			if !strings.Contains(res.ValidationError, "version") || !strings.Contains(res.ValidationError, "integer") {
				t.Errorf("ValidationError should mention version/integer; got: %q", res.ValidationError)
			}
		})
	}
}

func TestLoadArtifact_WithVersionSucceeds(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "data.txt", "v0 content", "text/plain")

	lt := &LoadArtifactTool{}
	res, err := lt.Execute(ctx, map[string]any{
		"filename": "data.txt",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", res.Status, res.Message)
	}
	if got, _ := res.Data["content"].(string); got != "v0 content" {
		t.Errorf("content = %q", got)
	}
}
