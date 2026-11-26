// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSkillsDoesNotPreemptInProcessBinding(t *testing.T) {
	const (
		skillName = "sam-bridgetest"
		toolName  = "bridgetool"
	)
	key := skillName + SkillToolSep + toolName

	withGlobalInProcessTool(t, newBridgeFixtureTool(toolName))

	skillsDir := t.TempDir()
	writeRuntimeTestSkillMetadataOnly(t, skillsDir, skillName, toolName)

	discovered := DiscoverSkills(skillsDir, t.TempDir(), "test-ns", testLogger())

	if discovered.ManifestPath != "" {
		t.Errorf("ManifestPath = %q, want empty: the only bundled tool resolves in-process, "+
			"so the generated manifest would describe nothing a worker needs to exec",
			discovered.ManifestPath)
	}

	sources := []string{writeEmptyManifest(t)}
	if discovered.ManifestPath != "" {
		sources = append(sources, discovered.ManifestPath)
	}
	w := NewWorker(&testServices{brk: newTestBroker(t)}, WorkerConfig{
		WorkerID:      "test-preemption",
		Namespace:     "test-ns",
		ManifestPaths: sources,
		SkillsDir:     skillsDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})
	ctx := context.Background()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(ctx) })

	entry := w.manifest.GetTool(key)
	if entry == nil {
		t.Fatalf("%q is not in the manifest at all", key)
	}
	if entry.inProcess == nil {
		t.Errorf("%q lost its in-process binding: a file entry from the generated manifest "+
			"pre-empted the registration that carries it, and that file entry points at a "+
			"binary which is not on disk", key)
	}
	if unservable, reason := w.manifest.UnservableState(entry); unservable {
		t.Errorf("%q is unservable (%s); the in-process handler needs no binary on disk",
			key, reason)
	}
}

func TestDiscoverSkillsWritesSubprocessToolsToManifest(t *testing.T) {
	const (
		skillName = "plainskill"
		toolName  = "plaintool"
	)

	skillsDir := t.TempDir()
	writeRuntimeTestSkill(t, skillsDir, skillName, toolName)

	discovered := DiscoverSkills(skillsDir, t.TempDir(), "test-ns", testLogger())
	if discovered.ManifestPath == "" {
		t.Fatal("no manifest written for a subprocess-only skill tool")
	}

	data, err := os.ReadFile(discovered.ManifestPath)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	body := string(data)
	wantKey := skillName + SkillToolSep + toolName
	if !strings.Contains(body, wantKey) {
		t.Errorf("generated manifest does not name %q; contents:\n%s", wantKey, body)
	}
	if !strings.Contains(body, "executable:") {
		t.Errorf("generated manifest carries no executable path; contents:\n%s", body)
	}
}

func writeEmptyManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
