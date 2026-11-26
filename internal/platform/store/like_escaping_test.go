// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func TestEvalDatasetRepo_NameFilterEscapesMetacharacters(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalDatasetRepo(db)
	ctx := t.Context()

	newTestDataset(ctx, t, repo, "Plain Dataset")
	literal := newTestDataset(ctx, t, repo, "50% off_dataset")

	for _, needle := range []string{"50%", "off_dataset", "%", "_"} {
		page, total, err := repo.ListPaginated(ctx, 1, 10, needle, EvalDatasetSortName, "asc")
		if err != nil {
			t.Errorf("filter %q: %v", needle, err)
			continue
		}
		if total != 1 || len(page) != 1 {
			t.Errorf("filter %q matched %d rows (total %d), want exactly the literal row", needle, len(page), total)
			continue
		}
		if page[0].ID != literal {
			t.Errorf("filter %q matched %s, want %s", needle, page[0].ID, literal)
		}
	}
}

func TestEvalExampleRepo_ContentFilterEscapesMetacharacters(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	exRepo := NewEvalExampleRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DS-escaping")
	resp := "plain response"
	plain := &EvalExample{DatasetID: dID, SequenceNumber: 1, Prompt: "plain prompt", ExpectedResponse: &resp}
	if err := exRepo.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}
	litResp := "gives 90% accuracy"
	literal := &EvalExample{DatasetID: dID, SequenceNumber: 2, Prompt: "explain snake_case", ExpectedResponse: &litResp}
	if err := exRepo.Create(ctx, literal); err != nil {
		t.Fatal(err)
	}

	for _, needle := range []string{"snake_case", "90%", "%", "_"} {
		page, total, err := exRepo.ListByDatasetPaginated(ctx, dID, 1, 10, needle, "", false)
		if err != nil {
			t.Errorf("filter %q: %v", needle, err)
			continue
		}
		if total != 1 || len(page) != 1 {
			t.Errorf("filter %q matched %d rows (total %d), want exactly the literal row", needle, len(page), total)
			continue
		}
		if page[0].ID != literal.ID {
			t.Errorf("filter %q matched %s, want %s", needle, page[0].ID, literal.ID)
		}
	}
}

func TestEvalRunRepo_NameFilterEscapesMetacharacters(t *testing.T) {
	db := TestDB(t)
	dRepo := NewEvalDatasetRepo(db)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	dID := newTestDataset(ctx, t, dRepo, "DS-run-escaping")
	exp := &EvalExperiment{Name: "Exp-run-escaping", DatasetID: &dID, TargetAgent: "agent-esc", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatal(err)
	}

	plainName := "Plain Run"
	literalName := "50% run_two"
	const literalGroup = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	for _, gr := range []struct {
		group string
		name  *string
	}{
		{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", &plainName},
		{literalGroup, &literalName},
	} {
		if err := runRepo.Create(ctx, &EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u", RunGroupID: gr.group, RunName: gr.name}); err != nil {
			t.Fatal(err)
		}
	}

	for _, needle := range []string{"50%", "run_two", "%", "_"} {
		groups, total, err := runRepo.ListRunGroupsPaginated(ctx, 1, 10, RunGroupFilter{Name: needle})
		if err != nil {
			t.Errorf("filter %q: %v", needle, err)
			continue
		}
		if total != 1 || len(groups) != 1 {
			t.Errorf("filter %q matched %d groups (total %d), want exactly the literal one", needle, len(groups), total)
			continue
		}
		if groups[0].RunGroupID != literalGroup {
			t.Errorf("filter %q matched %s, want %s", needle, groups[0].RunGroupID, literalGroup)
		}
	}
}

func TestUsersRepo_SearchEscapesMetacharacters(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	seed := []struct{ sub, name, email string }{
		{"plain", "Plain User", "plain@example.com"},
		{"literal", "100% Sure", "snake_case@example.com"},
	}
	for _, u := range seed {
		if err := repo.UpsertUser(ctx, &User{Issuer: "iss", Sub: u.sub, OIDCProvider: "p", DisplayName: new(u.name), Email: new(u.email), CreatedBy: "x", UpdatedBy: "x"}, nil); err != nil {
			t.Fatalf("seed %s: %v", u.sub, err)
		}
	}

	for _, needle := range []string{"100%", "snake_case", "%", "_"} {
		users, total, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, Search: needle})
		if err != nil {
			t.Errorf("search %q: %v", needle, err)
			continue
		}
		if total != 1 || len(users) != 1 {
			t.Errorf("search %q matched %d users (total %d), want exactly the literal one", needle, len(users), total)
			continue
		}
		if users[0].Sub != "literal" {
			t.Errorf("search %q matched %s, want the literal user", needle, users[0].Sub)
		}
	}
}
