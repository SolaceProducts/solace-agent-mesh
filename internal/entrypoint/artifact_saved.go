// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type SavedArtifact struct {
	Filename string
	MIMEType string
	Version  *int
	Tags     []string
}

func SavedArtifactsFromStatus(event *a2a.TaskStatusUpdateEvent) []SavedArtifact {
	if event == nil || event.Status.Message == nil {
		return nil
	}
	var saved []SavedArtifact
	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" {
			continue
		}
		if s, ok := savedArtifactFromData(part.Data); ok {
			saved = append(saved, s)
		}
	}
	return saved
}

func savedArtifactFromData(data any) (SavedArtifact, bool) {
	switch d := data.(type) {
	case map[string]any:
		filename, _ := d["filename"].(string)
		if d["type"] != string(a2a.SignalArtifactSaved) || filename == "" {
			return SavedArtifact{}, false
		}
		mimeType, _ := d["mime_type"].(string)
		return SavedArtifact{Filename: filename, MIMEType: mimeType, Version: DataIntPtr(d["version"]), Tags: tagStrings(d["tags"])}, true
	case a2a.ArtifactSavedData:
		return savedArtifactFromTyped(&d)
	case *a2a.ArtifactSavedData:
		if d == nil {
			return SavedArtifact{}, false
		}
		return savedArtifactFromTyped(d)
	}
	return SavedArtifact{}, false
}

func savedArtifactFromTyped(d *a2a.ArtifactSavedData) (SavedArtifact, bool) {
	if d.Type != string(a2a.SignalArtifactSaved) || d.Filename == "" {
		return SavedArtifact{}, false
	}
	version := d.Version
	return SavedArtifact{Filename: d.Filename, MIMEType: d.MIMEType, Version: &version, Tags: d.Tags}, true
}
