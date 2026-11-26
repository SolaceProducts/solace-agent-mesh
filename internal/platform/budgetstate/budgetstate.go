// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type Store interface {
	MeshBudget(ctx context.Context) (store.LimitingBudget, error)
	AnyLimited(ctx context.Context) (bool, error)
	MeshTopUp(ctx context.Context, nowMs int64) (int64, error)
	PeriodCost(ctx context.Context, subject store.BudgetPeriodSubject, periodStart int64) (int64, error)
	UserLimits(ctx context.Context, nowMs int64) (store.UserBudgetLimits, error)
	PeriodCostsByUser(ctx context.Context, periodStart int64) (map[store.BudgetPeriodSubject]int64, error)
	CheckMeshPeriod(ctx context.Context, periodStart int64) (store.BudgetPeriodMeshCheck, error)

	AlertStates(ctx context.Context, periodStart int64) ([]store.BudgetAlertState, error)
	SetAlertState(ctx context.Context, state store.BudgetAlertState) error
	DeleteAlertStatesBefore(ctx context.Context, periodStart int64) (int64, error)
	User(ctx context.Context, id string) (*store.User, error)
}

func NewStore(db *store.DB) Store {
	return dbStore{
		BudgetsRepo:           store.NewBudgetsRepo(db),
		SpendLedgerRepo:       store.NewSpendLedgerRepo(db),
		BudgetAlertStatesRepo: store.NewBudgetAlertStatesRepo(db),
		topUps:                store.NewBudgetTopUpsRepo(db),
		users:                 store.NewUsersRepo(db),
	}
}

type dbStore struct {
	*store.BudgetsRepo
	*store.SpendLedgerRepo
	*store.BudgetAlertStatesRepo
	topUps *store.BudgetTopUpsRepo
	users  *store.UsersRepo
}

func (s dbStore) User(ctx context.Context, id string) (*store.User, error) {
	user, _, err := s.users.GetByID(ctx, id)
	return user, err
}

func (s dbStore) MeshTopUp(ctx context.Context, nowMs int64) (int64, error) {
	t, err := s.topUps.Active(ctx, store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, nowMs)
	if err != nil || t == nil {
		return 0, err
	}
	return t.Amount, nil
}

const maxStatePayload = broker.MaxMessageSize - 1<<20

func Compute(ctx context.Context, s Store, now time.Time) (budget.EnforcementState, error) {
	a, err := assess(ctx, s, now)
	return a.state, err
}

type standing struct {
	subjectType  string
	subject      store.BudgetPeriodSubject
	subjectRef   string
	level        budget.Level
	capKey       string
	systemUserID string
	spent        int64
	budget       store.LimitingBudget
	topUp        int64
}

type assessment struct {
	state       budget.EnforcementState
	periodStart int64
	standings   []standing
}

func assess(ctx context.Context, s Store, now time.Time) (assessment, error) {
	return assessWithin(ctx, s, now, maxStatePayload)
}

func assessWithin(ctx context.Context, s Store, now time.Time, maxPayload int) (assessment, error) {
	a, err := assessAt(ctx, s, now)
	if err == nil {
		err = a.state.Validate()
	}
	if err == nil {
		a.state, err = fitWithin(a.state, maxPayload)
	}
	if err != nil {
		return a, fmt.Errorf("compute budget state: %w", err)
	}
	return a, nil
}

func fitWithin(state budget.EnforcementState, maxPayload int) (budget.EnforcementState, error) {
	for _, candidate := range []budget.EnforcementState{state, state.WithoutWarnings()} {
		payload, err := json.Marshal(candidate)
		if err != nil {
			return state, fmt.Errorf("marshal budget state: %w", err)
		}
		if len(payload) <= maxPayload {
			return candidate, nil
		}
	}
	return state.WithoutUsers(), nil
}

func assessAt(ctx context.Context, s Store, now time.Time) (assessment, error) {
	a := assessment{
		state:       budget.EnforcementState{PeriodEnd: budget.PeriodEnd(now), ComputedAt: now.UTC()},
		periodStart: budget.PeriodStart(now).UnixMilli(),
	}
	inUse, err := s.AnyLimited(ctx)
	if err != nil {
		return a, err
	}
	mesh, err := meshStanding(ctx, s, now, a.periodStart)
	if err != nil {
		return a, err
	}
	a.state.BudgetsInUse = inUse || mesh.budget.Amount != nil
	a.state.MeshBlocked = mesh.level == budget.LevelLimit
	if !a.state.BudgetsInUse {
		return a, nil
	}
	a.standings = append(a.standings, mesh)
	limits, err := s.UserLimits(ctx, now.UnixMilli())
	if err != nil {
		return a, err
	}
	costs, err := s.PeriodCostsByUser(ctx, a.periodStart)
	if err != nil {
		return a, err
	}
	users := userStandings(limits, costs)
	applyUserStandings(&a.state, limits.Default, users, mesh.budget.ID)
	a.standings = append(a.standings, users...)
	return a, nil
}

func meshStanding(ctx context.Context, s Store, now time.Time, periodStart int64) (standing, error) {
	mesh := standing{subjectType: store.BudgetSubjectMesh, subjectRef: store.BudgetMeshSubjectRef}
	b, err := s.MeshBudget(ctx)
	if err != nil {
		return mesh, err
	}
	mesh.budget = b
	if b.Amount == nil {
		mesh.judge(b.ID)
		return mesh, nil
	}
	if mesh.topUp, err = s.MeshTopUp(ctx, now.UnixMilli()); err != nil {
		return mesh, err
	}
	if mesh.spent, err = s.PeriodCost(ctx, store.BudgetPeriodMeshSubject, periodStart); err != nil {
		return mesh, err
	}
	mesh.judge(b.ID)
	return mesh, nil
}

func (st *standing) judge(budgetRef string) {
	st.level = budget.LevelOf(st.spent, st.budget.Amount, st.topUp, st.budget.WarnPercentage)
	st.capKey = capKey(budgetRef, budget.EffectiveCap(st.budget.Amount, st.topUp), st.budget.WarnPercentage)
}

func capKey(budgetRef string, effectiveCap *int64, warnPercentage *int) string {
	limit, warn := "unlimited", "none"
	if effectiveCap != nil {
		limit = strconv.FormatInt(*effectiveCap, 10)
	}
	if warnPercentage != nil {
		warn = strconv.Itoa(*warnPercentage)
	}
	return budgetRef + ":" + limit + ":" + warn
}

func opaqueCapKey(keyID, capKey string) string {
	mac := hmac.New(sha256.New, []byte(keyID))
	mac.Write([]byte(capKey))
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

const defaultCapRef = "default"

func applyUserStandings(state *budget.EnforcementState, def store.LimitingBudget, users []standing, keyID string) {
	switch budget.LevelOf(0, def.Amount, 0, def.WarnPercentage) {
	case budget.LevelLimit:
		state.DefaultUsersBlocked = true
	case budget.LevelWarn:
		state.DefaultUsersWarned = true
		state.DefaultWarnCapKey = opaqueCapKey(keyID, capKey(defaultCapRef, budget.EffectiveCap(def.Amount, 0), def.WarnPercentage))
	}
	for _, u := range users {
		blocked := u.level == budget.LevelLimit
		switch {
		case state.DefaultUsersBlocked && !blocked:
			state.AdmittedUsers = append(state.AdmittedUsers, u.subjectRef)
		case !state.DefaultUsersBlocked && blocked:
			state.BlockedUsers = append(state.BlockedUsers, u.subjectRef)
		}
		switch {
		case state.DefaultUsersWarned && u.level == budget.LevelNone:
			state.UnwarnedUsers = append(state.UnwarnedUsers, u.subjectRef)
		case !state.DefaultUsersWarned && u.level == budget.LevelWarn:
			state.WarnedUsers = append(state.WarnedUsers, u.subjectRef)
		}
		if u.level == budget.LevelWarn {
			if state.WarnCapKeys == nil {
				state.WarnCapKeys = make(map[string]string)
			}
			state.WarnCapKeys[u.subjectRef] = opaqueCapKey(keyID, u.capKey)
		}
	}
	slices.Sort(state.AdmittedUsers)
	slices.Sort(state.BlockedUsers)
	slices.Sort(state.WarnedUsers)
	slices.Sort(state.UnwarnedUsers)
}

type governed struct {
	budget store.LimitingBudget
	topUp  int64
	capRef string
}

func userStandings(limits store.UserBudgetLimits, costs map[store.BudgetPeriodSubject]int64) []standing {
	byUser := make(map[store.BudgetPeriodSubject]governed, len(costs)+len(limits.Assigned)+len(limits.Pending))
	for user := range costs {
		byUser[user] = subjectGoverned(limits, user)
	}
	for id := range limits.TopUps {
		byUser[byID(id)] = idGoverned(limits, id)
	}
	for id := range limits.Assigned {
		byUser[byID(id)] = idGoverned(limits, id)
	}
	for sub, id := range limits.SystemUserIDs {
		if g := idGoverned(limits, id); g != defaultGoverned(limits) {
			byUser[bySub(sub)] = g
		}
	}
	for name := range limits.Pending {
		byUser[bySub(name)] = subjectGoverned(limits, bySub(name))
	}
	users := make([]standing, 0, len(byUser))
	for user, g := range byUser {
		u := standing{
			systemUserID: systemUserIDOf(limits, user),
			subjectType:  store.BudgetSubjectUser,
			subject:      user,
			subjectRef:   user.UserKey(),
			spent:        costs[user],
			budget:       g.budget,
			topUp:        g.topUp,
		}
		u.judge(g.capRef)
		users = append(users, u)
	}
	slices.SortFunc(users, func(a, b standing) int { return strings.Compare(a.subjectRef, b.subjectRef) })
	return users
}

func systemUserIDOf(limits store.UserBudgetLimits, user store.BudgetPeriodSubject) string {
	if user.Kind != store.BudgetPeriodUserSub {
		return ""
	}
	return limits.SystemUserIDs[user.Ref]
}

func subjectGoverned(limits store.UserBudgetLimits, user store.BudgetPeriodSubject) governed {
	if user.Kind == store.BudgetPeriodUserID {
		return idGoverned(limits, user.Ref)
	}
	if systemID, ok := limits.SystemUserIDs[user.Ref]; ok {
		return idGoverned(limits, systemID)
	}
	if b, ok := limits.Pending[user.Ref]; ok {
		return governed{budget: b, capRef: b.ID}
	}
	if b, ok := limits.Pending[strings.ToLower(user.Ref)]; ok {
		return governed{budget: b, capRef: b.ID}
	}
	return defaultGoverned(limits)
}

func idGoverned(limits store.UserBudgetLimits, id string) governed {
	g := defaultGoverned(limits)
	g.topUp = limits.TopUps[id]
	if b, ok := limits.Assigned[id]; ok {
		g.budget, g.capRef = b, b.ID
	}
	return g
}

func defaultGoverned(limits store.UserBudgetLimits) governed {
	return governed{budget: limits.Default, capRef: defaultCapRef}
}

func byID(id string) store.BudgetPeriodSubject {
	return store.BudgetPeriodSubject{Kind: store.BudgetPeriodUserID, Ref: id}
}

func bySub(sub string) store.BudgetPeriodSubject {
	return store.BudgetPeriodSubject{Kind: store.BudgetPeriodUserSub, Ref: sub}
}
