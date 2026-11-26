// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newDocTool() *Tool {
	return NewTool(&ToolConfig{
		ToolName: "html_to_pdf",
		Parameters: map[string]ParamConfig{
			"input_document": {Type: "artifact", Required: true},
			"images":         {Type: "artifact_list"},
		},
	}, nil)
}

func saveTwice(t *testing.T, toolCtx *tool.Context, name string) {
	t.Helper()
	svc := toolCtx.ArtifactService
	loc := svc.Resolve("a", "u", "s", name)
	for range 2 {
		if _, err := svc.Inner.Save(context.Background(), loc, []byte("x"), "text/html"); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
}

func TestRefuseMissingArtifacts(t *testing.T) {
	t.Parallel()
	const savedHint = "Closest saved files: report.html."
	tests := []struct {
		name        string
		saved       []string
		args        map[string]any
		wantMessage string
	}{
		{
			name:  "saved file passes",
			saved: []string{"report.html"},
			args:  map[string]any{"input_document": "report.html"},
		},
		{
			name:  "pinned existing version passes",
			saved: []string{"report.html"},
			args:  map[string]any{"input_document": "report.html:1"},
		},
		{
			name:  "colon in the name resolves as a whole name",
			saved: []string{"Q3: Review.yaml"},
			args:  map[string]any{"input_document": "Q3: Review.yaml"},
		},
		{
			name:  "colon name pinned to a version resolves",
			saved: []string{"Q3: Review.yaml"},
			args:  map[string]any{"input_document": "Q3: Review.yaml:1"},
		},
		{
			name:  "latest suffix resolves to the plain name",
			saved: []string{"report.html"},
			args:  map[string]any{"input_document": "report.html:latest"},
		},
		{
			name:        "unsaved name lists the closest saved files and says to save first",
			saved:       []string{"report.html", "notes.txt"},
			args:        map[string]any{"input_document": "report.htm"},
			wantMessage: "Artifact 'report.htm' is not saved in this session. " + savedHint + " " + saveArtifactFirstHint,
		},
		{
			name:        "a very short saved name is not offered as close",
			saved:       []string{"a", "x.md"},
			args:        map[string]any{"input_document": "report.html"},
			wantMessage: "Artifact 'report.html' is not saved in this session. " + saveArtifactFirstHint,
		},
		{
			name:        "empty session says nothing is saved",
			args:        map[string]any{"input_document": "report.html"},
			wantMessage: "Artifact 'report.html' is not saved in this session. No files are saved in this session yet. " + saveArtifactFirstHint,
		},
		{
			name:        "missing version lists the available ones",
			saved:       []string{"report.html"},
			args:        map[string]any{"input_document": "report.html:7"},
			wantMessage: "Version 7 of artifact 'report.html' does not exist. Available versions: [0 1].",
		},
		{
			name:        "negative version other than latest is a syntax error",
			saved:       []string{"report.html"},
			args:        map[string]any{"input_document": "report.html:-2"},
			wantMessage: "Invalid version in 'report.html:-2'. Use a positive version number such as 'name:2', or omit it (or use ':-1') for the latest.",
		},
		{
			name:  "explicit latest -1 passes",
			saved: []string{"report.html"},
			args:  map[string]any{"input_document": "report.html:-1"},
		},
		{
			name:        "a missing entry in a list is refused",
			saved:       []string{"report.html"},
			args:        map[string]any{"input_document": "report.html", "images": []any{"chart.png"}},
			wantMessage: "Artifact 'chart.png' is not saved in this session. " + saveArtifactFirstHint,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toolCtx := newAutoResolveCtx(t, nil)
			for _, name := range tc.saved {
				saveTwice(t, toolCtx, name)
			}

			got := newDocTool().refuseMissingArtifacts(context.Background(), tc.args, toolCtx)

			if tc.wantMessage == "" {
				if got != nil {
					t.Fatalf("refused a saved reference: %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("a missing reference was not refused, want %q", tc.wantMessage)
			}
			if got.Status != tool.StatusError || got.Message != tc.wantMessage || got.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
				t.Errorf("result = {%v %q %q}, want {%v %q %q}", got.Status, got.Message, got.ErrorCode,
					tool.StatusError, tc.wantMessage, taxonomy.ErrorCodeArtifactNotFound)
			}
		})
	}
}

func TestRefuseMissingArtifactsWithoutArtifactService(t *testing.T) {
	t.Parallel()
	toolCtx := &tool.Context{UserID: "u", SessionID: "s", AgentName: "a"}

	got := newDocTool().refuseMissingArtifacts(context.Background(), map[string]any{"input_document": "report.html"}, toolCtx)

	if got != nil {
		t.Errorf("refused without an artifact service: %+v", got)
	}
}

func TestBuildArtifactReferencesKeepsColonNamesAndVersions(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"input_document": "Q3: Review.yaml",
		"images":         []any{"a.png:2", "Q3: Chart.png:latest", "Q3: Chart.png:4"},
	}

	got := newDocTool().buildArtifactReferences(args)

	want := artifactRefs(map[string]artifactRefWant{
		"input_document": {"Q3: Review.yaml", -1},
		"images[0]":      {"a.png", 2},
		"images[1]":      {"Q3: Chart.png", -1},
		"images[2]":      {"Q3: Chart.png", 4},
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("references = %+v, want %+v", got, want)
	}
}

type artifactRefWant struct {
	name    string
	version int
}

func artifactRefs(in map[string]artifactRefWant) map[string]remotetool.ArtifactReference {
	out := make(map[string]remotetool.ArtifactReference, len(in))
	for k, v := range in {
		out[k] = remotetool.ArtifactReference{Filename: v.name, Version: v.version}
	}
	return out
}

func TestParseArtifactSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		spec        string
		wantName    string
		wantVersion int
	}{
		{"report.html", "report.html", -1},
		{"report.html:3", "report.html", 3},
		{"report.html:0", "report.html", 0},
		{"report.html:latest", "report.html", -1},
		{"Q3: Review.yaml", "Q3: Review.yaml", -1},
		{"Q3: Review.yaml:2", "Q3: Review.yaml", 2},
		{"a:b.md:2", "a:b.md", 2},
		{"a:b.md", "a:b.md", -1},
		{"report.html:-1", "report.html", -1},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			t.Parallel()
			name, version := parseArtifactSpec(tc.spec)
			if name != tc.wantName || version != tc.wantVersion {
				t.Errorf("parseArtifactSpec(%q) = (%q, %d), want (%q, %d)", tc.spec, name, version, tc.wantName, tc.wantVersion)
			}
		})
	}
}

func executeDocTool(t *testing.T, args map[string]any, saved map[string]string) (*tool.Result, int) {
	t.Helper()
	return executeDocToolIn(t, args, newAutoResolveCtx(t, saved))
}

func executeDocToolIn(t *testing.T, args map[string]any, toolCtx *tool.Context) (*tool.Result, int) {
	t.Helper()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	invokeQ, err := brk.CreateQueue("capture-invokes", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue(capture-invokes): %v", err)
	}
	if err = brk.Subscribe(invokeQ, remotetool.InvokeTopic(testNamespace, "html_to_pdf")); err != nil {
		t.Fatalf("Subscribe(html_to_pdf invokes): %v", err)
	}
	var invokes atomic.Int32
	go func() {
		for {
			msg, recvErr := brk.Receive(t.Context(), invokeQ)
			if recvErr != nil {
				return
			}
			msg.Ack()
			invokes.Add(1)
			var req a2a.Request
			if json.Unmarshal(msg.Payload, &req) != nil {
				continue
			}
			payload, _ := json.Marshal(a2a.NewResponse(req.ID, remotetool.InvokeResult{ToolResult: map[string]any{"status": "success"}}))
			_ = brk.Publish(context.Background(), msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: payload})
		}
	}()

	cfg := newDocTool().cfg
	cfg.TimeoutSeconds = 5
	result, err := NewTool(cfg, exe).Execute(t.Context(), args, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return result, int(invokes.Load())
}

func TestExecuteRefusesMissingArtifactWithoutInvoking(t *testing.T) {
	t.Parallel()

	result, invokes := executeDocTool(t, map[string]any{"input_document": "report.html"}, map[string]string{"notes.txt": "x"})

	if result.Status != tool.StatusError || result.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
		t.Errorf("result = {%v %q}, want an %s error", result.Status, result.ErrorCode, taxonomy.ErrorCodeArtifactNotFound)
	}
	if invokes != 0 {
		t.Errorf("published %d invokes, want 0", invokes)
	}
}

func TestExecuteChecksReferencesAfterEmbedsResolve(t *testing.T) {
	t.Parallel()
	saved := map[string]string{"pointer.txt": "report.html", "report.html": "<html></html>"}

	result, invokes := executeDocTool(t, map[string]any{"input_document": "«artifact_content:pointer.txt»"}, saved)

	if result.Status != tool.StatusSuccess || invokes != 1 {
		t.Errorf("result = %v with %d invokes, want success with 1 (the embed names a saved file)", result.Status, invokes)
	}
}

func TestExecuteAcceptsFileSavedByASaveBlockEarlierInTheTurn(t *testing.T) {
	t.Parallel()
	toolCtx := newAutoResolveCtx(t, nil)
	_, err := artifact.SaveWithMetadata(context.Background(), toolCtx.ArtifactService,
		toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, "Q3: report.html",
		[]byte("<html></html>"), "text/html", artifact.MetadataOpts{Description: "report"}, slog.Default())
	if err != nil {
		t.Fatalf("save as the fenced block does: %v", err)
	}

	result, invokes := executeDocToolIn(t, map[string]any{"input_document": "Q3: report.html"}, toolCtx)

	if result.Status != tool.StatusSuccess || invokes != 1 {
		t.Errorf("result = %v with %d invokes, want success with 1 (the file was saved this turn)", result.Status, invokes)
	}
}

func TestRefuseMissingArtifactsAsksTheStoreOncePerName(t *testing.T) {
	t.Parallel()
	toolCtx := newAutoResolveCtx(t, nil)
	counting := &countingStore{Service: toolCtx.ArtifactService.Inner}
	toolCtx.ArtifactService.Inner = counting
	saveTwice(t, toolCtx, "report.html")
	args := map[string]any{
		"input_document": "report.html",
		"images":         []any{"report.html", "report.html:latest", "report.html:1", "report.html:1"},
	}

	got := newDocTool().refuseMissingArtifacts(context.Background(), args, toolCtx)

	if got != nil {
		t.Fatalf("refused saved references: %+v", got)
	}
	if counting.listKeys.Load() != 0 || counting.listVersions.Load() != 1 {
		t.Errorf("store calls = %d key listings and %d version listings, want 0 and 1",
			counting.listKeys.Load(), counting.listVersions.Load())
	}
}

func TestExecuteInvokesWhenTheListingOmitsAResolvableName(t *testing.T) {
	t.Parallel()
	toolCtx := newAutoResolveCtx(t, map[string]string{"report.html": "<html></html>"})
	toolCtx.ArtifactService.Inner = &blindListingStore{Service: toolCtx.ArtifactService.Inner}

	result, invokes := executeDocToolIn(t, map[string]any{"input_document": "report.html"}, toolCtx)

	if result.Status != tool.StatusSuccess || invokes != 1 {
		t.Errorf("result = %v with %d invokes, want success with 1 (the file loads although ListKeys omits it)", result.Status, invokes)
	}
}

type blindListingStore struct {
	artifact.Service
}

func (b *blindListingStore) ListKeys(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, nil
}

type countingStore struct {
	artifact.Service
	listKeys     atomic.Int32
	listVersions atomic.Int32
}

func (c *countingStore) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	c.listKeys.Add(1)
	return c.Service.ListKeys(ctx, scope)
}

func (c *countingStore) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	c.listVersions.Add(1)
	return c.Service.ListVersions(ctx, loc)
}
