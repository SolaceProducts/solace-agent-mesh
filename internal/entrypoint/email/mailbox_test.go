// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestThreadKeyMatchesNormalize(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join("testdata", "eml", "synthetic", "*.eml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("fixtures: %v, %d found", err, len(paths))
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		n, err := NormalizeWithOpts(raw, NormalizeOpts{})
		if err != nil {
			continue
		}
		if got := threadKey(raw); got != n.ThreadID {
			t.Errorf("%s: threadKey = %q, Normalize ThreadID = %q", filepath.Base(p), got, n.ThreadID)
		}
	}
	reply := testMail("2", "<t2@vendor.com>", "<t1@vendor.com>", "second")
	if got := threadKey(reply.RFC822); got != "<t1@vendor.com>" {
		t.Errorf("reply threadKey = %q, want the thread root", got)
	}
}

func TestEmailTaskIDIsDerivedFromTheMail(t *testing.T) {
	t.Parallel()
	id := emailTaskID("gw", "<m1@x>", "7")
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("task id %q is not a UUID: %v", id, err)
	}
	if again := emailTaskID("gw", "<m1@x>", "7"); again != id {
		t.Errorf("task id changed between fetches: %s, %s", id, again)
	}
	for _, other := range []string{emailTaskID("gw", "<m2@x>", "7"), emailTaskID("gw", "<m1@x>", "8"), emailTaskID("gw2", "<m1@x>", "7")} {
		if other == id {
			t.Errorf("distinct mail shares task id %s", id)
		}
	}
}

func TestMessageQueue_KeepsOrderWithinAKey(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var order []ProviderUID
	release := make(chan struct{})
	q := NewMessageQueue(QueueConfig{
		Capacity: 8,
		Workers:  4,
		Key: func(m RawMessage) string {
			if m.UID == "other" {
				return "b"
			}
			return "a"
		},
		Handler: func(_ context.Context, m RawMessage) {
			if m.UID == "a1" {
				<-release
			}
			mu.Lock()
			order = append(order, m.UID)
			mu.Unlock()
		},
		Logger: slog.Default(),
	})
	if q.worker(RawMessage{UID: "a1"}) == q.worker(RawMessage{UID: "other"}) {
		t.Skip("both keys hash to one worker")
	}
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()
	for _, uid := range []ProviderUID{"a1", "a2", "a3", "other"} {
		q.Enqueue(t.Context(), RawMessage{UID: uid})
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(order) == 1 && order[0] == "other"
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the other key was held behind a slow message: %v", order)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := append([]ProviderUID(nil), order...)
		mu.Unlock()
		if len(got) == 4 {
			if got[1] != "a1" || got[2] != "a2" || got[3] != "a3" {
				t.Fatalf("order = %v, want a1 a2 a3 after other", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("order = %v, want four", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
