// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestService_PerTaskConsent_Unredacts(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	fbRepo := store.NewFeedbackRepo(db)
	ctx := context.Background()
	svc := NewService(db)

	const base = int64(1_700_000_000_000)
	mkErr := func(id, user, prompt string) {
		req := prompt
		task := &store.Task{
			ID: id, UserID: user, StartTime: base, EndTime: i64p(base + 100),
			Status: strptr("error"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			InitialRequestText: &req,
			TotalInputTokens:   ip(0), TotalOutputTokens: ip(0), TotalCachedInputTokens: ip(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	granted := "c0000000-0000-0000-0000-000000000001"
	private := "c0000000-0000-0000-0000-000000000002"
	mkErr(granted, "alice", "alice's shared prompt")
	mkErr(private, "bob", "bob's private prompt")

	store.SeedFeedback(t, db, &store.Feedback{SessionID: "11111111-1111-1111-1111-111111111111",
		TaskID: granted, UserID: "alice", Rating: "down", ShareContent: true, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "22222222-2222-2222-2222-222222222222",
		TaskID: private, UserID: "bob", Rating: "down", ShareContent: false, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})

	cs, err := fbRepo.ConsentedTaskIDs(ctx, []string{granted, private})
	if err != nil {
		t.Fatal(err)
	}
	if !cs[granted] || cs[private] {
		t.Fatalf("ConsentedTaskIDs = %v, want only %s", cs, granted)
	}

	res, err := svc.FailedTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: false})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]TaskHealthRow{}
	for _, r := range res.Data {
		byID[r.ID] = r
	}
	if g := byID[granted]; g.UserID != "alice" || g.InitialRequestText != "alice's shared prompt" {
		t.Errorf("granted task should be unredacted, got userId=%q prompt=%q", g.UserID, g.InitialRequestText)
	}
	if p := byID[private]; p.UserID != "" || p.InitialRequestText != "" {
		t.Errorf("non-consented task must stay redacted, got userId=%q prompt=%q", p.UserID, p.InitialRequestText)
	}

	res2, err := svc.FailedTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res2.Data {
		if r.UserID == "" || r.InitialRequestText == "" {
			t.Errorf("content tier should show all content, got %+v", r)
		}
	}
}

func TestService_PerTaskConsent_FeedbackComment(t *testing.T) {
	db := store.TestDB(t)
	ctx := context.Background()
	svc := NewService(db)

	shared, priv := "shared comment", "private comment"
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "11111111-1111-1111-1111-111111111111",
		TaskID: "d0000000-0000-0000-0000-000000000001", UserID: "alice", Rating: "up", Comment: &shared, ShareContent: true, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI,
		CreatedAt: 1_700_000_000_000, UpdatedAt: 1_700_000_600_000})
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "22222222-2222-2222-2222-222222222222",
		TaskID: "d0000000-0000-0000-0000-000000000002", UserID: "bob", Rating: "up", Comment: &priv, ShareContent: false, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})

	res, err := svc.FeedbackSummary(ctx, Query{IncludeContent: false})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	gotUpdated := map[string]string{}
	for _, c := range res.RecentComments {
		got[c.TaskID] = c.Comment
		gotUpdated[c.TaskID] = c.UpdatedAt
	}
	if got["d0000000-0000-0000-0000-000000000001"] != "shared comment" {
		t.Errorf("consented comment should be shown, got %q", got["d0000000-0000-0000-0000-000000000001"])
	}
	if got["d0000000-0000-0000-0000-000000000002"] != "" {
		t.Errorf("non-consented comment must be redacted, got %q", got["d0000000-0000-0000-0000-000000000002"])
	}
	if want := "2023-11-14T22:23:20.000000+00:00"; gotUpdated["d0000000-0000-0000-0000-000000000001"] != want {
		t.Errorf("updatedAt = %q, want the LAST-write time %q (not first capture)", gotUpdated["d0000000-0000-0000-0000-000000000001"], want)
	}
}

func TestService_PerTaskConsent_CrossUserDenied(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	fbRepo := store.NewFeedbackRepo(db)
	ctx := context.Background()
	svc := NewService(db)

	const base = int64(1_700_000_000_000)
	victim := "e0000000-0000-0000-0000-000000000001"
	req := "alice's private prompt"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: victim, UserID: "alice", StartTime: base, EndTime: i64p(base + 100),
		Status: strptr("error"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		InitialRequestText: &req, TotalInputTokens: ip(0), TotalOutputTokens: ip(0), TotalCachedInputTokens: ip(0),
	}); err != nil {
		t.Fatal(err)
	}
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "11111111-1111-1111-1111-111111111111",
		TaskID: victim, UserID: "mallory", Rating: "up", ShareContent: true, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})

	cs, err := fbRepo.ConsentedTaskIDs(ctx, []string{victim})
	if err != nil {
		t.Fatal(err)
	}
	if cs[victim] {
		t.Fatal("cross-user consent must NOT grant access to the victim's task")
	}
	res, err := svc.FailedTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Data {
		if r.ID == victim && (r.UserID != "" || r.InitialRequestText != "") {
			t.Errorf("victim task leaked via cross-user consent: userId=%q prompt=%q", r.UserID, r.InitialRequestText)
		}
	}
}

func TestService_PerTaskConsent_GranularQueryTasks(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	ctx := context.Background()
	svc := NewService(db)

	const base = int64(1_700_000_000_000)
	mk := func(id, user string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: user, StartTime: base, EndTime: i64p(base + 100),
			Status: strptr("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			TotalInputTokens: ip(0), TotalOutputTokens: ip(0), TotalCachedInputTokens: ip(0),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	granted := "f0000000-0000-0000-0000-000000000001"
	other := "f0000000-0000-0000-0000-000000000002"
	mk(granted, "alice")
	mk(other, "bob")
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "11111111-1111-1111-1111-111111111111",
		TaskID: granted, UserID: "alice", Rating: "up", ShareContent: true, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})

	res, err := svc.QueryTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: false})
	if err != nil {
		t.Fatal(err)
	}
	page := res.(*Paginated[TaskQueryRow])
	got := map[string]string{}
	for _, r := range page.Data {
		got[r.ID] = r.UserID
	}
	if got[granted] != "alice" {
		t.Errorf("granted task userId = %q, want alice (consent unredacts)", got[granted])
	}
	if got[other] != "" {
		t.Errorf("non-granted task userId = %q, want redacted", got[other])
	}
}

func TestService_PerTaskConsent_DoesNotEnableUserQuery(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	ctx := context.Background()
	svc := NewService(db)

	const base = int64(1_700_000_000_000)
	id := "a1000000-0000-0000-0000-000000000001"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: id, UserID: "alice", StartTime: base, EndTime: i64p(base + 100),
		Status: strptr("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		TotalInputTokens: ip(0), TotalOutputTokens: ip(0), TotalCachedInputTokens: ip(0),
	}); err != nil {
		t.Fatal(err)
	}
	store.SeedFeedback(t, db, &store.Feedback{SessionID: "11111111-1111-1111-1111-111111111111",
		TaskID: id, UserID: "alice", Rating: "up", ShareContent: true, SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI})

	if _, err := svc.QueryTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: false, GroupDims: []string{"user"}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("group-by-user with a consented task present: err = %v, want ErrForbidden", err)
	}
	if _, err := svc.QueryTasks(ctx, Query{PageNumber: 1, PageSize: 100, IncludeContent: false, UserID: "alice"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("filter-by-user with a consented task present: err = %v, want ErrForbidden", err)
	}
}
