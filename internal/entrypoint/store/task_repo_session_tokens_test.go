// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestListBySessionForUserWithTokens(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	other := idgen.New()
	user := "user-a"
	otherUser := "user-b"

	in1, in2 := 100, 250
	now := time.Now().UnixMilli()

	wantNewer := idgen.New()
	if err := repo.Create(ctx, &Task{
		ID: wantNewer, UserID: user, SessionID: &sid,
		StartTime: now, ExecutionMode: "foreground",
		TotalInputTokens: &in2,
	}); err != nil {
		t.Fatal(err)
	}
	wantOlder := idgen.New()
	if err := repo.Create(ctx, &Task{
		ID: wantOlder, UserID: user, SessionID: &sid,
		StartTime: now - 1000, ExecutionMode: "foreground",
		TotalInputTokens: &in1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: idgen.New(), UserID: user, SessionID: &sid,
		StartTime: now, ExecutionMode: "foreground",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: idgen.New(), UserID: otherUser, SessionID: &sid,
		StartTime: now, ExecutionMode: "foreground",
		TotalInputTokens: &in1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{
		ID: idgen.New(), UserID: user, SessionID: &other,
		StartTime: now, ExecutionMode: "foreground",
		TotalInputTokens: &in1,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListBySessionForUserWithTokens(ctx, sid, user)
	if err != nil {
		t.Fatalf("ListBySessionForUserWithTokens: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2", len(got))
	}
	if got[0].ID != wantNewer {
		t.Errorf("got[0] = %s, want %s (newest first)", got[0].ID, wantNewer)
	}
	if got[1].ID != wantOlder {
		t.Errorf("got[1] = %s, want %s", got[1].ID, wantOlder)
	}
}

func TestListSessionRootsUpTo_ExcludesLaterAndChildren(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	sid, otherSid := idgen.New(), idgen.New()
	handler := "Orchestrator"
	create := func(start int64, session string, parent, handlerName *string, kind string) string {
		id := idgen.New()
		s := session
		if err := repo.Create(ctx, &Task{
			ID: id, UserID: "u", SessionID: &s, ParentTaskID: parent,
			StartTime: start, ExecutionMode: "foreground", Kind: kind, HandlerName: handlerName,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk := func(start int64, session string, parent *string, kind string) string {
		return create(start, session, parent, &handler, kind)
	}

	first := mk(1000, sid, nil, TaskKindNormal)
	second := mk(2000, sid, nil, TaskKindNormal)
	rated := mk(3000, sid, nil, TaskKindNormal)
	mk(4000, sid, nil, TaskKindNormal)
	mk(2500, sid, &first, TaskKindNormal)
	mk(2600, otherSid, nil, TaskKindNormal)
	mk(2700, sid, nil, TaskKindCompactionCost)
	create(2800, sid, nil, nil, TaskKindNormal)
	create(2900, sid, nil, new(string), TaskKindNormal)

	got, err := repo.ListSessionRootsUpTo(ctx, sid, rated, 3000, 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, g := range got {
		ids = append(ids, g.ID)
	}
	want := []string{rated, second, first}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v (newest first)", ids, want)
		}
	}

	ratedPlaceholder := create(3500, sid, nil, nil, TaskKindNormal)
	withPlaceholder, err := repo.ListSessionRootsUpTo(ctx, sid, ratedPlaceholder, 3500, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(withPlaceholder) != 4 || withPlaceholder[0].ID != ratedPlaceholder {
		t.Errorf("rated placeholder: got %d roots, newest %v; want it kept as the newest of 4", len(withPlaceholder), withPlaceholder)
	}

	limited, err := repo.ListSessionRootsUpTo(ctx, sid, rated, 3000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].ID != rated || limited[1].ID != second {
		t.Errorf("limited = %v, want the two newest", limited)
	}
}
