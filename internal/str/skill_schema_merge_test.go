// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import "testing"

func TestMergeDiscoveredSchemaIntoSkillEntry_Timeout(t *testing.T) {
	t.Parallel()

	t.Run("adopts the tool-declared timeout when the manifest is silent", func(t *testing.T) {
		t.Parallel()
		entry := &ManifestEntry{ToolName: "sk__slow"}
		mergeDiscoveredSchemaIntoSkillEntry(entry, DiscoveredTool{TimeoutSeconds: 120})
		if entry.TimeoutSeconds != 120 {
			t.Errorf("TimeoutSeconds = %d, want the tool-declared 120", entry.TimeoutSeconds)
		}
	})

	t.Run("does not override a manifest-declared timeout", func(t *testing.T) {
		t.Parallel()
		entry := &ManifestEntry{ToolName: "sk__slow", TimeoutSeconds: 90}
		mergeDiscoveredSchemaIntoSkillEntry(entry, DiscoveredTool{TimeoutSeconds: 120})
		if entry.TimeoutSeconds != 90 {
			t.Errorf("TimeoutSeconds = %d, want the manifest's 90 to win", entry.TimeoutSeconds)
		}
	})

	t.Run("leaves the entry alone when the tool declares nothing", func(t *testing.T) {
		t.Parallel()
		entry := &ManifestEntry{ToolName: "sk__slow"}
		mergeDiscoveredSchemaIntoSkillEntry(entry, DiscoveredTool{})
		if entry.TimeoutSeconds != 0 {
			t.Errorf("TimeoutSeconds = %d, want 0 so the sandbox default applies", entry.TimeoutSeconds)
		}
	})
}
