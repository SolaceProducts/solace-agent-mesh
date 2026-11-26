// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
)

const schemaEmittingTool = `#!/bin/sh
if [ "$1" = "--schema" ]; then
  printf '{"tools":{"greet":{"description":"d","parameters":{"type":"object","properties":{}}}}}'
fi
`

func TestPackageToDiscovery_SourceWithoutExecBits(t *testing.T) {
	srcDir := t.TempDir()
	files := map[string]string{
		"greet":         schemaEmittingTool,
		"manifest.yaml": "version: 1\ntools:\n  greet_tools:\n    executable: ./greet\n",
	}
	for name, body := range files {
		path := filepath.Join(srcDir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
	}

	zipBytes, _, err := bundle.BundleDir(srcDir)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}
	zipPath := filepath.Join(t.TempDir(), "package.zip")
	if writeErr := os.WriteFile(zipPath, zipBytes, 0o600); writeErr != nil {
		t.Fatalf("write zip: %v", writeErr)
	}

	pkgDir := t.TempDir()
	if extractErr := extractZipFile(zipPath, pkgDir); extractErr != nil {
		t.Fatalf("extractZipFile: %v", extractErr)
	}

	manifest, err := readPackageManifest(filepath.Join(pkgDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("readPackageManifest: %v", err)
	}
	p := NewPackageProcessor(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	tools, errs, _ := p.processFromManifest(pkgDir, "greet", manifest)
	if len(tools) == 0 {
		t.Fatalf("a toolset packaged without exec bits discovered no tools: %v", errs)
	}

	info, err := os.Stat(filepath.Join(pkgDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("manifest.yaml became executable (%v)", info.Mode().Perm())
	}
}

func TestExtractZipFile_ReExtractIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	zipPath := writeZipWithModes(t, dir, map[string]zipEntry{
		"tool":      {[]byte("#!/bin/sh\necho hi\n"), 0o644},
		"README.md": {[]byte("# docs\n"), 0o644},
	})

	dest := t.TempDir()
	for attempt := 1; attempt <= 2; attempt++ {
		if err := extractZipFile(zipPath, dest); err != nil {
			t.Fatalf("extract %d: %v", attempt, err)
		}
		tool, err := os.Stat(filepath.Join(dest, "tool"))
		if err != nil {
			t.Fatalf("stat tool: %v", err)
		}
		if tool.Mode().Perm()&0o111 == 0 {
			t.Errorf("extract %d left the tool non-executable (%v)", attempt, tool.Mode().Perm())
		}
		doc, err := os.Stat(filepath.Join(dest, "README.md"))
		if err != nil {
			t.Fatalf("stat doc: %v", err)
		}
		if doc.Mode().Perm()&0o111 != 0 {
			t.Errorf("extract %d made the doc executable (%v)", attempt, doc.Mode().Perm())
		}
	}
}
