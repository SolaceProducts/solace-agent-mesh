// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func newTestSQLStoreSharedURL(t *testing.T) (*SQLStore, string) {
	t.Helper()
	url := testTemplate.Clone(t)
	db, err := dbutil.Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLStore(db), url
}

func openStoreOnURL(t *testing.T, url string) *SQLStore {
	t.Helper()
	db, err := dbutil.Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLStore(db)
}

func turnMessages(turn int) []Message {
	return []Message{
		{Role: "user", Content: fmt.Sprintf("question for turn %d", turn), Timestamp: time.Now()},
		{Role: "assistant", Content: fmt.Sprintf("answer for turn %d", turn), Timestamp: time.Now()},
	}
}

func runConversation(t *testing.T, store *SQLStore, sessID string, turns int) int64 {
	t.Helper()
	ctx := context.Background()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for turn := 1; turn <= turns; turn++ {
		if _, err := store.Get(ctx, "app", "u", sessID); err != nil {
			t.Fatalf("get turn %d: %v", turn, err)
		}
		if err := store.AppendMessages(ctx, "app", "u", sessID, turnMessages(turn)); err != nil {
			t.Fatalf("append turn %d: %v", turn, err)
		}
	}
	return store.msgRowsRead.Load()
}

func TestMessageCacheEliminatesPerTurnReread(t *testing.T) {
	t.Parallel()
	const turns = 10

	cached, _ := newTestSQLStoreSharedURL(t)
	cachedRows := runConversation(t, cached, idgen.New(), turns)

	baseline, _ := newTestSQLStoreSharedURL(t)
	baseline.msgCache = nil
	baselineRows := runConversation(t, baseline, idgen.New(), turns)

	var wantBaseline int64
	for k := 1; k <= turns; k++ {
		wantBaseline += int64(2 * (k - 1))
	}
	if baselineRows != wantBaseline {
		t.Fatalf("baseline rows read = %d, want %d", baselineRows, wantBaseline)
	}
	if cachedRows != 0 {
		t.Fatalf("cached rows read = %d, want 0 (transcript should never be re-read)", cachedRows)
	}

	hits, misses := cached.msgCache.stats()
	if misses != 0 {
		t.Fatalf("cache misses = %d, want 0", misses)
	}
	if hits != turns {
		t.Fatalf("cache hits = %d, want %d (one hit per turn Get)", hits, turns)
	}
	t.Logf("rows read over %d turns: baseline=%d, cached=%d (%.0fx reduction)",
		turns, baselineRows, cachedRows, float64(baselineRows)/1)
}

func TestMessageCacheBehaviorNeutral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, url := newTestSQLStoreSharedURL(t)
	sessID := idgen.New()
	runConversation(t, store, sessID, 8)

	cachedGet, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}

	cold := openStoreOnURL(t, url)
	coldGet, err := cold.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}

	if !messagesEqual(cachedGet.Messages, coldGet.Messages) {
		t.Fatalf("cached history (%d msgs) differs from fresh DB read (%d msgs)",
			len(cachedGet.Messages), len(coldGet.Messages))
	}
	if len(cachedGet.Messages) != 16 {
		t.Fatalf("history len = %d, want 16", len(cachedGet.Messages))
	}
}

func TestMessageCacheStaleWriteFromOtherInstance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, url := newTestSQLStoreSharedURL(t)
	b := openStoreOnURL(t, url)

	sessID := idgen.New()
	if err := a.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}
	if err := a.AppendMessages(ctx, "app", "u", sessID, turnMessages(1)); err != nil {
		t.Fatal(err)
	}

	first, err := a.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Messages) != 2 {
		t.Fatalf("first get = %d msgs, want 2", len(first.Messages))
	}

	if err := b.AppendMessages(ctx, "app", "u", sessID, turnMessages(2)); err != nil {
		t.Fatal(err)
	}

	second, secondErr := a.Get(ctx, "app", "u", sessID)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	if len(second.Messages) != 4 {
		t.Fatalf("second get = %d msgs, want 4 (stale cache not revalidated)", len(second.Messages))
	}
}

func TestMessageCacheConcurrentAppendsSameSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, url := newTestSQLStoreSharedURL(t)

	sessID := idgen.New()
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u", SessionID: sessID}); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const perWriter = 5
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := range writers {
		wg.Go(func() {
			for i := range perWriter {
				msgs := []Message{{Role: "user", Content: fmt.Sprintf("w%d-m%d", w, i), Timestamp: time.Now()}}
				if err := store.AppendMessages(ctx, "app", "u", sessID, msgs); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}

	got, err := store.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != writers*perWriter {
		t.Fatalf("history len = %d, want %d", len(got.Messages), writers*perWriter)
	}

	cold := openStoreOnURL(t, url)
	authoritative, err := cold.Get(ctx, "app", "u", sessID)
	if err != nil {
		t.Fatal(err)
	}
	if !messagesEqual(got.Messages, authoritative.Messages) {
		t.Fatalf("cached order diverged from database seq order")
	}
}

func messagesEqual(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content || a[i].ToolCallID != b[i].ToolCallID {
			return false
		}
	}
	return true
}

func TestMessageCacheByteBoundCountsReasoning(t *testing.T) {
	const maxBytes = 1024
	cache := newMsgCache(10, maxBytes)
	key := msgKey{appName: "app", userID: "user", sessionID: "session"}
	turn := []Message{{Role: "assistant", Reasoning: strings.Repeat("r", 2*maxBytes)}}

	cache.put(key, turn, 1)

	if _, cached := cache.get(key, 1); cached {
		t.Errorf("get after putting a %d-byte reasoning turn into a %d-byte cache: cached, want evicted", len(turn[0].Reasoning), maxBytes)
	}
}
