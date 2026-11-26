// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"archive/zip"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeOneFileZip(t *testing.T, path, name, content string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZips_DoesNotWriteThroughALinkOutOfThePackage(t *testing.T) {
	pkg, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(pkg, "evil.zip.extracted")); err != nil {
		t.Fatal(err)
	}
	writeOneFileZip(t, filepath.Join(pkg, "package.zip"), "evil.zip.extracted/f", "x")
	_ = extractZips(pkg, true, slog.Default())
	if _, err := os.Stat(filepath.Join(outside, "f")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unzip wrote outside the package dir through a link")
	}
}

func TestReadPackageManifest_RefusesALinkOutOfThePackage(t *testing.T) {
	pkg := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.yaml")
	if err := os.WriteFile(secret, []byte("runtime: python\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(pkg, "manifest.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackageManifest(filepath.Join(pkg, "manifest.yaml")); err == nil {
		t.Fatal("manifest read followed a link out of the package")
	}
}

func TestExtractZipFile_RefusesAZipLinkOutOfThePackage(t *testing.T) {
	pkg, dest := t.TempDir(), t.TempDir()
	realZip := filepath.Join(t.TempDir(), "real.zip")
	writeOneFileZip(t, realZip, "planted.txt", "x")
	if err := os.Symlink(realZip, filepath.Join(pkg, "package.zip")); err != nil {
		t.Fatal(err)
	}
	if err := extractZipFile(filepath.Join(pkg, "package.zip"), dest); err == nil {
		t.Fatal("extractZipFile followed a package.zip link out of the package")
	}
	if _, err := os.Stat(filepath.Join(dest, "planted.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("extracted through a package.zip link out of the package")
	}
}
