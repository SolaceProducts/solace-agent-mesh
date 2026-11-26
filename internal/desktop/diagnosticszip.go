// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func diagnosticsZipFilename(version string, now time.Time) string {
	return fmt.Sprintf("solace-agent-mesh-diagnostics-%s-%s.zip", version, now.UTC().Format("20060102-150405"))
}

func resolveDiagnosticsZipDestination(dialogPath string, dialogErr error, downloadsDir, filename string) (string, error) {
	if dialogErr == nil {
		return dialogPath, nil
	}
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("create downloads dir: %w", err)
	}
	return filepath.Join(downloadsDir, filename), nil
}

type diagnosticsZipEntry struct {
	name string
	size int64
}

func writeDiagnosticsZip(w io.Writer, paths diagnosticsPaths, now time.Time) error {
	zw := zip.NewWriter(w)

	info := diagnosticsZipInfo(paths, now)
	if err := writeZipString(zw, "info.txt", info); err != nil {
		return err
	}
	entries := []diagnosticsZipEntry{{name: "info.txt", size: int64(len(info))}}

	packed, err := packDiagnosticsFiles(zw, paths.root)
	if err != nil {
		return err
	}
	entries = append(entries, packed...)

	if err := writeZipString(zw, "manifest.txt", diagnosticsZipManifest(entries)); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize diagnostics zip: %w", err)
	}
	return nil
}

func writeDiagnosticsZipFile(path string, paths diagnosticsPaths, now time.Time) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create diagnostics zip: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close diagnostics zip: %w", closeErr)
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	return writeDiagnosticsZip(f, paths, now)
}

func diagnosticsZipInfo(paths diagnosticsPaths, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== Solace Agent Mesh Desktop diagnostics ===\n")
	fmt.Fprintf(&b, "version:   %s\n", samversion.Version)
	fmt.Fprintf(&b, "platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "goVersion: %s\n", runtime.Version())
	fmt.Fprintf(&b, "createdAt: %s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "samHome:   %s\n", filepath.Dir(paths.root))
	fmt.Fprintf(&b, "=============================================\n")
	return b.String()
}

func packDiagnosticsFiles(zw *zip.Writer, root string) ([]diagnosticsZipEntry, error) {
	var entries []diagnosticsZipEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := "diagnostics/" + filepath.ToSlash(rel)
		size, err := copyFileToZip(zw, name, path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		entries = append(entries, diagnosticsZipEntry{name: name, size: size})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pack diagnostics folder: %w", err)
	}
	return entries, nil
}

func copyFileToZip(zw *zip.Writer, name, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	w, err := zw.Create(name)
	if err != nil {
		return 0, fmt.Errorf("create zip entry %s: %w", name, err)
	}
	n, err := io.Copy(w, f)
	if err != nil {
		return 0, fmt.Errorf("pack %s: %w", name, err)
	}
	return n, nil
}

func diagnosticsZipManifest(entries []diagnosticsZipEntry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%12d  %s\n", e.size, e.name)
	}
	return b.String()
}

func writeZipString(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}
