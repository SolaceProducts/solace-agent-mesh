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
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const (
	defaultChunkSize = 4 << 20

	defaultUploadTTL = time.Hour

	uploadCleanupInterval = 5 * time.Minute

	maxChunksPerUpload = 10_000

	MaxChunkBytes = 2 * defaultChunkSize

	uploadManifestName = "manifest"
)

const fieldUploadChunks = "chunks"

var (
	ErrChunkIndexOutOfRange      = errors.New("chunk index is past the last chunk the upload allows")
	ErrUploadExceedsDeclaredSize = errors.New("chunks exceed the declared totalSize")
)

type ChunkSizeMismatchError struct {
	Index int
	Want  int64
	Got   int64
}

func (e *ChunkSizeMismatchError) Error() string {
	return fmt.Sprintf("chunk %d is %d bytes, want %d", e.Index, e.Got, e.Want)
}

type ChunkedSession struct {
	ToolsetID       string    `json:"toolsetId"`
	Filename        string    `json:"filename"`
	TotalSize       int64     `json:"totalSize"`
	ChunkSize       int64     `json:"chunkSize"`
	CreatedAt       time.Time `json:"createdAt"`
	FixedBoundaries bool      `json:"fixedBoundaries"`
}

func (s *ChunkedSession) lastIndex() int {
	if s.FixedBoundaries {
		return int((s.TotalSize+s.ChunkSize-1)/s.ChunkSize) - 1
	}
	return int(min(s.TotalSize, maxChunksPerUpload)) - 1
}

func (s *ChunkedSession) fixedChunkSize(n int) int64 {
	if n < s.lastIndex() {
		return s.ChunkSize
	}
	return s.TotalSize - int64(n)*s.ChunkSize
}

type ChunkedSessionStatus struct {
	ToolsetID     string
	Filename      string
	TotalSize     int64
	ChunkSize     int64
	ReceivedBytes int64
	Received      []int
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type uploadStore interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	ListObjects(ctx context.Context, prefix string) ([]objectstore.ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

type ChunkedUploadManager struct {
	store        uploadStore
	prefix       string
	ttl          time.Duration
	chunkSize    int64
	maxTotalSize int64
	logger       *slog.Logger
}

func NewChunkedUploadManager(store uploadStore, namespace string, ttl time.Duration, maxTotalSize int64, logger *slog.Logger) *ChunkedUploadManager {
	if ttl == 0 {
		ttl = defaultUploadTTL
	}
	if maxTotalSize == 0 {
		maxTotalSize = platpkg.DefaultMaxUploadBytes
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ChunkedUploadManager{
		store:        store,
		prefix:       uploadsPrefix(namespace),
		ttl:          ttl,
		chunkSize:    defaultChunkSize,
		maxTotalSize: maxTotalSize,
		logger:       logger,
	}
}

func uploadsPrefix(namespace string) string {
	if namespace != "" {
		return namespace + "/toolset-uploads/"
	}
	return "toolset-uploads/"
}

func (m *ChunkedUploadManager) sessionPrefix(uploadID string) string {
	return m.prefix + uploadID + "/"
}

func (m *ChunkedUploadManager) chunkKey(uploadID string, n int) string {
	return m.sessionPrefix(uploadID) + strconv.Itoa(n)
}

func (m *ChunkedUploadManager) Init(ctx context.Context, toolsetID, filename string, totalSize int64) (uploadID string, chunkSize int64, err error) {
	return m.init(ctx, toolsetID, filename, totalSize, false)
}

func (m *ChunkedUploadManager) InitFixed(ctx context.Context, toolsetID, filename string, totalSize int64) (uploadID string, chunkSize int64, err error) {
	return m.init(ctx, toolsetID, filename, totalSize, true)
}

func (m *ChunkedUploadManager) init(ctx context.Context, toolsetID, filename string, totalSize int64, fixedBoundaries bool) (uploadID string, chunkSize int64, err error) {
	if totalSize <= 0 {
		return "", 0, &ValidationError{Field: "totalSize", Message: "totalSize must be > 0"}
	}
	if totalSize > m.maxTotalSize {
		return "", 0, &ValidationError{Field: "totalSize", Message: fmt.Sprintf("totalSize exceeds %d MB limit", m.maxTotalSize>>20)}
	}

	uploadID = idgen.New()
	manifest, err := json.Marshal(ChunkedSession{
		ToolsetID:       toolsetID,
		Filename:        filename,
		TotalSize:       totalSize,
		ChunkSize:       m.chunkSize,
		CreatedAt:       time.Now(),
		FixedBoundaries: fixedBoundaries,
	})
	if err != nil {
		return "", 0, fmt.Errorf("encode upload manifest: %w", err)
	}
	if err := m.store.Put(ctx, m.sessionPrefix(uploadID)+uploadManifestName, bytes.NewReader(manifest)); err != nil {
		return "", 0, fmt.Errorf("store upload manifest: %w", err)
	}
	return uploadID, m.chunkSize, nil
}

func (m *ChunkedUploadManager) PutChunk(ctx context.Context, uploadID string, n int, data []byte) error {
	session, err := m.liveSession(ctx, uploadID)
	if err != nil {
		return err
	}
	if n > session.lastIndex() {
		return fmt.Errorf("%w: index %d, last %d", ErrChunkIndexOutOfRange, n, session.lastIndex())
	}
	received, receivedBytes, err := m.receivedChunks(ctx, uploadID)
	if err != nil {
		return err
	}
	total := receivedBytes - received[n] + int64(len(data))
	if total > session.TotalSize {
		return fmt.Errorf("%w: %d bytes of %d", ErrUploadExceedsDeclaredSize, total, session.TotalSize)
	}
	if want := session.fixedChunkSize(n); session.FixedBoundaries && int64(len(data)) != want {
		return &ChunkSizeMismatchError{Index: n, Want: want, Got: int64(len(data))}
	}
	if err := m.store.Put(ctx, m.chunkKey(uploadID, n), bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write chunk %d: %w", n, err)
	}
	return nil
}

func (m *ChunkedUploadManager) Status(ctx context.Context, uploadID string) (*ChunkedSessionStatus, error) {
	session, err := m.liveSession(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	received, receivedBytes, err := m.receivedChunks(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	indices := make([]int, 0, len(received))
	for i := range received {
		indices = append(indices, i)
	}
	slices.Sort(indices)
	return &ChunkedSessionStatus{
		ToolsetID:     session.ToolsetID,
		Filename:      session.Filename,
		TotalSize:     session.TotalSize,
		ChunkSize:     session.ChunkSize,
		ReceivedBytes: receivedBytes,
		Received:      indices,
		CreatedAt:     session.CreatedAt,
		ExpiresAt:     session.CreatedAt.Add(m.ttl),
	}, nil
}

func (m *ChunkedUploadManager) liveSession(ctx context.Context, uploadID string) (*ChunkedSession, error) {
	notFound := &NotFoundError{Resource: "upload session", ID: uploadID}
	if _, err := uuid.Parse(uploadID); err != nil {
		return nil, notFound
	}
	session, err := m.readManifest(ctx, m.sessionPrefix(uploadID)+uploadManifestName)
	if objectstore.IsNotFound(err) {
		return nil, notFound
	}
	if err != nil {
		return nil, fmt.Errorf("upload session %s: %w", uploadID, err)
	}
	if time.Since(session.CreatedAt) > m.ttl {
		return nil, notFound
	}
	return session, nil
}

func (m *ChunkedUploadManager) readManifest(ctx context.Context, key string) (*ChunkedSession, error) {
	rc, err := m.store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read upload manifest %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	var session ChunkedSession
	if err := json.NewDecoder(rc).Decode(&session); err != nil {
		return nil, fmt.Errorf("decode upload manifest %s: %w", key, err)
	}
	return &session, nil
}

func (m *ChunkedUploadManager) receivedChunks(ctx context.Context, uploadID string) (map[int]int64, int64, error) {
	objects, err := m.store.ListObjects(ctx, m.sessionPrefix(uploadID))
	if err != nil {
		return nil, 0, fmt.Errorf("list upload chunks: %w", err)
	}
	received := make(map[int]int64, len(objects))
	var total int64
	for _, obj := range objects {
		n, err := strconv.Atoi(path.Base(obj.Key))
		if err != nil || n < 0 {
			continue
		}
		received[n] = obj.Size
		total += obj.Size
	}
	return received, total, nil
}

func (m *ChunkedUploadManager) Assemble(ctx context.Context, uploadID string) ([]byte, *ChunkedSession, error) {
	data, session, err := m.assemble(ctx, uploadID)
	if _, chunksWrong := errors.AsType[*ValidationError](err); chunksWrong {
		_, sessionErr := m.liveSession(ctx, uploadID)
		if _, deletedMeanwhile := errors.AsType[*NotFoundError](sessionErr); deletedMeanwhile {
			return nil, nil, sessionErr
		}
	}
	return data, session, err
}

func (m *ChunkedUploadManager) assemble(ctx context.Context, uploadID string) ([]byte, *ChunkedSession, error) {
	session, err := m.liveSession(ctx, uploadID)
	if err != nil {
		return nil, nil, err
	}
	received, receivedBytes, err := m.receivedChunks(ctx, uploadID)
	if err != nil {
		return nil, nil, err
	}
	if len(received) == 0 {
		return nil, nil, &ValidationError{Field: fieldUploadChunks, Message: "no chunks received"}
	}
	count := len(received)
	if session.FixedBoundaries {
		count = session.lastIndex() + 1
	}
	for i := range count {
		if _, ok := received[i]; !ok {
			return nil, nil, &ValidationError{Field: fieldUploadChunks, Message: fmt.Sprintf("missing chunk %d", i)}
		}
	}
	if receivedBytes != session.TotalSize {
		return nil, nil, &ValidationError{Field: fieldUploadChunks, Message: fmt.Sprintf(
			"assembled size %d != declared totalSize %d", receivedBytes, session.TotalSize)}
	}

	assembled := bytes.NewBuffer(make([]byte, 0, session.TotalSize))
	for i := range count {
		if err := m.appendChunk(ctx, assembled, uploadID, session, i); err != nil {
			return nil, nil, err
		}
	}
	if int64(assembled.Len()) != session.TotalSize {
		return nil, nil, &ValidationError{Field: fieldUploadChunks, Message: fmt.Sprintf(
			"assembled size %d != declared totalSize %d", assembled.Len(), session.TotalSize)}
	}
	return assembled.Bytes(), session, nil
}

func (m *ChunkedUploadManager) appendChunk(ctx context.Context, assembled *bytes.Buffer, uploadID string, session *ChunkedSession, i int) error {
	rc, err := m.store.Get(ctx, m.chunkKey(uploadID, i))
	if objectstore.IsNotFound(err) {
		return &ValidationError{Field: fieldUploadChunks, Message: fmt.Sprintf("missing chunk %d", i)}
	}
	if err != nil {
		return fmt.Errorf("read chunk %d: %w", i, err)
	}
	defer func() { _ = rc.Close() }()
	remaining := session.TotalSize - int64(assembled.Len())
	if _, err := io.Copy(assembled, io.LimitReader(rc, remaining+1)); err != nil {
		return fmt.Errorf("read chunk %d: %w", i, err)
	}
	if int64(assembled.Len()) > session.TotalSize {
		return &ValidationError{Field: fieldUploadChunks, Message: fmt.Sprintf("assembled size exceeds declared totalSize %d", session.TotalSize)}
	}
	return nil
}

func (m *ChunkedUploadManager) Cleanup(ctx context.Context, uploadID string) {
	ctx = context.WithoutCancel(ctx)
	if err := m.store.Delete(ctx, m.sessionPrefix(uploadID)+uploadManifestName); err != nil {
		m.logger.Warn("delete chunked upload failed", "uploadID", uploadID, "error", err)
		return
	}
	if _, err := m.store.DeletePrefix(ctx, m.sessionPrefix(uploadID)); err != nil {
		m.logger.Warn("delete chunked upload failed", "uploadID", uploadID, "error", err)
	}
}

func (m *ChunkedUploadManager) CleanupJob() leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformToolsetUploadCleanup,
		Every: uploadCleanupInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			return m.deleteExpired(ctx, time.Now().Add(-m.ttl))
		},
	}
}

func (m *ChunkedUploadManager) deleteExpired(ctx context.Context, cutoff time.Time) error {
	objects, err := m.store.ListObjects(ctx, m.prefix)
	if err != nil {
		return fmt.Errorf("list chunked uploads: %w", err)
	}
	oldest := make(map[string]time.Time)
	hasManifest := make(map[string]bool)
	for _, obj := range objects {
		uploadID, name, ok := strings.Cut(strings.TrimPrefix(obj.Key, m.prefix), "/")
		if !ok || uploadID == "" {
			continue
		}
		if name == uploadManifestName {
			hasManifest[uploadID] = true
		}
		if t, seen := oldest[uploadID]; !seen || obj.Modified.Before(t) {
			oldest[uploadID] = obj.Modified
		}
	}
	var errs []error
	for uploadID, created := range oldest {
		if hasManifest[uploadID] {
			if session, readErr := m.readManifest(ctx, m.sessionPrefix(uploadID)+uploadManifestName); readErr == nil {
				created = session.CreatedAt
			}
		}
		if !created.Before(cutoff) {
			continue
		}
		if _, delErr := m.store.DeletePrefix(ctx, m.sessionPrefix(uploadID)); delErr != nil {
			errs = append(errs, fmt.Errorf("delete chunked upload %s: %w", uploadID, delErr))
		}
	}
	return errors.Join(errs...)
}
