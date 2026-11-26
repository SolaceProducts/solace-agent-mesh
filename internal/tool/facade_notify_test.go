// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type captureNotifier struct {
	mu    sync.Mutex
	calls []ArtifactSavedInfo
}

func (c *captureNotifier) hook() func(context.Context, ArtifactSavedInfo) {
	return func(_ context.Context, info ArtifactSavedInfo) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls = append(c.calls, info)
	}
}

func (c *captureNotifier) snapshot() []ArtifactSavedInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ArtifactSavedInfo, len(c.calls))
	copy(out, c.calls)
	return out
}

type failingArtifactService struct {
	*memArtifactService
	saveErr error
}

func (f *failingArtifactService) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	return 0, f.saveErr
}

func TestFacadeNotifyArtifactSaved_AfterSaveArtifactWithMetadata(t *testing.T) {
	ctx, _ := testContextWithArtifacts()
	cap := &captureNotifier{}
	ctx.NotifyArtifactSaved = cap.hook()
	ctx.ToolCallID = "call-xyz-42"

	f := ctx.Facade()
	_, err := f.SaveArtifactWithMetadata(context.Background(), "report.md",
		[]byte("# Title\nbody"), "text/markdown",
		artifact.MetadataOpts{
			Description: "generated report",
			Tags:        []string{"__generated"},
		})
	if err != nil {
		t.Fatalf("SaveArtifactWithMetadata: %v", err)
	}

	calls := cap.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 hook call, got %d: %+v", len(calls), calls)
	}
	got := calls[0]
	if got.Filename != "report.md" {
		t.Errorf("Filename = %q, want report.md", got.Filename)
	}
	if got.MIMEType != "text/markdown" {
		t.Errorf("MIMEType = %q", got.MIMEType)
	}
	if got.SizeBytes != int64(len("# Title\nbody")) {
		t.Errorf("SizeBytes = %d", got.SizeBytes)
	}
	if got.Description != "generated report" {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "__generated" {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.ToolCallID != "call-xyz-42" {
		t.Errorf("ToolCallID = %q, want the context-propagated value", got.ToolCallID)
	}
}

func TestFacadeNotifyArtifactSaved_AfterSaveArtifact(t *testing.T) {
	ctx, _ := testContextWithArtifacts()
	cap := &captureNotifier{}
	ctx.NotifyArtifactSaved = cap.hook()

	f := ctx.Facade()
	if _, err := f.SaveArtifact(context.Background(), "note.txt", []byte("hi"), "text/plain"); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	calls := cap.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 hook call, got %d", len(calls))
	}
	if calls[0].Filename != "note.txt" || calls[0].MIMEType != "text/plain" || calls[0].SizeBytes != 2 {
		t.Errorf("unexpected payload: %+v", calls[0])
	}
	if calls[0].Description != "" || len(calls[0].Tags) != 0 {
		t.Errorf("non-empty optional fields: %+v", calls[0])
	}
}

func TestFacadeNotifyArtifactSaved_NotCalledOnFailure(t *testing.T) {
	mem := newMemArtifactService()
	failing := &failingArtifactService{memArtifactService: mem, saveErr: errors.New("disk full")}

	ctx := testContext()
	ctx.ArtifactService = &artifact.ScopedService{
		Inner:     failing,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	cap := &captureNotifier{}
	ctx.NotifyArtifactSaved = cap.hook()

	f := ctx.Facade()
	if _, err := f.SaveArtifact(context.Background(), "x.txt", []byte("x"), "text/plain"); err == nil {
		t.Fatal("expected SaveArtifact to fail")
	}
	if _, err := f.SaveArtifactWithMetadata(context.Background(), "y.txt", []byte("y"), "text/plain", artifact.MetadataOpts{}); err == nil {
		t.Fatal("expected SaveArtifactWithMetadata to fail")
	}

	if len(cap.snapshot()) != 0 {
		t.Errorf("hook must not fire on save failure; got %d calls", len(cap.snapshot()))
	}
}

func TestFacadeNotifyArtifactSaved_NilHookIsNoOp(t *testing.T) {
	ctx, _ := testContextWithArtifacts()

	f := ctx.Facade()
	if _, err := f.SaveArtifact(context.Background(), "a.txt", []byte("a"), "text/plain"); err != nil {
		t.Fatalf("SaveArtifact should not error when hook is nil: %v", err)
	}
	if _, err := f.SaveArtifactWithMetadata(context.Background(), "b.txt", []byte("b"), "text/plain", artifact.MetadataOpts{}); err != nil {
		t.Fatalf("SaveArtifactWithMetadata should not error when hook is nil: %v", err)
	}
}

func TestFacadeNotifyArtifactSaved_PanicInHookDoesNotBreakSave(t *testing.T) {
	ctx, _ := testContextWithArtifacts()
	ctx.NotifyArtifactSaved = func(_ context.Context, _ ArtifactSavedInfo) {
		panic("hook went bad")
	}

	f := ctx.Facade()
	if _, err := f.SaveArtifact(context.Background(), "crash.txt", []byte("x"), "text/plain"); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if _, err := f.SaveArtifactWithMetadata(context.Background(), "crash2.txt", []byte("x"), "text/plain", artifact.MetadataOpts{}); err != nil {
		t.Fatalf("SaveArtifactWithMetadata: %v", err)
	}
}
