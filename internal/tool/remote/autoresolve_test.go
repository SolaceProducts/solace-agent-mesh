// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newAutoResolveCtx(t *testing.T, files map[string]string) *tool.Context {
	t.Helper()
	store := memory.New()
	svc := &artifact.ScopedService{Inner: store, Scope: "agent", Namespace: "test-ns"}
	ctx := &tool.Context{UserID: "u", SessionID: "s", AgentName: "a", ArtifactService: svc}
	for name, content := range files {
		loc := svc.Resolve("a", "u", "s", name)
		if _, err := store.Save(context.Background(), loc, []byte(content), "text/csv"); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return ctx
}

func newPyTool() *Tool {
	return NewTool(&ToolConfig{
		ToolName: "execute_python_code",
		Parameters: map[string]ParamConfig{
			"python_code":     {Type: "string", Required: true},
			"input_artifacts": {Type: "artifact_list"},
		},
	}, nil)
}

func inputArtifactsOf(args map[string]any) []string {
	var out []string
	if raw, ok := args["input_artifacts"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestAutoResolveInputArtifacts_AmbiguousParamsNoOp(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{
		ToolName: "two_list_tool",
		Parameters: map[string]ParamConfig{
			"python_code": {Type: "string"},
			"a":           {Type: "artifact_list"},
			"b":           {Type: "artifact_list"},
		},
	}, nil)
	ctx := newAutoResolveCtx(t, map[string]string{"sales.csv": "x"})
	args := map[string]any{"python_code": `INPUT_ARTIFACTS["sales.csv"]`}
	rt.autoResolveInputArtifacts(context.Background(), args, ctx)
	if _, ok := args["a"]; ok {
		t.Errorf("ambiguous params should be a no-op, but param 'a' was set: %v", args["a"])
	}
	if _, ok := args["b"]; ok {
		t.Errorf("ambiguous params should be a no-op, but param 'b' was set: %v", args["b"])
	}
}

func TestResolveArtifactContentEmbeds_ReturnsDistinctMap(t *testing.T) {
	t.Parallel()
	rt := newPyTool()
	ctx := newAutoResolveCtx(t, nil)
	orig := map[string]any{"python_code": "print(1)"}
	out := rt.resolveArtifactContentEmbeds(context.Background(), orig, ctx)
	if &out == &orig {
		t.Fatal("unreachable")
	}
	out["python_code"] = "MUTATED"
	if orig["python_code"] != "print(1)" {
		t.Errorf("resolveArtifactContentEmbeds returned an aliased map; original was mutated to %q", orig["python_code"])
	}
}

func TestAutoResolveInputArtifacts(t *testing.T) {
	t.Parallel()
	rt := newPyTool()
	bg := context.Background()

	cases := []struct {
		name  string
		files map[string]string
		args  map[string]any
		want  []string
	}{
		{
			name:  "adds referenced session artifact when omitted",
			files: map[string]string{"sales.csv": "month,revenue\n1,100\n"},
			args:  map[string]any{"python_code": `df = INPUT_ARTIFACTS["sales.csv"].as_text()`},
			want:  []string{"sales.csv"},
		},
		{
			name:  "single-quote reference matched",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"python_code": `INPUT_ARTIFACTS['sales.csv']`},
			want:  []string{"sales.csv"},
		},
		{
			name:  "absent artifact not added (preserves explicit KeyError)",
			files: nil,
			args:  map[string]any{"python_code": `INPUT_ARTIFACTS["missing.csv"]`},
			want:  nil,
		},
		{
			name:  "already listed not duplicated",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"python_code": `INPUT_ARTIFACTS["sales.csv"]`, "input_artifacts": []any{"sales.csv"}},
			want:  []string{"sales.csv"},
		},
		{
			name:  "explicit version pin preserved, not duplicated",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"python_code": `INPUT_ARTIFACTS["sales.csv"]`, "input_artifacts": []any{"sales.csv:2"}},
			want:  []string{"sales.csv:2"},
		},
		{
			name:  "dynamic (non-literal) key not added",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"python_code": `name="sales.csv"; INPUT_ARTIFACTS[name]`},
			want:  nil,
		},
		{
			name:  "no python_code arg is a no-op",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"other": "x"},
			want:  nil,
		},
		{
			name:  "reference inside a comment still stages existing file",
			files: map[string]string{"sales.csv": "x"},
			args:  map[string]any{"python_code": "# example: INPUT_ARTIFACTS[\"sales.csv\"]\nprint(1)"},
			want:  []string{"sales.csv"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newAutoResolveCtx(t, tc.files)
			rt.autoResolveInputArtifacts(bg, tc.args, ctx)
			got := inputArtifactsOf(tc.args)
			if len(got) != len(tc.want) {
				t.Fatalf("input_artifacts = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("input_artifacts[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
