// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func userSubjectIDs(subjects []*UserBudgetSubject) []string {
	ids := make([]string, len(subjects))
	for i, s := range subjects {
		ids[i] = s.ID
	}
	return ids
}

func TestBudgetAssignmentsRepo_UserSubjects(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		bob := seedAssignmentUser(t, f.db, "bob", "jit", new("zz bob"), nil)
		carol := seedAssignmentUser(t, f.db, "carol", "jit", nil, new("zz-carol@acme.test"))
		alice := seedAssignmentUser(t, f.db, "alice", "jit", new("Zz Alice"), new("alice@acme.test"))
		blank := seedAssignmentUser(t, f.db, "blank", "jit", new(""), new("blank@acme.test"))
		assignment := f.assign(t, alice, eng.ID)

		list := func(filter UserBudgetSubjectFilter) []*UserBudgetSubject {
			t.Helper()
			filter.NowMs = time.Now().UnixMilli()
			subjects, err := f.repo.ListUserSubjects(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			return subjects
		}
		count := func(filter UserBudgetSubjectFilter) int64 {
			t.Helper()
			n, err := f.repo.CountUserSubjects(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}

		page := list(UserBudgetSubjectFilter{Search: "ZZ"})
		if got := userSubjectIDs(page); !slices.Equal(got, []string{alice, bob, carol}) {
			t.Fatalf("search, ascending = %v, want alice, bob, carol (carol sorts by her email)", got)
		}
		a, b, c := page[0], page[1], page[2]
		if derefString(a.AssignmentID) != assignment.ID || derefString(a.BudgetID) != eng.ID || derefString(a.BudgetName) != "Engineering" ||
			a.BudgetAmount == nil || *a.BudgetAmount != 100 {
			t.Errorf("alice's assignment = %+v", a)
		}
		if b.AssignmentID != nil || b.BudgetID != nil || b.BudgetName != nil || b.BudgetAmount != nil {
			t.Errorf("bob has no assignment, got %+v", b)
		}
		if c.DisplayName != nil || derefString(c.Email) != "zz-carol@acme.test" || c.IsSystemUser {
			t.Errorf("carol = %+v", c)
		}

		if got := userSubjectIDs(list(UserBudgetSubjectFilter{Search: "zz", Descending: true})); !slices.Equal(got, []string{carol, bob, alice}) {
			t.Errorf("descending = %v, want carol, bob, alice", got)
		}
		paged := UserBudgetSubjectFilter{Search: "zz", Limit: 1, Offset: 1}
		if got := userSubjectIDs(list(paged)); !slices.Equal(got, []string{bob}) {
			t.Errorf("second page of one = %v, want bob", got)
		}
		if n := count(paged); n != 3 {
			t.Errorf("count = %d, want 3 whatever the page", n)
		}

		if got := list(UserBudgetSubjectFilter{Search: "blank@"}); len(got) != 1 || got[0].ID != blank || got[0].DisplayName != nil {
			t.Errorf("a blank display name must read as null: %+v", got)
		}
	})
}

func TestBudgetAssignmentsRepo_UserSubjectsIgnoreOtherSubjectTypes(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() == "postgres" {
			t.Skip("the CHECK refuses a non-user assignment")
		}
		if _, err := f.db.ExecContext(t.Context(),
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, 'x', 'x', 1, 1)`, idgen.New(), BudgetSubjectMesh, refA, SeededMeshBudgetID); err != nil {
			t.Fatalf("insert a mesh assignment: %v", err)
		}
		subjects, err := f.repo.ListUserSubjects(t.Context(), UserBudgetSubjectFilter{NowMs: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.Index(userSubjectIDs(subjects), refA)
		if i < 0 || subjects[i].BudgetID != nil {
			t.Errorf("refA must be listed with no assignment, got index %d: %+v", i, subjects)
		}
	})
}

func TestBudgetAssignmentsRepo_UserSubjectsHidesSystemUsers(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		const channelSub = "system:channel"
		channel, err := dbutil.QueryVal[string](ctx, f.db,
			`SELECT CAST(id AS TEXT) FROM users WHERE issuer = ? AND sub = ?`, systemUserIssuer, channelSub)
		if err != nil {
			t.Fatalf("read the seeded channel user: %v", err)
		}
		namesake := seedAssignmentUser(t, f.db, channelSub, "jit", new("Namesake"), nil)
		defaultUser, err := dbutil.QueryVal[string](ctx, f.db,
			`SELECT CAST(id AS TEXT) FROM users WHERE issuer = ? AND sub = 'system:default'`, systemUserIssuer)
		if err != nil {
			t.Fatalf("read the seeded default user: %v", err)
		}

		all, err := f.repo.ListUserSubjects(ctx, UserBudgetSubjectFilter{NowMs: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		hiddenFilter := UserBudgetSubjectFilter{HiddenSystemSubs: []string{channelSub}, NowMs: time.Now().UnixMilli()}
		shown, err := f.repo.ListUserSubjects(ctx, hiddenFilter)
		if err != nil {
			t.Fatal(err)
		}
		ids := userSubjectIDs(shown)
		if slices.Contains(ids, channel) || !slices.Contains(userSubjectIDs(all), channel) {
			t.Errorf("the channel user must be listed only when not hidden: hidden %v, all %v", ids, userSubjectIDs(all))
		}
		if !slices.Contains(ids, namesake) {
			t.Error("a non-system user sharing the hidden sub was hidden too")
		}
		if len(shown) != len(all)-1 {
			t.Errorf("hiding one sub left %d of %d", len(shown), len(all))
		}
		if n, err := f.repo.CountUserSubjects(ctx, hiddenFilter); err != nil || n != int64(len(shown)) {
			t.Errorf("count = %d, %v; want %d", n, err, len(shown))
		}
		i := slices.Index(ids, defaultUser)
		if i < 0 || !shown[i].IsSystemUser {
			t.Errorf("system:default must be listed as a system user, got index %d", i)
		}
	})
}

func TestBudgetAssignmentsRepo_UserSubjectsByIDs(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		alice := seedAssignmentUser(t, f.db, "alice", "jit", new("Zz Alice"), nil)
		bob := seedAssignmentUser(t, f.db, "bob", "jit", new("Zz Bob"), nil)
		seedAssignmentUser(t, f.db, "carol", "jit", new("Zz Carol"), nil)
		channel, err := dbutil.QueryVal[string](ctx, f.db,
			`SELECT CAST(id AS TEXT) FROM users WHERE issuer = ? AND sub = 'system:channel'`, systemUserIssuer)
		if err != nil {
			t.Fatalf("read the seeded channel user: %v", err)
		}

		for _, c := range []struct {
			name   string
			filter UserBudgetSubjectFilter
			want   []string
		}{
			{"only the named users", UserBudgetSubjectFilter{IDs: []string{bob, alice}}, []string{alice, bob}},
			{"an uppercase id still matches", UserBudgetSubjectFilter{IDs: []string{strings.ToUpper(alice)}}, []string{alice}},
			{"combined with search", UserBudgetSubjectFilter{IDs: []string{alice, bob}, Search: "bob"}, []string{bob}},
			{"a hidden user stays hidden", UserBudgetSubjectFilter{IDs: []string{alice, channel}, HiddenSystemSubs: []string{"system:channel"}}, []string{alice}},
			{"a non-UUID matches nothing", UserBudgetSubjectFilter{IDs: []string{"system:default"}}, nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				c.filter.NowMs = time.Now().UnixMilli()
				subjects, err := f.repo.ListUserSubjects(ctx, c.filter)
				if err != nil {
					t.Fatal(err)
				}
				if got := userSubjectIDs(subjects); !slices.Equal(got, c.want) && (len(got) != 0 || len(c.want) != 0) {
					t.Errorf("list = %v, want %v", got, c.want)
				}
				if n, err := f.repo.CountUserSubjects(ctx, c.filter); err != nil || n != int64(len(c.want)) {
					t.Errorf("count = %d, %v; want %d", n, err, len(c.want))
				}
			})
		}
	})
}
