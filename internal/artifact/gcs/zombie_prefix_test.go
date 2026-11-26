// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gcs

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type zombieMockBucket struct {
	*mockBucket
	zmu            sync.Mutex
	zombiePrefixes map[string]bool
}

func newZombieMockBucket() *zombieMockBucket {
	return &zombieMockBucket{
		mockBucket:     newMockBucket(),
		zombiePrefixes: make(map[string]bool),
	}
}

func (z *zombieMockBucket) markZombie(prefix string) {
	z.zmu.Lock()
	defer z.zmu.Unlock()
	z.zombiePrefixes[prefix] = true
}

func (z *zombieMockBucket) Objects(ctx context.Context, query *storage.Query) ObjectIteratorAPI {
	it := z.mockBucket.Objects(ctx, query)
	if query == nil || query.Delimiter == "" {
		return it
	}

	var extras []*storage.ObjectAttrs
	z.zmu.Lock()
	for zp := range z.zombiePrefixes {
		if !strings.HasPrefix(zp, query.Prefix) {
			continue
		}
		rest := strings.TrimPrefix(zp, query.Prefix)
		inner := strings.TrimSuffix(rest, "/")
		if inner == "" || strings.Contains(inner, "/") {
			continue
		}
		extras = append(extras, &storage.ObjectAttrs{Prefix: zp})
	}
	z.zmu.Unlock()

	if len(extras) == 0 {
		return it
	}
	return &appendingIterator{inner: it, extras: extras}
}

type appendingIterator struct {
	inner  ObjectIteratorAPI
	extras []*storage.ObjectAttrs
	idx    int
	done   bool
}

func (a *appendingIterator) Next() (*storage.ObjectAttrs, error) {
	if !a.done {
		attrs, err := a.inner.Next()
		if err == nil {
			return attrs, nil
		}
		a.done = true
	}
	if a.idx < len(a.extras) {
		out := a.extras[a.idx]
		a.idx++
		return out, nil
	}
	return a.inner.Next()
}

func TestGCSListKeys_IgnoresZombiePrefix(t *testing.T) {
	bucket := newZombieMockBucket()
	store := New(bucket, "test-bucket")
	ctx := t.Context()

	loc := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "project-1", Filename: "deleted.docx",
	}
	if _, err := store.Save(ctx, loc, []byte("hi"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.Delete(ctx, loc); err != nil {
		t.Fatalf("delete: %v", err)
	}
	bucket.markZombie(artifact.BuildArtifactPrefix(loc))

	scope := artifact.KeyScope{AppName: "app", UserID: "alice", SessionID: "project-1"}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if slices.Contains(keys, "deleted.docx") {
		t.Fatalf("zombie prefix surfaced as artifact: %q", keys)
	}
}

func TestGCSListKeys_DedupsMultipleVersions(t *testing.T) {
	bucket := newZombieMockBucket()
	store := New(bucket, "test-bucket")
	ctx := t.Context()

	loc := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "project-1", Filename: "report.pdf",
	}
	for i := range 3 {
		if _, err := store.Save(ctx, loc, []byte{byte(i)}, "application/pdf"); err != nil {
			t.Fatalf("save v%d: %v", i, err)
		}
	}

	scope := artifact.KeyScope{AppName: "app", UserID: "alice", SessionID: "project-1"}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if !slices.Equal(keys, []string{"report.pdf"}) {
		t.Fatalf("ListKeys = %v, want [report.pdf]", keys)
	}
}

func TestGCSListKeys_IgnoresUserScopeZombie(t *testing.T) {
	bucket := newZombieMockBucket()
	store := New(bucket, "test-bucket")
	ctx := t.Context()

	dead := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "ignored", Filename: "user:secret.txt",
	}
	if _, err := store.Save(ctx, dead, []byte("hi"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.Delete(ctx, dead); err != nil {
		t.Fatalf("delete: %v", err)
	}
	bucket.markZombie(artifact.BuildArtifactPrefix(dead))

	scope := artifact.KeyScope{AppName: "app", UserID: "alice", SessionID: "any-session"}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	for _, k := range keys {
		if k == "user:secret.txt" {
			t.Fatalf("zombie user-scope prefix surfaced: %q", keys)
		}
	}
}
