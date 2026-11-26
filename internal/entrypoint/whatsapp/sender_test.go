// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type senderRig struct {
	pool  *senderPool
	repo  *store.WhatsAppOutboundRepo
	clock *testClock

	mu     sync.Mutex
	sent   []sentRecord
	sleeps []time.Duration
	script map[string][]error
	held   map[string]chan struct{}

	stopOnce sync.Once
	drained  bool
}

type sentRecord struct {
	rowID     string
	recipient string
	payload   string
	at        time.Time
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1720000000, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

const testJitter = 250 * time.Millisecond

func newSenderRig(t *testing.T, pace time.Duration) *senderRig {
	t.Helper()
	return newSenderRigWithLogger(t, pace, discardLogger())
}

func newSenderRigWithLogger(t *testing.T, pace time.Duration, logger *slog.Logger) *senderRig {
	t.Helper()
	return newSenderRigWith(t, pace, func(cfg *senderConfig) { cfg.Logger = logger })
}

func newSenderRigWith(t *testing.T, pace time.Duration, adjust func(*senderConfig)) *senderRig {
	t.Helper()
	rig := &senderRig{
		repo:   store.NewWhatsAppOutboundRepo(newSenderTestDB(t)),
		clock:  newTestClock(),
		script: map[string][]error{},
		held:   map[string]chan struct{}{},
	}
	cfg := senderConfig{
		Queue:             rig.repo,
		Jitter:            func(d time.Duration) time.Duration { return d + testJitter },
		Attempt:           rig.attempt,
		GatewayID:         "gw-test",
		Logger:            discardLogger(),
		Pace:              pace,
		IdleTimeout:       50 * time.Millisecond,
		RecoveryLease:     20 * time.Millisecond,
		RecoveryInterval:  20 * time.Millisecond,
		RecoveryHardLease: time.Hour,
		Now:               rig.clock.Now,
		Sleep:             rig.sleep,
	}
	if adjust != nil {
		adjust(&cfg)
	}
	rig.pool = newSenderPool(cfg)
	t.Cleanup(func() {
		if !rig.stop() {
			t.Error("sender pool did not drain; the cleanup below closes the database it is still writing to")
		}
	})
	return rig
}

func (r *senderRig) stop() bool {
	r.stopOnce.Do(func() {
		r.drained = r.pool.Stop(context.Background(), 2*time.Second)
	})
	return r.drained
}

func (r *senderRig) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.sleeps = append(r.sleeps, d)
	r.mu.Unlock()
	r.clock.advance(d)
	return ctx.Err()
}

func (r *senderRig) holdAttempts(t *testing.T, recipient string) (release func()) {
	gate := make(chan struct{})
	r.mu.Lock()
	r.held[recipient] = gate
	r.mu.Unlock()
	release = sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release)
	return release
}

func (r *senderRig) attempt(ctx context.Context, row *store.WhatsAppOutbound) (string, error) {
	r.mu.Lock()
	gate := r.held[row.Recipient]
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	r.mu.Lock()
	if queued := r.script[row.Recipient]; len(queued) > 0 {
		err := queued[0]
		r.script[row.Recipient] = queued[1:]
		r.mu.Unlock()
		return "", err
	}
	r.sent = append(r.sent, sentRecord{rowID: row.ID, recipient: row.Recipient, payload: row.Payload, at: r.clock.Now()})
	r.mu.Unlock()
	return "wamid." + row.ID, nil
}

func (r *senderRig) recordEvery() {
	inner := r.pool.attempt
	r.pool.attempt = func(ctx context.Context, row *store.WhatsAppOutbound) (string, error) {
		wamid, err := inner(ctx, row)
		if err == nil {
			r.mu.Lock()
			r.sent = append(r.sent, sentRecord{rowID: row.ID, recipient: row.Recipient, payload: row.Payload, at: r.clock.Now()})
			r.mu.Unlock()
		}
		return wamid, err
	}
}

func (r *senderRig) scriptFailures(recipient string, errs ...error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.script[recipient] = append(r.script[recipient], errs...)
}

func (r *senderRig) delivered() []sentRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sentRecord, len(r.sent))
	copy(out, r.sent)
	return out
}

func (r *senderRig) waits() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.sleeps))
	copy(out, r.sleeps)
	return out
}

func (r *senderRig) enqueue(t *testing.T, recipient, text string) *store.WhatsAppOutbound {
	t.Helper()
	row := r.newRow(recipient, text)
	if err := r.pool.Enqueue(context.Background(), nil, row); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return row
}

func (r *senderRig) insert(t *testing.T, recipient, text string) *store.WhatsAppOutbound {
	t.Helper()
	row := r.newRow(recipient, text)
	if err := r.repo.Enqueue(context.Background(), nil, row); err != nil {
		t.Fatalf("repo.Enqueue: %v", err)
	}
	return row
}

func (r *senderRig) newRow(recipient, text string) *store.WhatsAppOutbound {
	now := r.clock.Now().UnixMilli()
	return &store.WhatsAppOutbound{
		ID:        idgen.New(),
		GatewayID: "gw-test",
		Recipient: recipient,
		Kind:      kindText,
		Payload:   fmt.Sprintf(`{"text":%q}`, text),
		State:     store.WhatsAppOutboundQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (r *senderRig) waitDelivered(t *testing.T, n int) []sentRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := r.delivered(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d sends; got %d", n, len(r.delivered()))
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *senderRig) waitState(t *testing.T, id string, want ...string) *store.WhatsAppOutbound {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		row, err := r.repo.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if row != nil {
			for _, w := range want {
				if row.State == w {
					return row
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("row %s never reached %v (now %v)", id, want, row)
		}
		time.Sleep(time.Millisecond)
	}
}

func newSenderTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(testDBURL(t), discardLogger())
	if err != nil {
		t.Fatalf("open sender test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSenderPool_PerRecipientFIFOUnderConcurrentEnqueue(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	const chunks = 12
	var wg sync.WaitGroup
	ids := make([]string, chunks)
	for i := range chunks {
		row := rig.enqueue(t, "alice", fmt.Sprintf("chunk-%02d", i))
		ids[i] = row.ID
		wg.Go(func() { rig.pool.notify("alice") })
	}
	wg.Wait()

	got := rig.waitDelivered(t, chunks)
	for i, rec := range got[:chunks] {
		if rec.rowID != ids[i] {
			t.Fatalf("send %d was row %s, want %s — per-recipient order was not preserved", i, rec.rowID, ids[i])
		}
	}
}

func TestSenderPool_RecipientsAreIndependent(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)

	release := rig.holdAttempts(t, "alice")
	defer release()
	rig.pool.Start(t.Context())

	rig.enqueue(t, "alice", "alice-1")
	rig.enqueue(t, "bob", "bob-1")

	got := rig.waitDelivered(t, 1)
	if got[0].recipient != "bob" {
		t.Fatalf("first send was to %q; bob must not wait behind alice's parked attempt", got[0].recipient)
	}
}

func TestSenderPool_PacesSendsToOneRecipient(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, pairSendInterval)
	start := rig.clock.Now()
	rig.pool.Start(t.Context())

	rig.enqueue(t, "alice", "one")
	rig.enqueue(t, "alice", "two")
	rig.enqueue(t, "alice", "three")

	got := rig.waitDelivered(t, 3)
	for i := 1; i < 3; i++ {
		if gap := got[i].at.Sub(got[i-1].at); gap < pairSendInterval {
			t.Errorf("send %d followed %d after %v, want at least %v", i, i-1, gap, pairSendInterval)
		}
	}
	if first := got[0].at; !first.Equal(start) {
		t.Errorf("first send at %v, want the start time %v — nothing may delay the first send", first, start)
	}
}

func TestSenderPool_RetriesRetryableFailuresWithBackoff(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", throttled(), throttled())
	row := rig.enqueue(t, "alice", "eventually")

	got := rig.waitDelivered(t, 1)
	if got[0].rowID != row.ID {
		t.Fatalf("delivered %s, want %s", got[0].rowID, row.ID)
	}
	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if settled.Attempts != 3 {
		t.Errorf("attempts = %d, want 3 (two failures then success)", settled.Attempts)
	}
	for i, floor := range []time.Duration{time.Second, 4 * time.Second} {
		if !containsDuration(rig.waits(), floor+testJitter) {
			t.Errorf("waits = %v, want %v (backoff %v plus jitter) for attempt %d",
				rig.waits(), floor+testJitter, floor, i+1)
		}
	}
}

func TestSenderPool_ExhaustsAttemptsThenFailsTerminally(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	for range maxSendAttempts {
		rig.scriptFailures("alice", throttled())
	}
	row := rig.enqueue(t, "alice", "doomed")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundFailed)
	if settled.Attempts != maxSendAttempts {
		t.Errorf("attempts = %d, want the cap %d", settled.Attempts, maxSendAttempts)
	}
	if len(rig.delivered()) != 0 {
		t.Errorf("nothing should have been delivered, got %v", rig.delivered())
	}
}

func TestSenderPool_FailsFastOnPermanentRejection(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", permanentError())
	row := rig.enqueue(t, "alice", "rejected")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundFailed)
	if settled.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 — a permanent rejection must fail fast", settled.Attempts)
	}
	if settled.ErrorCode != 100 {
		t.Errorf("errorCode = %d, want Meta's 100 recorded on the row", settled.ErrorCode)
	}
}

func TestSenderPool_DrainLeavesRemainingRowsQueued(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	var held sync.Once
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		graph := cfg.Attempt
		cfg.Attempt = func(_ context.Context, row *store.WhatsAppOutbound) (string, error) {
			if strings.Contains(row.Payload, `"one"`) {
				held.Do(func() { close(started) })
				<-release
			}
			return graph(context.Background(), row)
		}
	})

	first := rig.enqueue(t, "alice", "one")
	rest := []string{
		rig.enqueue(t, "alice", "two").ID,
		rig.enqueue(t, "alice", "three").ID,
	}
	rig.pool.Start(t.Context())
	<-started

	stopped := make(chan struct{})
	go func() {
		rig.stop()
		close(stopped)
	}()
	gateClosed := time.Now().Add(2 * time.Second)
	for rig.pool.claimCtx.Err() == nil {
		if time.Now().After(gateClosed) {
			t.Fatal("Stop did not close the claim gate")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	<-stopped

	if row := mustGet(t, rig.repo, first.ID); row.State != store.WhatsAppOutboundSent {
		t.Errorf("the in-flight row is %q, want sent — the drain window exists to let it finish", row.State)
	}
	for _, id := range rest {
		if row := mustGet(t, rig.repo, id); row.State != store.WhatsAppOutboundQueued {
			t.Errorf("row %s is %q after the drain, want queued so the next instance resumes it", id, row.State)
		}
	}
}

func TestSenderPool_StartRecoversAbandonedSends(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	ctx := t.Context()
	nowMs := rig.clock.Now().UnixMilli()

	abandoned := rig.insert(t, "alice", "mid-send when the pod died")
	spent := rig.insert(t, "bob", "poison")
	if _, _, err := rig.repo.ClaimNext(ctx, "gw-test", "alice", nowMs); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	for range maxSendAttempts {
		if _, _, err := rig.repo.ClaimNext(ctx, "gw-test", "bob", nowMs); err != nil {
			t.Fatalf("claim bob: %v", err)
		}
		if _, _, err := rig.repo.RecoverInFlight(ctx, "gw-test", maxSendAttempts, 0, 0, nowMs, nil); err != nil {
			t.Fatalf("pre-recover: %v", err)
		}
	}
	if _, _, err := rig.repo.ClaimNext(ctx, "gw-test", "bob", nowMs); err != nil {
		t.Fatalf("re-claim bob: %v", err)
	}
	if _, _, err := rig.repo.ClaimNext(ctx, "gw-test", "alice", nowMs); err != nil {
		t.Fatalf("re-claim alice: %v", err)
	}

	rig.clock.advance(time.Second)
	rig.pool.Start(ctx)

	if got := rig.waitDelivered(t, 1); got[0].rowID != abandoned.ID {
		t.Errorf("delivered %s, want the recovered row %s", got[0].rowID, abandoned.ID)
	}
	if row := rig.waitState(t, spent.ID, store.WhatsAppOutboundFailed); row.Attempts < maxSendAttempts {
		t.Errorf("the spent row is %q with %d attempts; a crash loop must be bounded", row.State, row.Attempts)
	}
}

func TestSenderPool_RetiresIdleWorkersAndRestartsThem(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.enqueue(t, "alice", "one")
	rig.waitDelivered(t, 1)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rig.pool.mu.Lock()
		workers := len(rig.pool.workers)
		rig.pool.mu.Unlock()
		if workers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d workers still registered after the idle timeout", workers)
		}
		time.Sleep(time.Millisecond)
	}

	rig.enqueue(t, "alice", "two")
	if got := rig.waitDelivered(t, 2); got[1].payload != `{"text":"two"}` {
		t.Errorf("second message = %s; a retired worker must be replaced on the next enqueue", got[1].payload)
	}
}

func TestSenderPool_RecordsDeliveryWithoutWamidWhenTheIDIsUnusable(t *testing.T) {
	t.Parallel()
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Attempt = func(_ context.Context, row *store.WhatsAppOutbound) (string, error) {
			return "wamid.SAME", nil
		}
	})
	rig.recordEvery()
	rig.pool.Start(t.Context())

	first := rig.enqueue(t, "alice", "one")
	second := rig.enqueue(t, "alice", "two")
	rig.waitDelivered(t, 2)

	if row := rig.waitState(t, first.ID, store.WhatsAppOutboundSent); row.Wamid != "wamid.SAME" {
		t.Errorf("first row wamid = %q, want wamid.SAME", row.Wamid)
	}
	row := rig.waitState(t, second.ID, store.WhatsAppOutboundSent)
	if row.Wamid != "" {
		t.Errorf("second row wamid = %q, want it dropped rather than the row left mid-send", row.Wamid)
	}
}

func mustGet(t *testing.T, r *store.WhatsAppOutboundRepo, id string) *store.WhatsAppOutbound {
	t.Helper()
	row, err := r.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	if row == nil {
		t.Fatalf("row %s is gone", id)
	}
	return row
}

func containsDuration(haystack []time.Duration, needle time.Duration) bool {
	for _, d := range haystack {
		if d == needle {
			return true
		}
	}
	return false
}

func serverError() error {
	return &graphAPIError{Op: "send", HTTPStatus: 503, Attempts: 1, GraphErr: graphError{Message: "unavailable"}}
}

func throttled() error {
	return &graphAPIError{
		Op: "send", HTTPStatus: 429, Attempts: 1,
		GraphErr: graphError{Code: codeThroughputReached, Message: "message throughput reached"},
	}
}

func permanentError() error {
	return &graphAPIError{
		Op: "send", HTTPStatus: 400, Attempts: 1,
		GraphErr: graphError{Code: 100, Message: "invalid parameter",
			ErrorData: graphErrorData{Details: "to: 15551234567 is not reachable"}},
	}
}

func TestSenderPool_UnsendableRowFailsWithoutRetrying(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", fmt.Errorf("%w: no such artifact", errOutboundUnsendable))
	row := rig.enqueue(t, "alice", "unsendable")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundFailed)
	if settled.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 — an unsendable row must not be retried", settled.Attempts)
	}
}

func TestSenderPool_LongAttemptIsNotRecoveredFromItself(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		graph := cfg.Attempt
		cfg.Attempt = func(ctx context.Context, row *store.WhatsAppOutbound) (string, error) {
			once.Do(func() { close(started) })
			<-release
			return graph(ctx, row)
		}
		cfg.RecoveryLease = 30 * time.Millisecond
		cfg.RecoveryInterval = 5 * time.Millisecond
		cfg.RecoveryHardLease = time.Hour
	})

	row := rig.enqueue(t, "alice", "a big document")
	rig.pool.Start(t.Context())
	<-started

	time.Sleep(150 * time.Millisecond)
	held := mustGet(t, rig.repo, row.ID)
	if held.State != store.WhatsAppOutboundSending {
		t.Fatalf("row is %q while its attempt is still running; the pool recovered a send it holds itself", held.State)
	}
	if held.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 — a renewed claim must not be re-claimed", held.Attempts)
	}

	close(release)
	rig.waitDelivered(t, 1)
	if settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent); settled.Attempts != 1 {
		t.Errorf("attempts = %d after delivery, want 1", settled.Attempts)
	}
	if got := rig.delivered(); len(got) != 1 {
		t.Errorf("delivered %d times, want 1 — the message must not be re-sent", len(got))
	}
}

func TestSenderPool_AcceptedButLostResponseRecordsAsSent(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", fmt.Errorf("%w: %w", errRequestAccepted, errResponseLost))
	row := rig.enqueue(t, "alice", "the user has this")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if settled.Wamid != "" {
		t.Errorf("wamid = %q, want empty — there was no id to read", settled.Wamid)
	}
	if settled.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 — an accepted message must not be re-sent", settled.Attempts)
	}
	if got := rig.delivered(); len(got) != 0 {
		t.Errorf("the attempt was scripted to fail, so nothing should have reached the mock: %v", got)
	}
}

func TestSenderPool_SettleSurvivesShutdownCancellation(t *testing.T) {
	t.Parallel()
	var pool atomic.Pointer[senderPool]
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Attempt = func(_ context.Context, row *store.WhatsAppOutbound) (string, error) {
			if p := pool.Load(); p != nil {
				p.sendCancel()
			}
			return "wamid.LATE", nil
		}
	})
	pool.Store(rig.pool)
	rig.recordEvery()
	rig.pool.Start(t.Context())

	row := rig.enqueue(t, "alice", "settled after the grace timer")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if settled.Wamid != "wamid.LATE" {
		t.Errorf("wamid = %q, want it recorded despite the cancellation", settled.Wamid)
	}
}

func TestSenderPool_FailedAttemptIsNotPaced(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, pairSendInterval)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", throttled())
	rig.enqueue(t, "alice", "retried once")
	rig.waitDelivered(t, 1)

	waits := rig.waits()
	if len(waits) == 0 {
		t.Fatal("no waits recorded")
	}
	if want := time.Second + testJitter; waits[0] != want {
		t.Errorf("first wait = %v, want the retry backoff %v; waits = %v", waits[0], want, waits)
	}
}

type unwritableLedger struct{ senderQueue }

func (unwritableLedger) MarkSent(context.Context, dbutil.Querier, string, string, int64) error {
	return errors.New("ledger unavailable")
}

func TestSenderPool_DeliveredButUnrecordedIsCountedApart(t *testing.T) {
	t.Parallel()
	metrics := newCountingMetrics()
	reported := make(chan error, 4)
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Metrics = metrics
		cfg.Queue = unwritableLedger{cfg.Queue}
		cfg.OnResult = func(_ *store.WhatsAppOutbound, _ string, err error) { reported <- err }
		cfg.RecoveryInterval = time.Hour
		cfg.RecoveryLease = time.Hour
	})
	rig.pool.Start(t.Context())

	rig.enqueue(t, "alice", "hi")
	rig.waitDelivered(t, 1)

	select {
	case err := <-reported:
		if err != nil {
			t.Errorf("onResult err = %v; Meta accepted the message, so the user has it", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a delivered message was never reported to the adapter")
	}

	deadline := time.Now().Add(2 * time.Second)
	for metrics.outcomeCount(metricWhatsAppSends, "sent_unrecorded") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no sent_unrecorded emission: an unrecorded delivery was counted as an ordinary one")
		}
		time.Sleep(time.Millisecond)
	}
	if got := metrics.outcomeCount(metricWhatsAppSends, "sent"); got != 0 {
		t.Errorf("clean sends = %d, want 0: the ledger write failed, so recovery will re-send this row", got)
	}
}

func TestSenderPool_DoesNotRecoverItsOwnInFlightRow(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		graph := cfg.Attempt
		cfg.Attempt = func(ctx context.Context, row *store.WhatsAppOutbound) (string, error) {
			select {
			case <-gate:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return graph(ctx, row)
		}
		cfg.RecoveryLease = time.Nanosecond
		cfg.RecoveryInterval = time.Millisecond
		cfg.RecoveryHardLease = time.Hour
	})
	rig.pool.Start(t.Context())

	row := rig.enqueue(t, "alice", "held mid-send")
	rig.waitState(t, row.ID, store.WhatsAppOutboundSending)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, err := rig.repo.Get(context.Background(), row.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != store.WhatsAppOutboundSending {
			t.Fatalf("row went to %q while its own worker held it; recovery re-sends what it takes", got.State)
		}
		time.Sleep(2 * time.Millisecond)
	}

	close(gate)
	rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if got := rig.delivered(); len(got) != 1 {
		t.Errorf("delivered %d times, want exactly 1: %+v", len(got), got)
	}
}

func TestSenderPool_ServerErrorIsTerminalRatherThanRetried(t *testing.T) {
	t.Parallel()
	rig := newSenderRig(t, 0)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", serverError())
	row := rig.enqueue(t, "alice", "not sent twice")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundFailed)
	if settled.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: a 5xx send must not be repeated", settled.Attempts)
	}
	if got := rig.delivered(); len(got) != 0 {
		t.Errorf("delivered %+v; the scripted failure was the only attempt", got)
	}
	if waits := rig.waits(); len(waits) != 0 {
		t.Errorf("waits = %v, want none: nothing was scheduled for another attempt", waits)
	}
}

type wamidHostileLedger struct {
	senderQueue
}

func (q wamidHostileLedger) MarkSent(ctx context.Context, tx dbutil.Querier, id, wamid string, nowMs int64) error {
	if wamid != "" {
		<-ctx.Done()
		return ctx.Err()
	}
	return q.senderQueue.MarkSent(ctx, tx, id, wamid, nowMs)
}

func TestSenderPool_MarkSentFallbackRunsOnItsOwnBudget(t *testing.T) {
	t.Parallel()
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Queue = wamidHostileLedger{cfg.Queue}
		cfg.SettleTimeout = 50 * time.Millisecond
		cfg.RecoveryInterval = time.Hour
		cfg.RecoveryLease = time.Hour
	})
	rig.pool.Start(t.Context())

	row := rig.enqueue(t, "alice", "recorded without its id")
	rig.waitDelivered(t, 1)

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if settled.Wamid != "" {
		t.Errorf("wamid = %q, want it dropped by the fallback", settled.Wamid)
	}
}

func TestSenderPool_AcceptedButLostResponseIsCountedUnconfirmed(t *testing.T) {
	t.Parallel()
	metrics := newCountingMetrics()
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Metrics = metrics
		cfg.RecoveryInterval = time.Hour
		cfg.RecoveryLease = time.Hour
	})
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", fmt.Errorf("read whatsapp send response: %w: unexpected EOF", errRequestAccepted))
	row := rig.enqueue(t, "alice", "accepted, id unknown")

	rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	deadline := time.Now().Add(2 * time.Second)
	for metrics.outcomeCount(metricWhatsAppSends, "sent_unconfirmed") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no sent_unconfirmed emission; an unconfirmed delivery was counted as an ordinary one")
		}
		time.Sleep(time.Millisecond)
	}
	if got := metrics.outcomeCount(metricWhatsAppSends, "failed"); got != 0 {
		t.Errorf("failed = %d, want 0: Meta accepted this message", got)
	}
}

func TestSenderPool_RequeueSurvivesShutdownCancellation(t *testing.T) {
	t.Parallel()
	var pool atomic.Pointer[senderPool]
	var once sync.Once
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Jitter = func(d time.Duration) time.Duration {
			once.Do(func() {
				if p := pool.Load(); p != nil {
					p.sendCancel()
				}
			})
			return d + testJitter
		}
		cfg.RecoveryInterval = time.Hour
		cfg.RecoveryLease = time.Hour
	})
	pool.Store(rig.pool)
	rig.pool.Start(t.Context())

	rig.scriptFailures("alice", throttled())
	row := rig.enqueue(t, "alice", "requeued after the kill")

	var settled *store.WhatsAppOutbound
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := rig.repo.Get(context.Background(), row.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Attempts > 0 && got.State == store.WhatsAppOutboundQueued {
			settled = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row was never requeued after its attempt: %+v", got)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if settled.NotBefore == 0 {
		t.Error("not_before is unset; the next instance waits out the whole lease instead of the computed backoff")
	}
	if settled.ErrorCode == 0 && settled.ErrorDetail == "" {
		t.Error("neither the error code nor its detail survived; nothing records why this waited")
	}
}

func TestSenderPool_RenewalKeepsAnotherPoolsSweepOffTheRow(t *testing.T) {
	t.Parallel()
	db := newSenderTestDB(t)
	repo := store.NewWhatsAppOutboundRepo(db)
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	holder := newSenderPool(senderConfig{
		Queue: repo, GatewayID: "gw-test", Logger: discardLogger(),
		Attempt: func(context.Context, *store.WhatsAppOutbound) (string, error) {
			once.Do(func() { close(started) })
			<-release
			return "wamid.SLOW", nil
		},
		IdleTimeout:   time.Hour,
		RecoveryLease: time.Second, RecoveryInterval: 25 * time.Millisecond,
		RecoveryHardLease: time.Hour,
	})
	t.Cleanup(func() { holder.Stop(context.Background(), 2*time.Second) })

	row := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: "gw-test", Recipient: "alice", Kind: kindText,
		Payload: `{"text":"a big document"}`, State: store.WhatsAppOutboundQueued,
		CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli(),
	}
	if err := holder.Enqueue(context.Background(), nil, row); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	holder.Start(t.Context())
	<-started

	sweeper := newSenderPool(senderConfig{
		Queue: repo, GatewayID: "gw-test", Logger: discardLogger(),
		Attempt: func(context.Context, *store.WhatsAppOutbound) (string, error) {
			t.Error("the sweeping pool sent a message the holder was mid-POST on")
			return "", nil
		},
		IdleTimeout:   10 * time.Millisecond,
		RecoveryLease: time.Second, RecoveryInterval: 25 * time.Millisecond,
		RecoveryHardLease: time.Hour,
	})
	t.Cleanup(func() { sweeper.Stop(context.Background(), 2*time.Second) })
	sweeper.Start(t.Context())

	deadline := time.Now().Add(1400 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, err := repo.Get(context.Background(), row.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != store.WhatsAppOutboundSending {
			t.Fatalf("row went to %q while the holder was mid-attempt; the other pool's sweep took it", got.State)
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(release)
	settled := waitRowState(t, repo, row.ID, store.WhatsAppOutboundSent)
	if settled.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: a renewed claim must not be re-claimed", settled.Attempts)
	}
}

func waitRowState(t *testing.T, repo *store.WhatsAppOutboundRepo, id string, want string) *store.WhatsAppOutbound {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got != nil && got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("row %s never reached %q (now %+v)", id, want, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSenderPool_AcceptedDuringShutdownIsRecordedNotAbandoned(t *testing.T) {
	t.Parallel()
	var pool atomic.Pointer[senderPool]
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		cfg.Attempt = func(context.Context, *store.WhatsAppOutbound) (string, error) {
			if p := pool.Load(); p != nil {
				p.sendCancel()
			}
			return "", fmt.Errorf("read whatsapp send response: %w: unexpected EOF", errRequestAccepted)
		}
		cfg.RecoveryInterval = time.Hour
		cfg.RecoveryLease = time.Hour
	})
	pool.Store(rig.pool)
	rig.recordEvery()
	rig.pool.Start(t.Context())

	row := rig.enqueue(t, "alice", "accepted as the pod went down")

	settled := rig.waitState(t, row.ID, store.WhatsAppOutboundSent)
	if settled.Wamid != "" {
		t.Errorf("wamid = %q, want empty: the response that carried it was lost", settled.Wamid)
	}
}

type panickingQueue struct {
	senderQueue
	blown atomic.Bool
}

func (q *panickingQueue) MarkSent(ctx context.Context, tx dbutil.Querier, id, wamid string, nowMs int64) error {
	if q.blown.CompareAndSwap(false, true) {
		panic("outbound ledger exploded")
	}
	return q.senderQueue.MarkSent(ctx, tx, id, wamid, nowMs)
}

func TestSenderPool_PanicInASettleCostsOneStepNotTheProcess(t *testing.T) {
	var queue *panickingQueue
	rig := newSenderRigWith(t, 0, func(cfg *senderConfig) {
		queue = &panickingQueue{senderQueue: cfg.Queue}
		cfg.Queue = queue
	})
	rig.pool.Start(t.Context())

	const recipient = "15551230000"
	rig.enqueue(t, recipient, "first")
	rig.waitDelivered(t, 1)

	deadline := time.Now().Add(2 * time.Second)
	for !queue.blown.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the settle write never panicked; the net was not exercised")
		}
		time.Sleep(time.Millisecond)
	}

	rig.enqueue(t, recipient, "second")
	got := rig.waitDelivered(t, 2)
	if got[1].payload != `{"text":"second"}` {
		t.Fatalf("second send = %s; the worker did not survive the panic", got[1].payload)
	}
}
