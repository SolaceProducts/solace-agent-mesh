// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package azure

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type zombieMockContainer struct {
	*mockContainer
	zmu            sync.Mutex
	zombiePrefixes map[string]bool
}

func newZombieMockContainer() *zombieMockContainer {
	return &zombieMockContainer{
		mockContainer:  newMockContainer(),
		zombiePrefixes: make(map[string]bool),
	}
}

func (z *zombieMockContainer) markZombie(prefix string) {
	z.zmu.Lock()
	defer z.zmu.Unlock()
	z.zombiePrefixes[prefix] = true
}

func (z *zombieMockContainer) ListBlobs(ctx context.Context, prefix string) ([]string, error) {
	out, err := z.mockContainer.ListBlobs(ctx, prefix)
	if err != nil {
		return nil, err
	}
	z.zmu.Lock()
	defer z.zmu.Unlock()
	for zp := range z.zombiePrefixes {
		if !strings.HasPrefix(zp, prefix) {
			continue
		}
		out = append(out, zp)
	}
	sort.Strings(out)
	return out, nil
}

func newZombieStore(t *testing.T) (*Store, *zombieMockContainer) {
	t.Helper()
	mock := newZombieMockContainer()
	return New(mock, "test-container"), mock
}

func TestAzureListKeys_IgnoresZombiePrefix(t *testing.T) {
	store, mock := newZombieStore(t)
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
	mock.markZombie(artifact.BuildArtifactPrefix(loc))

	scope := artifact.KeyScope{AppName: "app", UserID: "alice", SessionID: "project-1"}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if slices.Contains(keys, "deleted.docx") {
		t.Fatalf("zombie prefix surfaced as artifact: %q", keys)
	}
}

func TestAzureListKeys_DedupsMultipleVersions(t *testing.T) {
	store, _ := newZombieStore(t)
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

func TestAzureListKeys_IgnoresUserScopeZombie(t *testing.T) {
	store, mock := newZombieStore(t)
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
	mock.markZombie(artifact.BuildArtifactPrefix(dead))

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
