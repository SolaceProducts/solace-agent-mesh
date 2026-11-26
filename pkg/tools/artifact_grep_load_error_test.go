// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type failingArtifactService struct {
	*mockArtifactService
	failFor map[string]bool
	goneFor map[string]bool
}

func (f *failingArtifactService) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	if f.failFor[loc.Filename] {
		return nil, "", errors.New("503 from object store")
	}
	if f.goneFor[loc.Filename] {
		return nil, "", nil
	}
	return f.mockArtifactService.Load(ctx, loc, version)
}

func TestGrepFailsWhenNoArtifactCouldBeRead(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := t.Context()
	saveTestArtifact(ctx, svc, toolCtx, "notes.md", "the term is here", "text/plain")

	failing := &failingArtifactService{mockArtifactService: svc, failFor: map[string]bool{"notes.md": true}}
	failCtx := newTestToolCtx(failing)

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "term", "filename": "notes.md"}, failCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error: nothing was searched, so a success reads as "+
			"proof the pattern is absent. message = %q", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "503") {
		t.Errorf("message drops the reason the read failed, so the caller cannot tell a "+
			"missing file from an outage: %q", result.Message)
	}
	if result.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want none: a store that failed to answer is not the agent's to fix", result.ErrorCode)
	}
}

func TestGrepUnreadableScanCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		failFor  map[string]bool
		goneFor  map[string]bool
		wantCode string
	}{
		{name: "missing then unreadable", args: map[string]any{"pattern": "term", "version": float64(0)},
			goneFor: map[string]bool{"a.md": true}, failFor: map[string]bool{"b.md": true}},
		{name: "unreadable then missing", args: map[string]any{"pattern": "term", "version": float64(0)},
			failFor: map[string]bool{"a.md": true}, goneFor: map[string]bool{"b.md": true}},
		{name: "every listed artifact gone when none was named", args: map[string]any{"pattern": "term"},
			goneFor: map[string]bool{"a.md": true, "b.md": true}},
		{name: "every artifact gone at the version named", args: map[string]any{"pattern": "term", "version": float64(0)},
			goneFor: map[string]bool{"a.md": true, "b.md": true}, wantCode: taxonomy.ErrorCodeArtifactNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			saveTestArtifact(t.Context(), svc, toolCtx, "a.md", "the term is here", "text/plain")
			saveTestArtifact(t.Context(), svc, toolCtx, "b.md", "the term is here", "text/plain")

			failing := &failingArtifactService{mockArtifactService: svc, failFor: tc.failFor, goneFor: tc.goneFor}

			result, err := (&GrepTool{}).Execute(t.Context(), tc.args, newTestToolCtx(failing))
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, "could be read") {
				t.Fatalf("result = %s: %q, want the nothing-read error", result.Status, result.Message)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestGrepReportsPartialReadFailures(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "a.md", "the term is here", "text/plain")
	saveTestArtifact(ctx, svc, toolCtx, "b.md", "nothing relevant", "text/plain")

	failing := &failingArtifactService{mockArtifactService: svc, failFor: map[string]bool{"b.md": true}}
	failCtx := newTestToolCtx(failing)

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "term"}, failCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success: a.md was read and matched", result.Status)
	}
	if names, _ := result.Data["skipped_files"].([]string); len(names) != 1 || names[0] != "b.md" {
		t.Errorf("skipped_files = %v, want [b.md] — an unread file must not vanish from a "+
			"result the model will treat as complete", result.Data["skipped_files"])
	}
}

func TestGrepFailsWhenCancelled(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "a.md", "the term is here", "text/plain")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "term", "filename": "a.md"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("a cancelled scan reported status %q with message %q — the caller cannot "+
			"tell it from a search that completed and found nothing", result.Status, result.Message)
	}
	if result.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want none for a cancelled scan", result.ErrorCode)
	}
}

func TestGrepPastDeadlineIsATimeout(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, "a.md", "the term is here", "text/plain")

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": "term", "filename": "a.md"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError || !strings.Contains(result.Message, "Search stopped before completing") {
		t.Fatalf("result = %s: %q, want the stopped search", result.Status, result.Message)
	}
	if want := tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
}

func TestGrepMixedBinaryAndUnreadableDoesNotClaimNoMatches(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()
	saveTestArtifact(ctx, svc, toolCtx, "report.pdf", string(buildTestPDF("quarterly revenue")), mimePDF)
	saveTestArtifact(ctx, svc, toolCtx, "notes.md", "the term is here", "text/plain")

	failing := &failingArtifactService{mockArtifactService: svc, failFor: map[string]bool{"notes.md": true}}
	failCtx := newTestToolCtx(failing)

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "term"}, failCtx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Message, "No matches found") {
		t.Errorf("nothing was searched — one file was binary, the other unreadable — yet the "+
			"result asserts the pattern is absent: %q", result.Message)
	}
	for _, want := range []string{"report.pdf", "notes.md"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message does not account for %s: %q", want, result.Message)
		}
	}
}
