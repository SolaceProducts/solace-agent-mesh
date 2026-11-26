// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package memory

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

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
	s := New()
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

func TestOpenReader(t *testing.T) {
	s := New()
	ctx := context.Background()
	loc := testLoc("data.csv")

	if _, err := s.Save(ctx, loc, []byte("a,b,c"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	rc, mime, size, err := s.OpenReader(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("reader is nil")
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err = rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != "a,b,c" {
		t.Errorf("content = %q, want %q", got, "a,b,c")
	}
	if mime != "text/csv" {
		t.Errorf("mime = %q, want %q", mime, "text/csv")
	}
	if size != int64(len("a,b,c")) {
		t.Errorf("size = %d, want %d", size, len("a,b,c"))
	}

	if _, err = s.Save(ctx, loc, []byte("d,e,f,g"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	v0 := 0
	rc, _, size, err = s.OpenReader(ctx, loc, &v0)
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err = rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != "a,b,c" {
		t.Errorf("v0 content = %q, want %q", got, "a,b,c")
	}
	if size != int64(len("a,b,c")) {
		t.Errorf("v0 size = %d, want %d", size, len("a,b,c"))
	}

	rc, mime, size, err = s.OpenReader(ctx, testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatalf("not-found should not error, got %v", err)
	}
	if rc != nil || mime != "" || size != 0 {
		t.Errorf("not-found = (%v, %q, %d), want (nil, \"\", 0)", rc, mime, size)
	}

	missing := 5
	if _, _, _, err = s.OpenReader(ctx, loc, &missing); err == nil {
		t.Error("explicit missing version should error")
	}
}

func TestSaveMultipleVersions(t *testing.T) {
	s := New()
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
		t.Errorf("latest content = %q", content)
	}

	ver := 0
	content, _, _ = s.Load(ctx, loc, &ver)
	if string(content) != "v0" {
		t.Errorf("v0 content = %q", content)
	}
}

func TestLoadNotFound(t *testing.T) {
	s := New()
	content, mime, err := s.Load(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content != nil || mime != "" {
		t.Errorf("expected nil/empty, got %q / %q", content, mime)
	}
}

func TestDelete(t *testing.T) {
	s := New()
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
	s := New()
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
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "a.txt" || keys[1] != "b.txt" {
		t.Errorf("keys = %v", keys)
	}
}

func TestListVersions(t *testing.T) {
	s := New()
	ctx := context.Background()
	loc := testLoc("data.txt")

	_, _ = s.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = s.Save(ctx, loc, []byte("v1"), "text/plain")
	_, _ = s.Save(ctx, loc, []byte("v2"), "text/plain")

	versions, err := s.ListVersions(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	for i, v := range versions {
		if v != i {
			t.Errorf("versions[%d] = %d", i, v)
		}
	}
}

func TestListArtifactVersions(t *testing.T) {
	s := New()
	ctx := context.Background()

	for range 3 {
		_, _ = s.Save(ctx, testLoc("data.txt"), []byte("x"), "text/plain")
	}
	_, _ = s.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")
	_, _ = s.Save(ctx, artifact.Locator{
		AppName: "app", UserID: "user-2", SessionID: "sess-1", Filename: "c.txt",
	}, []byte("c"), "text/plain")

	got, err := s.ListArtifactVersions(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"data.txt": {0, 1, 2},
		"a.txt":    {0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListArtifactVersions = %v, want %v", got, want)
	}

	keys, _ := s.ListKeys(ctx, testScope())
	if len(keys) != len(got) {
		t.Errorf("name count: ListKeys=%d ListArtifactVersions=%d", len(keys), len(got))
	}
	for name, versions := range got {
		direct, _ := s.ListVersions(ctx, testLoc(name))
		if !reflect.DeepEqual(direct, versions) {
			t.Errorf("%q: scan=%v perArtifact=%v", name, versions, direct)
		}
	}
}

func TestListVersionsNotFound(t *testing.T) {
	s := New()
	versions, err := s.ListVersions(context.Background(), testLoc("nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if versions != nil {
		t.Errorf("expected nil, got %v", versions)
	}
}

func TestSaveAndGetMetadata(t *testing.T) {
	s := New()
	ctx := context.Background()
	loc := testLoc("data.csv")

	v, _ := s.Save(ctx, loc, []byte("a,b"), "text/csv")

	meta := &artifact.VersionMetadata{
		MIMEType:    "text/csv",
		Description: "Test data",
		SizeBytes:   3,
	}
	if err := s.SaveMetadata(ctx, loc, v, meta); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetVersionMetadata(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("metadata not found")
	}
	if got.MIMEType != "text/csv" || got.Description != "Test data" || got.SizeBytes != 3 {
		t.Errorf("metadata = %+v", got)
	}
}

func TestGetMetadataNotFound(t *testing.T) {
	s := New()
	got, err := s.GetVersionMetadata(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil metadata for nonexistent artifact")
	}
}

func TestSaveMetadataNotFound(t *testing.T) {
	s := New()
	err := s.SaveMetadata(context.Background(), testLoc("nonexistent"), 0, &artifact.VersionMetadata{})
	if err == nil {
		t.Error("expected error for nonexistent artifact")
	}
}

func TestDeepCopy(t *testing.T) {
	s := New()
	ctx := context.Background()
	loc := testLoc("data.txt")

	original := []byte("original")
	_, _ = s.Save(ctx, loc, original, "text/plain")

	original[0] = 'X'

	content, _, _ := s.Load(ctx, loc, nil)
	if string(content) != "original" {
		t.Errorf("stored content mutated: %q", content)
	}

	content[0] = 'Y'
	content2, _, _ := s.Load(ctx, loc, nil)
	if string(content2) != "original" {
		t.Errorf("loaded content leaked: %q", content2)
	}
}

func TestConcurrentSave(t *testing.T) {
	s := New()
	ctx := context.Background()
	loc := testLoc("concurrent.txt")

	var wg sync.WaitGroup
	n := 10
	for i := 0; i < n; i++ {
		wg.Go(func() {
			_, err := s.Save(ctx, loc, []byte(fmt.Sprintf("v%d", i)), "text/plain")
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

func TestInterfaceCompliance(t *testing.T) {
	var _ artifact.Service = New()
}

func TestSaveVersion_Basic(t *testing.T) {
	s := New()
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
	s := New()
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
	s := New()
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
	s := New()
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
	s := New()
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
	s := New()
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
	s := New()
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
