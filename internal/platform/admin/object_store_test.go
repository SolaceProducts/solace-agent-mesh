// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewResourceStore_FilesystemYAML(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	store, err := objectstore.NewResourceStore(
		map[string]any{"type": "filesystem", "base_path": tmpDir},
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "resources",
			Logger:     discardLogger(),
		},
	)
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}

	if err := store.Put(t.Context(), "test.txt", strings.NewReader("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "resources", "test.txt")); err != nil {
		t.Errorf("expected file at <base>/resources/test.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "test.txt")); !os.IsNotExist(err) {
		t.Error("file must not exist at <base>/test.txt — subdirName was not appended")
	}
}

func TestNewResourceStore_FilesystemYAML_NoBasePathUsesEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", tmpDir)

	store, err := objectstore.NewResourceStore(
		map[string]any{"type": "filesystem"},
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "tools",
			Logger:     discardLogger(),
		},
	)
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}

	if err := store.Put(t.Context(), "pkg.zip", strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "tools", "pkg.zip")); err != nil {
		t.Errorf("expected file at SAM_DATA_DIR/tools/pkg.zip: %v", err)
	}
}

func TestNewResourceStore_YAMLInitError_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", tmpDir)

	store, err := objectstore.NewResourceStore(
		map[string]any{"type": "broken_type", "bucket_name": "irrelevant"},
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "resources",
			Logger:     discardLogger(),
		},
	)

	if err == nil {
		t.Fatal("expected error for unknown store type, got nil")
	}
	if store != nil {
		t.Error("expected nil store on init error")
	}
}

func TestNewResourceStore_EnvVarPath(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "memory")

	store, err := objectstore.NewResourceStore(
		nil,
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "resources",
			EnvBucket:  "test-bucket",
			Logger:     discardLogger(),
		},
	)
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}

	ctx := t.Context()
	if err := store.Put(ctx, "item.txt", strings.NewReader("env-path")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, getErr := store.Get(ctx, "item.txt")
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "env-path" {
		t.Errorf("Get = %q, want %q", string(got), "env-path")
	}
}

func TestNewResourceStore_DefaultFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", tmpDir)
	t.Setenv("OBJECT_STORAGE_TYPE", "")

	store, err := objectstore.NewResourceStore(
		nil,
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "resources",
			Logger:     discardLogger(),
		},
	)
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}

	if err := store.Put(t.Context(), "default.txt", strings.NewReader("default")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "resources", "default.txt")); err != nil {
		t.Errorf("expected file at SAM_DATA_DIR/resources/default.txt: %v", err)
	}
}

func TestNewResourceStore_EnvVarFilesystemType(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", tmpDir)
	t.Setenv("OBJECT_STORAGE_TYPE", "filesystem")

	store, err := objectstore.NewResourceStore(
		nil,
		objectstore.ResourceStoreOptions{
			YAMLKey:    "test_store",
			Purpose:    "test resources",
			SubdirName: "resources",
			EnvBucket:  "some-bucket",
			Logger:     discardLogger(),
		},
	)
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}

	if err := store.Put(t.Context(), "fs-type.txt", strings.NewReader("fs")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "resources", "fs-type.txt")); err != nil {
		t.Errorf("expected file at SAM_DATA_DIR/resources/fs-type.txt: %v", err)
	}
}
