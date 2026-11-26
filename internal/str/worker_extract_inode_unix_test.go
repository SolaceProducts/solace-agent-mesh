// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package str

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestExtractZipFile_PreservesRunningBinaryInode(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "tool", "tool")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := "#!/bin/sh\nwhile read line; do echo got $line; done\n"
	if err := os.WriteFile(target, []byte(v1), 0o755); err != nil {
		t.Fatal(err)
	}
	stV1, statErr := os.Stat(target)
	if statErr != nil {
		t.Fatal(statErr)
	}
	inoV1 := stV1.Sys().(*syscall.Stat_t).Ino

	cmd := exec.Command(target)
	stdin, pipeErr := cmd.StdinPipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	if startErr := cmd.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	src := t.TempDir()
	zipPath := filepath.Join(src, "v2.zip")
	zf, createErr := os.Create(zipPath)
	if createErr != nil {
		t.Fatal(createErr)
	}
	zw := zip.NewWriter(zf)
	hdr := &zip.FileHeader{Name: "tool/tool", Method: zip.Deflate}
	hdr.SetMode(0o755)
	fw, hdrErr := zw.CreateHeader(hdr)
	if hdrErr != nil {
		t.Fatal(hdrErr)
	}
	v2 := "#!/bin/sh\necho v2\n"
	if _, writeErr := fw.Write([]byte(v2)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := zw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := zf.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	if extractErr := extractZipFile(zipPath, dest); extractErr != nil {
		t.Fatalf("extractZipFile: %v", extractErr)
	}

	stV2, statErr2 := os.Stat(target)
	if statErr2 != nil {
		t.Fatal(statErr2)
	}
	inoV2 := stV2.Sys().(*syscall.Stat_t).Ino
	if inoV1 == inoV2 {
		t.Fatalf("expected new inode after extract (atomic rename); inode unchanged at %d — extractZipFile overwrote in place, which deadlocks concurrent exec on macOS",
			inoV1)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != v2 {
		t.Fatalf("post-extract file content = %q, want %q", got, v2)
	}
}
