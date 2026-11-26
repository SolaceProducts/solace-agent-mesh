// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budget

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestExhausted_AHardLimitIsReachedAtExactlyItsAmount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spent  int64
		amount *int64
		topUp  int64
		want   bool
	}{
		{"unlimited is never exhausted", math.MaxInt64, nil, 0, false},
		{"below the limit", 99, new(int64(100)), 0, false},
		{"exactly the limit", 100, new(int64(100)), 0, true},
		{"over the limit", 101, new(int64(100)), 0, true},
		{"a zero limit refuses everything", 0, new(int64(0)), 0, true},
		{"a top-up raises the limit", 120, new(int64(100)), 50, false},
		{"a top-up can be spent too", 150, new(int64(100)), 50, true},
		{"a top-up on an unlimited budget changes nothing", math.MaxInt64, nil, 50, false},
		{"a top-up that would overflow saturates", math.MaxInt64 - 1, new(int64(math.MaxInt64 - 10)), 100, false},
	} {
		if got := Exhausted(tc.spent, tc.amount, tc.topUp); got != tc.want {
			t.Errorf("%s: Exhausted(%d, %v, %d) = %v, want %v", tc.name, tc.spent, tc.amount, tc.topUp, got, tc.want)
		}
	}
}

func TestEnforcementState_SameDecisionIgnoresWhenItWasComputed(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	base := EnforcementState{BudgetsInUse: true, MeshBlocked: true, PeriodEnd: end, ComputedAt: end.Add(-time.Hour)}

	later := base
	later.ComputedAt = end.Add(-time.Minute)
	if !base.SameDecision(later) {
		t.Error("two states differing only in ComputedAt decide differently")
	}
	for name, change := range map[string]func(*EnforcementState){
		"blocked":               func(s *EnforcementState) { s.MeshBlocked = false },
		"in use":                func(s *EnforcementState) { s.BudgetsInUse = false },
		"period end":            func(s *EnforcementState) { s.PeriodEnd = end.AddDate(0, 1, 0) },
		"blocked users":         func(s *EnforcementState) { s.BlockedUsers = []string{UserKey("", "alice")} },
		"default users blocked": func(s *EnforcementState) { s.DefaultUsersBlocked = true },
		"admitted users":        func(s *EnforcementState) { s.AdmittedUsers = []string{UserKey("", "alice")} },
		"warned users":          func(s *EnforcementState) { s.WarnedUsers = []string{UserKey("", "alice")} },
		"default users warned":  func(s *EnforcementState) { s.DefaultUsersWarned = true },
		"unwarned users":        func(s *EnforcementState) { s.UnwarnedUsers = []string{UserKey("", "alice")} },
		"warn cap keys":         func(s *EnforcementState) { s.WarnCapKeys = map[string]string{UserKey("", "alice"): "k"} },
		"default warn cap key":  func(s *EnforcementState) { s.DefaultWarnCapKey = "k" },
	} {
		other := base
		change(&other)
		if base.SameDecision(other) {
			t.Errorf("a change to %s reads as the same decision", name)
		}
	}
}

func TestEnforcementState_UserBlockedReadsTheListTheStateCarries(t *testing.T) {
	alice, bob, carol := UserKey("", "alice"), UserKey("", "bob"), UserKey("", "carol")
	listsBlocked := EnforcementState{BlockedUsers: []string{alice, carol}}
	listsAdmitted := EnforcementState{DefaultUsersBlocked: true, AdmittedUsers: []string{alice, carol}}

	for _, tc := range []struct {
		name  string
		state EnforcementState
		user  string
		want  bool
	}{
		{"a listed blocked user", listsBlocked, carol, true},
		{"an unlisted user", listsBlocked, bob, false},
		{"a listed admitted user under a zero default", listsAdmitted, alice, false},
		{"an unlisted user under a zero default", listsAdmitted, bob, true},
		{"nobody when nothing is listed", EnforcementState{}, alice, false},
	} {
		if got := tc.state.UserBlocked(tc.user); got != tc.want {
			t.Errorf("%s: UserBlocked(%q) = %v, want %v", tc.name, tc.user, got, tc.want)
		}
	}
}

func TestEnforcementState_UserLevelPutsALimitAboveAWarning(t *testing.T) {
	alice, bob, carol := UserKey("", "alice"), UserKey("", "bob"), UserKey("", "carol")
	listsWarned := EnforcementState{BlockedUsers: []string{carol}, WarnedUsers: []string{alice, carol}}
	listsUnwarned := EnforcementState{DefaultUsersWarned: true, UnwarnedUsers: []string{alice}}

	for _, tc := range []struct {
		name  string
		state EnforcementState
		user  string
		want  Level
	}{
		{"a listed warned user", listsWarned, alice, LevelWarn},
		{"an unlisted user", listsWarned, bob, LevelNone},
		{"a blocked user who is also listed warned", listsWarned, carol, LevelLimit},
		{"an unwarned user under a warning default", listsUnwarned, alice, LevelNone},
		{"anyone else under a warning default", listsUnwarned, bob, LevelWarn},
		{"nobody when nothing is listed", EnforcementState{}, alice, LevelNone},
	} {
		if got := tc.state.UserLevel(tc.user); got != tc.want {
			t.Errorf("%s: UserLevel(%q) = %q, want %q", tc.name, tc.user, got, tc.want)
		}
	}
}

func TestEnforcementState_WarnCapKeyIsOnlyForAWarnedUser(t *testing.T) {
	alice, bob, carol := UserKey("", "alice"), UserKey("", "bob"), UserKey("", "carol")
	listed := EnforcementState{WarnedUsers: []string{alice}, BlockedUsers: []string{carol}, WarnCapKeys: map[string]string{alice: "a"}}
	inverse := EnforcementState{DefaultUsersWarned: true, UnwarnedUsers: []string{bob}, DefaultWarnCapKey: "d", WarnCapKeys: map[string]string{alice: "a"}}

	for _, tc := range []struct {
		name  string
		state EnforcementState
		user  string
		want  string
	}{
		{"a listed warned user", listed, alice, "a"},
		{"an unwarned user", listed, bob, ""},
		{"a blocked user", listed, carol, ""},
		{"a warned user on their own cap under a warning default", inverse, alice, "a"},
		{"a user on the warning default", inverse, carol, "d"},
		{"an unwarned user under a warning default", inverse, bob, ""},
	} {
		if got := tc.state.WarnCapKey(tc.user); got != tc.want {
			t.Errorf("%s: WarnCapKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUserKey_KeepsRowIDsAndIdentityNamesApart(t *testing.T) {
	const id = "0192F0A6-3C1B-7D2E-8F4A-5B6C7D8E9F00"
	if got, want := UserKey(id, "someone"), "id:0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f00"; got != want {
		t.Errorf("UserKey(id, sub) = %q, want %q: the row id wins, lowercased", got, want)
	}
	if UserKey("", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f00") == UserKey(id, "") {
		t.Error("an identity name that looks like a row id keys the same user as that row")
	}
	if got := UserKey("", ""); got != "" {
		t.Errorf("UserKey with neither = %q, want empty", got)
	}
}

func TestUserKey_KeepsNoIdentityNameInTheKey(t *testing.T) {
	key := UserKey("", "pat@example.com")
	if strings.Contains(key, "pat") || !strings.HasPrefix(key, "sub:") {
		t.Errorf("UserKey = %q, want a sub: key that does not carry the name", key)
	}
	if key != UserKey("", "pat@example.com") || key == UserKey("", "Pat@example.com") {
		t.Error("UserKey of a name is not stable, or folds case the spend rows do not")
	}
}
func TestSpentText_NamesWhoseBudgetIsSpent(t *testing.T) {
	resetsAt := time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
	mesh := &ExhaustedError{Tier: TierMesh, ResetsAt: resetsAt}
	own := &ExhaustedError{Tier: TierUser, ResetsAt: resetsAt}
	for _, tc := range []struct {
		name string
		err  *ExhaustedError
		want string
	}{
		{"mesh", mesh.ForPrincipal("system:nightly"), "this deployment's usage budget is used up until 1 November 2099 00:00 UTC"},
		{"a person's own", own.ForPrincipal("pat@example.com"), "your usage budget is used up until 1 November 2099 00:00 UTC"},
		{"a service account's own", own.ForPrincipal("system:nightly"), `the usage budget is used up until 1 November 2099 00:00 UTC for the service account "nightly" this runs as`},
	} {
		if got := SpentText(tc.err); got != tc.want {
			t.Errorf("%s: SpentText = %q, want %q", tc.name, got, tc.want)
		}
	}
	if own.SystemUser != "" {
		t.Error("ForPrincipal changed the refusal it was called on")
	}
}

func TestWithPrincipal_LeavesAnythingButAnExhaustedErrorAlone(t *testing.T) {
	other := errors.New("not a budget refusal")
	if got := WithPrincipal(other, "system:nightly"); got != other {
		t.Errorf("WithPrincipal = %v, want the error unchanged", got)
	}
	named := WithPrincipal(&ExhaustedError{Tier: TierUser}, "system:nightly")
	if exhausted, ok := errors.AsType[*ExhaustedError](named); !ok || exhausted.SystemUser != "nightly" {
		t.Errorf("WithPrincipal = %#v, want the refusal naming the service account", named)
	}
}

func TestEnforcementState_WithHeldUsersKeepsTheHeldWarnings(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	held := EnforcementState{BudgetsInUse: true, PeriodEnd: end, WarnedUsers: []string{"id:a"}, WarnCapKeys: map[string]string{"id:a": "k"}}
	omitted := EnforcementState{BudgetsInUse: true, PeriodEnd: end, ComputedAt: end.Add(-time.Hour)}.WithoutUsers()

	if got := omitted.WithHeldUsers(&held); got.UsersOmitted || got.UserLevel("id:a") != LevelWarn || got.WarnCapKey("id:a") != "k" {
		t.Errorf("with held lists = %+v, want the held warning and its cap key kept", got)
	}

	warnsAll := omitted
	warnsAll.DefaultUsersWarned = true
	if got := warnsAll.WithHeldUsers(&held); got.UserLevel("id:b") != LevelNone {
		t.Errorf("with a different default = %+v, want nobody warned rather than everybody", got)
	}
}

func TestEnforcementState_ValidateRejectsAnOmittedStateListingWarnings(t *testing.T) {
	s := EnforcementState{ComputedAt: time.Now(), UsersOmitted: true, WarnedUsers: []string{"id:a"}}
	if err := s.Validate(); err == nil {
		t.Error("Validate accepted a state that omits its users but lists a warned one")
	}
}

func TestEnforcementState_WithHeldUsersKeepsBlocksWhenOnlyTheWarningDefaultChanged(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	held := EnforcementState{BudgetsInUse: true, PeriodEnd: end, BlockedUsers: []string{"id:a"}}
	omitted := EnforcementState{BudgetsInUse: true, PeriodEnd: end, DefaultUsersWarned: true, ComputedAt: end.Add(-time.Hour)}.WithoutUsers()

	got := omitted.WithHeldUsers(&held)
	if !got.UserBlocked("id:a") {
		t.Errorf("with held lists = %+v, want the held block kept across a warning default change", got)
	}
	if got.UserLevel("id:b") != LevelNone {
		t.Errorf("with held lists = %+v, want nobody warned rather than everybody", got)
	}
}

func TestEnforcementState_WithHeldUsersFillsOnlyOmittedWarnings(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	held := EnforcementState{BudgetsInUse: true, PeriodEnd: end, BlockedUsers: []string{"id:a"}, WarnedUsers: []string{"id:w"}}
	sent := EnforcementState{BudgetsInUse: true, PeriodEnd: end, BlockedUsers: []string{"id:b"}, WarnedUsers: []string{"id:x"}, ComputedAt: end}.WithoutWarnings()

	got := sent.WithHeldUsers(&held)
	if got.WarningsOmitted || got.UserBlocked("id:a") || !got.UserBlocked("id:b") || got.UserLevel("id:w") != LevelWarn {
		t.Errorf("with held lists = %+v, want the sent blocks and the held warnings", got)
	}
}

func TestEnforcementState_ValidateRejectsAStateOmittingWarningsButListingThem(t *testing.T) {
	s := EnforcementState{ComputedAt: time.Now(), WarningsOmitted: true, WarnCapKeys: map[string]string{"id:a": "k"}}
	if err := s.Validate(); err == nil {
		t.Error("Validate accepted a state that omits its warnings but lists a cap key")
	}
}

func TestUserListAttrs_CountsUsersApartFromIdentitiesKnownOnlyByName(t *testing.T) {
	state := EnforcementState{
		DefaultUsersBlocked: true,
		AdmittedUsers:       []string{UserKey("u-1", ""), UserKey("u-2", ""), UserKey("", "pending@example.com")},
	}

	attrs := state.UserListAttrs()

	got := map[string]any{}
	for i := 0; i < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	want := map[string]any{
		"blockedUsers": 0, "blockedIdentities": 0, "defaultUsersBlocked": true,
		"admittedUsers": 2, "admittedIdentities": 1,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v (attrs %v)", key, got[key], value, attrs)
		}
	}
}
