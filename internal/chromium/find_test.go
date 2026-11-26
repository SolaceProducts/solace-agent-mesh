// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chromium

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFind_SamChromiumPathWins(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "my-chrome")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAM_CHROMIUM_PATH", fake)

	got, err := Find()
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != fake {
		t.Errorf("Find = %q, want %q (SAM_CHROMIUM_PATH must win)", got, fake)
	}
}

func TestFind_SamChromiumPathIgnoredWhenMissing(t *testing.T) {
	t.Setenv("SAM_CHROMIUM_PATH", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))

	_, err := Find()
	if err != nil && !strings.Contains(err.Error(), "SAM_CHROMIUM_PATH") {
		t.Errorf("error should mention SAM_CHROMIUM_PATH as a remedy, got %v", err)
	}
}

func TestWellKnownPaths_PerOS(t *testing.T) {
	paths := wellKnownPaths()
	if len(paths) == 0 {
		t.Fatal("expected some well-known paths for this OS")
	}
	switch runtime.GOOS {
	case "darwin":
		if !containsSubstr(paths, "Google Chrome.app") {
			t.Errorf("darwin paths should include the Chrome .app bundle: %v", paths)
		}
	case "windows":
		for _, p := range paths {
			if !strings.HasSuffix(p, ".exe") {
				t.Errorf("windows path should be an .exe: %q", p)
			}
		}
	default:
		if !containsSubstr(paths, "chromium") {
			t.Errorf("linux paths should include a chromium binary: %v", paths)
		}
	}
}

func containsSubstr(items []string, sub string) bool {
	for _, s := range items {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
