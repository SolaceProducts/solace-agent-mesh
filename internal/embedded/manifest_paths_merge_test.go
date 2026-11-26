// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStringSliceToAnyShape(t *testing.T) {
	out := stringSliceToAny([]string{"a", "b", "c"})
	if got := len(out); got != 3 {
		t.Fatalf("expected 3 entries, got %d", got)
	}
	for i, want := range []string{"a", "b", "c"} {
		got, ok := out[i].(string)
		if !ok {
			t.Errorf("entry %d not a string", i)
		}
		if got != want {
			t.Errorf("entry %d = %q, want %q", i, got, want)
		}
	}

	if empty := stringSliceToAny(nil); len(empty) != 0 {
		t.Errorf("expected empty result for nil input, got len=%d", len(empty))
	}
}

func TestAugmentSTRAppSourcesPreservesPrimary(t *testing.T) {
	appConfig := map[string]any{
		"manifest_path": "/primary.yaml",
	}
	augmentSTRAppSources(nil, appConfig, []string{"/extra1.yaml", "/extra2.yaml"}, "")

	got, ok := appConfig["manifest_paths"].([]any)
	if !ok {
		t.Fatalf("expected manifest_paths to be []any, got %T", appConfig["manifest_paths"])
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 merged paths, got %d", len(got))
	}
	if got[0].(string) != "/extra1.yaml" || got[1].(string) != "/extra2.yaml" {
		t.Errorf("unexpected merged paths: %v", got)
	}
}

func TestAugmentSTRAppSourcesDeduplicates(t *testing.T) {
	appConfig := map[string]any{
		"manifest_paths": []any{"/primary.yaml", "/existing.yaml"},
	}
	augmentSTRAppSources(nil, appConfig,
		[]string{"/primary.yaml", "/existing.yaml", "/new.yaml"},
		"")

	got := appConfig["manifest_paths"].([]any)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries after dedup, got %d: %v", len(got), got)
	}
	if got[0].(string) != "/primary.yaml" || got[1].(string) != "/existing.yaml" || got[2].(string) != "/new.yaml" {
		t.Errorf("unexpected entries after dedup: %v", got)
	}
}

func TestAugmentSTRAppSourcesSkillsDir(t *testing.T) {
	dir := t.TempDir()
	appConfig := map[string]any{}
	augmentSTRAppSources(nil, appConfig, nil, dir)
	if got, _ := appConfig["skills_dir"].(string); got != dir {
		t.Errorf("skills_dir not set: got %q, want %q", got, dir)
	}

	appConfig = map[string]any{"skills_dir": "/already-set"}
	augmentSTRAppSources(nil, appConfig, nil, dir)
	if got, _ := appConfig["skills_dir"].(string); got != dir {
		t.Errorf("expected platform skills_dir to override pre-set value, got %q", got)
	}
}

func TestAugmentSTRAppSourcesSkillsDirOverrideWarning(t *testing.T) {
	dir := t.TempDir()

	t.Run("fires on mismatch", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		appConfig := map[string]any{"skills_dir": "/opt/sam/skills"}
		augmentSTRAppSources(logger, appConfig, nil, dir)

		got := buf.String()
		if !strings.Contains(got, "level=WARN") {
			t.Errorf("expected WARN level, got: %s", got)
		}
		if !strings.Contains(got, "overriding STR skills directory") {
			t.Errorf("expected override diagnostic, got: %s", got)
		}
		if !strings.Contains(got, "/opt/sam/skills") {
			t.Errorf("expected discarded configValue in log, got: %s", got)
		}
		if !strings.Contains(got, dir) {
			t.Errorf("expected replacement dir %s in log, got: %s", dir, got)
		}
	})

	t.Run("silent when only cosmetically different", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		appConfig := map[string]any{"skills_dir": dir + string(filepath.Separator)}
		augmentSTRAppSources(logger, appConfig, nil, dir)

		if got := buf.String(); got != "" {
			t.Errorf("expected no log for filepath.Clean-equal values, got: %s", got)
		}
		if got, _ := appConfig["skills_dir"].(string); got != dir {
			t.Errorf("skills_dir should still be normalized to %q, got %q", dir, got)
		}
	})
}

func TestAppendDiscoveredSTRToolsCreatesNewWorker(t *testing.T) {
	toolsDir := t.TempDir()
	for _, name := range []string{"tool1", "tool2"} {
		dir := filepath.Join(toolsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", toolsDir)
	t.Setenv("SAM_SKILLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	mp, ok := classified.STR[0].AppConfig["manifest_paths"].([]any)
	if !ok {
		t.Fatalf("expected manifest_paths to be []any (so config.GetStringSlice picks it up), got %T", classified.STR[0].AppConfig["manifest_paths"])
	}
	if len(mp) != 2 {
		t.Errorf("expected 2 manifest paths (all discovered paths go into manifest_paths), got %d", len(mp))
	}
}
