// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResourceStore_HeadAndPutWithMetadata_Memory(t *testing.T) {
	t.Parallel()
	rs := NewStore(NewMemoryClient())
	runHeadPutMetadataSuite(t, rs)
}

func TestResourceStore_HeadAndPutWithMetadata_Filesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := NewStore(NewFilesystemClient(dir))
	runHeadPutMetadataSuite(t, rs)
}

func TestResourceStore_FilesystemSidecar(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := NewStore(NewFilesystemClient(dir))
	ctx := context.Background()
	if err := rs.PutWithMetadata(ctx, "nested/key.bin", strings.NewReader("x"), map[string]string{"k": "v"}); err != nil {
		t.Fatalf("PutWithMetadata: %v", err)
	}
	sidecar := filepath.Join(dir, "nested", "key.bin.sam-meta")
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("expected sidecar at %s: %v", sidecar, err)
	}
	if !strings.Contains(string(data), `"k":"v"`) {
		t.Errorf("sidecar JSON missing payload: %s", data)
	}

	if err := rs.Put(ctx, "nested/key.bin", strings.NewReader("y")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("expected sidecar gone after metadata-less Put, stat err=%v", err)
	}
}

func runHeadPutMetadataSuite(t *testing.T, rs *Store) {
	t.Helper()
	ctx := context.Background()
	key := "skills/test/skill.zip"
	md := map[string]string{"sam-builtin-hash": "abcdef"}

	if err := rs.PutWithMetadata(ctx, key, strings.NewReader("payload"), md); err != nil {
		t.Fatalf("PutWithMetadata: %v", err)
	}
	got, err := rs.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got["sam-builtin-hash"] != "abcdef" {
		t.Errorf("metadata sam-builtin-hash = %q, want %q", got["sam-builtin-hash"], "abcdef")
	}

	if putErr := rs.Put(ctx, key, strings.NewReader("payload2")); putErr != nil {
		t.Fatalf("Put overwrite: %v", putErr)
	}
	got, err = rs.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head after overwrite: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty metadata after Put, got %v", got)
	}

	rc, err := rs.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(body, []byte("payload2")) {
		t.Errorf("body = %q, want payload2", body)
	}

	if _, err := rs.Head(ctx, "missing/key"); err == nil {
		t.Error("expected error on Head of missing key")
	} else if !IsNotFound(err) {
		t.Errorf("expected NotFoundError, got %T %v", err, err)
	}

	if err := rs.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := rs.Head(ctx, key); err == nil {
		t.Error("expected NotFound after delete")
	}
}

func TestStore_DeletePrefix(t *testing.T) {
	t.Parallel()
	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		runDeletePrefixSuite(t, NewStore(NewMemoryClient()))
	})
	t.Run("filesystem", func(t *testing.T) {
		t.Parallel()
		runDeletePrefixSuite(t, NewStore(NewFilesystemClient(t.TempDir())))
	})
}

func TestStore_ListObjectsReportsSizeAndModifiedTime(t *testing.T) {
	t.Parallel()
	for name, rs := range map[string]*Store{
		"memory":     NewStore(NewMemoryClient()),
		"filesystem": NewStore(NewFilesystemClient(t.TempDir())),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			before := time.Now().Add(-time.Second)
			for key, body := range map[string]string{"p/a": "abc", "p/sub/b": "de", "q/c": "x"} {
				if err := rs.Put(ctx, key, strings.NewReader(body)); err != nil {
					t.Fatalf("Put %s: %v", key, err)
				}
			}
			infos, err := rs.ListObjects(ctx, "p/")
			if err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			got := map[string]int64{}
			for _, info := range infos {
				got[info.Key] = info.Size
				if info.Modified.Before(before) || info.Modified.After(time.Now().Add(time.Second)) {
					t.Errorf("%s modified %v, want about now", info.Key, info.Modified)
				}
			}
			if len(got) != 2 || got["p/a"] != 3 || got["p/sub/b"] != 2 {
				t.Errorf("ListObjects(p/) = %v, want p/a:3 p/sub/b:2", got)
			}
		})
	}
}

func TestStore_DeletePrefixLargeCount(t *testing.T) {
	t.Parallel()
	const total = 5 * deletePrefixConcurrency
	for name, rs := range map[string]*Store{
		"memory":     NewStore(NewMemoryClient()),
		"filesystem": NewStore(NewFilesystemClient(t.TempDir())),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			for i := range total {
				if err := rs.Put(ctx, fmt.Sprintf("ns1/objects/o%d", i), strings.NewReader("x")); err != nil {
					t.Fatalf("Put %d: %v", i, err)
				}
			}
			if err := rs.Put(ctx, "ns2/keep", strings.NewReader("x")); err != nil {
				t.Fatalf("Put keep: %v", err)
			}

			n, err := rs.DeletePrefix(ctx, "ns1/")
			if err != nil {
				t.Fatalf("DeletePrefix: %v", err)
			}
			if n != total {
				t.Errorf("deleted = %d, want %d", n, total)
			}
			if remaining, listErr := rs.List(ctx, "ns1/"); listErr != nil || len(remaining) != 0 {
				t.Errorf("ns1/ should be empty; got %d keys, err=%v", len(remaining), listErr)
			}
			if _, getErr := rs.Get(ctx, "ns2/keep"); getErr != nil {
				t.Errorf("ns2/keep should survive; got err=%v", getErr)
			}
		})
	}
}

func TestStore_DeletePrefixContextCancelled(t *testing.T) {
	t.Parallel()
	for name, rs := range map[string]*Store{
		"memory":     NewStore(NewMemoryClient()),
		"filesystem": NewStore(NewFilesystemClient(t.TempDir())),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			seedCtx := context.Background()
			for i := range 3 {
				if err := rs.Put(seedCtx, fmt.Sprintf("ns1/o%d", i), strings.NewReader("x")); err != nil {
					t.Fatalf("Put %d: %v", i, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			n, err := rs.DeletePrefix(ctx, "ns1/")
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled, got n=%d err=%v", n, err)
			}
			if remaining, listErr := rs.List(seedCtx, "ns1/"); listErr != nil || len(remaining) != 3 {
				t.Errorf("objects should survive a cancelled sweep; got %d keys, err=%v", len(remaining), listErr)
			}
		})
	}
}

func TestResourceStore_FilesystemPutAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := NewStore(NewFilesystemClient(dir))
	ctx := context.Background()
	const key = "skills/skill_x/skill.zip"
	finalPath := filepath.Join(dir, filepath.FromSlash(key))

	zipBytes := func(payloadSize int) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("SKILL.md")
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(bytes.Repeat([]byte("x"), payloadSize)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("zip close: %v", err)
		}
		return buf.Bytes()
	}
	const raceWindowPayloadBytes = 256 * 1024
	payload := zipBytes(raceWindowPayloadBytes)

	const iterations = 200
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < iterations; i++ {
			if err := rs.Put(ctx, key, bytes.NewReader(payload)); err != nil {
				t.Errorf("Put iter %d: %v", i, err)
				return
			}
		}
		close(done)
	})

	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(finalPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				t.Errorf("read: %v", err)
				return
			}
			if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
				t.Errorf("observed partial file: len=%d zip err=%v", len(data), err)
				return
			}
		}
	})
	wg.Wait()
}

func runDeletePrefixSuite(t *testing.T, rs *Store) {
	t.Helper()
	ctx := context.Background()

	for _, k := range []string{"runs/r1/data.json", "runs/r1/events.json", "runs/r2/data.json", "other/file"} {
		if err := rs.Put(ctx, k, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
	}

	n, deleteErr := rs.DeletePrefix(ctx, "runs/r1/")
	if deleteErr != nil {
		t.Fatalf("DeletePrefix: %v", deleteErr)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}

	if _, getErr := rs.Get(ctx, "runs/r1/data.json"); !IsNotFound(getErr) {
		t.Errorf("runs/r1/data.json should be gone; got err=%v", getErr)
	}
	if _, getErr := rs.Get(ctx, "runs/r2/data.json"); getErr != nil {
		t.Errorf("runs/r2/data.json should survive; got err=%v", getErr)
	}
	if _, getErr := rs.Get(ctx, "other/file"); getErr != nil {
		t.Errorf("other/file should survive; got err=%v", getErr)
	}

	if _, emptyErr := rs.DeletePrefix(ctx, ""); emptyErr == nil {
		t.Error("expected error for empty prefix")
	}

	n, missingErr := rs.DeletePrefix(ctx, "no/such/prefix/")
	if missingErr != nil {
		t.Errorf("DeletePrefix on missing prefix returned err: %v", missingErr)
	}
	if n != 0 {
		t.Errorf("DeletePrefix on missing prefix returned %d, want 0", n)
	}
}

func TestNewResourceStore_FilesystemDefault(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("SAM_DATA_DIR", t.TempDir())

	st, err := NewResourceStore(nil, ResourceStoreOptions{
		YAMLKey:    "resource_store",
		Purpose:    "connector resources",
		SubdirName: "resources",
		EnvBucket:  "",
		Logger:     slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}
	if st == nil {
		t.Fatal("NewResourceStore returned nil")
	}

	ctx := context.Background()
	if err := st.Put(ctx, "a/b.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, getErr := st.Get(ctx, "a/b.txt")
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Errorf("round-trip = %q, want %q", got, "hello")
	}
}

func TestNewResourceStore_YAMLBlockMemory(t *testing.T) {
	st, err := NewResourceStore(map[string]any{"type": "memory"}, ResourceStoreOptions{
		YAMLKey: "resource_store", Purpose: "test", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}
	if st.client.StorageType() != "memory" {
		t.Errorf("StorageType = %q, want memory", st.client.StorageType())
	}
}
