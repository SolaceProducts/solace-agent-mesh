// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type overlappingSource struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *overlappingSource) Snapshot(context.Context) (*DBState, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		select {
		case <-s.release:
		case <-time.After(100 * time.Millisecond):
		}
		return &DBState{}, nil
	}
	return &DBState{Roles: []DBRole{{ID: "role-1", Name: "responder", Scopes: []string{"agent:*:read"}}}}, nil
}

func TestComposer_OverlappingRebuildsNeverStoreTheOlderSnapshotLast(t *testing.T) {
	t.Parallel()
	source := &overlappingSource{entered: make(chan struct{}), release: make(chan struct{})}
	c := NewComposer(YAMLState{}, source, newTestLogger())

	var wg sync.WaitGroup
	wg.Go(func() { _ = c.Rebuild(t.Context()) })
	<-source.entered
	second := make(chan struct{})
	wg.Go(func() {
		_ = c.Rebuild(t.Context())
		close(second)
	})
	select {
	case <-second:
	case <-time.After(200 * time.Millisecond):
	}
	close(source.release)
	wg.Wait()

	if _, err := c.ScopesForRoleIDs([]string{"role-1"}); err != nil {
		t.Fatalf("the older snapshot was stored last: %v", err)
	}
}

type unansweredSource struct{}

func (unansweredSource) Snapshot(ctx context.Context) (*DBState, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestComposer_RebuildGivesUpOnAnUnansweredSnapshot(t *testing.T) {
	t.Parallel()
	c := NewComposer(YAMLState{}, unansweredSource{}, newTestLogger())
	c.snapshotTimeout = 20 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- c.Rebuild(t.Context()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Rebuild returned nil for a snapshot that never answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Rebuild still blocked on the snapshot; it holds rebuildMu with it")
	}
}
