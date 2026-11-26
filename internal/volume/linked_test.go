// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeHostDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	return dir
}

func TestFilesystemStore_LinkedCreate_ValidatesExternalPath(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		path string
	}{
		{"non-absolute", "relative/path"},
		{"non-existent", "/no/such/path/at/all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, CreateOptions{
				Name:         "linked",
				UserID:       "u",
				CreatorType:  CallerHuman,
				ExternalPath: tc.path,
			})
			if !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("expected ErrInvalidOptions, got %v", err)
			}
		})
	}

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(filePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, CreateOptions{
		Name:         "linked",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: filePath,
	}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("file path should be rejected; got %v", err)
	}
}

func TestFilesystemStore_LinkedCreate_PersistsAndResolves_HostView(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	hostDir := makeHostDir(t, "project")
	containerPath := "/host/home/" + filepath.Base(hostDir)

	id, err := store.Create(ctx, CreateOptions{
		Name:                  "work",
		UserID:                "u",
		CreatorType:           CallerHuman,
		ExternalPath:          hostDir,
		ExternalPathContainer: containerPath,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	v, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !v.Linked {
		t.Fatalf("expected Linked=true")
	}
	if v.ExternalPath != hostDir {
		t.Fatalf("ExternalPath = %q, want %q", v.ExternalPath, hostDir)
	}
	if v.ExternalPathContainer != containerPath {
		t.Fatalf("ExternalPathContainer = %q, want %q", v.ExternalPathContainer, containerPath)
	}

	got, err := store.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != hostDir {
		t.Fatalf("Resolve host view = %q, want %q", got, hostDir)
	}

	if _, err := os.Stat(filepath.Join(store.root, id, "data")); !os.IsNotExist(err) {
		t.Fatalf("expected no data subdir for linked volume, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, id, sidecarFilename)); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
}

func TestFilesystemStore_LinkedResolve_ContainerView(t *testing.T) {
	root := t.TempDir()
	hostStore, err := NewFilesystemStore(root, slog.Default())
	if err != nil {
		t.Fatalf("NewFilesystemStore host: %v", err)
	}
	defer func() { _ = hostStore.Close() }()

	hostDir := makeHostDir(t, "project")
	containerPath := "/host/home/" + filepath.Base(hostDir)

	id, err := hostStore.Create(context.Background(), CreateOptions{
		Name:                  "work",
		UserID:                "u",
		CreatorType:           CallerHuman,
		ExternalPath:          hostDir,
		ExternalPathContainer: containerPath,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	containerStore, err := NewFilesystemStoreInView(root, ViewContainer, slog.Default())
	if err != nil {
		t.Fatalf("NewFilesystemStoreInView: %v", err)
	}
	defer func() { _ = containerStore.Close() }()

	got, err := containerStore.Resolve(context.Background(), id)
	if err != nil {
		t.Fatalf("Resolve container view: %v", err)
	}
	if got != containerPath {
		t.Fatalf("Resolve container view = %q, want %q", got, containerPath)
	}
}

func TestFilesystemStore_LinkedResolve_ContainerView_MissingMapping(t *testing.T) {
	root := t.TempDir()
	hostStore, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	defer func() { _ = hostStore.Close() }()

	hostDir := makeHostDir(t, "outside-scope")
	id, err := hostStore.Create(context.Background(), CreateOptions{
		Name:         "work",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: hostDir,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	containerStore, err := NewFilesystemStoreInView(root, ViewContainer, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStoreInView: %v", err)
	}
	defer func() { _ = containerStore.Close() }()

	_, err = containerStore.Resolve(context.Background(), id)
	if err == nil {
		t.Fatalf("expected error for linked volume without container path")
	}
}

func TestFilesystemStore_LinkedDelete_PreservesHostDirectory(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	hostDir := makeHostDir(t, "irreplaceable")
	markerFile := filepath.Join(hostDir, "marker.txt")

	id, err := store.Create(ctx, CreateOptions{
		Name:         "work",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: hostDir,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if delErr := store.Delete(ctx, id, CallerHuman); delErr != nil {
		t.Fatalf("Delete: %v", delErr)
	}

	if _, getErr := store.Get(ctx, id); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", getErr)
	}
	if _, statErr := os.Stat(filepath.Join(store.root, id)); !os.IsNotExist(statErr) {
		t.Fatalf("expected sidecar dir removed, got err=%v", statErr)
	}
	if _, statErr := os.Stat(hostDir); statErr != nil {
		t.Fatalf("host directory was deleted! err=%v", statErr)
	}
	data, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("host marker file was deleted! err=%v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("host marker file modified: %q", string(data))
	}
}

func TestFilesystemStore_LinkedDelete_AgentCannotDeleteHuman(t *testing.T) {
	store := newTestFilesystemStore(t)
	ctx := context.Background()

	hostDir := makeHostDir(t, "human-owned")
	id, err := store.Create(ctx, CreateOptions{
		Name:         "work",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: hostDir,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete(ctx, id, CallerAgent); !errors.Is(err, ErrPermission) {
		t.Fatalf("agent should not delete human-created linked volume; err=%v", err)
	}
}

func TestDirectoryStore_Resolve_LinkedSidecar(t *testing.T) {
	root := t.TempDir()
	hostStore, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	defer func() { _ = hostStore.Close() }()

	containerTarget := makeHostDir(t, "container-visible")
	id, err := hostStore.Create(context.Background(), CreateOptions{
		Name:                  "work",
		UserID:                "u",
		CreatorType:           CallerHuman,
		ExternalPath:          containerTarget,
		ExternalPathContainer: containerTarget,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	dir, err := NewDirectoryStore(root, nil)
	if err != nil {
		t.Fatalf("NewDirectoryStore: %v", err)
	}
	defer func() { _ = dir.Close() }()

	got, err := dir.Resolve(context.Background(), id)
	if err != nil {
		t.Fatalf("DirectoryStore.Resolve: %v", err)
	}
	if got != containerTarget {
		t.Fatalf("got %q, want %q", got, containerTarget)
	}
}

func TestDirectoryStore_Resolve_LinkedSidecar_MissingTarget(t *testing.T) {
	root := t.TempDir()
	id := "vol-test-missing"
	volDir := filepath.Join(root, id)
	if err := os.MkdirAll(volDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLinkedSidecar(volDir, id, "/some/host", "/host/home/missing"); err != nil {
		t.Fatal(err)
	}

	dir, err := NewDirectoryStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()

	_, err = dir.Resolve(context.Background(), id)
	if err == nil {
		t.Fatalf("expected error when sidecar target does not exist inside container")
	}
}

func TestMemoryStore_LinkedCreate(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	id, err := m.Create(ctx, CreateOptions{
		Name:         "work",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: "/abs/path",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v, err := m.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !v.Linked || v.ExternalPath != "/abs/path" {
		t.Fatalf("linked fields not stored: %+v", v)
	}
	got, err := m.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "/abs/path" {
		t.Fatalf("Resolve = %q", got)
	}

	if _, err := m.Create(ctx, CreateOptions{
		Name: "x", UserID: "u", ExternalPath: "relative",
	}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions for relative path, got %v", err)
	}
}

func TestFilesystemStore_LinkedPath_RejectsSymlink(t *testing.T) {
	store := newTestFilesystemStore(t)

	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink create not supported on this platform: %v", err)
	}

	_, err := store.Create(context.Background(), CreateOptions{
		Name:         "x",
		UserID:       "u",
		CreatorType:  CallerHuman,
		ExternalPath: linkDir,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions for symlinked external_path, got %v", err)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink-mention in error, got %q", err.Error())
	}
}

func TestFilesystemStore_CopyFromPath_RejectsSymlink(t *testing.T) {
	store := newTestFilesystemStore(t)

	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "a"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink create not supported on this platform: %v", err)
	}

	_, err := store.Create(context.Background(), CreateOptions{
		Name:         "x",
		UserID:       "u",
		CreatorType:  CallerHuman,
		CopyFromPath: linkDir,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions for symlinked copy_from_path, got %v", err)
	}
}
