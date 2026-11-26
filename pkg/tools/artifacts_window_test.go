// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestLoadArtifactMarksItsContentWindow(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	var lines strings.Builder
	for i := 1; i <= 1_000; i++ {
		fmt.Fprintf(&lines, "line%d\n", i)
	}
	saveTestArtifact(context.Background(), svc, toolCtx, "rows.txt", lines.String(), "text/plain")
	saveTestArtifact(context.Background(), svc, toolCtx, "rows.txt", lines.String(), "text/plain")

	tests := []struct {
		name string
		args map[string]any
		want tool.ContentWindow
	}{
		{"no offset starts at line 1 of the latest version", map[string]any{},
			tool.ContentWindow{Filename: "rows.txt", Version: 1, FirstLine: 1}},
		{"offset 0 is treated as line 1", map[string]any{"offset": float64(0)},
			tool.ContentWindow{Filename: "rows.txt", Version: 1, FirstLine: 1}},
		{"offset and limit start the window at the offset", map[string]any{"offset": float64(501), "limit": float64(10)},
			tool.ContentWindow{Filename: "rows.txt", Version: 1, FirstLine: 501}},
		{"a pinned version is recorded", map[string]any{"version": float64(0), "offset": float64(20)},
			tool.ContentWindow{Filename: "rows.txt", Version: 0, FirstLine: 20}},
		{"line numbers do not move the window", map[string]any{"offset": float64(7), "include_line_numbers": true},
			tool.ContentWindow{Filename: "rows.txt", Version: 1, FirstLine: 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"filename": "rows.txt"}
			for k, v := range tt.args {
				args[k] = v
			}
			result, err := (&LoadArtifactTool{}).Execute(context.Background(), args, toolCtx)
			if err != nil || result.Status != tool.StatusSuccess {
				t.Fatalf("Execute: status %v, err %v", result.Status, err)
			}
			if result.ContentWindow == nil || *result.ContentWindow != tt.want {
				t.Errorf("ContentWindow = %+v, want %+v", result.ContentWindow, tt.want)
			}
		})
	}
}

func TestLoadArtifactOmitsTheWindowWhenTheVersionIsUnknown(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, "rows.txt", "older\n", "text/plain")
	saveTestArtifact(context.Background(), svc, toolCtx, "rows.txt", "latest\n", "text/plain")
	svc.failListVersions = map[string]bool{"rows.txt": true}

	result, err := (&LoadArtifactTool{}).Execute(context.Background(), map[string]any{"filename": "rows.txt"}, toolCtx)

	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute: status %v, err %v", result.Status, err)
	}
	if result.Data["content"] != "latest\n" {
		t.Errorf("content = %q; with no version asked for and none resolved, the latest is loaded", result.Data["content"])
	}
	if result.ContentWindow != nil {
		t.Errorf("ContentWindow = %+v; a window needs the version its content came from", result.ContentWindow)
	}
}

type saveAfterListing struct {
	*mockArtifactService
}

func (s saveAfterListing) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	versions, err := s.mockArtifactService.ListVersions(ctx, loc)
	_, _ = s.Save(ctx, loc, []byte("newer\n"), "text/plain")
	return versions, err
}

func TestLoadArtifactLoadsTheVersionItsWindowNames(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(saveAfterListing{svc})
	saveTestArtifact(context.Background(), svc, toolCtx, "rows.txt", "original\n", "text/plain")

	result, err := (&LoadArtifactTool{}).Execute(context.Background(), map[string]any{"filename": "rows.txt"}, toolCtx)

	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute: status %v, err %v", result.Status, err)
	}
	if result.ContentWindow == nil || result.ContentWindow.Version != 0 || result.Data["content"] != "original\n" {
		t.Errorf("window %+v with content %q; both must be version 0", result.ContentWindow, result.Data["content"])
	}
}
