// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRegisterTools_MCPManifestFileIsResolvedByTheRuntime(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.yaml")
	manifest := "- name: get_weather\n  inputSchema: {type: object}\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	host := newTestHost("manifest-file-agent")
	stopSubComponents(t, host)

	entry := mcpEntry(manifestOnlyServerURL, map[string]any{"manifest_file": manifestPath}, nil)
	_, entries, err := RegisterTools(host, []any{entry}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if len(entries) != 1 || entries[0].Status != EntryStatusReady || !slices.Equal(entries[0].ToolNames, []string{"get_weather"}) {
		t.Fatalf("entries = %+v, want one ready entry registering get_weather from the manifest file", entries)
	}
}
