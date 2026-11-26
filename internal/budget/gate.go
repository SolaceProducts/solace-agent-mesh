// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Tier string

const (
	TierMesh Tier = "mesh"
	TierUser Tier = "user"
)

const (
	heldStateTrustWindow   = 60 * time.Second
	clockSkewWarnThreshold = time.Minute
	unconfirmedRetryAfter  = 30 * time.Second
	defaultPersistTimeout  = 5 * time.Second
)

var ErrExhausted = errors.New("budget exhausted")

const ExhaustedMessageFormat = "the %s budget is spent until %s"

type ExhaustedError struct {
	Tier       Tier
	ResetsAt   time.Time
	SystemUser string
}

func (e *ExhaustedError) ForPrincipal(principal string) *ExhaustedError {
	if e.Tier != TierUser || !rbac.IsSystemPrincipal(principal) {
		return e
	}
	named := *e
	named.SystemUser = rbac.SystemUserName(principal)
	return &named
}

func WithPrincipal(err error, principal string) error {
	if exhausted, ok := errors.AsType[*ExhaustedError](err); ok {
		return exhausted.ForPrincipal(principal)
	}
	return err
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf(ExhaustedMessageFormat, e.Tier, e.ResetsAt.UTC().Format(time.RFC3339))
}

func (e *ExhaustedError) Unwrap() error { return ErrExhausted }

var ErrUnconfirmed = errors.New("budget state unconfirmed")

type UnconfirmedError struct {
	RetryAt time.Time
}

func (e *UnconfirmedError) Error() string {
	return "budgets are in use and their current state is not confirmed"
}

func (e *UnconfirmedError) Unwrap() error { return ErrUnconfirmed }

type StateStore interface {
	LoadBudgetState(ctx context.Context) (*EnforcementState, error)
	SaveBudgetState(ctx context.Context, state EnforcementState) error
}

type Gate struct {
	logger         *slog.Logger
	now            func() time.Time
	persistTimeout time.Duration

	mu              sync.RWMutex
	state           *EnforcementState
	storeUnreadable bool
	appliedAt       time.Time
	saveFailed      bool
	skewWarned      bool
	stores          []*attachedStore
	changed         chan struct{}

	persistMu sync.Mutex
}

type attachedStore struct{ StateStore }

func NewGate(logger *slog.Logger) *Gate {
	return &Gate{logger: logger.With("logger_name", "budget-gate"), now: time.Now, persistTimeout: defaultPersistTimeout}
}

func (g *Gate) Check(user string) error {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	inUse := g.state != nil && g.state.BudgetsInUse
	switch {
	case g.state != nil && (g.state.MeshBlocked || user != "" && g.state.UserBlocked(user)):
		return g.blockedError()
	case g.storeUnreadable && g.appliedAt.IsZero():
		return &UnconfirmedError{RetryAt: g.now().Add(unconfirmedRetryAfter)}
	case inUse && g.appliedAt.IsZero():
		return &UnconfirmedError{RetryAt: g.now().Add(unconfirmedRetryAfter)}
	case user == "" && inUse:
		g.logger.Debug("admitting work that names no user against the mesh budget only")
	}
	return nil
}

func (g *Gate) Changed() <-chan struct{} {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return g.changed
}

func (g *Gate) signalChanged() {
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}

func (g *Gate) blockedError() error {
	switch {
	case !g.now().Before(g.state.PeriodEnd):
		return &UnconfirmedError{RetryAt: g.now().Add(unconfirmedRetryAfter)}
	case g.state.MeshBlocked:
		return &ExhaustedError{Tier: TierMesh, ResetsAt: g.state.PeriodEnd}
	default:
		return &ExhaustedError{Tier: TierUser, ResetsAt: g.state.PeriodEnd}
	}
}

func (g *Gate) HoldsBlock() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state != nil && g.state.AnyBlocked() && g.now().Before(g.state.PeriodEnd)
}

func (g *Gate) Unconfirmed() bool {
	if g == nil {
		return false
	}
	if errors.Is(g.Check(""), ErrUnconfirmed) {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state != nil && g.state.AnyBlocked() && !g.now().Before(g.state.PeriodEnd)
}

func (g *Gate) CheckKnownBlock(user string) error {
	if err := g.Check(user); errors.Is(err, ErrExhausted) {
		return err
	}
	return nil
}

func (g *Gate) Attach(ctx context.Context, s StateStore) (detach func()) {
	stored, err := s.LoadBudgetState(ctx)
	if err != nil {
		g.logger.Warn("could not read the stored budget state; new work is refused as unconfirmed until the platform sends one", "error", err)
	}
	attached := &attachedStore{s}
	g.mu.Lock()
	g.storeUnreadable = g.storeUnreadable || err != nil
	g.stores = append(g.stores, attached)
	heldFromPlatform := !g.appliedAt.IsZero()
	if stored != nil && !heldFromPlatform && (g.state == nil || stored.ComputedAt.After(g.state.ComputedAt)) {
		g.state = stored
		g.signalChanged()
	}
	g.mu.Unlock()
	g.persist(ctx)
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.stores = slices.DeleteFunc(g.stores, func(a *attachedStore) bool { return a == attached })
	}
}

func (g *Gate) Apply(ctx context.Context, state EnforcementState) {
	now := g.now()
	g.mu.Lock()
	heldFromPlatform := !g.appliedAt.IsZero()
	if heldFromPlatform && state.ComputedAt.Before(g.state.ComputedAt) && now.Sub(g.appliedAt) < heldStateTrustWindow {
		g.mu.Unlock()
		return
	}
	state = state.WithHeldUsers(g.state)
	changed := g.state == nil || !g.state.SameDecision(state)
	headlineChanged := g.state == nil || !g.state.SameHeadline(state)
	resave := g.saveFailed
	g.state, g.appliedAt = &state, now
	g.signalChanged()
	skewed := state.ComputedAt.Sub(now) > clockSkewWarnThreshold
	warnSkew := skewed && !g.skewWarned
	g.skewWarned = skewed
	g.mu.Unlock()
	if warnSkew {
		g.logger.Warn("budget state is stamped ahead of this process's clock; check clock sync between the platform and this process",
			"aheadSeconds", int64(state.ComputedAt.Sub(now).Seconds()))
	}
	level := slog.LevelDebug
	if headlineChanged || !heldFromPlatform {
		level = slog.LevelInfo
	}
	attrs := append([]any{"meshBlocked", state.MeshBlocked, "budgetsInUse", state.BudgetsInUse, "periodEnd", state.PeriodEnd}, state.UserListAttrs()...)
	switch {
	case changed:
		g.logger.Log(ctx, level, "budget state changed", attrs...)
	case !heldFromPlatform:
		g.logger.Log(ctx, level, "budget state confirmed by the platform; unchanged from the stored state", attrs...)
	}
	if changed || resave {
		g.persist(ctx)
	}
}

func (g *Gate) persist(ctx context.Context) {
	g.persistMu.Lock()
	defer g.persistMu.Unlock()
	g.mu.RLock()
	state, stores := g.state, slices.Clone(g.stores)
	g.mu.RUnlock()
	if state == nil {
		return
	}
	failed := false
	for _, s := range stores {
		saveCtx, cancel := context.WithTimeout(ctx, g.persistTimeout)
		err := s.SaveBudgetState(saveCtx, *state)
		cancel()
		if err != nil {
			failed = true
			g.logger.Warn("could not store the budget state; a restart before it is saved again may not see it", "error", err)
		}
	}
	g.mu.Lock()
	g.saveFailed = failed
	g.mu.Unlock()
}

type Status struct {
	OwnLevel         Level
	MeshLimitReached bool
	PeriodEnd        time.Time
	WarnCapKey       string
}

func (g *Gate) Status(user string) Status {
	now := time.Now()
	if g != nil {
		now = g.now()
	}
	status := Status{OwnLevel: LevelNone, PeriodEnd: PeriodEnd(now)}
	if g == nil {
		return status
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.state == nil || !g.state.BudgetsInUse || !now.Before(g.state.PeriodEnd) {
		return status
	}
	status.PeriodEnd = g.state.PeriodEnd
	status.MeshLimitReached = g.state.MeshBlocked
	if user != "" {
		status.OwnLevel = g.state.UserLevel(user)
		status.WarnCapKey = g.state.WarnCapKey(user)
	}
	return status
}
