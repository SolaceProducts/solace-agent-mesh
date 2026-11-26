// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailBuffer_KeepsOnlyTheTail(t *testing.T) {
	tb := newTailBuffer(8)
	for _, chunk := range []string{"abc", "def", "ghi"} {
		if _, err := tb.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := tb.String(); got != "bcdefghi" {
		t.Fatalf("tail = %q, want %q", got, "bcdefghi")
	}
	if tb.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", tb.Dropped())
	}
	if _, err := tb.Write([]byte(strings.Repeat("x", 20))); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "xxxxxxxx" {
		t.Fatalf("tail after oversized write = %q", got)
	}
	if tb.Dropped() != 1+8+12 {
		t.Fatalf("dropped = %d, want %d", tb.Dropped(), 1+8+12)
	}
}

func TestEstimatedContentSize_MatchesDecodedLength(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 100, 4097} {
		raw := strings.Repeat("z", n)
		enc := base64.StdEncoding.EncodeToString([]byte(raw))
		if got := estimatedContentSize(enc, true); got != n {
			t.Errorf("binary n=%d: estimate %d", n, got)
		}
		if got := estimatedContentSize(raw, false); got != n {
			t.Errorf("text n=%d: estimate %d", n, got)
		}
	}
}

func TestScheduleWorkDirCleanup_RemovesInlineWithoutRemover(t *testing.T) {
	r := &SandboxRunner{cleanupCh: make(chan string, 1), cleanupDone: make(chan struct{})}
	first := filepath.Join(t.TempDir(), "w1")
	second := filepath.Join(t.TempDir(), "w2")
	for _, d := range []string{first, second} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.scheduleWorkDirCleanup(first)
	r.scheduleWorkDirCleanup(second)
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatal("second dir should have been removed inline once the queue was full")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatal("first dir should still be queued for the remover")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.runWorkDirCleanup(ctx)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("remover must drain the queue on shutdown")
	}
}

func TestScheduleWorkDirCleanup_RemovesInlineAfterRemoverExits(t *testing.T) {
	r := &SandboxRunner{
		cleanupCh:   make(chan string, workDirCleanupQueue),
		cleanupDone: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.runWorkDirCleanup(ctx)

	dir := filepath.Join(t.TempDir(), "late")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.scheduleWorkDirCleanup(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("work dir scheduled after the remover exited must be removed inline")
	}
}
