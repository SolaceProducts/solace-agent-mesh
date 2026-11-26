// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFilesystemMirrorForTest(t *testing.T) (mirror *cloudMirror, remote, local string) {
	t.Helper()
	remote = t.TempDir()
	local = t.TempDir()
	client := NewFilesystemClient(remote)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newCloudMirror(client, "ns/tool-packages/", local, logger), remote, local
}

func writeRemote(t *testing.T, remote, key string, data []byte) {
	t.Helper()
	full := filepath.Join(remote, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestMirror_FetchOneBundle(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/foo/package.zip", []byte("foo-bytes"))
	writeRemote(t, remote, "ns/tool-packages/bar/package.zip", []byte("bar-bytes"))

	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(local, "foo", "package.zip"))
	if err != nil {
		t.Fatalf("read fetched foo: %v", err)
	}
	if string(got) != "foo-bytes" {
		t.Errorf("foo content = %q, want %q", got, "foo-bytes")
	}
	if _, err := os.Stat(filepath.Join(local, "bar")); !os.IsNotExist(err) {
		t.Errorf("bar should not exist locally after Fetch(foo); err=%v", err)
	}
}

func TestMirror_FetchAllDownloadsAndPrunes(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/alpha/package.zip", []byte("alpha"))
	writeRemote(t, remote, "ns/tool-packages/beta/package.zip", []byte("beta"))

	if err := m.FetchAll(t.Context()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		path := filepath.Join(local, name, "package.zip")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s, got err=%v", path, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(remote, "ns", "tool-packages", "beta")); err != nil {
		t.Fatalf("remove remote beta: %v", err)
	}
	if err := m.FetchAll(t.Context()); err != nil {
		t.Fatalf("FetchAll after prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "beta")); !os.IsNotExist(err) {
		t.Errorf("beta should be pruned locally; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "alpha", "package.zip")); err != nil {
		t.Errorf("alpha should survive prune; err=%v", err)
	}
}

func TestMirror_ETagShortCircuit(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/foo/package.zip", []byte("v1"))
	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("initial Fetch: %v", err)
	}

	localPath := filepath.Join(local, "foo", "package.zip")
	if err := os.WriteFile(localPath, []byte("local-edit"), 0o600); err != nil {
		t.Fatalf("local edit: %v", err)
	}
	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "local-edit" {
		t.Errorf("local copy should be untouched (ETag short-circuit); got %q", got)
	}
}

func TestMirror_Remove(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/foo/package.zip", []byte("foo"))
	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := m.Remove(t.Context(), "foo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "foo")); !os.IsNotExist(err) {
		t.Errorf("foo dir should be gone after Remove; err=%v", err)
	}
	if err := m.Remove(t.Context(), "foo"); err != nil {
		t.Errorf("Remove on missing: %v", err)
	}
}

func TestMirror_FetchAtomicLeavesNoTmp(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/foo/package.zip", []byte("foo"))
	if err := m.FetchAll(t.Context()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	var stray []string
	err := filepath.Walk(local, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".tmp") {
			stray = append(stray, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(stray) > 0 {
		t.Errorf(".tmp files should not survive a successful Fetch: %v", stray)
	}
}

func TestMirror_NewMirrorReturnsNoopForFilesystem(t *testing.T) {
	client := NewFilesystemClient(t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := NewMirror(client, "ns/tool-packages/", t.TempDir(), logger)
	if _, ok := got.(noopMirror); !ok {
		t.Errorf("NewMirror with filesystem client should return noopMirror; got %T", got)
	}
}

func TestMirror_NewMirrorReturnsNoopForMemory(t *testing.T) {
	client := NewMemoryClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := NewMirror(client, "ns/tool-packages/", t.TempDir(), logger)
	if _, ok := got.(noopMirror); !ok {
		t.Errorf("NewMirror with memory client should return noopMirror; got %T", got)
	}
}

func TestMirror_NoopMirrorAllMethodsReturnNil(t *testing.T) {
	m := NewNoopMirror()
	ctx := t.Context()
	if err := m.Fetch(ctx, "anything"); err != nil {
		t.Errorf("noop Fetch: %v", err)
	}
	if err := m.Remove(ctx, "anything"); err != nil {
		t.Errorf("noop Remove: %v", err)
	}
	if err := m.FetchAll(ctx); err != nil {
		t.Errorf("noop FetchAll: %v", err)
	}
}

func TestMirror_FilesystemDispatchIsCloudPath(t *testing.T) {
	m, _, _ := newFilesystemMirrorForTest(t)
	if m.client.storageType != "filesystem" {
		t.Fatalf("expected filesystem client; got %s", m.client.storageType)
	}
}

func TestMirror_FetchRejectsInvalidBundleNames(t *testing.T) {
	m, _, _ := newFilesystemMirrorForTest(t)
	cases := []string{"", ".", "..", "foo/bar", "foo\\bar", "../etc"}
	for _, name := range cases {
		t.Run("name="+name, func(t *testing.T) {
			if err := m.Fetch(t.Context(), name); err == nil {
				t.Errorf("Fetch(%q) should have rejected; got nil", name)
			}
			if err := m.Remove(t.Context(), name); err == nil {
				t.Errorf("Remove(%q) should have rejected; got nil", name)
			}
		})
	}
}

func TestMirror_RemoveClearsETagCacheSoNextFetchRedownloads(t *testing.T) {
	m, remote, local := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/foo/package.zip", []byte("v1"))
	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("initial Fetch: %v", err)
	}
	if err := m.Remove(t.Context(), "foo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := m.Fetch(t.Context(), "foo"); err != nil {
		t.Fatalf("re-Fetch: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(local, "foo", "package.zip"))
	if err != nil {
		t.Fatalf("read after re-Fetch: %v", err)
	}
	if string(got) != "v1" {
		t.Errorf("re-Fetch content = %q, want v1", got)
	}
}

func TestMirror_SyncPrefixSkipsPathTraversalKey(t *testing.T) {
	m, _, local := newFilesystemMirrorForTest(t)
	parent := filepath.Dir(local)
	sentinel := filepath.Join(parent, "sentinel.zip")
	if err := os.WriteFile(sentinel, []byte("must-not-be-deleted"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	m.etagCache["../sentinel.zip"] = "stale"
	if err := m.FetchAll(t.Context()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel outside localDir was deleted: %v", err)
	}
}

func TestCloudMirror_RemoteNamesReturnsBundleNames(t *testing.T) {
	m, remote, _ := newFilesystemMirrorForTest(t)

	writeRemote(t, remote, "ns/tool-packages/alpha/package.zip", []byte("a"))
	writeRemote(t, remote, "ns/tool-packages/alpha/manifest.json", []byte("{}"))
	writeRemote(t, remote, "ns/tool-packages/beta/package.zip", []byte("b"))

	names, err := m.RemoteNames(t.Context())
	if err != nil {
		t.Fatalf("RemoteNames: %v", err)
	}

	got := make(map[string]bool, len(names))
	for _, n := range names {
		got[n] = true
	}
	for _, want := range []string{"alpha", "beta"} {
		if !got[want] {
			t.Errorf("RemoteNames missing %q; got %v", want, names)
		}
	}
	if len(names) != 2 {
		t.Errorf("RemoteNames returned %d names, want 2: %v", len(names), names)
	}
}

func TestCloudMirror_RemoteNamesEmptyReturnsNil(t *testing.T) {
	m, _, _ := newFilesystemMirrorForTest(t)

	names, err := m.RemoteNames(t.Context())
	if err != nil {
		t.Fatalf("RemoteNames on empty store: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("RemoteNames on empty store returned %v, want nil/empty", names)
	}
}

func TestNoopMirror_RemoteNamesReturnsNil(t *testing.T) {
	m := NewNoopMirror()
	names, err := m.RemoteNames(t.Context())
	if err != nil {
		t.Errorf("noopMirror.RemoteNames returned error: %v", err)
	}
	if names != nil {
		t.Errorf("noopMirror.RemoteNames returned %v, want nil", names)
	}
}

func TestIsNoop_ReturnsTrueForNoop(t *testing.T) {
	m := NewNoopMirror()
	if !IsNoop(m) {
		t.Errorf("IsNoop(NewNoopMirror()) = false, want true")
	}
}

func TestIsNoop_ReturnsFalseForCloud(t *testing.T) {
	m, _, _ := newFilesystemMirrorForTest(t)
	if IsNoop(m) {
		t.Errorf("IsNoop(cloudMirror) = true, want false")
	}
}
