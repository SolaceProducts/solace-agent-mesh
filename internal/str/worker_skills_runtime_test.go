// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileSkillChanges_RuntimeArrival(t *testing.T) {
	skillsDir := t.TempDir()

	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := WorkerConfig{
		WorkerID:      "test-worker-runtime-skill",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     skillsDir,
		Sandbox: SandboxConfig{
			Mode: "direct",
		},
	}
	worker := NewWorker(svc, cfg)

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	if got := countBundledKeys(worker.manifest.EntriesByKey()); got != 0 {
		t.Fatalf("at boot: expected 0 bundled skill tools, got %d", got)
	}

	const skillName = "dates_kit"
	const toolName = "dates"
	writeRuntimeTestSkill(t, skillsDir, skillName, toolName)

	worker.reconcileSkillChanges(ctx)

	prefixedKey := skillName + SkillToolSep + toolName
	if entry := worker.manifest.GetTool(prefixedKey); entry == nil {
		keys := make([]string, 0, len(worker.manifest.EntriesByKey()))
		for k := range worker.manifest.EntriesByKey() {
			keys = append(keys, k)
		}
		t.Fatalf("after reconcile: bundled tool %q not in manifest; keys=%v", prefixedKey, keys)
	}

	worker.subsMu.Lock()
	_, subscribed := worker.subscribedTools[prefixedKey]
	worker.subsMu.Unlock()
	if !subscribed {
		t.Fatalf("after reconcile: worker did not subscribe to invoke topic for %q", prefixedKey)
	}

	if err := os.RemoveAll(filepath.Join(skillsDir, skillName)); err != nil {
		t.Fatalf("remove skill dir: %v", err)
	}
	worker.reconcileSkillChanges(ctx)

	if entry := worker.manifest.GetTool(prefixedKey); entry != nil {
		t.Errorf("after skill removal: expected %q unregistered, but still in manifest", prefixedKey)
	}
	worker.subsMu.Lock()
	_, stillSubscribed := worker.subscribedTools[prefixedKey]
	worker.subsMu.Unlock()
	if stillSubscribed {
		t.Errorf("after skill removal: worker still subscribed to %q invoke topic", prefixedKey)
	}
}

func TestReconcileSkillChanges_ImageBuiltinBinaryWins(t *testing.T) {
	const skillName = "sam-datekit"
	const toolName = "dates"

	builtinDir := t.TempDir()
	writeRuntimeTestSkill(t, builtinDir, skillName, toolName)
	t.Setenv("SAM_BUILTIN_SKILLS_DIR", builtinDir)

	skillsDir := t.TempDir()
	writeRuntimeTestSkillMetadataOnly(t, skillsDir, skillName, toolName)

	brk := newTestBroker(t)
	svc := &testServices{brk: brk}
	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker-image-builtin",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     skillsDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	prefixedKey := skillName + SkillToolSep + toolName
	entry := worker.manifest.GetTool(prefixedKey)
	if entry == nil {
		t.Fatalf("bundled tool %q not registered; the image-baked builtin (with binary) must win over the metadata-only mirror", prefixedKey)
	}

	wantPrefix := filepath.Clean(builtinDir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(entry.Executable), wantPrefix) {
		t.Errorf("Executable = %q, want a path under the builtin dir %q (not the binary-less mirror)", entry.Executable, builtinDir)
	}
	if !entryExecutableAvailable(entry) {
		t.Errorf("resolved executable %q is not runnable", entry.Executable)
	}

	worker.reconcileSkillChanges(ctx)
	e2 := worker.manifest.GetTool(prefixedKey)
	if e2 == nil {
		t.Fatalf("after re-reconcile: bundled tool %q dropped", prefixedKey)
	}
	if filepath.Clean(e2.Executable) != filepath.Clean(entry.Executable) {
		t.Errorf("after re-reconcile: executable changed from %q to %q", entry.Executable, e2.Executable)
	}
}

func writeRuntimeTestSkillMetadataOnly(t *testing.T, skillsDir, skillName, toolName string) {
	t.Helper()
	writeRuntimeTestSkill(t, skillsDir, skillName, toolName)
	if err := os.Remove(filepath.Join(skillsDir, skillName, "tools", toolName)); err != nil {
		t.Fatalf("strip mirror executable: %v", err)
	}
}

func countBundledKeys(entries map[string]*ManifestEntry) int {
	n := 0
	for k, e := range entries {
		if e == nil || e.IsBuiltin {
			continue
		}
		if containsSkillSep(k) {
			n++
		}
	}
	return n
}

func containsSkillSep(s string) bool {
	for i := 0; i+len(SkillToolSep) <= len(s); i++ {
		if s[i:i+len(SkillToolSep)] == SkillToolSep {
			return true
		}
	}
	return false
}

func TestReconcileSkillChanges_ModifiedRebuildsManifestEntry(t *testing.T) {
	skillsDir := t.TempDir()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker-modified",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     skillsDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	const skillName = "modkit"
	writeRuntimeTestSkill(t, skillsDir, skillName, "first_tool")
	worker.reconcileSkillChanges(ctx)
	if worker.manifest.GetTool(skillName+SkillToolSep+"first_tool") == nil {
		t.Fatal("after first reconcile: first_tool not registered")
	}

	writeRuntimeTestSkill(t, skillsDir, skillName, "second_tool")
	worker.reconcileSkillChanges(ctx)

	if worker.manifest.GetTool(skillName+SkillToolSep+"second_tool") == nil {
		t.Errorf("after modify: second_tool not registered")
	}
	if worker.manifest.GetTool(skillName+SkillToolSep+"first_tool") != nil {
		t.Errorf("after modify: stale first_tool should have been unregistered")
	}
}

func TestReconcileSkillChanges_RegistryReloadErrorIsTolerated(t *testing.T) {
	skillsDir := t.TempDir()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker-reload-err",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     skillsDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	if err := os.RemoveAll(skillsDir); err != nil {
		t.Fatal(err)
	}
	worker.reconcileSkillChanges(ctx)
}

func TestReconcileSkillChanges_ExposeGloballyRoundTrip(t *testing.T) {
	skillsDir := t.TempDir()
	brk := newTestBroker(t)
	svc := &testServices{brk: brk}

	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(svc, WorkerConfig{
		WorkerID:      "test-worker-global",
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     skillsDir,
		Sandbox:       SandboxConfig{Mode: "direct"},
	})

	ctx := context.Background()
	if err := worker.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(ctx) })

	const skillName = "globalkit"
	const toolName = "datestool"
	writeRuntimeTestSkillExposed(t, skillsDir, skillName, toolName)
	worker.reconcileSkillChanges(ctx)

	prefixed := skillName + SkillToolSep + toolName
	if worker.manifest.GetTool(prefixed) == nil {
		t.Fatalf("after reconcile: prefixed key %q missing", prefixed)
	}
	if worker.manifest.GetTool(toolName) == nil {
		t.Fatalf("after reconcile: bare key %q missing (expose_globally)", toolName)
	}

	if err := os.RemoveAll(filepath.Join(skillsDir, skillName)); err != nil {
		t.Fatal(err)
	}
	worker.reconcileSkillChanges(ctx)

	if worker.manifest.GetTool(prefixed) != nil {
		t.Errorf("after removal: prefixed key %q should be unregistered", prefixed)
	}
	if worker.manifest.GetTool(toolName) != nil {
		t.Errorf("after removal: bare key %q should be unregistered via ToolDir fallback", toolName)
	}
}

func writeRuntimeTestSkillExposed(t *testing.T, skillsDir, skillName, toolName string) {
	t.Helper()
	writeRuntimeTestSkill(t, skillsDir, skillName, toolName)
	toolManifest := filepath.Join(skillsDir, skillName, "tools", "manifest.yaml")
	manifest := fmt.Sprintf(`version: 1
expose_globally: true
tools:
  %s:
    executable: ./%s
    description: Test tool
    parameters:
      type: object
      properties:
        message:
          type: string
`, toolName, toolName)
	if err := os.WriteFile(toolManifest, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeTestSkill(t *testing.T, skillsDir, skillName, toolName string) {
	t.Helper()
	skillDir := filepath.Join(skillsDir, skillName)
	toolsDir := filepath.Join(skillDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillMD := fmt.Sprintf("---\nname: %s\ndescription: Runtime-arrival fixture skill for STR tests.\n---\n\nBody.", skillName)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	exePath := filepath.Join(toolsDir, toolName)
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\necho '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := fmt.Sprintf(`version: 1
tools:
  %s:
    executable: ./%s
    description: Test tool
    parameters:
      type: object
      properties:
        message:
          type: string
`, toolName, toolName)
	if err := os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
