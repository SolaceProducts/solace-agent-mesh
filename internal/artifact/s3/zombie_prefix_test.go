// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package s3

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type zombieMockS3 struct {
	*mockS3
	mu             sync.Mutex
	zombiePrefixes map[string]bool
}

func newZombieMockS3() *zombieMockS3 {
	return &zombieMockS3{
		mockS3:         newMockS3(),
		zombiePrefixes: make(map[string]bool),
	}
}

func (z *zombieMockS3) markZombie(prefix string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.zombiePrefixes[prefix] = true
}

func (z *zombieMockS3) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	out, err := z.mockS3.ListObjectsV2(ctx, params, optFns...)
	if err != nil || params.Delimiter == nil || *params.Delimiter == "" {
		return out, err
	}

	listingPrefix := ""
	if params.Prefix != nil {
		listingPrefix = *params.Prefix
	}

	z.mu.Lock()
	defer z.mu.Unlock()

	existing := make(map[string]bool, len(out.CommonPrefixes))
	for _, cp := range out.CommonPrefixes {
		if cp.Prefix != nil {
			existing[*cp.Prefix] = true
		}
	}

	for zp := range z.zombiePrefixes {
		if !strings.HasPrefix(zp, listingPrefix) {
			continue
		}
		rest := strings.TrimPrefix(zp, listingPrefix)
		if rest == "" || !strings.HasSuffix(rest, "/") {
			continue
		}
		inner := strings.TrimSuffix(rest, "/")
		if strings.Contains(inner, "/") {
			continue
		}
		if !existing[zp] {
			out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(zp)})
		}
	}

	slices.SortFunc(out.CommonPrefixes, func(a, b types.CommonPrefix) int {
		return cmp.Compare(aws.ToString(a.Prefix), aws.ToString(b.Prefix))
	})
	return out, nil
}

func TestListKeys_IgnoresZombieCommonPrefix(t *testing.T) {
	mock := newZombieMockS3()
	store := New(mock, "test-bucket")
	ctx := t.Context()

	loc := artifact.Locator{
		AppName:   "app",
		UserID:    "alice",
		SessionID: "project-1",
		Filename:  "deleted.docx",
	}

	if _, err := store.Save(ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.Delete(ctx, loc); err != nil {
		t.Fatalf("delete: %v", err)
	}

	mock.markZombie(artifact.BuildArtifactPrefix(loc))

	scope := artifact.KeyScope{
		AppName:   loc.AppName,
		UserID:    loc.UserID,
		SessionID: loc.SessionID,
	}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if slices.Contains(keys, "deleted.docx") {
		t.Fatalf("zombie prefix surfaced as artifact in ListKeys: %q", keys)
	}
}

func TestListKeys_StillReturnsLiveArtifacts(t *testing.T) {
	mock := newZombieMockS3()
	store := New(mock, "test-bucket")
	ctx := t.Context()

	live := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "project-1", Filename: "live.txt",
	}
	if _, err := store.Save(ctx, live, []byte("data"), "text/plain"); err != nil {
		t.Fatalf("save live: %v", err)
	}

	dead := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "project-1", Filename: "dead.docx",
	}
	if _, err := store.Save(ctx, dead, []byte("data"), "text/plain"); err != nil {
		t.Fatalf("save dead: %v", err)
	}
	if err := store.Delete(ctx, dead); err != nil {
		t.Fatalf("delete dead: %v", err)
	}
	mock.markZombie(artifact.BuildArtifactPrefix(dead))

	scope := artifact.KeyScope{AppName: "app", UserID: "alice", SessionID: "project-1"}
	keys, err := store.ListKeys(ctx, scope)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	want := []string{"live.txt"}
	if !slices.Equal(keys, want) {
		t.Fatalf("ListKeys = %v, want %v", keys, want)
	}
}

func TestListKeys_DedupsMultipleVersions(t *testing.T) {
	mock := newZombieMockS3()
	store := New(mock, "test-bucket")
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

func TestListKeys_IgnoresUserScopeZombie(t *testing.T) {
	mock := newZombieMockS3()
	store := New(mock, "test-bucket")
	ctx := t.Context()

	dead := artifact.Locator{
		AppName: "app", UserID: "alice", SessionID: "ignored", Filename: "user:secret.txt",
	}
	if _, err := store.Save(ctx, dead, []byte("hi"), "text/plain"); err != nil {
		t.Fatalf("save user-scope: %v", err)
	}
	if err := store.Delete(ctx, dead); err != nil {
		t.Fatalf("delete user-scope: %v", err)
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
