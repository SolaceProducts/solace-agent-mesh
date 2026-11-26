// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/execbit"
)

var fixedMtime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

var excludedDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"dist":         {},
	"__pycache__":  {},
	".sam-cache":   {},
}

var excludedExactFiles = map[string]struct{}{
	".DS_Store": {},
}

var excludedExtensions = map[string]struct{}{
	".pyc": {},
}

func shouldExclude(name string, isDir bool) bool {
	if isDir {
		_, ok := excludedDirs[name]
		return ok
	}
	if _, ok := excludedExactFiles[name]; ok {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := excludedExtensions[ext]
	return ok
}

func BundleDir(srcDir string) ([]byte, string, error) {
	return BundleDirWithOverlay(srcDir, nil)
}

func BundleDirWithOverlay(srcDir string, overlay map[string][]byte) ([]byte, string, error) {
	st, err := os.Stat(srcDir)
	if err != nil {
		return nil, "", fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !st.IsDir() {
		return nil, "", fmt.Errorf("%s: not a directory", srcDir)
	}

	entries, err := collectEntries(srcDir)
	if err != nil {
		return nil, "", err
	}

	if len(overlay) > 0 {
		onDisk := make(map[string]struct{}, len(entries))
		for _, e := range entries {
			if !e.isDir {
				onDisk[e.relPath] = struct{}{}
			}
		}
		var extras []entry
		for rel := range overlay {
			if _, ok := onDisk[rel]; ok {
				continue
			}
			extras = append(extras, entry{
				relPath: filepath.ToSlash(rel),
				isDir:   false,
				mode:    0o755,
			})
		}
		if len(extras) > 0 {
			entries = append(entries, extras...)
			slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.relPath, b.relPath) })
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		if err := writeEntry(zw, srcDir, e, overlay); err != nil {
			_ = zw.Close()
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("close zip writer: %w", err)
	}

	data := buf.Bytes()
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func LoadZip(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

const FormatVersion = 4

type entry struct {
	relPath string
	isDir   bool
	mode    os.FileMode
}

func collectEntries(srcDir string) ([]entry, error) {
	var out []entry
	walkErr := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}
		name := d.Name()
		if shouldExclude(name, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("rel %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		var mode os.FileMode
		if !d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return fmt.Errorf("stat %s: %w", path, infoErr)
			}
			mode = info.Mode().Perm()
		}
		out = append(out, entry{relPath: rel, isDir: d.IsDir(), mode: mode})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	slices.SortFunc(out, func(a, b entry) int { return strings.Compare(a.relPath, b.relPath) })
	return out, nil
}

var hostGOOS = runtime.GOOS

func isExecutableEntry(srcDir string, e entry, overrideBytes []byte, overridden bool) bool {
	if e.mode&0o111 != 0 {
		return true
	}
	if hostGOOS != "windows" {
		return false
	}
	if overridden {
		return execbit.LooksRunnableBytes(overrideBytes)
	}
	return execbit.LooksRunnable(filepath.Join(srcDir, filepath.FromSlash(e.relPath)))
}

func writeEntry(zw *zip.Writer, srcDir string, e entry, overlay map[string][]byte) error {
	header := &zip.FileHeader{
		Name:     e.relPath,
		Modified: fixedMtime,
	}
	if e.isDir {
		header.Name = e.relPath + "/"
		header.Method = zip.Store
		header.SetMode(0o755 | os.ModeDir)
		if _, err := zw.CreateHeader(header); err != nil {
			return fmt.Errorf("write dir %s: %w", e.relPath, err)
		}
		return nil
	}
	header.Method = zip.Deflate
	overrideBytes, overridden := overlay[e.relPath]
	fileMode := os.FileMode(0o644)
	if isExecutableEntry(srcDir, e, overrideBytes, overridden) {
		fileMode = 0o755
	}
	header.SetMode(fileMode)
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create entry %s: %w", e.relPath, err)
	}
	if overridden {
		if _, writeErr := w.Write(overrideBytes); writeErr != nil {
			return fmt.Errorf("write overlay %s: %w", e.relPath, writeErr)
		}
		return nil
	}
	src, openErr := os.Open(filepath.Join(srcDir, filepath.FromSlash(e.relPath)))
	if openErr != nil {
		return fmt.Errorf("open %s: %w", e.relPath, openErr)
	}
	defer func() { _ = src.Close() }()
	if _, copyErr := io.Copy(w, src); copyErr != nil {
		return fmt.Errorf("copy %s: %w", e.relPath, copyErr)
	}
	return nil
}
