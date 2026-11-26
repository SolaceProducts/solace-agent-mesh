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

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func saveVersions(t *testing.T, toolCtx *tool.Context, svc artifact.Service, filename string, contents ...string) {
	t.Helper()
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", filename)
	for _, c := range contents {
		if _, err := svc.Save(context.Background(), loc, []byte(c), "text/plain"); err != nil {
			t.Fatalf("save %s: %v", filename, err)
		}
	}
}

func TestLoadArtifact_PinnedFilename(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveVersions(t, toolCtx, svc, "same.txt", "mine", "sibling")

	tests := []struct {
		name        string
		args        map[string]any
		wantContent string
		wantErr     string
	}{
		{"pinned version", map[string]any{"filename": "same.txt:0"}, "mine", ""},
		{"pin agrees with version", map[string]any{"filename": "same.txt:0", "version": float64(0)}, "mine", ""},
		{"pin conflicts with version", map[string]any{"filename": "same.txt:0", "version": float64(1)}, "", "pins version 0 but version is 1"},
		{"bare name is latest", map[string]any{"filename": "same.txt"}, "sibling", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := (&LoadArtifactTool{}).Execute(context.Background(), tt.args, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" {
				if result.Status != tool.StatusError || !strings.Contains(result.Message, tt.wantErr) {
					t.Fatalf("got %s %q, want an error containing %q", result.Status, result.Message, tt.wantErr)
				}
				return
			}
			if result.Status != tool.StatusSuccess {
				t.Fatalf("status = %s: %s", result.Status, result.Message)
			}
			if result.Data["content"] != tt.wantContent || result.Data["filename"] != "same.txt" {
				t.Errorf("loaded %v from %v, want %q from same.txt", result.Data["content"], result.Data["filename"], tt.wantContent)
			}
		})
	}
}

func TestAppendToArtifact_PinnedFilenameAppendsToThatVersion(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveVersions(t, toolCtx, svc, "same.txt", "mine", "sibling")
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "same.txt")
	for v, desc := range []string{"my description", "sibling description"} {
		if err := svc.SaveMetadata(context.Background(), loc, v, &artifact.VersionMetadata{Description: desc}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (&AppendToArtifactTool{}).Execute(context.Background(), map[string]any{
		"filename":      "same.txt:0",
		"content_chunk": "+more",
		"mime_type":     "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", result.Status, result.Message)
	}
	if result.Data["appended_from_version"] != 0 {
		t.Errorf("appended_from_version = %v, want the pinned version 0", result.Data["appended_from_version"])
	}
	loaded, err := (&LoadArtifactTool{}).Execute(context.Background(), map[string]any{"filename": "same.txt"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Data["content"] != "mine+more" {
		t.Errorf("latest = %q, want the pinned version extended: %q", loaded.Data["content"], "mine+more")
	}
	newVersion := result.Data["new_version"].(int)
	meta, err := svc.GetVersionMetadata(context.Background(), loc, &newVersion)
	if err != nil || meta == nil {
		t.Fatalf("metadata of v%d: %v, %v", newVersion, meta, err)
	}
	if meta.Description != "my description" {
		t.Errorf("description = %q, want the pinned version's, not a sibling's", meta.Description)
	}
}

func TestPinnedFilename_MissingVersion(t *testing.T) {
	svc := filesystem.New(t.TempDir())
	toolCtx := newTestToolCtx(svc)
	saveVersions(t, toolCtx, svc, "same.txt", "mine")
	ctx := context.Background()

	loaded, err := (&LoadArtifactTool{}).Execute(ctx, map[string]any{"filename": "same.txt:7"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != tool.StatusError || !strings.Contains(loaded.Message, "Available versions: [0]") {
		t.Errorf("load_artifact: got %s %q, want the missing version and the ones that exist", loaded.Status, loaded.Message)
	}

	appended, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "same.txt:7", "content_chunk": "x", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Status != tool.StatusError || !strings.Contains(appended.Message, "Available versions: [0]") || strings.Contains(appended.Message, "create") {
		t.Errorf("append_to_artifact: got %s %q, want the versions that exist and no create hint", appended.Status, appended.Message)
	}
	if loaded.ErrorCode != taxonomy.ErrorCodeArtifactNotFound || appended.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
		t.Errorf("ErrorCode = %q and %q, want %q for a version that does not exist", loaded.ErrorCode, appended.ErrorCode, taxonomy.ErrorCodeArtifactNotFound)
	}

	grepped, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": "mine", "filename": "same.txt:7"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(grepped.Message, "No matches") || !strings.Contains(grepped.Message, "version 7 not found") {
		t.Errorf("artifact_grep: got %s %q, want the missing version reported, not an empty search", grepped.Status, grepped.Message)
	}
}

func TestAppendToArtifact_UnknownSourceVersionIsOmitted(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveVersions(t, toolCtx, svc, "x.txt", "base")
	svc.failListVersions = map[string]bool{"x.txt": true}

	result, err := (&AppendToArtifactTool{}).Execute(context.Background(), map[string]any{
		"filename": "x.txt", "content_chunk": "+more", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", result.Status, result.Message)
	}
	if v, ok := result.Data["appended_from_version"]; ok {
		t.Errorf("appended_from_version = %v, want it absent when unknown", v)
	}
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "x.txt")
	newVersion := result.Data["new_version"].(int)
	if meta, _ := svc.GetVersionMetadata(context.Background(), loc, &newVersion); meta != nil {
		if v, ok := meta.Custom["appended_from_version"]; ok {
			t.Errorf("metadata appended_from_version = %v, want it absent when unknown", v)
		}
	}
}

func TestAppendToArtifact_PinnedListFailureIsReported(t *testing.T) {
	svc := filesystemWithListFailure{Store: filesystem.New(t.TempDir()), fail: "nope.txt"}
	toolCtx := newTestToolCtx(svc)

	result, err := (&AppendToArtifactTool{}).Execute(context.Background(), map[string]any{
		"filename": "nope.txt:3", "content_chunk": "x", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError || !strings.Contains(result.Message, "could not be listed") || strings.Contains(result.Message, "create") {
		t.Errorf("got %s %q, want the list failure reported without a create hint", result.Status, result.Message)
	}
}

type filesystemWithListFailure struct {
	*filesystem.Store
	fail string
}

func (s filesystemWithListFailure) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	if loc.Filename == s.fail {
		return nil, errors.New("list throttled")
	}
	return s.Store.ListVersions(ctx, loc)
}

func TestArtifactGrep_PinnedMissingNameIsNotFound(t *testing.T) {
	svc := filesystem.New(t.TempDir())
	toolCtx := newTestToolCtx(svc)

	result, err := (&GrepTool{}).Execute(context.Background(), map[string]any{"pattern": "x", "filename": "nope.txt:3"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Message, "No matches") || strings.Contains(result.Message, "version 3") || !strings.Contains(result.Message, "not found") {
		t.Errorf("got %s %q, want a plain not-found", result.Status, result.Message)
	}
}

func TestAppendToArtifact_PinnedMissingNameKeepsCreateHint(t *testing.T) {
	svc := filesystem.New(t.TempDir())
	toolCtx := newTestToolCtx(svc)

	result, err := (&AppendToArtifactTool{}).Execute(context.Background(), map[string]any{
		"filename": "nope.txt:3", "content_chunk": "x", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError || strings.Contains(result.Message, "Available versions") || !strings.Contains(result.Message, "no versions") {
		t.Errorf("got %s %q, want the no-versions error", result.Status, result.Message)
	}
	if result.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ErrorCodeArtifactNotFound)
	}
}

func TestDeleteArtifact_VersionedFilenameIsRejected(t *testing.T) {
	for _, ref := range []string{"same.txt:0", "same.txt:latest"} {
		t.Run(ref, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			saveVersions(t, toolCtx, svc, "same.txt", "mine")

			result, err := (&DeleteArtifactTool{}).Execute(context.Background(), map[string]any{
				"filename":       ref,
				"confirm_delete": true,
			}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, "pass 'same.txt'") {
				t.Fatalf("got %s %q, want an error naming the bare filename to pass", result.Status, result.Message)
			}
			wantWording := "names a single version"
			if strings.HasSuffix(ref, ":latest") {
				wantWording = "is a version reference"
			}
			if !strings.Contains(result.Message, wantWording) {
				t.Errorf("message = %q, want it to say the reference %s", result.Message, wantWording)
			}
			if result.Data["filename"] != ref {
				t.Errorf("data filename = %v, want the argument the caller passed, %q", result.Data["filename"], ref)
			}
			loaded, _ := (&LoadArtifactTool{}).Execute(context.Background(), map[string]any{"filename": "same.txt"}, toolCtx)
			if loaded.Data["content"] != "mine" {
				t.Errorf("artifact was deleted: %v", loaded.Data)
			}
		})
	}
}

func TestArtifactGrep_PinnedFilename(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveVersions(t, toolCtx, svc, "same.txt", "needle in mine", "only hay")

	result, err := (&GrepTool{}).Execute(context.Background(), map[string]any{
		"pattern":  "needle",
		"filename": "same.txt:0",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess || !strings.Contains(result.Message, "1 matching") {
		t.Errorf("got %s %q, want the match from the pinned version", result.Status, result.Message)
	}

	conflict, err := (&GrepTool{}).Execute(context.Background(), map[string]any{
		"pattern":  "needle",
		"filename": "same.txt:0",
		"version":  float64(1),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Status != tool.StatusError || !strings.Contains(conflict.Message, "pins version 0 but version is 1") {
		t.Errorf("got %s %q, want the pin/version conflict error", conflict.Status, conflict.Message)
	}
}
