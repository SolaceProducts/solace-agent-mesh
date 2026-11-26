// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestNewEvalStorageFromEnv_Disabled(t *testing.T) {
	t.Setenv("EVAL_DATA_BUCKET_NAME", "")
	t.Setenv("OBJECT_STORAGE_FS_ROOT", "")
	svc := newEvalStorageFromEnv("test-ns", slog.Default())
	if svc == nil {
		t.Fatal("expected non-nil StorageService even when storage is disabled")
	}
	if svc.Available() {
		t.Error("Available() = true, want false when no env vars set")
	}
}

func TestNewEvalStorageFromEnv_FilesystemMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EVAL_DATA_BUCKET_NAME", "")
	t.Setenv("OBJECT_STORAGE_FS_ROOT", dir)
	svc := newEvalStorageFromEnv("test-ns", slog.Default())
	if svc == nil || !svc.Available() {
		t.Fatalf("expected available StorageService, got %v", svc)
	}

	key, err := svc.UploadExecutionData("run-1", "ex-1", 0, map[string]any{"hello": "world"})
	if err != nil {
		t.Fatalf("UploadExecutionData: %v", err)
	}
	full := filepath.Join(dir, key)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("expected blob at %s: %v", full, err)
	}
}

func TestNewEvalStorageFromEnv_BucketWithoutCredsFallsBackGracefully(t *testing.T) {
	t.Setenv("EVAL_DATA_BUCKET_NAME", "test-bucket")
	t.Setenv("OBJECT_STORAGE_TYPE", "s3")
	t.Setenv("OBJECT_STORAGE_FS_ROOT", "")
	t.Setenv("AWS_REGION", "us-east-1")
	svc := newEvalStorageFromEnv("test-ns", slog.Default())
	if svc == nil {
		t.Fatal("expected non-nil StorageService even on init failure")
	}
}

func TestNewEvalModelResolver_NilService(t *testing.T) {
	if newEvalModelResolver(nil, slog.Default()) != nil {
		t.Error("expected nil resolver for nil svc")
	}
}

func TestNewEvalModelResolver_LookupAndMissing(t *testing.T) {
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4o",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-secret"},
	}, "user1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	resolve := newEvalModelResolver(svc, slog.Default())
	if resolve == nil {
		t.Fatal("expected non-nil resolver")
	}

	raw, snap := resolve("general")
	if raw == nil || snap == nil {
		t.Fatalf("expected (raw, snap) for known alias, got (%v, %v)", raw, snap)
	}
	if raw["model"] != "gpt-4o" {
		t.Errorf("raw[model] = %v, want gpt-4o", raw["model"])
	}
	if snap["alias"] != "general" {
		t.Errorf("snap[alias] = %v", snap["alias"])
	}

	if raw, snap := resolve(""); raw != nil || snap != nil {
		t.Errorf("expected (nil, nil) for empty input, got (%v, %v)", raw, snap)
	}

	if raw, snap := resolve("does-not-exist"); raw != nil || snap != nil {
		t.Errorf("expected (nil, nil) for unknown alias, got (%v, %v)", raw, snap)
	}
}
