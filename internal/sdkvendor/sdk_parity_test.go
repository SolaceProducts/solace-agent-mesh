// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sdkvendor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedGoSDKMatchesLiveSource(t *testing.T) {
	liveDir := filepath.Join("..", "..", "pkg", "samtoolsdk")

	entries, err := os.ReadDir(liveDir)
	if err != nil {
		t.Fatalf("read live SDK dir %s: %v", liveDir, err)
	}

	var liveFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		liveFiles = append(liveFiles, name)
	}

	if len(liveFiles) == 0 {
		t.Fatalf("found no .go source files in %s — path likely wrong", liveDir)
	}

	for _, name := range liveFiles {
		base := strings.TrimSuffix(name, ".go")
		gotxtName := base + ".gotxt"

		liveData, readErr := os.ReadFile(filepath.Join(liveDir, name))
		if readErr != nil {
			t.Errorf("read live file %s: %v", name, readErr)
			continue
		}

		embeddedData, readErr := goSDKFS.ReadFile(filepath.Join(goSDKRoot, gotxtName))
		if readErr != nil {
			t.Errorf("embedded snapshot missing for %s (expected embed/gosdk/%s)\n"+
				"Fix: cp pkg/samtoolsdk/%s internal/sdkvendor/embed/gosdk/%s",
				name, gotxtName, name, gotxtName)
			continue
		}

		if string(liveData) != string(embeddedData) {
			t.Errorf("snapshot drift: embed/gosdk/%s != pkg/samtoolsdk/%s\n"+
				"Fix: cp pkg/samtoolsdk/%s internal/sdkvendor/embed/gosdk/%s",
				gotxtName, name, name, gotxtName)
		}
	}

	embedEntries, err := goSDKFS.ReadDir(goSDKRoot)
	if err != nil {
		t.Fatalf("read embedded gosdk dir: %v", err)
	}

	liveSet := make(map[string]bool, len(liveFiles))
	for _, f := range liveFiles {
		liveSet[f] = true
	}

	for _, e := range embedEntries {
		if e.IsDir() {
			continue
		}
		embName := e.Name()
		if !strings.HasSuffix(embName, ".gotxt") {
			continue
		}
		base := strings.TrimSuffix(embName, ".gotxt")
		goName := base + ".go"
		if !liveSet[goName] {
			t.Errorf("stale snapshot embed/gosdk/%s has no matching pkg/samtoolsdk/%s", embName, goName)
		}
	}
}
