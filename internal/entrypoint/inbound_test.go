// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type replayingAdapter struct {
	noopAdapter
	core      *GatewayCore
	tellFails bool
	mu        sync.Mutex
	replayed  []InboundMessage
	abandoned []InboundMessage
}

func (a *replayingAdapter) ReplayInbound(_ context.Context, msg InboundMessage) {
	a.mu.Lock()
	a.replayed = append(a.replayed, msg)
	a.mu.Unlock()
	a.core.InboundDone(msg.ID, msg.Token)
}

func (a *replayingAdapter) AbandonInbound(_ context.Context, msg InboundMessage) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.abandoned = append(a.abandoned, msg)
	return !a.tellFails
}

var _ InboundReplayer = (*replayingAdapter)(nil)

func newInboundCore(t *testing.T, db *store.DB, adapter GatewayAdapter) *GatewayCore {
	t.Helper()
	appConfig := map[string]any{"namespace": "test-ns", "gateway_id": "slack-gw"}
	g := NewGatewayCore(adapter, appConfig)
	cfg, err := ParseGatewayConfig(appConfig)
	if err != nil {
		t.Fatal(err)
	}
	g.cfg = cfg
	g.logger = slog.Default()
	g.useDatabase(db)
	return g
}

func inboundRowState(t *testing.T, db *store.DB, messageID string) string {
	t.Helper()
	var state string
	if err := db.QueryRowContext(t.Context(),
		`SELECT state FROM entrypoint_inbound_messages WHERE gateway_id = 'slack-gw' AND message_id = ?`,
		messageID).Scan(&state); err != nil {
		t.Fatalf("read inbound row: %v", err)
	}
	return state
}

func TestAcceptInbound_OneInstanceProcesses(t *testing.T) {
	db := store.TestDB(t)
	a := newInboundCore(t, db, noopAdapter{})
	b := newInboundCore(t, db, noopAdapter{})
	ctx := t.Context()

	first, err := a.AcceptInbound(ctx, "C1:1.1", `{"event":{}}`)
	if err != nil || first.Outcome != InboundProcess || first.Token == 0 {
		t.Fatalf("first accept = %+v, %v; want process", first, err)
	}
	if copyB, err := b.AcceptInbound(ctx, "C1:1.1", `{"event":{}}`); err != nil || copyB.Outcome != InboundSkip {
		t.Fatalf("copy on the other instance = %+v, %v; want skip", copyB, err)
	}
	a.InboundDone("C1:1.1", first.Token)
	if state := inboundRowState(t, db, "C1:1.1"); state != store.InboundDone {
		t.Fatalf("state after done = %q", state)
	}
	if later, err := b.AcceptInbound(ctx, "C1:1.1", `{"event":{}}`); err != nil || later.Outcome != InboundSkip {
		t.Fatalf("copy after done = %+v, %v; want skip", later, err)
	}
}

func TestAcceptInbound_NoRecord(t *testing.T) {
	g := NewGatewayCore(noopAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "slack-gw"})
	claim, err := g.AcceptInbound(t.Context(), "C1:1.1", "p")
	if err != nil || claim.Outcome != InboundProcess || claim.Token != 0 {
		t.Fatalf("accept without a database = %+v, %v; want process", claim, err)
	}
	g.InboundDone("C1:1.1", claim.Token)

	withDB := newInboundCore(t, store.TestDB(t), noopAdapter{})
	if claim, err := withDB.AcceptInbound(t.Context(), "", "p"); err != nil || claim.Outcome != InboundProcess {
		t.Fatalf("accept with no id = %+v, %v; want process", claim, err)
	}
}

func TestAcceptInbound_DatabaseDown(t *testing.T) {
	db := store.TestDB(t)
	g := newInboundCore(t, db, noopAdapter{})
	_ = db.Close()
	if _, err := g.AcceptInbound(t.Context(), "C1:1.1", "p"); err == nil {
		t.Fatal("accept on a closed database succeeded")
	}
	g.InboundDone("C1:1.1", 42)
	if err := g.pruneInbound(t.Context()); err == nil {
		t.Error("prune on a closed database succeeded")
	}
	g.recoverInbound(t.Context())
}

func TestRecoverInbound(t *testing.T) {
	db := store.TestDB(t)
	adapter := &replayingAdapter{}
	g := newInboundCore(t, db, adapter)
	adapter.core = g
	ctx := t.Context()

	lapsed := time.Now().Add(-inboundLease - time.Minute).UnixMilli()
	stale := time.Now().Add(-inboundReplayWindow - time.Minute).UnixMilli()
	old := time.Now().Add(-inboundRetention - time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at) VALUES
		 ('slack-gw', 'lost', 'processing', 'lost-payload', ?, 0, ?),
		 ('slack-gw', 'spent', 'processing', 'spent-payload', ?, ?, ?),
		 ('slack-gw', 'stale', 'processing', 'stale-payload', ?, 0, ?),
		 ('slack-gw', 'expired', 'done', '', ?, 0, ?),
		 ('other-gw', 'theirs', 'processing', 'p', ?, 0, ?)`,
		lapsed, lapsed, lapsed, inboundMaxAttempts, lapsed, lapsed, stale, old, old, lapsed, lapsed); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AcceptInbound(ctx, "held", "held-payload"); err != nil {
		t.Fatal(err)
	}

	g.recoverInbound(ctx)
	if err := g.pruneInbound(ctx); err != nil {
		t.Fatal(err)
	}
	g.loopWg.Wait()

	if len(adapter.replayed) != 1 || adapter.replayed[0].ID != "lost" || adapter.replayed[0].Payload != "lost-payload" || adapter.replayed[0].Attempts != 1 {
		t.Fatalf("replayed = %+v; want the one lapsed message", adapter.replayed)
	}
	abandoned := map[string]bool{}
	for _, m := range adapter.abandoned {
		abandoned[m.ID] = true
	}
	if len(adapter.abandoned) != 2 || !abandoned["spent"] || !abandoned["stale"] {
		t.Fatalf("abandoned = %+v; want the message whose attempts are spent and the one older than the replay window", adapter.abandoned)
	}
	for _, id := range []string{"lost", "spent", "stale"} {
		if state := inboundRowState(t, db, id); state != store.InboundDone {
			t.Errorf("state of %s = %q, want done", id, state)
		}
	}
	if state := inboundRowState(t, db, "held"); state != store.InboundProcessing {
		t.Errorf("held message state = %q, want untouched", state)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entrypoint_inbound_messages WHERE message_id = 'expired'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("expired record count = %d, %v; want pruned", n, err)
	}
}

func TestTaskRecordSweeper_RunsInboundPasses(t *testing.T) {
	db := store.TestDB(t)
	adapter := &replayingAdapter{}
	g := newInboundCore(t, db, adapter)
	adapter.core = g
	lapsed := time.Now().Add(-inboundLease - time.Minute).UnixMilli()
	old := time.Now().Add(-inboundRetention - time.Hour).UnixMilli()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at) VALUES
		 ('slack-gw', 'lost', 'processing', 'lost-payload', ?, 0, ?),
		 ('slack-gw', 'expired', 'done', '', ?, 0, ?)`,
		lapsed, lapsed, old, old); err != nil {
		t.Fatal(err)
	}

	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(t.Context())
	g.loopWg.Go(func() { g.sweepTaskRecords(ctx, g.tasks.(taskRecordSweeper), tick) })
	defer func() { cancel(); g.loopWg.Wait() }()
	counts := func() (replayed, expired int) {
		if err := db.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM entrypoint_inbound_messages WHERE message_id = 'expired'`).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.replayed), expired
	}

	g.RunTaskSweepNow()
	time.Sleep(200 * time.Millisecond)
	if replayed, expired := counts(); replayed != 0 || expired != 1 {
		t.Fatalf("after a requested pass: replayed %d, want 0; expired records %d, want 1", replayed, expired)
	}

	tick <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		replayed, expired := counts()
		if replayed == 1 && expired == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a tick: replayed %d, want 1; expired records %d, want 1 until the leader prunes", replayed, expired)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := g.cleanUpTaskRecords(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, expired := counts(); expired != 0 {
		t.Errorf("after the leader's cleanup: expired records %d, want 0", expired)
	}
}

func TestRecoverInbound_AdapterWithoutReplay(t *testing.T) {
	db := store.TestDB(t)
	g := newInboundCore(t, db, noopAdapter{})
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES ('slack-gw', 'lost', 'processing', 'p', 1, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	g.recoverInbound(t.Context())
	if state := inboundRowState(t, db, "lost"); state != store.InboundProcessing {
		t.Fatalf("state = %q, want untouched", state)
	}
}

func TestRecoverInbound_AbandonNotToldKeepsTheRecord(t *testing.T) {
	db := store.TestDB(t)
	adapter := &replayingAdapter{tellFails: true}
	g := newInboundCore(t, db, adapter)
	adapter.core = g
	lapsed := time.Now().Add(-inboundLease - time.Minute).UnixMilli()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES ('slack-gw', 'spent', 'processing', 'p', ?, ?, ?)`, lapsed, inboundMaxAttempts, lapsed); err != nil {
		t.Fatal(err)
	}
	g.recoverInbound(t.Context())
	g.loopWg.Wait()
	if state := inboundRowState(t, db, "spent"); state != store.InboundProcessing {
		t.Fatalf("state = %q, want the record kept for the next pass", state)
	}
}

func TestRecoverInbound_GivesUpTelling(t *testing.T) {
	db := store.TestDB(t)
	adapter := &replayingAdapter{tellFails: true}
	g := newInboundCore(t, db, adapter)
	adapter.core = g
	lapsed := time.Now().Add(-inboundLease - time.Minute).UnixMilli()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES ('slack-gw', 'untellable', 'processing', 'p', ?, ?, ?)`, lapsed, inboundGiveUpAttempts, lapsed); err != nil {
		t.Fatal(err)
	}
	g.recoverInbound(t.Context())
	g.loopWg.Wait()
	if state := inboundRowState(t, db, "untellable"); state != store.InboundDone {
		t.Fatalf("state = %q, want done once the attempts are spent", state)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.abandoned) != 0 {
		t.Fatalf("notices tried = %d, want 0 past the limit", len(adapter.abandoned))
	}
}

func TestInboundRenew(t *testing.T) {
	db := store.TestDB(t)
	g := newInboundCore(t, db, noopAdapter{})
	claim, err := g.AcceptInbound(t.Context(), "C1:9.9", "p")
	if err != nil {
		t.Fatal(err)
	}
	token, held := g.InboundRenew("C1:9.9", claim.Token)
	if !held || token <= claim.Token {
		t.Fatalf("renew right after the claim = %d, %v; want held with a later token", token, held)
	}
	for time.Now().UnixMilli() <= token {
		time.Sleep(time.Millisecond)
	}
	if _, err := store.NewEntrypointInboundRepo(db).TakeLapsed(t.Context(), "slack-gw", 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, held := g.InboundRenew("C1:9.9", token); held {
		t.Fatal("held after another instance took the message over")
	}
	if token, held := g.InboundRenew("C1:9.9", 0); !held || token != 0 {
		t.Fatal("a message never recorded must read as held")
	}
}
