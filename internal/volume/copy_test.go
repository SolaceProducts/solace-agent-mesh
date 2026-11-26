// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func populateSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644))
	must(os.MkdirAll(filepath.Join(src, "sub", "deep"), 0o755))
	must(os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("a\n"), 0o600))
	must(os.WriteFile(filepath.Join(src, "sub", "deep", "b.txt"), []byte("b\n"), 0o644))
	must(os.WriteFile(filepath.Join(src, ".hidden"), []byte("h\n"), 0o644))
	must(os.Symlink(filepath.Join(src, "README.md"), filepath.Join(src, "link-to-readme")))
	return src
}

func TestFilesystemStore_CopyFromDir_BasicTreeCopied(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	src := populateSource(t)
	id, err := store.Create(ctx, CreateOptions{
		Name:         "copied",
		UserID:       "u",
		CreatorType:  CallerHuman,
		CopyFromPath: src,
	})
	if err != nil {
		t.Fatalf("Create with copy: %v", err)
	}

	dataDir := filepath.Join(store.root, id, "data")
	for _, rel := range []string{
		"README.md",
		filepath.Join("sub", "a.txt"),
		filepath.Join("sub", "deep", "b.txt"),
		".hidden",
	} {
		got, readErr := os.ReadFile(filepath.Join(dataDir, rel))
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		want, _ := os.ReadFile(filepath.Join(src, rel))
		if string(got) != string(want) {
			t.Fatalf("content mismatch for %s: got %q want %q", rel, got, want)
		}
	}
	if _, lstatErr := os.Lstat(filepath.Join(dataDir, "link-to-readme")); !os.IsNotExist(lstatErr) {
		t.Fatalf("symlink should not be copied; lstat err=%v", lstatErr)
	}
	info, err := os.Stat(filepath.Join(dataDir, "sub", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("a.txt mode = %v, want 0o600", info.Mode().Perm())
	}
}

func TestFilesystemStore_CopyFromDir_ValidatesSource(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		path string
	}{
		{"non-absolute", "relative"},
		{"non-existent", "/no/such/place"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, CreateOptions{
				Name: "x", UserID: "u", CreatorType: CallerHuman,
				CopyFromPath: tc.path,
			})
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("expected ErrInvalidOptions, got %v", err)
			}
		})
	}

	tmp := t.TempDir()
	f := filepath.Join(tmp, "a")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, CreateOptions{
		Name: "x", UserID: "u", CreatorType: CallerHuman, CopyFromPath: f,
	}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions for file source, got %v", err)
	}
}

func TestFilesystemStore_CopyFromDir_MutuallyExclusiveWithExternalPath(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()
	src := populateSource(t)

	_, err := store.Create(ctx, CreateOptions{
		Name:         "x",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: src,
		CopyFromPath: src,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestFilesystemStore_CopyFromDir_FailureRollsBack(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	src := populateSource(t)

	bad := filepath.Join(src, "unreadable")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	id, err := store.Create(ctx, CreateOptions{
		Name: "x", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src,
	})
	if err == nil {
		t.Fatalf("expected copy failure, got volume id=%s", id)
	}

	if vs, _ := store.List(ctx, "u", ListFilter{}); len(vs) != 0 {
		t.Fatalf("expected zero volumes after rollback, got %d", len(vs))
	}

	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("expected no volume directories left after rollback; found %s", e.Name())
		}
	}
}

func TestFilesystemStore_CopyFromDir_EmptySource(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()
	src := t.TempDir()

	id, err := store.Create(ctx, CreateOptions{
		Name: "empty", UserID: "u", CreatorType: CallerHuman, CopyFromPath: src,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dataDir := filepath.Join(store.root, id, "data")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty data dir, got %d entries", len(entries))
	}
}

func TestMemoryStore_CopyFromDir_Rejected(t *testing.T) {
	m := NewMemoryStore()
	_, err := m.Create(context.Background(), CreateOptions{
		Name: "x", UserID: "u", CopyFromPath: "/anything",
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestCopyHostDirInto_SourceMustBeDirectory(t *testing.T) {
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	err := copyHostDirInto(srcFile, dst)
	if err == nil {
		t.Fatal("expected error when source is a regular file")
	}
}

func TestCopyHostDirInto_DestinationMustBeDirectory(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dstFile := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(dstFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := copyHostDirInto(src, dstFile)
	if err == nil {
		t.Fatal("expected error when destination is a regular file")
	}
}

func TestCopyHostDirInto_MissingSource(t *testing.T) {
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	err := copyHostDirInto(filepath.Join(tmp, "does-not-exist"), dst)
	if err == nil {
		t.Fatal("expected error when source path is missing")
	}
}

func TestCopyHostDirInto_PreservesFileMode(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "public"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyHostDirInto(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		filepath.Join(dst, "secret"): 0o600,
		filepath.Join(dst, "public"): 0o644,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode for %s = %v, want %v", filepath.Base(path), got, want)
		}
	}
}

func TestCopyHostDirInto_SkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "outside.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyHostDirInto(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dst, "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink should not exist in dst, got lstat err=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "real.txt"))
	if err != nil || string(got) != "real" {
		t.Fatalf("real.txt content = %q err=%v", got, err)
	}
}

func TestDirSizeBytes_MissingDirReturnsZero(t *testing.T) {
	tmp := t.TempDir()
	n, err := dirSizeBytes(filepath.Join(tmp, "absent"))
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes, got %d", n)
	}
}

func TestDirSizeBytes_SumsRegularFilesAndSkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "nested", "b"), []byte("xy"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 10_000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(big, filepath.Join(tmp, "link")); err != nil {
		t.Fatal(err)
	}

	n, err := dirSizeBytes(tmp)
	if err != nil {
		t.Fatalf("dirSizeBytes: %v", err)
	}
	if n != 7 {
		t.Fatalf("expected 7 bytes (5 + 2), got %d", n)
	}
}
