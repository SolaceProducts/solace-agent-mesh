// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func tarGz(t *testing.T, headers ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		body := []byte("x")
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestABundleCannotWriteOutsideItsWorkspace(t *testing.T) {
	cases := map[string]*tar.Header{
		"parent path":   {Name: "../escaped.txt", Typeflag: tar.TypeReg, Mode: 0o600},
		"absolute path": {Name: "/tmp/escaped.txt", Typeflag: tar.TypeReg, Mode: 0o600},
		"symlink":       {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"hard link":     {Name: "link", Typeflag: tar.TypeLink, Linkname: "../outside"},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "ws")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := unpackBundle(tarGz(t, h), dir); err == nil {
				t.Fatal("the entry was accepted")
			}
			if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
				t.Fatal("a file was written outside the workspace")
			}
		})
	}
}

func TestABundleUnpacksItsFiles(t *testing.T) {
	dir := t.TempDir()
	n, err := unpackBundle(tarGz(t,
		&tar.Header{Name: "traces/", Typeflag: tar.TypeDir, Mode: 0o700},
		&tar.Header{Name: "./traces/1.json", Typeflag: tar.TypeReg, Mode: 0o600},
	), dir)
	if err != nil || n != 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "traces", "1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestABundleRunCanLeaveTheDiffOutOfItsResult(t *testing.T) {
	out := &Outcome{Report: "found it", Changes: &ChangeSet{Diff: "--- a\n+++ b\n", Files: []string{"M a.txt"}}}

	if full := bundleRunData(out, false); full["diff"] == nil || full["files_changed"] == nil {
		t.Errorf("result = %v, want the diff kept when the caller did not ask to omit it", full)
	}
	lean := bundleRunData(out, true)
	if _, ok := lean["diff"]; ok {
		t.Errorf("result = %v, want no diff", lean)
	}
	if _, ok := lean["files_changed"]; ok {
		t.Errorf("result = %v, want no changed files", lean)
	}
	if lean["report"] != "found it" {
		t.Errorf("report = %v, want it kept", lean["report"])
	}
}
