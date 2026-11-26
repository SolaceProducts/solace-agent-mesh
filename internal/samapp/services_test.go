// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func TestCreateSessionStore_Memory(t *testing.T) {
	store, err := CreateSessionStore(map[string]any{"type": "memory"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCreateSessionStore_DefaultIsMemory(t *testing.T) {
	store, err := CreateSessionStore(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCreateSessionStore_UnknownTypeErrors(t *testing.T) {
	store, err := CreateSessionStore(map[string]any{"type": "redis"})
	if err == nil {
		t.Fatal("expected error for unsupported type, got nil")
	}
	if store != nil {
		t.Fatal("expected nil store on error")
	}
}

func TestCreateSessionStore_SQLNoURLFallsBackToMemory(t *testing.T) {
	store, err := CreateSessionStore(map[string]any{"type": "sql"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil in-memory store")
	}
}

func TestCreateSessionStore_SQLBadURLErrors(t *testing.T) {
	store, err := CreateSessionStore(map[string]any{
		"type":         "sql",
		"database_url": "mysql://user@host:3306/db",
	})
	if err == nil {
		t.Fatal("expected error for unsupported database URL, got nil")
	}
	if store != nil {
		t.Fatal("expected nil store on error")
	}
}

func TestCreateSessionStore_SQLitePersistsAcrossReopen(t *testing.T) {
	dbURL := "sqlite:///" + filepath.Join(t.TempDir(), "sessions.db")
	cfg := map[string]any{"type": "sql", "database_url": dbURL}
	ctx := context.Background()

	store1, err := CreateSessionStore(cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	want := &session.Session{
		AppName:   "agent_abc",
		UserID:    "user1",
		SessionID: "ctx1",
		ContextID: "ctx1",
		Messages:  []session.Message{{Role: "user", Content: "remember this"}},
	}
	if err = store1.Create(ctx, want); err != nil {
		t.Fatalf("create session: %v", err)
	}

	store2, err := CreateSessionStore(cfg)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got, err := store2.Get(ctx, "agent_abc", "user1", "ctx1")
	if err != nil {
		t.Fatalf("get session after reopen: %v", err)
	}
	if got == nil {
		t.Fatal("session did not survive store reopen — history would be lost on restart")
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "remember this" {
		t.Errorf("messages = %+v, want the single persisted message", got.Messages)
	}
}

func TestCreateSessionStore_Sqlite(t *testing.T) {
	dir := t.TempDir()
	store, err := CreateSessionStore(map[string]any{
		"type": "sqlite",
		"path": dir + "/test.db",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestCreateArtifactService_Memory(t *testing.T) {
	svc := CreateArtifactService(map[string]any{"type": "memory"}, map[string]any{"namespace": "test-ns"})
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Scope != "namespace" {
		t.Errorf("scope = %q, want namespace", svc.Scope)
	}
	if svc.Namespace != "test-ns" {
		t.Errorf("namespace = %q, want test-ns", svc.Namespace)
	}
}

func TestCreateArtifactService_NamespaceTrailingSlashIsNormalized(t *testing.T) {
	dir := t.TempDir()
	svc := CreateArtifactService(
		map[string]any{"type": "filesystem", "base_path": dir},
		map[string]any{"namespace": "local-acme-dev/"},
	)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Namespace != "local-acme-dev" {
		t.Errorf("namespace = %q, want local-acme-dev", svc.Namespace)
	}

	uri, _, err := artifact.SaveUpload(t.Context(), svc,
		artifact.UploadScope{UserID: "whatsapp:16135550142", SessionID: "sess-1"},
		artifact.Upload{Filename: "image.jpeg", MIMEType: "image/jpeg", Content: []byte("bytes")},
		artifact.MetadataOpts{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}
	if want := "artifact://local-acme-dev/whatsapp:16135550142/sess-1/image.jpeg?version=0"; uri != want {
		t.Errorf("uri = %q, want %q", uri, want)
	}

	appDir := filepath.Join(dir, "artifacts", "local-acme-dev")
	if _, statErr := os.Stat(appDir); statErr != nil {
		t.Errorf("artifact not stored under the normalized app dir %q: %v", appDir, statErr)
	}
}

func TestCreateArtifactService_UnroutableNamespaceStillBuilds(t *testing.T) {
	for _, ns := range []string{"acme/prod", "/"} {
		svc := CreateArtifactService(map[string]any{"type": "memory"}, map[string]any{"namespace": ns})
		if svc == nil {
			t.Fatalf("namespace %q: expected the service to still be built", ns)
		}
		_, _, err := artifact.SaveUpload(t.Context(), svc,
			artifact.UploadScope{UserID: "u", SessionID: "s"},
			artifact.Upload{Filename: "f.txt", Content: []byte("x")},
			artifact.MetadataOpts{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if !errors.Is(err, artifact.ErrUnroutableAppName) {
			t.Errorf("namespace %q: err = %v, want ErrUnroutableAppName", ns, err)
		}
	}
}

func TestCreateArtifactService_Filesystem(t *testing.T) {
	dir := t.TempDir()
	svc := CreateArtifactService(
		map[string]any{"type": "filesystem", "base_path": dir},
		map[string]any{"namespace": "ns1"},
	)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Scope != "namespace" {
		t.Errorf("scope = %q, want namespace", svc.Scope)
	}
}

func TestCreateArtifactService_CustomScope(t *testing.T) {
	svc := CreateArtifactService(
		map[string]any{"type": "memory", "artifact_scope": "user"},
		map[string]any{"namespace": "ns1"},
	)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Scope != "user" {
		t.Errorf("scope = %q, want user", svc.Scope)
	}
}

func TestCreateArtifactService_EmptyType(t *testing.T) {
	svc := CreateArtifactService(map[string]any{}, map[string]any{})
	if svc != nil {
		t.Error("expected nil service for empty type")
	}
}

func TestCreateArtifactService_UnknownType(t *testing.T) {
	svc := CreateArtifactService(map[string]any{"type": "dynamodb"}, map[string]any{})
	if svc != nil {
		t.Error("expected nil service for unknown type")
	}
}

func TestCreateVolumeService_Memory(t *testing.T) {
	svc, err := CreateVolumeService(map[string]any{"type": "memory"})
	if err != nil || svc == nil {
		t.Fatalf("CreateVolumeService memory: svc=%v err=%v", svc, err)
	}
}

func TestCreateVolumeService_Filesystem(t *testing.T) {
	dir := t.TempDir()
	svc, err := CreateVolumeService(map[string]any{
		"type": "filesystem",
		"root": dir + "/volumes",
	})
	if err != nil || svc == nil {
		t.Fatalf("CreateVolumeService filesystem: svc=%v err=%v", svc, err)
	}
}

func TestCreateVolumeService_ListLivesInTheConfiguredDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "volumes")
	sharedDatabase := filepath.Join(t.TempDir(), "shared.db")
	svc, err := CreateVolumeService(map[string]any{
		"type":         "filesystem",
		"root":         root,
		"database_url": "sqlite:///" + sharedDatabase,
	})
	if err != nil {
		t.Fatalf("CreateVolumeService: %v", err)
	}
	if _, err := svc.Create(t.Context(), volume.CreateOptions{Name: "notes", UserID: "alice", CreatorType: volume.CallerHuman}); err != nil {
		t.Fatalf("svc.Create(notes for alice): %v", err)
	}
	if _, err := os.Stat(sharedDatabase); err != nil {
		t.Fatalf("the configured volume list database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a list file exists under the volume root although database_url is set: err=%v", err)
	}
}

func TestCreateVolumeService_DefaultIsFilesystem(t *testing.T) {
	svc, err := CreateVolumeService(map[string]any{})
	if err != nil || svc == nil {
		t.Fatalf("CreateVolumeService default: svc=%v err=%v", svc, err)
	}
}

func TestCreateVolumeService_UnknownType(t *testing.T) {
	svc, err := CreateVolumeService(map[string]any{"type": "redis"})
	if err == nil {
		t.Fatal("expected error for unknown volume service type")
	}
	if svc != nil {
		t.Errorf("expected nil service for unknown type, got %T", svc)
	}
}

func TestCreateVolumeService_PerUserQuotaBytesAccepted(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []any{int(123), int64(123), float64(123)} {
		t.Run(fmt.Sprintf("type=%T", key), func(t *testing.T) {
			svc, err := CreateVolumeService(map[string]any{
				"type":                 "filesystem",
				"root":                 t.TempDir(),
				"per_user_quota_bytes": key,
			})
			if err != nil {
				t.Fatalf("CreateVolumeService: %v", err)
			}
			if svc == nil {
				t.Fatal("nil service")
			}
		})
	}
	_ = dir
}

func TestVolumeServiceConfigEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want bool
	}{
		{"nil config", nil, false},
		{"absent enabled key = enabled (legacy presence-means-enabled)", map[string]any{"type": "filesystem"}, true},
		{"enabled:true", map[string]any{"enabled": true, "type": "memory"}, true},
		{"enabled:false", map[string]any{"enabled": false, "type": "memory"}, false},
		{"non-bool enabled is ignored, falls back to enabled", map[string]any{"enabled": "true"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := VolumeServiceConfigEnabled(tc.cfg); got != tc.want {
				t.Errorf("VolumeServiceConfigEnabled(%v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

func shortenBucketWait(t *testing.T, timeout time.Duration) {
	t.Helper()
	origTimeout, origInterval, origMax := bucketWaitTimeout, bucketWaitInterval, bucketWaitMaxDelay
	bucketWaitTimeout, bucketWaitInterval, bucketWaitMaxDelay = timeout, time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() {
		bucketWaitTimeout, bucketWaitInterval, bucketWaitMaxDelay = origTimeout, origInterval, origMax
	})
}

func TestWaitForBucket_SucceedsAfterTransientFailures(t *testing.T) {
	shortenBucketWait(t, time.Second)
	calls := 0
	err := waitForBucket(t.Context(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("InvalidAccessKeyId")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestWaitForBucket_ReturnsLastErrorAfterTimeout(t *testing.T) {
	shortenBucketWait(t, 30*time.Millisecond)
	want := errors.New("NoSuchBucket")
	err := waitForBucket(t.Context(), func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestWaitForBucket_KeepsCauseWhenDeadlineCutsOffAttempt(t *testing.T) {
	shortenBucketWait(t, 30*time.Millisecond)
	want := errors.New("NoSuchBucket")
	calls := 0
	err := waitForBucket(t.Context(), func(ctx context.Context) error {
		calls++
		if calls == 1 {
			return want
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
