// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

const (
	ledgerActor   = "builder-actor@example.com"
	ledgerSession = "0197b2c4-0000-7000-8000-000000000001"
	ledgerAgentA  = "0197b2c4-0000-7000-8000-0000000000a1"
	ledgerAgentB  = "0197b2c4-0000-7000-8000-0000000000b1"
)

func ledgerRow(kind, id string, created bool) BuilderSessionResource {
	return BuilderSessionResource{Actor: ledgerActor, SessionID: ledgerSession, Kind: kind, ResourceID: id, CreatedBySession: created}
}

func TestBuilderSessionResourceRepo_CreatedIsNeverClearedByAnUpdate(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewBuilderSessionResourceRepo(db)

	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentA, true)}); err != nil {
		t.Fatalf("record create: %v", err)
	}
	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentA, false)}); err != nil {
		t.Fatalf("record update: %v", err)
	}

	rows, err := repo.ListForSession(ctx, ledgerActor, ledgerSession)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || !rows[0].CreatedBySession {
		t.Fatalf("rows = %+v, want one row still created by the session", rows)
	}
}

func TestBuilderSessionResourceRepo_UpdatedOnlyIsNotCreated(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewBuilderSessionResourceRepo(db)

	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentB, false)}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentB, false)}); err != nil {
		t.Fatalf("record again: %v", err)
	}

	rows, err := repo.ListForSession(ctx, ledgerActor, ledgerSession)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].CreatedBySession {
		t.Fatalf("rows = %+v, want one row not created by the session", rows)
	}
}

func TestBuilderSessionResourceRepo_ListIsScopedToOwnerAndSession(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewBuilderSessionResourceRepo(db)

	other := ledgerRow("agent", ledgerAgentB, true)
	other.Actor = "someone-else@example.com"
	otherSession := ledgerRow("workflow", ledgerAgentB, true)
	otherSession.SessionID = "0197b2c4-0000-7000-8000-000000000002"
	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentA, true), other, otherSession}); err != nil {
		t.Fatalf("record: %v", err)
	}

	rows, err := repo.ListForSession(ctx, ledgerActor, ledgerSession)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ResourceID != ledgerAgentA {
		t.Fatalf("rows = %+v, want only the caller's record for this session", rows)
	}
}

func TestBuilderSessionResourceRepo_ForgetDropsEveryRecordOfAResource(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewBuilderSessionResourceRepo(db)

	other := ledgerRow("agent", ledgerAgentA, false)
	other.Actor = "someone-else@example.com"
	if err := repo.Record(ctx, []BuilderSessionResource{ledgerRow("agent", ledgerAgentA, true), other, ledgerRow("agent", ledgerAgentB, true)}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := repo.Forget(ctx, "agent", ledgerAgentA); err != nil {
		t.Fatalf("forget: %v", err)
	}

	for _, actor := range []string{ledgerActor, "someone-else@example.com"} {
		rows, err := repo.ListForSession(ctx, actor, ledgerSession)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, row := range rows {
			if row.ResourceID == ledgerAgentA {
				t.Fatalf("%s still has a record of the forgotten resource: %+v", actor, rows)
			}
		}
	}
	rows, _ := repo.ListForSession(ctx, ledgerActor, ledgerSession)
	if len(rows) != 1 || rows[0].ResourceID != ledgerAgentB {
		t.Fatalf("rows = %+v, want the other resource kept", rows)
	}
}
