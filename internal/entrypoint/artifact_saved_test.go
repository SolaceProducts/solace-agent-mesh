// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func statusWithData(t *testing.T, data any, decode bool) *a2a.TaskStatusUpdateEvent {
	t.Helper()
	event := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{a2a.NewDataPart(data)},
	}}}
	if !decode {
		return event
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &decoded
}

func TestSavedArtifactsFromStatus(t *testing.T) {
	t.Parallel()
	two := 2
	cases := []struct {
		name   string
		data   any
		decode bool
		want   *SavedArtifact
	}{
		{
			name:   "conforming signal",
			data:   map[string]any{"type": "artifact_saved", "filename": "a.md", "mime_type": "text/markdown", "version": 2, "tags": []string{"x"}},
			decode: true,
			want:   &SavedArtifact{Filename: "a.md", MIMEType: "text/markdown", Version: &two, Tags: []string{"x"}},
		},
		{
			name:   "string version",
			data:   map[string]any{"type": "artifact_saved", "filename": "a.md", "version": "2"},
			decode: true,
			want:   &SavedArtifact{Filename: "a.md", Version: &two},
		},
		{
			name:   "missing version means latest",
			data:   map[string]any{"type": "artifact_saved", "filename": "a.md"},
			decode: true,
			want:   &SavedArtifact{Filename: "a.md"},
		},
		{
			name:   "loosely typed tags and description keep the save",
			data:   map[string]any{"type": "artifact_saved", "filename": "a.md", "version": 2, "tags": "x", "description": 7},
			decode: true,
			want:   &SavedArtifact{Filename: "a.md", Version: &two},
		},
		{
			name: "typed struct",
			data: a2a.ArtifactSavedData{Type: "artifact_saved", Filename: "a.md", Version: 2, MIMEType: "text/markdown"},
			want: &SavedArtifact{Filename: "a.md", MIMEType: "text/markdown", Version: &two},
		},
		{
			name: "typed struct pointer",
			data: &a2a.ArtifactSavedData{Type: "artifact_saved", Filename: "a.md", Version: 2},
			want: &SavedArtifact{Filename: "a.md", Version: &two},
		},
		{
			name:   "other signal type",
			data:   map[string]any{"type": "artifact_creation_progress", "filename": "a.md"},
			decode: true,
		},
		{
			name:   "no filename",
			data:   map[string]any{"type": "artifact_saved", "version": 2},
			decode: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SavedArtifactsFromStatus(statusWithData(t, tc.data, tc.decode))
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no saves", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d saves, want 1", len(got))
			}
			assertSavedArtifact(t, got[0], *tc.want)
		})
	}
}

func assertSavedArtifact(t *testing.T, got, want SavedArtifact) {
	t.Helper()
	if got.Filename != want.Filename || got.MIMEType != want.MIMEType {
		t.Errorf("got %+v, want %+v", got, want)
	}
	switch {
	case (got.Version == nil) != (want.Version == nil):
		t.Errorf("version = %v, want %v", got.Version, want.Version)
	case got.Version != nil && *got.Version != *want.Version:
		t.Errorf("version = %d, want %d", *got.Version, *want.Version)
	}
	if len(got.Tags) != len(want.Tags) {
		t.Errorf("tags = %v, want %v", got.Tags, want.Tags)
	}
}
