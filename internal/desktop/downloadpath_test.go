// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDownloadFileNumbersCollisions(t *testing.T) {
	dir := t.TempDir()

	for _, want := range []string{"report.pdf", "report (1).pdf", "report (2).pdf"} {
		f, err := createDownloadFile(dir, "report.pdf")
		if err != nil {
			t.Fatalf("createDownloadFile: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(f.Name()); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestCreateDownloadFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(existing, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := createDownloadFile(dir, "report.pdf")
	if err != nil {
		t.Fatalf("createDownloadFile: %v", err)
	}
	if _, err = f.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "first" {
		t.Errorf("existing file was overwritten: got %q", kept)
	}
}

func TestCreateDownloadFileConfinesWritesToDir(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"../escaped.txt", "a/b/../../c.txt", "nested/dir/report.pdf"} {
		f, err := createDownloadFile(dir, name)
		if err != nil {
			t.Fatalf("createDownloadFile(%q): %v", name, err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if got := filepath.Dir(f.Name()); got != dir {
			t.Errorf("createDownloadFile(%q) wrote to %q, want a file directly in %q", name, f.Name(), dir)
		}
	}
}

func TestCreateDownloadFileRejectsNamesWithNoFile(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"", "   ", ".", "..", "/", "some/dir/.."} {
		if _, err := createDownloadFile(dir, name); err == nil {
			t.Errorf("createDownloadFile(%q) succeeded, want an error", name)
		}
	}
}

func TestCreateDownloadFileNumbersBeforeTheFinalExtensionOnly(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"archive.tar.gz", "README", ".env"} {
		for range 2 {
			f, err := createDownloadFile(dir, name)
			if err != nil {
				t.Fatalf("createDownloadFile(%q): %v", name, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, want := range []string{"archive.tar (1).gz", "README (1)", ".env (1)"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %q to exist: %v", want, err)
		}
	}
}
