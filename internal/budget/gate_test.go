// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	gateTestLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	gatePeriodEnd  = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
	gateNow        = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
)

func stateAt(at time.Time, blocked bool) EnforcementState {
	return EnforcementState{BudgetsInUse: true, MeshBlocked: blocked, PeriodEnd: gatePeriodEnd, ComputedAt: at}
}

type memoryStore struct {
	mu      sync.Mutex
	state   *EnforcementState
	saves   int
	failing bool
}

func (m *memoryStore) LoadBudgetState(context.Context) (*EnforcementState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failing {
		return nil, errors.New("store down")
	}
	return m.state, nil
}

func (m *memoryStore) SaveBudgetState(_ context.Context, s EnforcementState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failing {
		return errors.New("store down")
	}
	m.state, m.saves = &s, m.saves+1
	return nil
}

func (m *memoryStore) stored() (*EnforcementState, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, m.saves
}

func assertRefuses(t *testing.T, g *Gate) {
	t.Helper()
	err := g.Check("")
	exhausted, ok := errors.AsType[*ExhaustedError](err)
	if !ok || !errors.Is(err, ErrExhausted) {
		t.Fatalf("Check() = %v, want an *ExhaustedError", err)
	}
	if exhausted.Tier != TierMesh || !exhausted.ResetsAt.Equal(gatePeriodEnd) {
		t.Errorf("refusal = %+v, want the mesh tier resetting at %s", exhausted, gatePeriodEnd)
	}
}

func TestGate_AdmitsUntilItSeesAState(t *testing.T) {
	var nilGate *Gate
	if err := nilGate.Check(""); err != nil {
		t.Errorf("a nil gate refused: %v", err)
	}
	if err := NewGate(gateTestLogger).Check(""); err != nil {
		t.Errorf("a gate with no state refused: %v", err)
	}
}

func TestGate_RefusesWhileTheMeshIsBlockedAndAdmitsOnceItIsNot(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Apply(t.Context(), stateAt(gateNow, true))
	assertRefuses(t, g)

	g.Apply(t.Context(), stateAt(gateNow.Add(time.Second), false))
	if err := g.Check(""); err != nil {
		t.Errorf("an unblocked state still refused: %v", err)
	}
}

func TestGate_IgnoresAStateOlderThanTheOneItHolds(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Apply(t.Context(), stateAt(gateNow, true))
	g.Apply(t.Context(), stateAt(gateNow.Add(-time.Second), false))
	assertRefuses(t, g)
}

func TestGate_SavesOnlyStatesThatChangeTheDecision(t *testing.T) {
	g := NewGate(gateTestLogger)
	s := &memoryStore{}
	g.Attach(t.Context(), s)

	g.Apply(t.Context(), stateAt(gateNow, true))
	g.Apply(t.Context(), stateAt(gateNow.Add(30*time.Second), true))
	g.Apply(t.Context(), stateAt(gateNow.Add(60*time.Second), false))

	stored, saves := s.stored()
	if saves != 2 {
		t.Errorf("saves = %d, want one per decision change: a heartbeat is not saved", saves)
	}
	if stored == nil || stored.MeshBlocked {
		t.Errorf("stored = %+v, want the latest, unblocked state", stored)
	}
}

func TestGate_ASaveThatFailedIsRetriedWithTheNextStateEvenWhenItChangesNothing(t *testing.T) {
	s := &memoryStore{state: &EnforcementState{ComputedAt: gateNow.Add(-time.Hour)}}
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), s)

	s.mu.Lock()
	s.failing = true
	s.mu.Unlock()
	g.Apply(t.Context(), stateAt(gateNow, false))

	s.mu.Lock()
	s.failing = false
	s.mu.Unlock()
	g.Apply(t.Context(), stateAt(gateNow.Add(30*time.Second), false))

	if stored, _ := s.stored(); stored == nil || !stored.BudgetsInUse {
		t.Fatalf("stored = %+v, want the state with budgets in use saved once the store recovered", stored)
	}
	restarted := NewGate(gateTestLogger)
	restarted.Attach(t.Context(), s)
	if err := restarted.Check(""); !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("a gateway restarted after the save recovered admitted: Check() = %v, want unconfirmed", err)
	}

	_, saves := s.stored()
	g.Apply(t.Context(), stateAt(gateNow.Add(60*time.Second), false))
	if _, after := s.stored(); after != saves {
		t.Errorf("saves went from %d to %d on a heartbeat after a successful save, want none", saves, after)
	}
}

func TestGate_ARestartedGateRefusesFromItsStoreAlone(t *testing.T) {
	s := &memoryStore{}
	before := NewGate(gateTestLogger)
	before.Attach(t.Context(), s)
	before.Apply(t.Context(), stateAt(gateNow, true))

	restarted := NewGate(gateTestLogger)
	restarted.Attach(t.Context(), s)
	assertRefuses(t, restarted)
}

func TestGate_AttachKeepsAStateFromThePlatformOverAStoredOne(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Apply(t.Context(), stateAt(gateNow, false))

	futureBlock := &memoryStore{state: new(stateAt(gateNow.Add(time.Hour), true))}
	g.Attach(t.Context(), futureBlock)
	if err := g.Check(""); err != nil {
		t.Errorf("a stored block replaced the state the platform sent: %v", err)
	}
	if stored, _ := futureBlock.stored(); stored.MeshBlocked {
		t.Error("the store was not brought up to the platform's state")
	}
}

func TestGate_AttachPrefersTheNewerOfTwoStoredStates(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{state: new(stateAt(gateNow, false))})
	g.Attach(t.Context(), &memoryStore{state: new(stateAt(gateNow.Add(time.Minute), true))})
	assertRefuses(t, g)
}

func TestGate_TheFirstStateFromThePlatformReplacesAStoredOneWhateverItsStamp(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{state: new(stateAt(gateNow.Add(24*time.Hour), true))})
	assertRefuses(t, g)

	g.Apply(t.Context(), stateAt(gateNow, false))
	if err := g.Check(""); err != nil {
		t.Errorf("a stored, future-stamped block outranked the platform: %v", err)
	}
}

func TestGate_AFutureStampedStateIsReplacedOnceItsTrustWindowPasses(t *testing.T) {
	clock := gateNow
	g := NewGate(gateTestLogger)
	g.now = func() time.Time { return clock }

	g.Apply(t.Context(), stateAt(gateNow.Add(24*time.Hour), true))
	clock = clock.Add(heldStateTrustWindow - time.Second)
	g.Apply(t.Context(), stateAt(clock, false))
	assertRefuses(t, g)

	clock = clock.Add(2 * time.Second)
	g.Apply(t.Context(), stateAt(clock, false))
	if err := g.Check(""); err != nil {
		t.Errorf("a future-stamped block outlasted its trust window: %v", err)
	}
}

func TestGate_WarnsOnceWhileStatesAreStampedAheadOfItsClock(t *testing.T) {
	var logs syncBuffer
	g := NewGate(slog.New(slog.NewTextHandler(&logs, nil)))
	g.now = func() time.Time { return gateNow }

	g.Apply(t.Context(), stateAt(gateNow.Add(10*time.Minute), false))
	g.Apply(t.Context(), stateAt(gateNow.Add(11*time.Minute), false))
	if n := strings.Count(logs.String(), "stamped ahead"); n != 1 {
		t.Errorf("skew warnings = %d, want 1 while the skew lasts", n)
	}
	if err := g.Check(""); err != nil {
		t.Errorf("a skewed state was not applied: %v", err)
	}

	g.now = func() time.Time { return gateNow.Add(11 * time.Minute) }
	g.Apply(t.Context(), stateAt(gateNow.Add(11*time.Minute), false))
	g.Apply(t.Context(), stateAt(gateNow.Add(30*time.Minute), false))
	if n := strings.Count(logs.String(), "stamped ahead"); n != 2 {
		t.Errorf("skew warnings = %d, want a second one when skew returns after clearing", n)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestGate_AFailingStoreDoesNotStopTheGate(t *testing.T) {
	s := &memoryStore{failing: true}
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), s)
	g.Apply(t.Context(), stateAt(gateNow, true))
	assertRefuses(t, g)
}

func TestGate_AStoreItCannotReadIsUnconfirmedUntilThePlatformAnswers(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{failing: true})

	if err := g.Check(""); !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("Check() = %v, want unconfirmed while the stored state is unknown", err)
	}
	if err := g.CheckKnownBlock(""); err != nil {
		t.Errorf("CheckKnownBlock() = %v, want admitted work to run: no block is known", err)
	}

	g.Apply(t.Context(), stateAt(gateNow, false))
	if err := g.Check(""); err != nil {
		t.Errorf("Check() = %v after the platform confirmed the mesh is not blocked", err)
	}
}

func TestGate_AKnownBlockOutranksAStoreItCannotRead(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.now = func() time.Time { return gateNow }
	g.Attach(t.Context(), &memoryStore{failing: true})
	g.Attach(t.Context(), &memoryStore{state: new(stateAt(gateNow, true))})

	assertRefuses(t, g)
}

func TestGate_ChecksWhileStatesArriveAreSafe(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{})
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() { g.Apply(t.Context(), stateAt(gateNow.Add(time.Duration(i)*time.Second), i%2 == 0)) })
		wg.Go(func() { _ = g.Check("") })
	}
	wg.Wait()
}

func TestGate_ADetachedStoreIsNoLongerSaved(t *testing.T) {
	g := NewGate(gateTestLogger)
	s := &memoryStore{}
	detach := g.Attach(t.Context(), s)
	detach()

	g.Apply(t.Context(), stateAt(gateNow, true))
	if _, saves := s.stored(); saves != 0 {
		t.Errorf("saves = %d after detach, want 0", saves)
	}
}

type stalledStore struct{}

func (stalledStore) LoadBudgetState(context.Context) (*EnforcementState, error) { return nil, nil }

func (stalledStore) SaveBudgetState(ctx context.Context, _ EnforcementState) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestGate_AStalledStoreDoesNotHoldUpTheNextState(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.persistTimeout = 10 * time.Millisecond
	g.Attach(t.Context(), stalledStore{})

	applied := make(chan struct{})
	go func() {
		g.Apply(t.Context(), stateAt(gateNow, true))
		g.Apply(t.Context(), stateAt(gateNow.Add(time.Second), false))
		close(applied)
	}()
	select {
	case <-applied:
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled save held up the states after it")
	}
	if err := g.Check(""); err != nil {
		t.Errorf("the unblock behind a stalled save was not applied: %v", err)
	}
}

func TestGate_ARestartedGateRefusesUntilThePlatformConfirmsBudgetsInUse(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.now = func() time.Time { return gateNow }
	g.Attach(t.Context(), &memoryStore{state: new(stateAt(gateNow, false))})

	err := g.Check("")
	unconfirmed, ok := errors.AsType[*UnconfirmedError](err)
	if !ok || !errors.Is(err, ErrUnconfirmed) {
		t.Fatalf("Check() = %v, want an *UnconfirmedError while only a stored state with budgets in use is held", err)
	}
	if want := gateNow.Add(unconfirmedRetryAfter); !unconfirmed.RetryAt.Equal(want) {
		t.Errorf("RetryAt = %s, want %s", unconfirmed.RetryAt, want)
	}
	if err := g.CheckKnownBlock(""); err != nil {
		t.Errorf("CheckKnownBlock() = %v, want admitted work to run while the state is only unconfirmed", err)
	}

	g.Apply(t.Context(), stateAt(gateNow.Add(-time.Hour), false))
	if err := g.Check(""); err != nil {
		t.Errorf("the platform confirmed the mesh is not blocked, but the gate still refused: %v", err)
	}
}

func TestGate_AStoredUserBlockStopsAdmittedWorkBeforeThePlatformConfirms(t *testing.T) {
	alice, bob := UserKey("", "alice"), UserKey("", "bob")
	stored := stateAt(gateNow, false)
	stored.BlockedUsers = []string{alice}
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{state: &stored})

	if exhausted, ok := errors.AsType[*ExhaustedError](g.CheckKnownBlock(alice)); !ok || exhausted.Tier != TierUser {
		t.Errorf("CheckKnownBlock(alice) = %v, want her stored block to stop admitted work", exhausted)
	}
	if err := g.CheckKnownBlock(bob); err != nil {
		t.Errorf("CheckKnownBlock(bob) = %v, want admitted work to run while the state is only unconfirmed", err)
	}
	if !errors.Is(g.Check(bob), ErrUnconfirmed) {
		t.Error("Check(bob) admitted new work before the platform confirmed a state with budgets in use")
	}
}

func TestGate_AStoredStateWithNoBudgetsInUseAdmits(t *testing.T) {
	unlimited := stateAt(gateNow, false)
	unlimited.BudgetsInUse = false
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{state: &unlimited})
	if err := g.Check(""); err != nil {
		t.Errorf("a stored state with no budget in use refused: %v", err)
	}
}

func TestGate_CheckKnownBlockStillRefusesAKnownBlock(t *testing.T) {
	g := NewGate(gateTestLogger)
	g.Apply(t.Context(), stateAt(gateNow, true))
	if err := g.CheckKnownBlock(""); !errors.Is(err, ErrExhausted) {
		t.Errorf("CheckKnownBlock() = %v, want the known block", err)
	}
}

func TestGate_ABlockPastItsPeriodEndIsUnconfirmedUntilThePlatformSpeaks(t *testing.T) {
	periodEnd := gateNow.Add(time.Hour)
	g := NewGate(gateTestLogger)
	clock := gateNow
	g.now = func() time.Time { return clock }
	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: periodEnd, ComputedAt: gateNow})

	clock = periodEnd.Add(-time.Second)
	if err := g.Check(""); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Check() just before the period end = %v, want the block", err)
	}

	clock = periodEnd
	err := g.Check("")
	unconfirmed, ok := errors.AsType[*UnconfirmedError](err)
	if !ok || !unconfirmed.RetryAt.Equal(periodEnd.Add(unconfirmedRetryAfter)) {
		t.Fatalf("Check() at the period end = %v, want an *UnconfirmedError retrying after %s", err, unconfirmedRetryAfter)
	}
	if err := g.CheckKnownBlock(""); err != nil {
		t.Errorf("CheckKnownBlock() at the period end = %v, want admitted work to carry on", err)
	}

	nextEnd := periodEnd.AddDate(0, 1, 0)
	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, PeriodEnd: nextEnd, ComputedAt: periodEnd.Add(time.Second)})
	if err := g.Check(""); err != nil {
		t.Errorf("Check() once the platform confirmed the new period = %v, want admitted", err)
	}
}

func TestGate_ChangedIsClosedWhenTheGateTakesAState(t *testing.T) {
	gate := NewGate(gateTestLogger)
	changed := gate.Changed()
	select {
	case <-changed:
		t.Fatal("Changed was closed before any state arrived")
	default:
	}

	gate.Apply(t.Context(), stateAt(time.Now(), true))

	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("Changed was not closed when the gate took a state")
	}
	select {
	case <-gate.Changed():
		t.Error("a channel taken after the state is already closed; it must wait for the next one")
	default:
	}
}

func TestGate_AStoredBlockWhosePeriodHasEndedIsUnconfirmed(t *testing.T) {
	ended := EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: time.Now().Add(-time.Hour), ComputedAt: time.Now().Add(-2 * time.Hour)}
	g := NewGate(gateTestLogger)
	g.Attach(t.Context(), &memoryStore{state: &ended})

	err := g.Check("")
	if !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("Check() = %v, want unconfirmed rather than the block that ended", err)
	}
	if err := g.CheckKnownBlock(""); err != nil {
		t.Errorf("CheckKnownBlock() = %v, want work already admitted to run: the block it holds has ended", err)
	}
}

func TestDecodeState_RejectsAStateNoPlatformSends(t *testing.T) {
	for name, payload := range map[string]string{
		"empty":                  `{}`,
		"a block with no budget": `{"meshBlocked":true,"periodEnd":"2099-11-01T00:00:00Z","computedAt":"2026-10-16T12:00:00Z"}`,
		"a block with no period": `{"budgetsInUse":true,"meshBlocked":true,"computedAt":"2026-10-16T12:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeState([]byte(payload)); err == nil {
				t.Errorf("DecodeState(%s) accepted it", payload)
			}
		})
	}
	if _, err := DecodeState([]byte(`{"budgetsInUse":false,"computedAt":"2026-10-16T12:00:00Z"}`)); err != nil {
		t.Errorf("DecodeState rejected the flag-off answer: %v", err)
	}
}

func TestGate_RefusesAUserWhoseOwnBudgetIsSpent(t *testing.T) {
	alice, bob := UserKey("", "alice"), UserKey("", "bob")
	g := NewGate(gateTestLogger)
	state := stateAt(gateNow, false)
	state.BlockedUsers = []string{alice}
	g.Apply(t.Context(), state)

	exhausted, ok := errors.AsType[*ExhaustedError](g.Check(alice))
	if !ok || exhausted.Tier != TierUser || !exhausted.ResetsAt.Equal(gatePeriodEnd) {
		t.Errorf("Check(alice) = %v, want her own budget refusing until %s", exhausted, gatePeriodEnd)
	}
	if err := g.Check(bob); err != nil {
		t.Errorf("Check(bob) = %v, want admitted", err)
	}
	if err := g.Check(""); err != nil {
		t.Errorf("work naming no user = %v, want admitted against the mesh only", err)
	}
}

func TestGate_ASpentMeshIsReportedAheadOfAUsersOwnBudget(t *testing.T) {
	alice := UserKey("", "alice")
	g := NewGate(gateTestLogger)
	state := stateAt(gateNow, true)
	state.BlockedUsers = []string{alice}
	g.Apply(t.Context(), state)

	if exhausted, ok := errors.AsType[*ExhaustedError](g.Check(alice)); !ok || exhausted.Tier != TierMesh {
		t.Errorf("Check(alice) = %v, want the mesh named", exhausted)
	}
}

func TestGate_AZeroDefaultRefusesEveryoneNotAdmitted(t *testing.T) {
	allowed := UserKey("0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40", "")
	g := NewGate(gateTestLogger)
	state := stateAt(gateNow, false)
	state.DefaultUsersBlocked, state.AdmittedUsers = true, []string{allowed}
	g.Apply(t.Context(), state)

	if err := g.Check(allowed); err != nil {
		t.Errorf("an admitted user = %v, want admitted", err)
	}
	if _, ok := errors.AsType[*ExhaustedError](g.Check(UserKey("", "never-seen"))); !ok {
		t.Error("a user the platform has never seen was admitted under a zero default")
	}
}

func TestGate_AUserBlockPastItsPeriodEndIsUnconfirmed(t *testing.T) {
	alice := UserKey("", "alice")
	periodEnd := gateNow.Add(time.Hour)
	g := NewGate(gateTestLogger)
	g.now = func() time.Time { return periodEnd }
	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, BlockedUsers: []string{alice}, PeriodEnd: periodEnd, ComputedAt: gateNow})

	if err := g.Check(alice); !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("Check(alice) at the period end = %v, want an *UnconfirmedError", err)
	}
	if err := g.CheckKnownBlock(alice); err != nil {
		t.Errorf("CheckKnownBlock(alice) at the period end = %v, want her admitted work to carry on", err)
	}
}

func TestApply_LogsAUserListChangeAtDebugAndAMeshChangeAtInfo(t *testing.T) {
	var logs bytes.Buffer
	g := NewGate(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	end, now := time.Now().Add(time.Hour), time.Now()
	state := EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now}
	g.Apply(t.Context(), state)
	logs.Reset()

	state.BlockedUsers, state.ComputedAt = []string{UserKey("", "spent-user")}, now.Add(time.Second)
	g.Apply(t.Context(), state)
	if line := logs.String(); !strings.Contains(line, "level=DEBUG") || !strings.Contains(line, "budget state changed") {
		t.Errorf("a user-list change logged %q, want the state change at DEBUG", line)
	}
	logs.Reset()

	state.MeshBlocked, state.ComputedAt = true, now.Add(2*time.Second)
	g.Apply(t.Context(), state)
	if line := logs.String(); !strings.Contains(line, "level=INFO") || !strings.Contains(line, "budget state changed") {
		t.Errorf("a mesh change logged %q, want the state change at INFO", line)
	}
}

func TestApply_AStateThatOmitsItsUsersKeepsTheUserBlocksHeld(t *testing.T) {
	end, now := time.Now().Add(time.Hour), time.Now()
	spent := UserKey("", "spent-user")
	held := EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now, BlockedUsers: []string{spent}}
	omitted := EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: end, ComputedAt: now.Add(time.Second), UsersOmitted: true}

	g := NewGate(gateTestLogger)
	s := &memoryStore{}
	g.Attach(t.Context(), s)
	g.Apply(t.Context(), held)
	g.Apply(t.Context(), omitted)
	if stored, _ := s.stored(); stored == nil || stored.Validate() != nil || len(stored.BlockedUsers) != 1 {
		t.Errorf("stored %+v, want a valid whole state carrying the held user block", stored)
	}
	if _, ok := errors.AsType[*ExhaustedError](g.Check("")); !ok {
		t.Errorf("Check(\"\") = %v, want the omitted state's mesh block", g.Check(""))
	}
	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now.Add(2 * time.Second), UsersOmitted: true})
	if exhausted, ok := errors.AsType[*ExhaustedError](g.Check(spent)); !ok || exhausted.Tier != TierUser {
		t.Errorf("Check(spent user) = %v, want their held block kept while the lists are omitted", g.Check(spent))
	}

	nextPeriod := omitted
	nextPeriod.MeshBlocked, nextPeriod.PeriodEnd, nextPeriod.ComputedAt = false, end.Add(time.Hour), now.Add(3*time.Second)
	g.Apply(t.Context(), nextPeriod)
	if err := g.Check(spent); err != nil {
		t.Errorf("Check(spent user) = %v in a new period, want the old period's block dropped", err)
	}
}

func TestApply_UnderAZeroDefaultAStateThatOmitsItsUsersKeepsTheHeldAdmittedList(t *testing.T) {
	end, now := time.Now().Add(time.Hour), time.Now()
	admitted, unseen := UserKey("0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40", ""), UserKey("", "never-seen")
	g := NewGate(gateTestLogger)
	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now, DefaultUsersBlocked: true, AdmittedUsers: []string{admitted}})

	g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now.Add(time.Second), DefaultUsersBlocked: true, UsersOmitted: true})

	if err := g.Check(admitted); err != nil {
		t.Errorf("Check(admitted user) = %v, want them still admitted from the held list", err)
	}
	if _, ok := errors.AsType[*ExhaustedError](g.Check(unseen)); !ok {
		t.Error("a user the held list does not admit was admitted under a zero default")
	}
}

func TestApply_UnderAZeroDefaultAStateThatOmitsItsUsersWithNothingHeldAdmitsEveryUser(t *testing.T) {
	end, now := time.Now().Add(time.Hour), time.Now()
	admitted := UserKey("0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40", "")
	for name, held := range map[string]*EnforcementState{
		"a gate that holds no state": nil,
		"the first state of a new period": {BudgetsInUse: true, PeriodEnd: end.Add(-time.Hour), ComputedAt: now.Add(-time.Hour),
			DefaultUsersBlocked: true, AdmittedUsers: []string{admitted}},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewGate(gateTestLogger)
			s := &memoryStore{}
			g.Attach(t.Context(), s)
			if held != nil {
				g.Apply(t.Context(), *held)
			}

			g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: now, DefaultUsersBlocked: true, UsersOmitted: true})

			for _, user := range []string{admitted, UserKey("", "never-seen")} {
				if err := g.Check(user); err != nil {
					t.Errorf("Check(%s) = %v, want every user admitted while nothing says who is", user, err)
				}
				if err := g.CheckKnownBlock(user); err != nil {
					t.Errorf("CheckKnownBlock(%s) = %v, want running work left alone", user, err)
				}
			}
			if stored, _ := s.stored(); stored == nil || stored.Validate() != nil {
				t.Errorf("stored %+v, want a valid state", stored)
			}

			g.Apply(t.Context(), EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: end, ComputedAt: now.Add(time.Second), DefaultUsersBlocked: true, UsersOmitted: true})
			if exhausted, ok := errors.AsType[*ExhaustedError](g.Check(admitted)); !ok || exhausted.Tier != TierMesh {
				t.Errorf("Check = %v, want the mesh block still applied", g.Check(admitted))
			}
		})
	}
}

func TestDecodeState_RefusesAStateThatOmitsItsUsersButListsSome(t *testing.T) {
	_, err := DecodeState([]byte(`{"budgetsInUse":true,"usersOmitted":true,"blockedUsers":["id:x"],"periodEnd":"2099-11-01T00:00:00Z","computedAt":"2099-10-01T00:00:00Z"}`))
	if err == nil {
		t.Error("decoded a state that omits its users but lists some, want it refused")
	}
}

func TestGate_StatusReportsTheUsersLevelTheMeshAndThePeriod(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	alice := UserKey("", "alice")
	gate := NewGate(slog.New(slog.DiscardHandler))
	gate.now = func() time.Time { return end.Add(-time.Hour) }

	if s := gate.Status(alice); s.OwnLevel != LevelNone || s.MeshLimitReached || !s.PeriodEnd.Equal(end) {
		t.Errorf("status with no state = %+v, want none until %s", s, end)
	}
	gate.Apply(t.Context(), EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: end, ComputedAt: end.Add(-time.Hour),
		WarnedUsers: []string{alice}, WarnCapKeys: map[string]string{alice: "k"}})
	if s := gate.Status(alice); s.OwnLevel != LevelWarn || !s.MeshLimitReached || s.WarnCapKey != "k" || !s.PeriodEnd.Equal(end) {
		t.Errorf("status = %+v, want alice warned under k and the mesh reached until %s", s, end)
	}
	if s := gate.Status(""); s.OwnLevel != LevelNone || !s.MeshLimitReached {
		t.Errorf("status for no user = %+v, want only the mesh", s)
	}

	gate.now = func() time.Time { return end.Add(time.Minute) }
	if s := gate.Status(alice); s.OwnLevel != LevelNone || s.MeshLimitReached || !s.PeriodEnd.Equal(PeriodEnd(end)) {
		t.Errorf("status on a state whose period has ended = %+v, want nothing reached until the next period's end", s)
	}
	if s := (*Gate)(nil).Status(alice); s.OwnLevel != LevelNone || s.MeshLimitReached {
		t.Errorf("a nil gate's status = %+v, want nothing reached", s)
	}
}

func TestApply_ConfirmsAStoredStateThePlatformRepeatsOnceAtInfo(t *testing.T) {
	const confirmed = "budget state confirmed by the platform; unchanged from the stored state"
	var logs bytes.Buffer
	g := NewGate(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	stored := stateAt(gateNow, false)
	stored.BlockedUsers = []string{UserKey("user-1", ""), UserKey("", "pending-sub")}
	g.Attach(t.Context(), &memoryStore{state: &stored})
	logs.Reset()

	repeated := stored
	repeated.ComputedAt = gateNow.Add(time.Second)
	g.Apply(t.Context(), repeated)
	line := logs.String()
	if strings.Count(line, confirmed) != 1 || !strings.Contains(line, "level=INFO") || strings.Contains(line, "budget state changed") {
		t.Fatalf("the platform's first state, equal to the stored one, logged %q; want one INFO confirming it", line)
	}
	for _, want := range []string{"blockedUsers=1", "blockedIdentities=1"} {
		if !strings.Contains(line, want) {
			t.Errorf("confirmation %q lacks %s", line, want)
		}
	}
	logs.Reset()

	repeated.ComputedAt = gateNow.Add(2 * time.Second)
	g.Apply(t.Context(), repeated)
	if line := logs.String(); line != "" {
		t.Errorf("a later unchanged state logged %q, want nothing", line)
	}
}
