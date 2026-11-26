// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	fbTestSession = "00000000-0000-0000-0000-000000000001"
	fbTestTask    = "10000000-0000-0000-0000-000000000001"
)

func submission(s FeedbackSubmission) FeedbackSubmission {
	s.SessionID = cmp.Or(s.SessionID, fbTestSession)
	s.TaskID = cmp.Or(s.TaskID, fbTestTask)
	s.UserID = cmp.Or(s.UserID, "u1")
	s.SubjectID = cmp.Or(s.SubjectID, "agent-1")
	s.SubjectType = cmp.Or(s.SubjectType, SubjectTypeAgent)
	s.EntrypointType = cmp.Or(s.EntrypointType, EntrypointTypeWebUI)
	return s
}

func mustSaveFeedback(t *testing.T, repo *FeedbackRepo, f *Feedback) {
	t.Helper()
	SeedFeedback(t, repo.db, f)
}

func mustSubmit(t *testing.T, repo *FeedbackRepo, s FeedbackSubmission) FeedbackResult {
	t.Helper()
	res, err := repo.Submit(context.Background(), submission(s))
	if err != nil {
		t.Fatalf("submit feedback %s: %v", s.TaskID, err)
	}
	return res
}

func onlyFeedbackRow(t *testing.T, repo *FeedbackRepo) *Feedback {
	t.Helper()
	rows, err := repo.Search(context.Background(), "*", 0, 0, "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d feedback rows, want 1", len(rows))
	}
	return rows[0]
}

func TestFeedbackRepo_Submit_RejectsMissingAttribution(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()

	for _, tt := range []struct {
		field string
		mut   func(*FeedbackSubmission)
	}{
		{"SubjectID", func(s *FeedbackSubmission) { s.SubjectID = "" }},
		{"SubjectType sentinel", func(s *FeedbackSubmission) { s.SubjectType = SubjectTypeUnknown }},
		{"SubjectType out of enum", func(s *FeedbackSubmission) { s.SubjectType = "session" }},
		{"EntrypointType sentinel", func(s *FeedbackSubmission) { s.EntrypointType = EntrypointTypeUnknown }},
		{"EntrypointType gateway-only", func(s *FeedbackSubmission) { s.EntrypointType = GatewayTypeEmail }},
	} {
		t.Run(tt.field, func(t *testing.T) {
			s := submission(FeedbackSubmission{Rating: "up"})
			tt.mut(&s)
			if _, err := repo.Submit(ctx, s); err == nil {
				t.Errorf("Submit with bad %s should be rejected", tt.field)
			}
		})
	}
	rows, err := repo.Search(ctx, "*", 0, 0, "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rejected submissions must persist nothing, found %d rows", len(rows))
	}
}

func TestFeedbackRepo_Search(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()

	mustSubmit(t, repo, FeedbackSubmission{TaskID: "10000000-0000-0000-0000-000000000001", Rating: "up"})
	mustSubmit(t, repo, FeedbackSubmission{TaskID: "10000000-0000-0000-0000-000000000002", Rating: "down"})
	mustSubmit(t, repo, FeedbackSubmission{TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u2", Rating: "up"})

	for _, tc := range []struct {
		name           string
		user, task, rt string
		want           int
	}{
		{"by user", "u1", "", "", 2},
		{"by rating", "*", "", "up", 2},
		{"by task across users", "*", "10000000-0000-0000-0000-000000000001", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := repo.Search(ctx, tc.user, 0, 0, tc.task, "", tc.rt, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.want {
				t.Fatalf("len = %d, want %d", len(rows), tc.want)
			}
			for _, row := range rows {
				if (tc.user != "*" && row.UserID != tc.user) || (tc.task != "" && row.TaskID != tc.task) || (tc.rt != "" && row.Rating != tc.rt) {
					t.Errorf("row %+v does not match filter user=%q task=%q rating=%q", row, tc.user, tc.task, tc.rt)
				}
			}
		})
	}
}

func TestFeedbackRepo_Submit_RoundTripsAttribution(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)

	res := mustSubmit(t, repo, FeedbackSubmission{
		Rating: "down", SubjectID: "wf-9", SubjectType: SubjectTypeWorkflow, EntrypointType: EntrypointTypeWhatsApp,
	})
	got := onlyFeedbackRow(t, repo)
	if got.SubjectID != "wf-9" || got.SubjectType != SubjectTypeWorkflow || got.EntrypointType != EntrypointTypeWhatsApp {
		t.Errorf("attribution did not round-trip: %+v", got)
	}
	if res.Feedback.ID != got.ID || res.Feedback.CreatedAt != got.CreatedAt {
		t.Errorf("result row %+v does not match stored row %+v", res.Feedback, got)
	}
}

func TestB10_FeedbackRepoRatingThenDetailsOnce(t *testing.T) {
	comment := "cited the wrong doc"
	other := "changed my mind"

	type step struct {
		name    string
		sub     FeedbackSubmission
		want    FeedbackOutcome
		wantErr error
	}
	for _, tc := range []struct {
		name  string
		steps []step
	}{
		{"B10_rating_then_details_once", []step{
			{"first rating accepted", FeedbackSubmission{Rating: "down"}, FeedbackInserted, nil},
			{"identical rating redelivery idempotent", FeedbackSubmission{Rating: "down"}, FeedbackIdempotent, nil},
			{"details with a changed rating conflict while details are open", FeedbackSubmission{Rating: "up", Comment: &comment}, "", ErrFeedbackConflict},
			{"details added once", FeedbackSubmission{Rating: "down", Comment: &comment, ShareContent: true}, FeedbackDetailsAdded, nil},
			{"identical details redelivery idempotent", FeedbackSubmission{Rating: "down", Comment: &comment, ShareContent: true}, FeedbackIdempotent, nil},
			{"rating-only after details idempotent", FeedbackSubmission{Rating: "down"}, FeedbackIdempotent, nil},
			{"changed comment conflicts", FeedbackSubmission{Rating: "down", Comment: &other, ShareContent: true}, "", ErrFeedbackConflict},
			{"changed share conflicts", FeedbackSubmission{Rating: "down", Comment: &comment}, "", ErrFeedbackConflict},
			{"removed comment conflicts", FeedbackSubmission{Rating: "down", ShareContent: true}, "", ErrFeedbackConflict},
			{"changed rating conflicts", FeedbackSubmission{Rating: "up"}, "", ErrFeedbackConflict},
			{"changed rating with same details conflicts", FeedbackSubmission{Rating: "up", Comment: &comment, ShareContent: true}, "", ErrFeedbackConflict},
		}},
		{"B10_details_in_first_submission", []step{
			{"first submission with details accepted", FeedbackSubmission{Rating: "up", Comment: &comment}, FeedbackInserted, nil},
			{"identical redelivery idempotent", FeedbackSubmission{Rating: "up", Comment: &comment}, FeedbackIdempotent, nil},
			{"adding share later conflicts", FeedbackSubmission{Rating: "up", Comment: &comment, ShareContent: true}, "", ErrFeedbackConflict},
			{"changed rating conflicts", FeedbackSubmission{Rating: "down"}, "", ErrFeedbackConflict},
		}},
		{"B10_share_only_details", []step{
			{"first rating", FeedbackSubmission{Rating: "up"}, FeedbackInserted, nil},
			{"share consent added", FeedbackSubmission{Rating: "up", ShareContent: true}, FeedbackDetailsAdded, nil},
			{"comment added later conflicts", FeedbackSubmission{Rating: "up", Comment: &comment, ShareContent: true}, "", ErrFeedbackConflict},
		}},
		{"B10_empty_comment_is_no_details", []step{
			{"first rating", FeedbackSubmission{Rating: "up", Comment: new("")}, FeedbackInserted, nil},
			{"details still open", FeedbackSubmission{Rating: "up", Comment: &comment}, FeedbackDetailsAdded, nil},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			repo := NewFeedbackRepo(db)
			var accepted *Feedback
			for _, st := range tc.steps {
				before := onlyFeedbackRowOrNil(t, repo)
				res, err := repo.Submit(t.Context(), submission(st.sub))
				if st.wantErr != nil {
					if !errors.Is(err, st.wantErr) {
						t.Fatalf("%s: err = %v, want %v", st.name, err, st.wantErr)
					}
					if after := onlyFeedbackRow(t, repo); !sameFeedback(before, after) {
						t.Fatalf("%s: rejected submission changed the row: before %+v after %+v", st.name, before, after)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", st.name, err)
				}
				if res.Outcome != st.want {
					t.Fatalf("%s: outcome = %q, want %q", st.name, res.Outcome, st.want)
				}
				after := onlyFeedbackRow(t, repo)
				if res.Changed() != (st.want != FeedbackIdempotent) {
					t.Fatalf("%s: Changed() = %v for outcome %q", st.name, res.Changed(), res.Outcome)
				}
				if st.want == FeedbackIdempotent && !sameFeedback(before, after) {
					t.Fatalf("%s: idempotent submission changed the row: before %+v after %+v", st.name, before, after)
				}
				if st.want != FeedbackIdempotent && before != nil && after.UpdatedAt <= before.UpdatedAt {
					t.Fatalf("%s: updated_at did not advance (%d -> %d)", st.name, before.UpdatedAt, after.UpdatedAt)
				}
				if res.Feedback == nil || res.Feedback.ID != after.ID || res.Feedback.Rating != after.Rating {
					t.Fatalf("%s: result row %+v does not reflect stored row %+v", st.name, res.Feedback, after)
				}
				if res.Feedback.Inserted != (st.want == FeedbackInserted) {
					t.Fatalf("%s: Inserted = %v for outcome %q", st.name, res.Feedback.Inserted, st.want)
				}
				if accepted != nil && after.ID != accepted.ID {
					t.Fatalf("%s: row id changed from %s to %s", st.name, accepted.ID, after.ID)
				}
				accepted = after
			}
		})
	}
}

func TestB10_FeedbackRepoDetailsSubmittedTimeFreezesDetails(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	comment := "why"

	mustSubmit(t, repo, FeedbackSubmission{Rating: "down"})
	if got := onlyFeedbackRow(t, repo).DetailsSubmittedTime; got != nil {
		t.Fatalf("rating-only row details_submitted_time = %d, want NULL", *got)
	}
	mustSubmit(t, repo, FeedbackSubmission{Rating: "down", Comment: &comment})
	row := onlyFeedbackRow(t, repo)
	if row.DetailsSubmittedTime == nil {
		t.Fatal("details_submitted_time not set when details were added")
	}
	if row.Comment == nil || *row.Comment != comment || row.ShareContent {
		t.Fatalf("stored details = (%v, %v), want (%q, false)", row.Comment, row.ShareContent, comment)
	}
}

func TestB10_FeedbackRepoConcurrentSubmissionsProduceOneState(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := t.Context()

	const writers = 8
	type result struct {
		res FeedbackResult
		err error
	}
	results := make([]result, writers)
	var wg sync.WaitGroup
	for i := range writers {
		rating := "up"
		if i%2 == 1 {
			rating = "down"
		}
		wg.Go(func() {
			res, err := repo.Submit(ctx, submission(FeedbackSubmission{Rating: rating}))
			results[i] = result{res, err}
		})
	}
	wg.Wait()

	stored := onlyFeedbackRow(t, repo)
	var inserted int
	for _, r := range results {
		switch {
		case errors.Is(r.err, ErrFeedbackConflict):
		case r.err != nil:
			t.Fatalf("concurrent submit: %v", r.err)
		case r.res.Outcome == FeedbackInserted:
			inserted++
		case r.res.Outcome != FeedbackIdempotent:
			t.Fatalf("unexpected outcome %q", r.res.Outcome)
		}
		if r.err == nil && r.res.Feedback.Rating != stored.Rating {
			t.Fatalf("accepted rating %q disagrees with stored %q", r.res.Feedback.Rating, stored.Rating)
		}
	}
	if inserted != 1 {
		t.Fatalf("inserted outcomes = %d, want exactly 1", inserted)
	}
}

func TestB10_FeedbackRepoConcurrentDetailsAddedOnce(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := t.Context()
	mustSubmit(t, repo, FeedbackSubmission{Rating: "down"})

	const writers = 8
	outcomes := make([]FeedbackOutcome, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		comment := "same"
		if i%2 == 1 {
			comment = "different"
		}
		wg.Go(func() {
			res, err := repo.Submit(ctx, submission(FeedbackSubmission{Rating: "down", Comment: &comment}))
			outcomes[i], errs[i] = res.Outcome, err
		})
	}
	wg.Wait()

	var added int
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrFeedbackConflict) {
			t.Fatalf("concurrent details submit: %v", err)
		}
		if err == nil && outcomes[i] == FeedbackDetailsAdded {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("details_added outcomes = %d, want exactly 1", added)
	}
}

func TestFeedbackRepo_Submit_DifferentUsersSameTaskAreIndependent(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)

	if res := mustSubmit(t, repo, FeedbackSubmission{UserID: "u1", Rating: "up"}); res.Outcome != FeedbackInserted {
		t.Fatalf("u1 outcome = %q", res.Outcome)
	}
	if res := mustSubmit(t, repo, FeedbackSubmission{UserID: "u2", Rating: "down"}); res.Outcome != FeedbackInserted {
		t.Fatalf("u2 outcome = %q, want inserted: the unique key is per task and user", res.Outcome)
	}
}

func onlyFeedbackRowOrNil(t *testing.T, repo *FeedbackRepo) *Feedback {
	t.Helper()
	rows, err := repo.Search(context.Background(), "*", 0, 0, "", "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return nil
	}
	return onlyFeedbackRow(t, repo)
}

func sameFeedback(a, b *Feedback) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Rating == b.Rating && ptrEq(a.Comment, b.Comment) && a.ShareContent == b.ShareContent &&
		a.UpdatedAt == b.UpdatedAt && ptrEq(a.DetailsSubmittedTime, b.DetailsSubmittedTime)
}

func seedCrossUserFeedback(t *testing.T, db *DB, repo *FeedbackRepo) {
	t.Helper()
	ctx := context.Background()

	rows := []*Feedback{
		{TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u1", SessionID: "00000000-0000-0000-0000-000000000001", Rating: "up", SubjectID: "agent-1", SubjectType: SubjectTypeAgent, EntrypointType: EntrypointTypeWebUI, CreatedAt: 1000, UpdatedAt: 1000},
		{TaskID: "10000000-0000-0000-0000-000000000002", UserID: "u2", SessionID: "00000000-0000-0000-0000-000000000001", Rating: "down", SubjectID: "agent-2", SubjectType: SubjectTypeAgent, EntrypointType: EntrypointTypeWebUI, CreatedAt: 2000, UpdatedAt: 2000},
		{TaskID: "10000000-0000-0000-0000-000000000003", UserID: "u1", SessionID: "00000000-0000-0000-0000-000000000002", Rating: "down", SubjectID: "wf-1", SubjectType: SubjectTypeWorkflow, EntrypointType: EntrypointTypeWebUI, CreatedAt: 3000, UpdatedAt: 3000},
	}
	for _, f := range rows {
		SeedFeedback(t, db, f)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO feedback (id, task_id, user_id, session_id, rating, comment, share_content,
			subject_id, subject_type, entrypoint_type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		idgen.New(), "10000000-0000-0000-0000-000000000004", "u3", "00000000-0000-0000-0000-000000000003",
		"up", nil, 0, SubjectTypeUnknown, SubjectTypeUnknown, EntrypointTypeUnknown, 4000, 4000); err != nil {
		t.Fatalf("seed unknown row: %v", err)
	}
}

func subjectIDsOf(rows []*Feedback) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.SubjectID)
	}
	sort.Strings(out)
	return out
}

func TestFeedbackRepo_SearchCrossUser_Reach(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	all := SubjectScope{All: true}
	none := SubjectScope{}

	tests := []struct {
		name string
		vis  FeedbackVisibility
		want []string
	}{
		{"both wildcards see every row including unknown", FeedbackVisibility{Agents: all, Workflows: all}, []string{"agent-1", "agent-2", "unknown", "wf-1"}},
		{"agent wildcard only sees agents, never workflows or unknown", FeedbackVisibility{Agents: all}, []string{"agent-1", "agent-2"}},
		{"workflow wildcard only sees workflows, never unknown", FeedbackVisibility{Workflows: all}, []string{"wf-1"}},
		{"per-subject agent sees only that agent", FeedbackVisibility{Agents: SubjectScope{IDs: []string{"agent-1"}}}, []string{"agent-1"}},
		{"agent wildcard plus one workflow — the case a flat id list cannot express", FeedbackVisibility{Agents: all, Workflows: SubjectScope{IDs: []string{"wf-1"}}}, []string{"agent-1", "agent-2", "wf-1"}},
		{"no reach at all returns nothing", FeedbackVisibility{Agents: none, Workflows: none}, []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, count, err := repo.SearchCrossUser(context.Background(), tc.vis, CrossUserFeedbackFilter{}, 50, 0)
			if err != nil {
				t.Fatalf("SearchCrossUser: %v", err)
			}
			if got := subjectIDsOf(rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("subjects = %v, want %v", got, tc.want)
			}
			if count != len(tc.want) {
				t.Errorf("count = %d, want %d", count, len(tc.want))
			}
		})
	}
}

func TestFeedbackRepo_SearchCrossUser_FilterCannotWidenReach(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{IDs: []string{"agent-1"}}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{AgentIDs: []string{"agent-2"}}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if len(rows) != 0 || count != 0 {
		t.Errorf("asking for an agent outside the grant returned %d rows (count %d), want 0", len(rows), count)
	}
}

func TestFeedbackRepo_SearchCrossUser_SubjectFiltersOr(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{AgentIDs: []string{"agent-1"}, WorkflowIDs: []string{"wf-1"}}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if got, want := subjectIDsOf(rows), []string{"agent-1", "wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestFeedbackRepo_SearchCrossUser_CountIsTotalNotPageLength(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("page length = %d, want 2", len(rows))
	}
	if count != 4 {
		t.Errorf("count = %d, want 4 (all matching rows)", count)
	}
}

func TestFeedbackRepo_SearchCrossUser_DateFiltersUseCreatedAt(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, _, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{StartDate: 2000, EndDate: 3000}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if got, want := subjectIDsOf(rows), []string{"agent-2", "wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
}

func TestFeedbackRepo_SearchCrossUser_RatingFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{Rating: "down"}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if got, want := subjectIDsOf(rows), []string{"agent-2", "wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestCrossUserWhere_EmptyScopeIsUnreachable(t *testing.T) {
	where, _, err := crossUserWhere(FeedbackVisibility{}, CrossUserFeedbackFilter{})
	if err != nil {
		t.Fatalf("crossUserWhere: %v", err)
	}
	if got, want := strings.Count(where, "1 = 0"), 2; got != want {
		t.Errorf("count of %q in where = %d, want %d: %s", "1 = 0", got, want, where)
	}
	if strings.Contains(where, "subject_type = ?") {
		t.Errorf("where contains %q, want no wildcard or unknown-sentinel branch reachable from an empty scope: %s", "subject_type = ?", where)
	}
}

func TestFeedbackRepo_SearchCrossUser_StableOrderingAcrossPages(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()

	const sharedCreatedAt = int64(5000)
	seeded := make(map[string]bool, 3)
	for i := 0; i < 3; i++ {
		f := &Feedback{
			TaskID: idgen.New(), UserID: "u1", SessionID: idgen.New(), Rating: "up",
			SubjectID: "agent-1", SubjectType: SubjectTypeAgent, EntrypointType: EntrypointTypeWebUI,
			CreatedAt: sharedCreatedAt, UpdatedAt: sharedCreatedAt,
		}
		SeedFeedback(t, db, f)
		seeded[f.ID] = true
	}

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}}
	page1, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser page 1: %v", err)
	}
	page2, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{}, 2, 2)
	if err != nil {
		t.Fatalf("SearchCrossUser page 2: %v", err)
	}

	seen := make(map[string]int, 3)
	for _, f := range append(page1, page2...) {
		seen[f.ID]++
	}
	for id := range seen {
		if !seeded[id] {
			t.Errorf("page union contained unseeded id %s", id)
		}
	}
	for id := range seeded {
		switch seen[id] {
		case 0:
			t.Errorf("seeded id %s missing from both pages", id)
		case 1:
		default:
			t.Errorf("seeded id %s appeared %d times across both pages, want exactly once", id, seen[id])
		}
	}
}

func ptrEq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestFeedbackRepo_Delete_RemovesOnlyThatUsersRow(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)

	mustSubmit(t, repo, FeedbackSubmission{UserID: "u1", Rating: "up"})
	mustSubmit(t, repo, FeedbackSubmission{UserID: "u2", Rating: "down"})

	deleted, err := repo.Delete(context.Background(), fbTestTask, "u1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("delete reported no row removed, want true")
	}
	row := onlyFeedbackRow(t, repo)
	if row.UserID != "u2" || row.Rating != "down" {
		t.Fatalf("surviving row = %+v, want u2's down: a withdrawal must not take another rater's feedback", row)
	}
}

func TestFeedbackRepo_Delete_IsIdempotent(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()
	mustSubmit(t, repo, FeedbackSubmission{UserID: "u1", Rating: "up"})

	if deleted, err := repo.Delete(ctx, fbTestTask, "u1"); err != nil || !deleted {
		t.Fatalf("first delete: deleted=%v err=%v", deleted, err)
	}
	deleted, err := repo.Delete(ctx, fbTestTask, "u1")
	if err != nil {
		t.Fatalf("second delete errored: %v", err)
	}
	if deleted {
		t.Fatal("second delete reported a row removed; a repeated withdrawal or a webhook redelivery must be a no-op")
	}
}

func TestFeedbackRepo_Delete_FreesTheUniqueKeyForAFreshRating(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()
	mustSubmit(t, repo, FeedbackSubmission{UserID: "u1", Rating: "up"})

	if _, err := repo.Delete(ctx, fbTestTask, "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	res := mustSubmit(t, repo, FeedbackSubmission{UserID: "u1", Rating: "down"})
	if res.Outcome != FeedbackInserted {
		t.Fatalf("outcome = %v, want FeedbackInserted: withdrawing must leave the unique key free to rate again", res.Outcome)
	}
	if row := onlyFeedbackRow(t, repo); row.Rating != "down" {
		t.Fatalf("rating = %q, want down", row.Rating)
	}
}

func TestFeedbackRepo_Delete_ErrorOmitsUserID(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	ctx := context.Background()
	const userID = "person@example.com"

	if _, err := db.ExecContext(ctx, `DROP TABLE feedback`); err != nil {
		t.Fatalf("drop feedback table: %v", err)
	}
	_, err := repo.Delete(ctx, fbTestTask, userID)
	if err == nil {
		t.Fatal("delete against a missing table returned no error")
	}
	if strings.Contains(err.Error(), userID) {
		t.Fatalf("error text carries the user id (%q); it is often an email and error text reaches logs", err)
	}
}

func TestFeedbackRepo_SearchCrossUser_UserFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{UserIDs: []string{"u1"}}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if got, want := subjectIDsOf(rows), []string{"agent-1", "wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestFeedbackRepo_SearchCrossUser_UserFilterAndsWithSubject(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis,
		CrossUserFeedbackFilter{AgentIDs: []string{"agent-1"}, UserIDs: []string{"u1"}}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if got, want := subjectIDsOf(rows), []string{"agent-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestFeedbackRepo_SearchCrossUser_UserFilterCannotWidenReach(t *testing.T) {
	db := newTestDB(t)
	repo := NewFeedbackRepo(db)
	seedCrossUserFeedback(t, db, repo)

	vis := FeedbackVisibility{Agents: SubjectScope{IDs: []string{"agent-1"}}}
	rows, count, err := repo.SearchCrossUser(context.Background(), vis, CrossUserFeedbackFilter{UserIDs: []string{"u2"}}, 50, 0)
	if err != nil {
		t.Fatalf("SearchCrossUser: %v", err)
	}
	if len(rows) != 0 || count != 0 {
		t.Errorf("asking for a user outside the grant returned %d rows (count %d), want 0", len(rows), count)
	}
}
