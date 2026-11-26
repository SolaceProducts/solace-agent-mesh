// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package filesystem

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return New(dir)
}

func testLoc(filename string) artifact.Locator {
	return artifact.Locator{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Filename:  filename,
	}
}

func testScope() artifact.KeyScope {
	return artifact.KeyScope{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
	}
}

func TestSaveAndLoad(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc := testLoc("data.csv")
	v, err := s.Save(ctx, loc, []byte("a,b,c"), "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("first version = %d, want 0", v)
	}

	content, mime, err := s.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b,c" {
		t.Errorf("content = %q", content)
	}
	if mime != "text/csv" {
		t.Errorf("mime = %q", mime)
	}
}

func TestSaveMultipleVersions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("data.txt")

	v0, _ := s.Save(ctx, loc, []byte("v0"), "text/plain")
	v1, _ := s.Save(ctx, loc, []byte("v1"), "text/plain")
	v2, _ := s.Save(ctx, loc, []byte("v2"), "text/plain")

	if v0 != 0 || v1 != 1 || v2 != 2 {
		t.Errorf("versions = %d, %d, %d", v0, v1, v2)
	}

	content, _, _ := s.Load(ctx, loc, nil)
	if string(content) != "v2" {
		t.Errorf("latest = %q", content)
	}

	ver := 0
	content, _, _ = s.Load(ctx, loc, &ver)
	if string(content) != "v0" {
		t.Errorf("v0 = %q", content)
	}
}

func TestLoadNotFound(t *testing.T) {
	s := newTestStore(t)
	content, mime, err := s.Load(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content != nil || mime != "" {
		t.Errorf("expected nil/empty, got %q / %q", content, mime)
	}
}

func TestOpenReader(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("data.txt")

	want := []byte("v0")
	if _, err := s.Save(ctx, loc, want, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, loc, []byte("v1"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	rc, mime, size, err := s.OpenReader(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("reader is nil")
	}
	defer func() { _ = rc.Close() }()

	latest := []byte("v1")
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(latest) {
		t.Errorf("content = %q, want %q", data, latest)
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q, want text/plain", mime)
	}
	if size != int64(len(latest)) {
		t.Errorf("size = %d, want %d", size, len(latest))
	}

	ver := 0
	rc0, _, size0, err := s.OpenReader(ctx, loc, &ver)
	if err != nil {
		t.Fatal(err)
	}
	if rc0 == nil {
		t.Fatal("reader for version 0 is nil")
	}
	defer func() { _ = rc0.Close() }()

	data0, err := io.ReadAll(rc0)
	if err != nil {
		t.Fatal(err)
	}
	if string(data0) != string(want) {
		t.Errorf("v0 content = %q, want %q", data0, want)
	}
	if size0 != int64(len(want)) {
		t.Errorf("v0 size = %d, want %d", size0, len(want))
	}
}

func TestOpenReaderNotFound(t *testing.T) {
	s := newTestStore(t)
	rc, mime, size, err := s.OpenReader(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != nil || mime != "" || size != 0 {
		t.Errorf("expected nil/empty/0, got %v / %q / %d", rc, mime, size)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("temp.txt")

	_, _ = s.Save(ctx, loc, []byte("data"), "text/plain")

	if err := s.Delete(ctx, loc); err != nil {
		t.Fatal(err)
	}

	content, _, _ := s.Load(ctx, loc, nil)
	if content != nil {
		t.Error("artifact should be deleted")
	}
}

func TestListKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, _ = s.Save(ctx, testLoc("b.txt"), []byte("b"), "text/plain")
	_, _ = s.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")

	_, _ = s.Save(ctx, artifact.Locator{
		AppName: "app", UserID: "user-2", SessionID: "sess-1", Filename: "c.txt",
	}, []byte("c"), "text/plain")

	keys, err := s.ListKeys(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a.txt" || keys[1] != "b.txt" {
		t.Errorf("keys = %v", keys)
	}
}

func blockedScopeDirectories() map[string]func(*Store) string {
	return map[string]func(*Store) string{
		"session directory": func(s *Store) string { return s.sessionDir(testScope()) },
		"user directory":    func(s *Store) string { return s.userDir(testScope()) },
	}
}

func blockScopeDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListKeysStrict_MissingScopeListsNothing(t *testing.T) {
	s := newTestStore(t)

	keys, err := s.ListKeysStrict(context.Background(), testScope())

	if err != nil || len(keys) != 0 {
		t.Fatalf("ListKeysStrict of a scope nothing was saved to = %v, %v, want no keys and no error", keys, err)
	}
}

func TestListKeysStrict_ListsWhatListKeysLists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, testLoc("b.txt"), []byte("b"), "text/plain")
	_, _ = s.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")
	_, _ = s.Save(ctx, artifact.Locator{AppName: "app", UserID: "user-1", Filename: "user:mine.txt"}, []byte("m"), "text/plain")

	strict, err := s.ListKeysStrict(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}
	lenient, err := s.ListKeys(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(strict, lenient) || !slices.Equal(strict, []string{"a.txt", "b.txt", "user:mine.txt"}) {
		t.Fatalf("ListKeysStrict = %v, ListKeys = %v, want both a.txt, b.txt, user:mine.txt", strict, lenient)
	}
}

func TestListKeysStrict_ReportsAScopeItCannotRead(t *testing.T) {
	for name, blocked := range blockedScopeDirectories() {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			blockScopeDirectory(t, blocked(s))

			keys, err := s.ListKeysStrict(context.Background(), testScope())

			if err == nil {
				t.Fatalf("ListKeysStrict = %v, nil, want an error: an unreadable %s is not an empty one", keys, name)
			}
		})
	}
}

func TestListKeysStrict_ErrorNamesNeitherThePathNorTheUser(t *testing.T) {
	for name, blocked := range blockedScopeDirectories() {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			blockScopeDirectory(t, blocked(s))

			_, err := s.ListKeysStrict(context.Background(), testScope())

			if err == nil {
				t.Fatal("ListKeysStrict = nil error, want one")
			}
			for _, leaked := range []string{s.basePath, "user-1", "sess-1"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("error %q carries %q", err, leaked)
				}
			}
		})
	}
}

func TestListKeys_StaysLenientAboutAScopeItCannotRead(t *testing.T) {
	tests := map[string]struct {
		blocked  string
		wantKeys []string
	}{
		"session directory": {"session directory", []string{"user:mine.txt"}},
		"user directory":    {"user directory", []string{"a.txt"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			_, _ = s.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")
			_, _ = s.Save(ctx, artifact.Locator{AppName: "app", UserID: "user-1", Filename: "user:mine.txt"}, []byte("m"), "text/plain")
			path := blockedScopeDirectories()[tc.blocked](s)
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			blockScopeDirectory(t, path)

			keys, err := s.ListKeys(ctx, testScope())

			if err != nil || !slices.Equal(keys, tc.wantKeys) {
				t.Fatalf("ListKeys with an unreadable %s = %v, %v, want %v and no error", name, keys, err, tc.wantKeys)
			}
		})
	}
}

func TestListVersions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("data.txt")

	_, _ = s.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = s.Save(ctx, loc, []byte("v1"), "text/plain")

	versions, err := s.ListVersions(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0] != 0 || versions[1] != 1 {
		t.Errorf("versions = %v", versions)
	}
}

func TestListVersionsNotFound(t *testing.T) {
	s := newTestStore(t)
	versions, err := s.ListVersions(context.Background(), testLoc("nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if versions != nil {
		t.Errorf("expected nil, got %v", versions)
	}
}

func TestGetVersionMetadata(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("data.csv")

	_, _ = s.Save(ctx, loc, []byte("a,b,c"), "text/csv")

	meta, err := s.GetVersionMetadata(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("metadata not found")
	}
	if meta.MIMEType != "text/csv" {
		t.Errorf("mime = %q", meta.MIMEType)
	}
	if meta.SizeBytes != 5 {
		t.Errorf("size = %d, want 5", meta.SizeBytes)
	}
}

func TestGetVersionMetadataNotFound(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.GetVersionMetadata(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Error("expected nil metadata")
	}
}

func TestSaveMetadata(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("data.csv")

	v, _ := s.Save(ctx, loc, []byte("a,b"), "text/csv")

	err := s.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType: "text/tsv",
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, _ := s.GetVersionMetadata(ctx, loc, nil)
	if meta.MIMEType != "text/tsv" {
		t.Errorf("mime = %q, want text/tsv", meta.MIMEType)
	}
}

func TestSaveMetadata_AllFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("full-meta.json")

	v, _ := s.Save(ctx, loc, []byte(`{}`), "application/json")

	schema := map[string]any{"type": "object"}
	custom := map[string]any{"source": "test", "priority": float64(1)}

	err := s.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType:    "application/json",
		Description: "Full metadata test",
		Schema:      schema,
		Custom:      custom,
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, err := s.GetVersionMetadata(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "Full metadata test" {
		t.Errorf("description = %q, want %q", meta.Description, "Full metadata test")
	}
	if meta.Schema == nil || meta.Schema["type"] != "object" {
		t.Errorf("schema = %v, want map with type=object", meta.Schema)
	}
	if meta.Custom == nil || meta.Custom["source"] != "test" {
		t.Errorf("custom = %v, want map with source=test", meta.Custom)
	}
}

func TestDirectoryLayout(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc := testLoc("report.csv")
	_, _ = s.Save(ctx, loc, []byte("data"), "text/csv")

	dataFile := filepath.Join(s.basePath, "app", "user-1", "sess-1", "report.csv", "0")
	metaFile := dataFile + ".meta"

	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		t.Error("data file not created at expected path")
	}
	if _, err := os.Stat(metaFile); os.IsNotExist(err) {
		t.Error("meta file not created at expected path")
	}
}

func TestUserScopedArtifact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc := testLoc("user:preferences.json")
	_, err := s.Save(ctx, loc, []byte(`{"theme":"dark"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}

	userFile := filepath.Join(s.basePath, "app", "user-1", "user", "preferences.json", "0")
	if _, statErr := os.Stat(userFile); os.IsNotExist(statErr) {
		t.Error("user-scoped artifact not at expected path")
	}

	content, _, err := s.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"theme":"dark"}` {
		t.Errorf("content = %q", content)
	}

	keys, _ := s.ListKeys(ctx, testScope())
	found := false
	for _, k := range keys {
		if k == "user:preferences.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("user-scoped artifact not in ListKeys: %v", keys)
	}
}

func TestConcurrentSave(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("concurrent.txt")

	n := 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			_, err := s.Save(ctx, loc, []byte("data"), "text/plain")
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	versions, _ := s.ListVersions(ctx, loc)
	if len(versions) != n {
		t.Errorf("expected %d versions, got %d", n, len(versions))
	}
}

func TestMetaFileFormat(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	_, _ = s.Save(ctx, loc, []byte("hello"), "text/plain")

	metaPath := filepath.Join(s.basePath, "app", "user-1", "sess-1", "test.txt", "0.meta")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"mime_type":"text/plain"}` {
		t.Errorf("meta file = %q", data)
	}
}

func TestPathTraversalPrevention(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc := artifact.Locator{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Filename:  "../../../etc/passwd",
	}
	_, err := s.Save(ctx, loc, []byte("malicious"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}

	sanitizedPath := filepath.Join(s.basePath, "app", "user-1", "sess-1", "passwd", "0")
	if _, err := os.Stat(sanitizedPath); os.IsNotExist(err) {
		t.Error("sanitized file not at expected path")
	}

	dangerousPath := filepath.Join(s.basePath, "..", "etc", "passwd")
	if _, err := os.Stat(dangerousPath); err == nil {
		t.Error("path traversal not prevented!")
	}
}

func TestSentinelFilenameSanitization(t *testing.T) {
	for _, sentinel := range []string{"..", ".", "", "../..", "/etc/.."} {
		t.Run(sentinel, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()

			loc := artifact.Locator{
				AppName:   "app",
				UserID:    "user-1",
				SessionID: "sess-1",
				Filename:  sentinel,
			}
			if _, err := s.Save(ctx, loc, []byte("content"), "text/plain"); err != nil {
				t.Fatal(err)
			}

			expected := filepath.Join(s.basePath, "app", "user-1", "sess-1", "_", "0")
			if _, err := os.Stat(expected); err != nil {
				t.Errorf("sentinel %q: expected file at %s, got %v", sentinel, expected, err)
			}

			polluted := filepath.Join(s.basePath, "app", "user-1", "0")
			if _, err := os.Stat(polluted); err == nil {
				t.Errorf("sentinel %q: cross-session pollution, file written at %s", sentinel, polluted)
			}
		})
	}
}

func TestNonASCIIFilenameSanitization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loc := artifact.Locator{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Filename:  "café_résumé.pdf",
	}
	_, err := s.Save(ctx, loc, []byte("content"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}

	sanitizedPath := filepath.Join(s.basePath, "app", "user-1", "sess-1", "caf__r_sum_.pdf", "0")
	if _, statErr := os.Stat(sanitizedPath); os.IsNotExist(statErr) {
		t.Error("sanitized file not at expected path")
	}

	data, _, err := s.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "content" {
		t.Errorf("loaded data = %q, want %q", data, "content")
	}
}

func TestInterfaceCompliance(t *testing.T) {
	var _ artifact.Service = New("/tmp")
}

func TestSaveVersion_Basic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := s.SaveVersion(ctx, loc, 0, []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion(0): %v", err)
	}

	v := 0
	content, mime, err := s.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(0): %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want %q", content, "hello")
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q, want %q", mime, "text/plain")
	}
}

func TestSaveVersion_Conflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := s.SaveVersion(ctx, loc, 0, []byte("first"), "text/plain"); err != nil {
		t.Fatalf("first SaveVersion(0): %v", err)
	}

	err := s.SaveVersion(ctx, loc, 0, []byte("second"), "text/plain")
	if err != artifact.ErrVersionConflict {
		t.Fatalf("second SaveVersion(0) = %v, want ErrVersionConflict", err)
	}
}

func TestSaveVersion_ConflictPreservesOriginal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := s.SaveVersion(ctx, loc, 0, []byte("original"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion(0): %v", err)
	}

	_ = s.SaveVersion(ctx, loc, 0, []byte("intruder"), "text/plain")

	v := 0
	content, _, err := s.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(0): %v", err)
	}
	if string(content) != "original" {
		t.Errorf("content = %q, want %q", content, "original")
	}
}

func TestSaveVersion_NewArtifact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "new.txt"}

	if err := s.SaveVersion(ctx, loc, 0, []byte("brand new"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion on new artifact: %v", err)
	}

	v := 0
	content, _, err := s.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(content) != "brand new" {
		t.Errorf("content = %q, want %q", content, "brand new")
	}
}

func TestSaveVersion_NonContiguous(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	for i := 0; i < 3; i++ {
		if _, err := s.Save(ctx, loc, []byte("v"), "text/plain"); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	if err := s.SaveVersion(ctx, loc, 5, []byte("five"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion(5): %v", err)
	}

	v := 5
	content, _, err := s.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(5): %v", err)
	}
	if string(content) != "five" {
		t.Errorf("content = %q, want %q", content, "five")
	}
}

func TestSaveVersion_MixedWithSave(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("test.txt")

	v0, err := s.Save(ctx, loc, []byte("auto0"), "text/plain")
	if err != nil || v0 != 0 {
		t.Fatalf("Save = (%d, %v), want (0, nil)", v0, err)
	}

	if svErr := s.SaveVersion(ctx, loc, 1, []byte("cond1"), "text/plain"); svErr != nil {
		t.Fatalf("SaveVersion(1): %v", svErr)
	}

	v2, err := s.Save(ctx, loc, []byte("auto2"), "text/plain")
	if err != nil || v2 != 2 {
		t.Fatalf("Save = (%d, %v), want (2, nil)", v2, err)
	}

	for _, tc := range []struct {
		ver  int
		want string
	}{{0, "auto0"}, {1, "cond1"}, {2, "auto2"}} {
		content, _, err := s.Load(ctx, loc, &tc.ver)
		if err != nil {
			t.Fatalf("Load(%d): %v", tc.ver, err)
		}
		if string(content) != tc.want {
			t.Errorf("Load(%d) = %q, want %q", tc.ver, content, tc.want)
		}
	}
}

func TestSaveVersion_ConcurrentConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := testLoc("concurrent.txt")

	if _, err := s.Save(ctx, loc, []byte("v0"), "text/plain"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	const numGoroutines = 10
	var (
		wg        sync.WaitGroup
		barrier   = make(chan struct{})
		mu        sync.Mutex
		wins      int
		conflicts int
	)

	for i := 0; i < numGoroutines; i++ {
		wg.Go(func() {
			<-barrier
			err := s.SaveVersion(ctx, loc, 1, []byte("winner"), "text/plain")
			mu.Lock()
			defer mu.Unlock()
			switch err {
			case nil:
				wins++
			case artifact.ErrVersionConflict:
				conflicts++
			default:
				t.Errorf("goroutine %d: unexpected error: %v", i, err)
			}
		})
	}

	close(barrier)
	wg.Wait()

	if wins != 1 {
		t.Errorf("wins = %d, want 1", wins)
	}
	if conflicts != numGoroutines-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, numGoroutines-1)
	}

	v := 1
	content, _, err := s.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(1): %v", err)
	}
	if string(content) != "winner" {
		t.Errorf("content = %q, want %q", content, "winner")
	}
}

func TestSaveUnderAPrincipalWindowsRejects(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	loc := artifact.Locator{AppName: "solace-agent-mesh", UserID: "system:default", SessionID: "sess-1", Filename: "wf_handle_input.json"}
	if _, err := s.Save(ctx, loc, []byte("{}"), "application/json"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	keys, err := s.ListKeys(ctx, artifact.KeyScope{AppName: loc.AppName, UserID: loc.UserID, SessionID: loc.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys, []string{"wf_handle_input.json"}) {
		t.Errorf("ListKeys = %q, want the saved artifact", keys)
	}
}

func TestSaveFromTwoStoresOnOneDirectoryKeepsEveryVersion(t *testing.T) {
	dir := t.TempDir()
	stores := []*Store{New(dir), New(dir)}
	const savesPerStore = 20
	loc := testLoc("shared.txt")
	ctx := t.Context()

	var wg sync.WaitGroup
	errs := make(chan error, len(stores)*savesPerStore)
	for si, s := range stores {
		for i := range savesPerStore {
			wg.Go(func() {
				body := []byte{byte('a' + si), byte(i)}
				v, err := s.Save(ctx, loc, body, "text/plain")
				if err != nil {
					errs <- err
					return
				}
				got, _, err := s.Load(ctx, loc, &v)
				if err != nil {
					errs <- err
					return
				}
				if string(got) != string(body) {
					t.Errorf("Load(version %d) = %q, want %q", v, got, body)
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Save() or Load() error = %v", err)
	}

	versions, err := stores[0].ListVersions(ctx, loc)
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if len(versions) != len(stores)*savesPerStore {
		t.Errorf("ListVersions() returned %d versions, want %d", len(versions), len(stores)*savesPerStore)
	}
}
