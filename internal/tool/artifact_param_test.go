// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func newTestToolCtx() (*Context, *memory.Store) {
	store := memory.New()
	svc := &artifact.ScopedService{
		Inner:     store,
		Scope:     "agent",
		Namespace: "test-ns",
	}
	return &Context{
		UserID:          "user-1",
		SessionID:       "sess-1",
		AgentName:       "test-agent",
		ArtifactService: svc,
	}, store
}

type stubTool struct {
	name           string
	artifactParams map[string]ArtifactParamInfo
}

func (s *stubTool) Name() string                                 { return s.name }
func (s *stubTool) Description() string                          { return "" }
func (s *stubTool) Parameters() *JSONSchema                      { return nil }
func (s *stubTool) Instructions() string                         { return "" }
func (s *stubTool) IsLongRunning() bool                          { return false }
func (s *stubTool) RequiredScopes() []string                     { return nil }
func (s *stubTool) RawStringArgs() []string                      { return nil }
func (s *stubTool) ArtifactParams() map[string]ArtifactParamInfo { return s.artifactParams }
func (s *stubTool) ContextParam() string                         { return "" }
func (s *stubTool) Execute(_ context.Context, _ map[string]any, _ *Context) (*Result, error) {
	return OK("ok"), nil
}

func TestParseFilenameVersion(t *testing.T) {
	tests := []struct {
		input       string
		wantFile    string
		wantVersion *int
	}{
		{"data.csv", "data.csv", nil},
		{"data.csv:3", "data.csv", intPtr(3)},
		{"data.csv:0", "data.csv", intPtr(0)},
		{"file:with:colons:5", "file:with:colons", intPtr(5)},
		{"noversion:", "noversion:", nil},
		{"user:prefs.json", "user:prefs.json", nil},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			file, ver := parseFilenameVersion(tc.input)
			if file != tc.wantFile {
				t.Errorf("filename = %q, want %q", file, tc.wantFile)
			}
			if (ver == nil) != (tc.wantVersion == nil) {
				t.Errorf("version = %v, want %v", ver, tc.wantVersion)
			} else if ver != nil && *ver != *tc.wantVersion {
				t.Errorf("version = %d, want %d", *ver, *tc.wantVersion)
			}
		})
	}
}

func intPtr(n int) *int { return &n }

func TestResolveSingleArtifactParam(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "data.csv")
	_, _ = store.Save(ctx, loc, []byte("a,b,c"), "text/csv")

	tool := &stubTool{
		name: "process",
		artifactParams: map[string]ArtifactParamInfo{
			"input_file": {IsList: false},
		},
	}

	args := map[string]any{
		"input_file": "data.csv",
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	art, ok := args["input_file"].(*Artifact)
	if !ok {
		t.Fatalf("expected *Artifact, got %T", args["input_file"])
	}
	if string(art.Content) != "a,b,c" {
		t.Errorf("content = %q", art.Content)
	}
	if art.Filename != "data.csv" {
		t.Errorf("filename = %q", art.Filename)
	}
	if art.MIMEType != "text/csv" {
		t.Errorf("mime = %q", art.MIMEType)
	}
	if art.Version != 0 {
		t.Errorf("version = %d, want 0", art.Version)
	}
}

func TestResolveListArtifactParam(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc1 := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "a.json")
	_, _ = store.Save(ctx, loc1, []byte(`{"x":1}`), "application/json")
	loc2 := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "b.json")
	_, _ = store.Save(ctx, loc2, []byte(`{"y":2}`), "application/json")

	tool := &stubTool{
		name: "merge",
		artifactParams: map[string]ArtifactParamInfo{
			"files": {IsList: true},
		},
	}

	args := map[string]any{
		"files": []any{"a.json", "b.json"},
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	artifacts, ok := args["files"].([]*Artifact)
	if !ok {
		t.Fatalf("expected []*Artifact, got %T", args["files"])
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}
	if artifacts[0].Filename != "a.json" || artifacts[1].Filename != "b.json" {
		t.Errorf("filenames = %q, %q", artifacts[0].Filename, artifacts[1].Filename)
	}
}

func TestResolveWithVersion(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "data.txt")
	_, _ = store.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v1"), "text/plain")

	tool := &stubTool{
		name: "read",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{
		"file": "data.txt:0",
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	art := args["file"].(*Artifact)
	if string(art.Content) != "v0" {
		t.Errorf("content = %q, want v0", art.Content)
	}
	if art.Version != 0 {
		t.Errorf("version = %d, want 0", art.Version)
	}
}

func TestResolveLatestVersionNumber(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "data.txt")
	_, _ = store.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v1"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v2"), "text/plain")

	tool := &stubTool{
		name: "read",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{
		"file": "data.txt",
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	art := args["file"].(*Artifact)
	if string(art.Content) != "v2" {
		t.Errorf("content = %q, want v2", art.Content)
	}
	if art.Version != 2 {
		t.Errorf("version = %d, want 2 (latest)", art.Version)
	}
}

func TestResolveSingleVersionReportsVersion(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "single.txt")
	_, _ = store.Save(ctx, loc, []byte("only"), "text/plain")

	tool := &stubTool{
		name: "read",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{
		"file": "single.txt",
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	art := args["file"].(*Artifact)
	if art.Version != 0 {
		t.Errorf("version = %d, want 0", art.Version)
	}
}

func TestResolveExplicitVersionMidRange(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "data.txt")
	_, _ = store.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v1"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v2"), "text/plain")

	tool := &stubTool{
		name: "read",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{
		"file": "data.txt:1",
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	art := args["file"].(*Artifact)
	if string(art.Content) != "v1" {
		t.Errorf("content = %q, want v1", art.Content)
	}
	if art.Version != 1 {
		t.Errorf("version = %d, want 1", art.Version)
	}
}

func TestResolveListLatestVersionNumbers(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc1 := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "a.txt")
	_, _ = store.Save(ctx, loc1, []byte("a-v0"), "text/plain")
	_, _ = store.Save(ctx, loc1, []byte("a-v1"), "text/plain")

	loc2 := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "b.txt")
	_, _ = store.Save(ctx, loc2, []byte("b-v0"), "text/plain")
	_, _ = store.Save(ctx, loc2, []byte("b-v1"), "text/plain")
	_, _ = store.Save(ctx, loc2, []byte("b-v2"), "text/plain")

	tool := &stubTool{
		name: "merge",
		artifactParams: map[string]ArtifactParamInfo{
			"files": {IsList: true},
		},
	}

	args := map[string]any{
		"files": []any{"a.txt", "b.txt"},
	}

	err := resolveArtifactParams(ctx, tool, args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	artifacts := args["files"].([]*Artifact)
	if artifacts[0].Version != 1 {
		t.Errorf("a.txt version = %d, want 1", artifacts[0].Version)
	}
	if artifacts[1].Version != 2 {
		t.Errorf("b.txt version = %d, want 2", artifacts[1].Version)
	}
}

type silentStore struct{ artifact.Service }

func (silentStore) Load(context.Context, artifact.Locator, *int) ([]byte, string, error) {
	return nil, "", nil
}

func newSilentToolCtx() *Context {
	return &Context{
		UserID:          "user-1",
		SessionID:       "sess-1",
		AgentName:       "test-agent",
		ArtifactService: &artifact.ScopedService{Inner: silentStore{}, Scope: "agent", Namespace: "test-ns"},
	}
}

func TestVersionMissIsNotAFileMiss(t *testing.T) {
	single := &stubTool{name: "read", artifactParams: map[string]ArtifactParamInfo{"file": {}}}
	list := &stubTool{name: "merge", artifactParams: map[string]ArtifactParamInfo{"files": {IsList: true}}}

	tests := []struct {
		name         string
		tl           *stubTool
		args         map[string]any
		wantNotFound bool
	}{
		{"single/latest", single, map[string]any{"file": "gone.txt"}, true},
		{"single/explicit version", single, map[string]any{"file": "gone.txt:7"}, false},
		{"list/latest", list, map[string]any{"files": []any{"gone.txt"}}, true},
		{"list/explicit version", list, map[string]any{"files": []any{"gone.txt:7"}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolveArtifactParams(t.Context(), tc.tl, tc.args, newSilentToolCtx())
			if err == nil {
				t.Fatal("expected a resolution error for absent content")
			}
			if got := errors.Is(err, ErrArtifactNotFound); got != tc.wantNotFound {
				t.Errorf("errors.Is(err, ErrArtifactNotFound) = %v, want %v (err = %v)", got, tc.wantNotFound, err)
			}
		})
	}
}

func TestResolveNotFound(t *testing.T) {
	toolCtx, _ := newTestToolCtx()

	tool := &stubTool{
		name: "read",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{
		"file": "nonexistent.txt",
	}

	err := resolveArtifactParams(context.Background(), tool, args, toolCtx)
	if err == nil {
		t.Error("expected error for nonexistent artifact")
	}
}

func TestResolveOptionalMissing(t *testing.T) {
	toolCtx, _ := newTestToolCtx()

	tool := &stubTool{
		name: "process",
		artifactParams: map[string]ArtifactParamInfo{
			"optional_file": {IsOptional: true},
		},
	}

	args := map[string]any{}
	err := resolveArtifactParams(context.Background(), tool, args, toolCtx)
	if err != nil {
		t.Errorf("optional param should not error: %v", err)
	}
}

func TestResolveRequiredMissing(t *testing.T) {
	toolCtx, _ := newTestToolCtx()

	tool := &stubTool{
		name: "process",
		artifactParams: map[string]ArtifactParamInfo{
			"required_file": {IsOptional: false},
		},
	}

	args := map[string]any{}
	err := resolveArtifactParams(context.Background(), tool, args, toolCtx)
	if err == nil {
		t.Error("expected error for missing required param")
	}
}

func TestResolveNoArtifactParams(t *testing.T) {
	toolCtx, _ := newTestToolCtx()

	tool := &stubTool{name: "simple"}

	args := map[string]any{"x": "hello"}
	err := resolveArtifactParams(context.Background(), tool, args, toolCtx)
	if err != nil {
		t.Errorf("no-op resolution should not error: %v", err)
	}
	if args["x"] != "hello" {
		t.Errorf("args mutated: %v", args)
	}
}

func TestResolveNoArtifactService(t *testing.T) {
	toolCtx := &Context{
		UserID:    "user-1",
		SessionID: "sess-1",
		AgentName: "test-agent",
	}

	tool := &stubTool{
		name: "process",
		artifactParams: map[string]ArtifactParamInfo{
			"file": {},
		},
	}

	args := map[string]any{"file": "data.csv"}
	err := resolveArtifactParams(context.Background(), tool, args, toolCtx)
	if err != nil {
		t.Errorf("should not error with no artifact service: %v", err)
	}
}

func TestDispatchWithArtifactResolution(t *testing.T) {
	toolCtx, store := newTestToolCtx()
	ctx := context.Background()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "input.csv")
	_, _ = store.Save(ctx, loc, []byte("1,2,3"), "text/csv")

	var receivedArt *Artifact
	processedTool := &stubToolWithExec{
		stubTool: stubTool{
			name: "process_csv",
			artifactParams: map[string]ArtifactParamInfo{
				"csv_file": {},
			},
		},
		execFn: func(_ context.Context, args map[string]any, _ *Context) (*Result, error) {
			art, ok := args["csv_file"].(*Artifact)
			if ok {
				receivedArt = art
			}
			return OK("processed"), nil
		},
	}

	ts := NewSet()
	if err := ts.Register(processedTool); err != nil {
		t.Fatal(err)
	}

	result, err := ts.Dispatch(ctx, Call{
		ID:        "c1",
		Name:      "process_csv",
		Arguments: `{"csv_file": "input.csv"}`,
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if receivedArt == nil {
		t.Fatal("tool did not receive resolved artifact")
	}
	if string(receivedArt.Content) != "1,2,3" {
		t.Errorf("artifact content = %q", receivedArt.Content)
	}
}

type stubToolWithExec struct {
	stubTool
	execFn func(context.Context, map[string]any, *Context) (*Result, error)
}

func (s *stubToolWithExec) Execute(ctx context.Context, args map[string]any, toolCtx *Context) (*Result, error) {
	if s.execFn != nil {
		return s.execFn(ctx, args, toolCtx)
	}
	return OK("ok"), nil
}

func TestDispatchCodesAMissingArtifact(t *testing.T) {
	store := filesystem.New(t.TempDir())
	toolCtx, _ := newTestToolCtx()
	toolCtx.ArtifactService = &artifact.ScopedService{Inner: store, Scope: "agent", Namespace: "test-ns"}
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "sess-1", "data.txt")
	if _, err := store.Save(context.Background(), loc, []byte("v0"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	ts := NewSet()
	if err := ts.Register(&stubTool{name: "read", artifactParams: map[string]ArtifactParamInfo{"file": {}}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing artifact", `{"file":"nonexistent.txt"}`, taxonomy.ErrorCodeArtifactNotFound},
		{"missing version", `{"file":"data.txt:7"}`, taxonomy.ErrorCodeArtifactNotFound},
		{"wrong shape", `{"file":42}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ts.Dispatch(context.Background(), Call{Name: "read", Arguments: tc.args}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != StatusError {
				t.Fatalf("status = %q, want error", r.Status)
			}
			if r.ErrorCode != tc.want {
				t.Errorf("ErrorCode = %q, want %q", r.ErrorCode, tc.want)
			}
		})
	}
}
