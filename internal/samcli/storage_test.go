// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

func clearBucketEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"OBJECT_STORAGE_TYPE", "OBJECT_STORAGE_BUCKET_NAME",
		"STR_BUCKET_NAME", "ARTIFACT_BUCKET_NAME", "CONNECTOR_SPEC_BUCKET_NAME", "EVAL_DATA_BUCKET_NAME",
		"S3_BUCKET_NAME", "GCS_BUCKET_NAME", "AZURE_STORAGE_CONTAINER_NAME",
	} {
		t.Setenv(v, "")
	}
}

func setCloudEnv(t *testing.T, bucket string) {
	t.Helper()
	clearBucketEnv(t)
	t.Setenv("OBJECT_STORAGE_TYPE", "s3")
	if bucket != "" {
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", bucket)
	}
}

func newMemStore(t *testing.T, keys ...string) *objectstore.Store {
	t.Helper()
	store := objectstore.NewStore(objectstore.NewMemoryClient())
	for _, key := range keys {
		if err := store.Put(context.Background(), key, strings.NewReader("data")); err != nil {
			t.Fatalf("seed key %q: %v", key, err)
		}
	}
	return store
}

func keysOf(t *testing.T, store *objectstore.Store) []string {
	t.Helper()
	keys, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list store: %v", err)
	}
	return keys
}

func testCleanupOpts(namespace string, stores map[string]*objectstore.Store) (*storageCleanupOpts, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &storageCleanupOpts{
		namespace: namespace,
		force:     true,
		logger:    slog.New(slog.NewJSONHandler(out, nil)),
		storeFactory: func(_, bucket string) (*objectstore.Store, error) {
			store, ok := stores[bucket]
			if !ok {
				return nil, fmt.Errorf("unexpected bucket %q", bucket)
			}
			return store, nil
		},
	}, out
}

func TestStorageCleanupSweepsNamespacePrefix(t *testing.T) {
	setCloudEnv(t, "shared")

	store := newMemStore(t,
		"mesh-a/artifacts/file.bin",
		"mesh-a/connectors/conn1/spec.yaml",
		"mesh-a/str-runtime/skills/s1/skill.zip",
		"mesh-a/str-runtime/toolsets/t1/pkg.tgz",
		"mesh-b/artifacts/keep.bin",
		"mesh-ab/keep.bin",
	)
	o, out := testCleanupOpts("mesh-a", map[string]*objectstore.Store{"shared": store})

	if err := runStorageCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	remaining := keysOf(t, store)
	if len(remaining) != 2 {
		t.Fatalf("expected 2 surviving keys, got %d: %v", len(remaining), remaining)
	}
	for _, key := range remaining {
		if strings.HasPrefix(key, "mesh-a/") {
			t.Errorf("key %q should have been deleted", key)
		}
	}
	if !strings.Contains(out.String(), `"deleted":4`) {
		t.Errorf("expected deleted count 4 in output, got: %s", out.String())
	}
}

func TestStorageCleanupDedupesComponentBuckets(t *testing.T) {
	setCloudEnv(t, "shared")
	t.Setenv("ARTIFACT_BUCKET_NAME", "shared")
	t.Setenv("STR_BUCKET_NAME", "tools")

	shared := newMemStore(t, "ns1/a.bin")
	tools := newMemStore(t, "ns1/str-runtime/toolsets/t/pkg.tgz")
	stores := map[string]*objectstore.Store{"shared": shared, "tools": tools}

	var factoryCalls []string
	o, _ := testCleanupOpts("ns1", stores)
	inner := o.storeFactory
	o.storeFactory = func(storageType, bucket string) (*objectstore.Store, error) {
		factoryCalls = append(factoryCalls, bucket)
		return inner(storageType, bucket)
	}

	if err := runStorageCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(factoryCalls) != 2 || factoryCalls[0] != "shared" || factoryCalls[1] != "tools" {
		t.Errorf("expected one sweep each of [shared tools], got %v", factoryCalls)
	}
	if keys := keysOf(t, shared); len(keys) != 0 {
		t.Errorf("shared bucket not emptied: %v", keys)
	}
	if keys := keysOf(t, tools); len(keys) != 0 {
		t.Errorf("tools bucket not emptied: %v", keys)
	}
}

func TestStorageCleanupRequiresForce(t *testing.T) {
	setCloudEnv(t, "shared")

	store := newMemStore(t, "ns1/a.bin")
	o, _ := testCleanupOpts("ns1", map[string]*objectstore.Store{"shared": store})
	o.force = false

	err := runStorageCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected --force refusal, got: %v", err)
	}
	if keys := keysOf(t, store); len(keys) != 1 {
		t.Errorf("store should be untouched, got keys: %v", keys)
	}
}

func TestStorageCleanupNamespaceValidation(t *testing.T) {
	setCloudEnv(t, "shared")

	for _, namespace := range []string{"", "   ", "/", "a/b", ".", "..", " . "} {
		store := newMemStore(t, "a/b/c.bin")
		o, _ := testCleanupOpts(namespace, map[string]*objectstore.Store{"shared": store})
		if err := runStorageCleanup(context.Background(), o); err == nil {
			t.Errorf("namespace %q: expected validation error, got nil", namespace)
		}
		if keys := keysOf(t, store); len(keys) != 1 {
			t.Errorf("namespace %q: store should be untouched, got keys: %v", namespace, keys)
		}
	}
}

func TestStorageCleanupTrimsTrailingSlash(t *testing.T) {
	setCloudEnv(t, "shared")

	store := newMemStore(t, "ns1/a.bin")
	o, _ := testCleanupOpts("ns1/", map[string]*objectstore.Store{"shared": store})
	if err := runStorageCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys := keysOf(t, store); len(keys) != 0 {
		t.Errorf("expected empty store, got keys: %v", keys)
	}
}

func TestStorageCleanupIdempotent(t *testing.T) {
	setCloudEnv(t, "shared")

	o, out := testCleanupOpts("ghost", map[string]*objectstore.Store{"shared": newMemStore(t)})
	if err := runStorageCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"deleted":0`) {
		t.Errorf("expected deleted count 0 in output, got: %s", out.String())
	}
}

func TestStorageCleanupPartialFailure(t *testing.T) {
	setCloudEnv(t, "broken")
	t.Setenv("STR_BUCKET_NAME", "tools")

	tools := newMemStore(t, "ns1/str-runtime/toolsets/t/pkg.tgz")
	o, _ := testCleanupOpts("ns1", map[string]*objectstore.Store{"tools": tools})
	inner := o.storeFactory
	o.storeFactory = func(storageType, bucket string) (*objectstore.Store, error) {
		if bucket == "broken" {
			return nil, errors.New("credentials rejected")
		}
		return inner(storageType, bucket)
	}

	err := runStorageCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), `"broken"`) {
		t.Fatalf("expected error naming bucket \"broken\", got: %v", err)
	}
	if keys := keysOf(t, tools); len(keys) != 0 {
		t.Errorf("tools bucket should still have been swept, got keys: %v", keys)
	}
}

func TestStorageCleanupCancelledContextStopsWithError(t *testing.T) {
	setCloudEnv(t, "shared")
	t.Setenv("STR_BUCKET_NAME", "tools")

	store := newMemStore(t, "ns1/a.bin", "ns1/b.bin")
	tools := newMemStore(t, "ns1/c.bin")
	o, _ := testCleanupOpts("ns1", map[string]*objectstore.Store{"shared": store, "tools": tools})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runStorageCleanup(ctx, o)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected a cancellation error, got: %v", err)
	}
	if keys := keysOf(t, store); len(keys) != 2 {
		t.Errorf("shared should be untouched by a cancelled sweep, got keys: %v", keys)
	}
	if keys := keysOf(t, tools); len(keys) != 1 {
		t.Errorf("tools should be untouched by a cancelled sweep, got keys: %v", keys)
	}
}

func TestStorageCleanupRejectsLocalBackends(t *testing.T) {
	for _, backend := range []string{"filesystem", "memory"} {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", backend)
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "shared")

		store := newMemStore(t, "ns1/a.bin")
		o, _ := testCleanupOpts("ns1", map[string]*objectstore.Store{"shared": store})
		err := runStorageCleanup(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), backend) {
			t.Errorf("backend %q: expected not-supported error, got: %v", backend, err)
		}
		if keys := keysOf(t, store); len(keys) != 1 {
			t.Errorf("backend %q: store should be untouched, got keys: %v", backend, keys)
		}
	}
}

func TestStorageCleanupRequiresStorageType(t *testing.T) {
	clearBucketEnv(t)
	t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "shared")

	o, _ := testCleanupOpts("ns1", nil)
	err := runStorageCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "OBJECT_STORAGE_TYPE") {
		t.Fatalf("expected OBJECT_STORAGE_TYPE error, got: %v", err)
	}
}

func TestStorageCleanupRequiresBucket(t *testing.T) {
	clearBucketEnv(t)
	t.Setenv("OBJECT_STORAGE_TYPE", "s3")

	o, _ := testCleanupOpts("ns1", nil)
	err := runStorageCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "OBJECT_STORAGE_BUCKET_NAME") {
		t.Fatalf("expected no-bucket error, got: %v", err)
	}
}

func TestStorageCleanupCommandWiring(t *testing.T) {
	clearBucketEnv(t)
	t.Setenv("OBJECT_STORAGE_TYPE", "memory")
	t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "shared")

	cmd := newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"storage", "cleanup", "--namespace", "ns1", "--force"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("expected local-backend rejection, got: %v", err)
	}

	cmd = newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"storage", "cleanup", "--force"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("expected required --namespace error, got: %v", err)
	}
}

func TestStorageCommandHidden(t *testing.T) {
	if !newStorageCmd().Hidden {
		t.Error("sam storage must be hidden from help/completion")
	}
	var help bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&help)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if strings.Contains(help.String(), "storage") {
		t.Errorf("root help must not mention the storage command, got:\n%s", help.String())
	}
}
