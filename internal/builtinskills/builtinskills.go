// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

//go:embed files
var fsys embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(fsys, "files")
	if err != nil {
		panic(fmt.Errorf("builtinskills: fs.Sub: %w", err))
	}
	return sub
}

func Names() []string {
	entries, err := fs.ReadDir(fsys, "files")
	if err != nil {
		panic(fmt.Errorf("builtinskills: read files dir: %w", err))
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func HasName(name string) bool {
	return slices.Contains(Names(), name)
}

var fixedModTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func BuildZip(name string) ([]byte, error) {
	root := path.Join("files", name)
	if _, err := fs.Stat(fsys, root); err != nil {
		return nil, fmt.Errorf("builtinskills: skill %q not found: %w", name, err)
	}

	type entry struct {
		zipName string
		isDir   bool
		data    []byte
	}
	var entries []entry

	if err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := relPath(root, p)
		if relErr != nil {
			return relErr
		}
		if rel == "" {
			return nil
		}
		zipName := rel
		if d.IsDir() {
			zipName += "/"
			entries = append(entries, entry{zipName: zipName, isDir: true})
			return nil
		}
		if IsBundledExecutable(rel) {
			return nil
		}
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", p, readErr)
		}
		entries = append(entries, entry{zipName: zipName, isDir: false, data: data})
		return nil
	}); err != nil {
		return nil, err
	}

	slices.SortFunc(entries, func(a, b entry) int {
		return strings.Compare(a.zipName, b.zipName)
	})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.zipName, Method: zip.Deflate, Modified: fixedModTime}
		switch {
		case e.isDir:
			hdr.Method = zip.Store
			hdr.SetMode(0o755 | fs.ModeDir)
		default:
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, fmt.Errorf("zip create %q: %w", e.zipName, err)
		}
		if !e.isDir {
			if _, err := w.Write(e.data); err != nil {
				return nil, fmt.Errorf("zip write %q: %w", e.zipName, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}

func HashZip(name string) (zipBytes []byte, hashHex string, err error) {
	zipBytes, err = BuildZip(name)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(zipBytes)
	return zipBytes, hex.EncodeToString(sum[:]), nil
}

func relPath(root, p string) (string, error) {
	if p == root {
		return "", nil
	}
	prefix := root + "/"
	if !strings.HasPrefix(p, prefix) {
		return "", fmt.Errorf("path %q outside root %q", p, root)
	}
	return p[len(prefix):], nil
}

func IsBundledExecutable(rel string) bool {
	if !strings.HasPrefix(rel, "tools/") {
		return false
	}
	if strings.Count(rel, "/") < 2 {
		return false
	}
	return true
}
