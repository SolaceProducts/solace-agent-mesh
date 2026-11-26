// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFormatVersion_PinsTheSourceToZipMapping(t *testing.T) {
	tree := map[string]struct {
		content []byte
		mode    os.FileMode
	}{
		"manifest.yaml":       {[]byte("name: tool\n"), 0o644},
		"tool":                {[]byte{0x7f, 'E', 'L', 'F', 0x02}, 0o755},
		"docs/README.md":      {[]byte("# docs\n"), 0o644},
		"scripts/setup.sh":    {[]byte("#!/bin/sh\ntrue\n"), 0o755},
		"python/lib/thing.py": {[]byte("X = 1\n"), 0o644},
		"regions.csv":         {[]byte("MZ,Country\nMozambique,MZ\n"), 0o644},
		"notes.txt":           {[]byte("#! is how a shebang starts\n"), 0o644},
		".git/config":         {[]byte("[core]\n"), 0o644},
		"node_modules/x/i.js": {[]byte("//\n"), 0o644},
		"dist/stale":          {[]byte("old\n"), 0o644},
		"__pycache__/m.pyc":   {[]byte("\x00\n"), 0o644},
		".sam-cache/b.zip":    {[]byte("PK\n"), 0o644},
		".DS_Store":           {[]byte("\x00\n"), 0o644},
		"lib/module.pyc":      {[]byte("\x00\n"), 0o644},
	}

	stage := func(t *testing.T, windowsHost bool) []zipOrderedEntry {
		t.Helper()
		dir := t.TempDir()
		for name, f := range tree {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
			if err := os.WriteFile(path, f.content, 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			mode := f.mode
			if windowsHost {
				mode = 0o666
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("chmod %s: %v", name, err)
			}
		}
		if windowsHost {
			forceWindowsHost(t)
		}
		data, _, err := BundleDir(dir)
		if err != nil {
			t.Fatalf("BundleDir: %v", err)
		}
		return zipOrderedEntries(t, data)
	}

	assert := func(t *testing.T, got []zipOrderedEntry, byVersion map[int][]string) {
		t.Helper()
		want, ok := byVersion[FormatVersion]
		if !ok {
			t.Fatalf("no pinned mapping for bundle.FormatVersion %d — the constant moved, "+
				"so add the new expected mapping to this test rather than editing the old one",
				FormatVersion)
		}
		var lines []string
		for _, e := range got {
			lines = append(lines, fmt.Sprintf("%s %04o", e.name, e.mode.Perm()))
		}
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Errorf("the source-tree-to-zip mapping changed.\n got:\n%s\nwant:\n%s\n\n"+
				"Entry order is significant here, so this also pins collectEntries' "+
				"sort. If the change is intentional — including any change to what "+
				"execbit.LooksRunnable answers, or to the exclude list — bump "+
				"bundle.FormatVersion (currently %d) so the toolset build cache "+
				"invalidates, then add a fixture for the new version.",
				strings.Join(lines, "\n"), strings.Join(want, "\n"), FormatVersion)
		}
	}

	t.Run("mode-driven host", func(t *testing.T) {
		want := []string{
			"docs/ 0755",
			"docs/README.md 0644",
			"lib/ 0755",
			"manifest.yaml 0644",
			"notes.txt 0644",
			"python/ 0755",
			"python/lib/ 0755",
			"python/lib/thing.py 0644",
			"regions.csv 0644",
			"scripts/ 0755",
			"scripts/setup.sh 0755",
			"tool 0755",
		}
		assert(t, stage(t, false), map[int][]string{3: want, 4: want})
	})

	t.Run("inference-driven host", func(t *testing.T) {
		want := []string{
			"docs/ 0755",
			"docs/README.md 0644",
			"lib/ 0755",
			"manifest.yaml 0644",
			"notes.txt 0755",
			"python/ 0755",
			"python/lib/ 0755",
			"python/lib/thing.py 0644",
			"regions.csv 0644",
			"scripts/ 0755",
			"scripts/setup.sh 0755",
			"tool 0755",
		}
		assert(t, stage(t, true), map[int][]string{3: want, 4: want})
	})
}

func TestFormatVersion_PinsTheExcludeList(t *testing.T) {
	v3 := map[string][]string{
		"dirs":       {".git", ".sam-cache", "__pycache__", "dist", "node_modules"},
		"exactFiles": {".DS_Store"},
		"extensions": {".pyc"},
	}
	byVersion := map[int]map[string][]string{3: v3, 4: v3}
	want, ok := byVersion[FormatVersion]
	if !ok {
		t.Fatalf("no pinned exclude list for bundle.FormatVersion %d — add one for the new version",
			FormatVersion)
	}

	got := map[string][]string{
		"dirs":       keysOf(excludedDirs),
		"exactFiles": keysOf(excludedExactFiles),
		"extensions": keysOf(excludedExtensions),
	}
	for kind, wantNames := range want {
		if strings.Join(got[kind], ",") != strings.Join(wantNames, ",") {
			t.Errorf("excluded %s changed: got %v, want %v\n\n"+
				"What a bundle omits is part of how a source tree maps onto zip "+
				"entries, so bump bundle.FormatVersion (currently %d) and pin the "+
				"new list — otherwise an upgraded CLI keeps serving bundles the "+
				"older one cached.", kind, got[kind], wantNames, FormatVersion)
		}
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
