// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func newUploadManagerForTest(t *testing.T, ttl time.Duration, maxTotalSize int64) *ChunkedUploadManager {
	t.Helper()
	store := objectstore.NewStore(objectstore.NewMemoryClient())
	return NewChunkedUploadManager(store, "testns", ttl, maxTotalSize, slog.New(slog.DiscardHandler))
}

func TestChunkedUploadManager_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	data := []byte("hello chunked world")
	uploadID, chunkSize, initErr := m.Init(ctx, "ts-1", "pkg.zip", int64(len(data)))
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}
	if chunkSize != defaultChunkSize {
		t.Errorf("chunkSize = %d, want %d", chunkSize, defaultChunkSize)
	}

	if putErr := m.PutChunk(ctx, uploadID, 0, data); putErr != nil {
		t.Fatalf("PutChunk: %v", putErr)
	}

	assembled, session, err := m.Assemble(ctx, uploadID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(assembled) != string(data) {
		t.Errorf("assembled %q, want %q", assembled, data)
	}
	if session.ToolsetID != "ts-1" {
		t.Errorf("session.ToolsetID = %q, want ts-1", session.ToolsetID)
	}
	if session.Filename != "pkg.zip" {
		t.Errorf("session.Filename = %q, want pkg.zip", session.Filename)
	}
}

func TestChunkedUploadManager_MultipleChunks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	chunk0 := []byte("chunk-zero-")
	chunk1 := []byte("chunk-one")
	total := append(append([]byte(nil), chunk0...), chunk1...)

	uploadID, _, initErr := m.Init(ctx, "ts-2", "multi.zip", int64(len(total)))
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}

	if putErr := m.PutChunk(ctx, uploadID, 0, chunk0); putErr != nil {
		t.Fatalf("PutChunk 0: %v", putErr)
	}
	if putErr := m.PutChunk(ctx, uploadID, 1, chunk1); putErr != nil {
		t.Fatalf("PutChunk 1: %v", putErr)
	}

	assembled, _, err := m.Assemble(ctx, uploadID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(assembled) != string(total) {
		t.Errorf("assembled %q, want %q", assembled, total)
	}
}

func TestChunkedUploadManager_IdempotentPut(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	data := []byte("idempotent")
	uploadID, _, initErr := m.Init(ctx, "ts-3", "idem.zip", int64(len(data)))
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}

	if putErr := m.PutChunk(ctx, uploadID, 0, data); putErr != nil {
		t.Fatalf("PutChunk first: %v", putErr)
	}
	if putErr := m.PutChunk(ctx, uploadID, 0, data); putErr != nil {
		t.Fatalf("PutChunk second (idempotent): %v", putErr)
	}

	assembled, _, err := m.Assemble(ctx, uploadID)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(assembled) != string(data) {
		t.Errorf("assembled %q, want %q", assembled, data)
	}
}

func TestChunkedUploadManager_MissingChunk(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	uploadID, _, initErr := m.Init(ctx, "ts-4", "gap.zip", 100)
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}

	if putErr := m.PutChunk(ctx, uploadID, 0, []byte("first")); putErr != nil {
		t.Fatalf("PutChunk 0: %v", putErr)
	}
	if putErr := m.PutChunk(ctx, uploadID, 2, []byte("third")); putErr != nil {
		t.Fatalf("PutChunk 2: %v", putErr)
	}

	_, _, err := m.Assemble(ctx, uploadID)
	if err == nil {
		t.Fatal("expected error for missing chunk 1")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_TotalSizeMismatch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	uploadID, _, initErr := m.Init(ctx, "ts-5", "size.zip", 999)
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}

	if putErr := m.PutChunk(ctx, uploadID, 0, []byte("short")); putErr != nil {
		t.Fatalf("PutChunk: %v", putErr)
	}

	_, _, err := m.Assemble(ctx, uploadID)
	if err == nil {
		t.Fatal("expected ValidationError for size mismatch")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_ExpiredSession(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, -time.Second, 0)

	uploadID, _, err := m.Init(ctx, "ts-6", "ttl.zip", 10)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	err = m.PutChunk(ctx, uploadID, 0, []byte("data"))
	if err == nil {
		t.Fatal("expected NotFoundError for expired session on PutChunk")
	}
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_UnknownUploadID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	for _, id := range []string{"0190c7d8-4e2b-7f6a-9b1c-5d3e7f9a1b2c", "no-such-id", ".."} {
		err := m.PutChunk(ctx, id, 0, []byte("x"))
		if _, ok := err.(*NotFoundError); !ok {
			t.Errorf("PutChunk(%q): expected *NotFoundError, got %T: %v", id, err, err)
		}
		_, _, err = m.Assemble(ctx, id)
		if _, ok := err.(*NotFoundError); !ok {
			t.Errorf("Assemble(%q): expected *NotFoundError, got %T: %v", id, err, err)
		}
	}
}

func TestChunkedUploadManager_ExceedsMaxSize(t *testing.T) {
	t.Parallel()
	m := newUploadManagerForTest(t, time.Minute, 0)

	_, _, err := m.Init(t.Context(), "ts-7", "big.zip", platpkg.DefaultMaxUploadBytes+1)
	if err == nil {
		t.Fatal("expected ValidationError for oversized totalSize")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_ConfiguredMaxSize(t *testing.T) {
	t.Parallel()
	const limit = 1 << 10
	m := newUploadManagerForTest(t, time.Minute, limit)

	if _, _, err := m.Init(t.Context(), "ts-7", "fits.zip", limit); err != nil {
		t.Fatalf("Init at the limit: %v", err)
	}
	_, _, err := m.Init(t.Context(), "ts-7", "big.zip", limit+1)
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("Init over the limit: expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_Cleanup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	data := []byte("cleanup me")
	uploadID, _, initErr := m.Init(ctx, "ts-8", "clean.zip", int64(len(data)))
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}
	if putErr := m.PutChunk(ctx, uploadID, 0, data); putErr != nil {
		t.Fatalf("PutChunk: %v", putErr)
	}

	m.Cleanup(ctx, uploadID)

	_, _, err := m.Assemble(ctx, uploadID)
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
	if left := uploadObjectKeys(t, m); len(left) != 0 {
		t.Errorf("objects left after Cleanup: %v", left)
	}
}

func TestChunkedUploadManager_NoChunks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)

	uploadID, _, err := m.Init(ctx, "ts-9", "empty.zip", 42)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, _, err = m.Assemble(ctx, uploadID)
	if err == nil {
		t.Fatal("expected error assembling with no chunks")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_ZeroTotalSize(t *testing.T) {
	t.Parallel()
	m := newUploadManagerForTest(t, time.Minute, 0)

	_, _, err := m.Init(t.Context(), "ts-z", "zero.zip", 0)
	if err == nil {
		t.Fatal("expected ValidationError for zero totalSize")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestChunkedUploadManager_StepsOnAnotherProcess(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := objectstore.NewStore(objectstore.NewFilesystemClient(t.TempDir()))
	logger := slog.New(slog.DiscardHandler)
	a := NewChunkedUploadManager(store, "testns", time.Minute, 0, logger)
	b := NewChunkedUploadManager(store, "testns", time.Minute, 0, logger)
	a.chunkSize, b.chunkSize = 4, 4

	uploadID, _, err := a.InitFixed(ctx, "ts-11", "shared.zip", 10)
	if err != nil {
		t.Fatalf("Init on a: %v", err)
	}
	if err = b.PutChunk(ctx, uploadID, 0, []byte("abcd")); err != nil {
		t.Fatalf("PutChunk 0 on b: %v", err)
	}
	if err = a.PutChunk(ctx, uploadID, 2, []byte("ij")); err != nil {
		t.Fatalf("PutChunk 2 on a: %v", err)
	}
	status, err := b.Status(ctx, uploadID)
	if err != nil {
		t.Fatalf("Status on b: %v", err)
	}
	if status.ReceivedBytes != 6 || !slices.Equal(status.Received, []int{0, 2}) || status.ToolsetID != "ts-11" {
		t.Errorf("status on b = %+v", status)
	}
	if err = b.PutChunk(ctx, uploadID, 1, []byte("efgh")); err != nil {
		t.Fatalf("PutChunk 1 on b: %v", err)
	}
	assembled, session, err := a.Assemble(ctx, uploadID)
	if err != nil || string(assembled) != "abcdefghij" || session.Filename != "shared.zip" {
		t.Fatalf("Assemble on a = %q, %+v, %v", assembled, session, err)
	}
	a.Cleanup(ctx, uploadID)
	if _, err := b.Status(ctx, uploadID); !isNotFound(err) {
		t.Errorf("Status on b after cleanup on a = %v, want not found", err)
	}
}

func TestChunkedUploadManager_CleanupJobDeletesExpiredSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := objectstore.NewStore(objectstore.NewMemoryClient())
	logger := slog.New(slog.DiscardHandler)
	live := NewChunkedUploadManager(store, "testns", time.Hour, 0, logger)
	expired := NewChunkedUploadManager(store, "testns", -time.Second, 0, logger)

	liveID, _, err := live.Init(ctx, "ts-12", "live.zip", 10)
	if err != nil {
		t.Fatalf("Init live: %v", err)
	}
	if err = live.PutChunk(ctx, liveID, 0, []byte("abc")); err != nil {
		t.Fatalf("PutChunk live: %v", err)
	}
	orphan := live.chunkKey("0190c7d8-4e2b-7f6a-9b1c-5d3e7f9a1b2c", 0)
	packageZip := packageKey("testns", "kept")
	for _, key := range []string{orphan, packageZip} {
		if err = store.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	job := live.CleanupJob()
	if job.Name != leader.JobPlatformToolsetUploadCleanup || job.Every != uploadCleanupInterval {
		t.Errorf("job = %s every %v", job.Name, job.Every)
	}
	if err = job.Run(ctx, leader.Term{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if left := uploadObjectKeys(t, live); len(left) != 3 {
		t.Errorf("objects after a run with nothing expired = %v, want the live session and the orphan", left)
	}

	staleID := "0190c7d8-4e2b-7f6a-9b1c-5d3e7f9a1b2d"
	staleManifest, err := json.Marshal(ChunkedSession{ToolsetID: "ts-12", Filename: "stale.zip", TotalSize: 10, ChunkSize: 4, CreatedAt: time.Now().Add(-2 * time.Hour)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := store.Put(ctx, live.sessionPrefix(staleID)+uploadManifestName, bytes.NewReader(staleManifest)); err != nil {
		t.Fatalf("Put stale manifest: %v", err)
	}
	if err := store.Put(ctx, live.chunkKey(staleID, 0), strings.NewReader("abcd")); err != nil {
		t.Fatalf("Put stale chunk: %v", err)
	}
	if err := job.Run(ctx, leader.Term{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, key := range uploadObjectKeys(t, live) {
		if strings.HasPrefix(key, live.sessionPrefix(staleID)) {
			t.Errorf("%s kept, want the session deleted by its manifest's creation time", key)
		}
	}

	if err := expired.CleanupJob().Run(ctx, leader.Term{}); err != nil {
		t.Fatalf("Run with the session expired: %v", err)
	}
	if left := uploadObjectKeys(t, live); len(left) != 0 {
		t.Errorf("objects after the session expired = %v, want none", left)
	}
	if _, err := store.Head(ctx, packageZip); err != nil {
		t.Errorf("package outside the upload prefix: %v", err)
	}
}

func uploadObjectKeys(t *testing.T, m *ChunkedUploadManager) []string {
	t.Helper()
	objects, err := m.store.ListObjects(t.Context(), m.prefix)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	keys := make([]string, 0, len(objects))
	for _, obj := range objects {
		keys = append(keys, obj.Key)
	}
	slices.Sort(keys)
	return keys
}

func TestChunkedUploadManager_FixedBoundariesRejectAChunkPastTheLastIndex(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)
	m.chunkSize = 4
	uploadID, _, err := m.InitFixed(ctx, "ts-6", "bounds.zip", 10)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.PutChunk(ctx, uploadID, 2, []byte("ab")); err != nil {
		t.Fatalf("PutChunk 2 (the last index): %v", err)
	}
	if err := m.PutChunk(ctx, uploadID, 3, []byte("x")); !errors.Is(err, ErrChunkIndexOutOfRange) {
		t.Fatalf("PutChunk 3 = %v, want ErrChunkIndexOutOfRange", err)
	}
	if status, _ := m.Status(ctx, uploadID); !slices.Equal(status.Received, []int{2}) {
		t.Errorf("received %v, want only chunk 2", status.Received)
	}
}

func TestChunkedUploadManager_RejectsBytesPastTheDeclaredSize(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)
	m.chunkSize = 4
	uploadID, _, err := m.Init(ctx, "ts-7", "bounds.zip", 6)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, step := range []struct {
		n    int
		data string
		want error
	}{
		{0, "abcd", nil},
		{0, "abcd", nil},
		{1, "efg", ErrUploadExceedsDeclaredSize},
		{1, "ef", nil},
	} {
		if err = m.PutChunk(ctx, uploadID, step.n, []byte(step.data)); !errors.Is(err, step.want) {
			t.Fatalf("PutChunk(%d, %q) = %v, want %v", step.n, step.data, err, step.want)
		}
	}
	assembled, _, err := m.Assemble(ctx, uploadID)
	if err != nil || string(assembled) != "abcdef" {
		t.Fatalf("Assemble = %q, %v", assembled, err)
	}
	status, err := m.Status(ctx, uploadID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.ReceivedBytes != 6 || !slices.Equal(status.Received, []int{0, 1}) || status.ToolsetID != "ts-7" {
		t.Errorf("status %+v", status)
	}
	if !status.ExpiresAt.Equal(status.CreatedAt.Add(time.Minute)) {
		t.Errorf("expires %v, created %v", status.ExpiresAt, status.CreatedAt)
	}
}

func TestChunkedUploadManager_FreeSizeChunksJoinInIndexOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)
	uploadID, _, err := m.Init(ctx, "ts-8", "free.zip", 6)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i, chunk := range []string{"a", "bcd", "ef"} {
		if putErr := m.PutChunk(ctx, uploadID, i, []byte(chunk)); putErr != nil {
			t.Fatalf("PutChunk %d: %v", i, putErr)
		}
	}
	if putErr := m.PutChunk(ctx, uploadID, 6, []byte("x")); !errors.Is(putErr, ErrChunkIndexOutOfRange) {
		t.Errorf("PutChunk 6 of a 6-byte upload = %v, want ErrChunkIndexOutOfRange", putErr)
	}
	assembled, _, err := m.Assemble(ctx, uploadID)
	if err != nil || string(assembled) != "abcdef" {
		t.Fatalf("Assemble = %q, %v, want abcdef", assembled, err)
	}

	large, _, err := m.Init(ctx, "ts-8", "large.zip", maxChunksPerUpload*2)
	if err != nil {
		t.Fatalf("Init large: %v", err)
	}
	if putErr := m.PutChunk(ctx, large, maxChunksPerUpload-1, []byte("x")); putErr != nil {
		t.Errorf("PutChunk at the last allowed index: %v", putErr)
	}
	if putErr := m.PutChunk(ctx, large, maxChunksPerUpload, []byte("x")); !errors.Is(putErr, ErrChunkIndexOutOfRange) {
		t.Errorf("PutChunk past maxChunksPerUpload = %v, want ErrChunkIndexOutOfRange", putErr)
	}
}

func TestChunkedUploadManager_FixedBoundariesRejectAChunkOfTheWrongSize(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newUploadManagerForTest(t, time.Minute, 0)
	m.chunkSize = 4
	uploadID, _, err := m.InitFixed(ctx, "ts-8", "fixed.zip", 10)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, step := range []struct {
		n    int
		data string
		want int64
	}{
		{0, "ab", 4},
		{2, "abc", 2},
	} {
		putErr := m.PutChunk(ctx, uploadID, step.n, []byte(step.data))
		mismatch, ok := errors.AsType[*ChunkSizeMismatchError](putErr)
		if !ok || mismatch.Index != step.n || mismatch.Want != step.want {
			t.Errorf("PutChunk(%d, %q) = %v, want a mismatch wanting %d bytes", step.n, step.data, putErr, step.want)
		}
	}
	for i, chunk := range []string{"abcd", "efgh", "ij"} {
		if putErr := m.PutChunk(ctx, uploadID, i, []byte(chunk)); putErr != nil {
			t.Fatalf("PutChunk %d: %v", i, putErr)
		}
	}
	assembled, _, err := m.Assemble(ctx, uploadID)
	if err != nil || string(assembled) != "abcdefghij" {
		t.Fatalf("Assemble = %q, %v", assembled, err)
	}
}

func TestChunkedUploadManager_ShortUploadIsRejectedBeforeAssembly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		init  func(context.Context, *ChunkedUploadManager) (string, int64, error)
		index int
		chunk []byte
		want  string
	}{
		{
			name: "free size",
			init: func(ctx context.Context, m *ChunkedUploadManager) (string, int64, error) {
				return m.Init(ctx, "ts-9", "big.zip", platpkg.DefaultMaxUploadBytes)
			},
			chunk: []byte("abc"),
			want:  fmt.Sprintf("assembled size 3 != declared totalSize %d", platpkg.DefaultMaxUploadBytes),
		},
		{
			name: "fixed boundaries",
			init: func(ctx context.Context, m *ChunkedUploadManager) (string, int64, error) {
				return m.InitFixed(ctx, "ts-9", "big.zip", platpkg.DefaultMaxUploadBytes)
			},
			index: platpkg.DefaultMaxUploadBytes/defaultChunkSize - 1,
			chunk: make([]byte, defaultChunkSize),
			want:  "missing chunk 0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			m := newUploadManagerForTest(t, time.Minute, 0)
			uploadID, _, err := tc.init(ctx, m)
			if err != nil {
				t.Fatalf("Init: %v", err)
			}
			if putErr := m.PutChunk(ctx, uploadID, tc.index, tc.chunk); putErr != nil {
				t.Fatalf("PutChunk: %v", putErr)
			}

			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, _, err = m.Assemble(ctx, uploadID)
			runtime.ReadMemStats(&after)

			valErr, ok := errors.AsType[*ValidationError](err)
			if !ok || valErr.Field != fieldUploadChunks || valErr.Message != tc.want {
				t.Fatalf("Assemble = %v, want %q under chunks", err, tc.want)
			}
			const allocationCeiling = 1 << 20
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > allocationCeiling {
				t.Errorf("Assemble allocated %d bytes before rejecting, want at most %d", allocated, allocationCeiling)
			}
		})
	}
}

type shrinkingStore struct {
	*objectstore.Store
	key string
}

func (s *shrinkingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == s.key {
		return io.NopCloser(strings.NewReader("d")), nil
	}
	return s.Store.Get(ctx, key)
}

func TestChunkedUploadManager_ChunkShorterThanListedIsRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := &shrinkingStore{Store: objectstore.NewStore(objectstore.NewMemoryClient())}
	m := NewChunkedUploadManager(store, "testns", time.Minute, 0, slog.New(slog.DiscardHandler))
	uploadID, _, err := m.Init(ctx, "ts-10", "short.zip", 6)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i, chunk := range []string{"abc", "def"} {
		if putErr := m.PutChunk(ctx, uploadID, i, []byte(chunk)); putErr != nil {
			t.Fatalf("PutChunk %d: %v", i, putErr)
		}
	}
	store.key = m.chunkKey(uploadID, 1)

	assembled, _, err := m.Assemble(ctx, uploadID)
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok || validation.Message != "assembled size 4 != declared totalSize 6" {
		t.Fatalf("Assemble = %q, %v, want a short-assembly ValidationError", assembled, err)
	}
}
