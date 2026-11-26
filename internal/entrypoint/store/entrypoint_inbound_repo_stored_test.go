// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestEntrypointInboundRepo_StoredEventLifecycle(t *testing.T) {
	ctx := t.Context()
	repo := NewEntrypointInboundRepo(TestDB(t))

	inserted, err := repo.Store(ctx, "gw", "evt-1", "task-1", `{"a":1}`)
	if err != nil || !inserted {
		t.Fatalf("Store = (%v, %v), want inserted", inserted, err)
	}
	inserted, err = repo.Store(ctx, "gw", "evt-1", "task-2", `{"a":2}`)
	if err != nil || inserted {
		t.Fatalf("Store of a copy = (%v, %v), want not inserted", inserted, err)
	}
	if _, storeErr := repo.Store(ctx, "other-gw", "evt-1", "task-3", `{}`); storeErr != nil {
		t.Fatal(storeErr)
	}

	rows, err := repo.Stored(ctx, "gw", 10)
	if err != nil || len(rows) != 1 || rows[0].TaskID != "task-1" || rows[0].Payload != `{"a":1}` {
		t.Fatalf("Stored = %+v (err %v), want the first write only", rows, err)
	}

	if markErr := repo.MarkSubmitted(ctx, "gw", "evt-1", "task-other", ""); markErr != nil {
		t.Fatal(markErr)
	}
	if rows, _ := repo.Stored(ctx, "gw", 10); len(rows) != 1 {
		t.Fatal("a mark for another task settled the row")
	}
	if markErr := repo.MarkSubmitted(ctx, "gw", "evt-1", "task-1", ""); markErr != nil {
		t.Fatal(markErr)
	}
	if rows, _ := repo.Stored(ctx, "gw", 10); len(rows) != 0 {
		t.Fatalf("Stored after the mark = %+v, want none", rows)
	}

	if written, setErr := repo.SetOutcome(ctx, "gw", "evt-1", "task-other", InboundOutcomeAck); setErr != nil || written {
		t.Fatalf("SetOutcome for another task = %v, %v; want nothing written", written, setErr)
	}
	if got, _ := repo.Outcomes(ctx, "gw", []string{"evt-1"}); len(got) != 0 {
		t.Fatalf("Outcomes = %v, want none from another task", got)
	}
	if written, setErr := repo.SetOutcome(ctx, "gw", "evt-1", "task-1", InboundOutcomeReject); setErr != nil || !written {
		t.Fatalf("SetOutcome = %v, %v; want written", written, setErr)
	}
	if written, setErr := repo.SetOutcome(ctx, "gw", "evt-1", "task-1", InboundOutcomeAck); setErr != nil || written {
		t.Fatalf("second SetOutcome = %v, %v; want nothing written", written, setErr)
	}
	got, err := repo.Outcomes(ctx, "gw", []string{"evt-1", "evt-unknown"})
	if err != nil || len(got) != 1 || got["evt-1"] != InboundOutcomeReject {
		t.Fatalf("Outcomes = %v (err %v), want the first outcome kept", got, err)
	}
	if got, _ := repo.Outcomes(ctx, "gw", nil); len(got) != 0 {
		t.Fatalf("Outcomes of nothing = %v", got)
	}
}

func TestEntrypointInboundRepo_MarkSubmittedWithOutcome(t *testing.T) {
	ctx := t.Context()
	repo := NewEntrypointInboundRepo(TestDB(t))
	if _, err := repo.Store(ctx, "gw", "evt-1", "task-1", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSubmitted(ctx, "gw", "evt-1", "task-1", InboundOutcomeReject); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Outcomes(ctx, "gw", []string{"evt-1"})
	if err != nil || got["evt-1"] != InboundOutcomeReject {
		t.Fatalf("Outcomes = %v (err %v), want reject", got, err)
	}
}

func TestEntrypointInboundRepo_StoredEventsAreNotTakenAsLapsed(t *testing.T) {
	ctx := t.Context()
	repo := NewEntrypointInboundRepo(TestDB(t))
	if _, err := repo.Store(ctx, "gw", "evt-1", "task-1", `{}`); err != nil {
		t.Fatal(err)
	}
	lapsed, err := repo.TakeLapsed(ctx, "gw", 0, 10)
	if err != nil || len(lapsed) != 0 {
		t.Fatalf("TakeLapsed = %+v (err %v), want none", lapsed, err)
	}
}

func TestStoredEventRepos_ClosedDatabase_ReturnErrors(t *testing.T) {
	ctx := t.Context()
	db := TestDB(t)
	inbound := NewEntrypointInboundRepo(db)
	links := NewMCPArtifactLinkRepo(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := inbound.Store(ctx, "gw", "evt-1", "task-1", "{}"); err == nil {
		t.Error("Store succeeded on a closed database")
	}
	if _, err := inbound.Stored(ctx, "gw", 10); err == nil {
		t.Error("Stored succeeded on a closed database")
	}
	if err := inbound.MarkSubmitted(ctx, "gw", "evt-1", "task-1", ""); err == nil {
		t.Error("MarkSubmitted succeeded on a closed database")
	}
	if _, err := inbound.SetOutcome(ctx, "gw", "evt-1", "task-1", InboundOutcomeAck); err == nil {
		t.Error("SetOutcome succeeded on a closed database")
	}
	if _, err := inbound.Outcomes(ctx, "gw", []string{"evt-1"}); err == nil {
		t.Error("Outcomes succeeded on a closed database")
	}
	if err := links.Put(ctx, MCPArtifactLink{MCPSessionID: "s", URI: "artifact://s/f"}); err == nil {
		t.Error("link Put succeeded on a closed database")
	}
	if _, err := links.ListBySession(ctx, "s"); err == nil {
		t.Error("link ListBySession succeeded on a closed database")
	}
	if _, err := links.DeleteOlderThan(ctx, time.Now(), 10); err == nil {
		t.Error("link DeleteOlderThan succeeded on a closed database")
	}
}

func TestEntrypointInboundRepo_Outcomes_TooManyIDs(t *testing.T) {
	ids := make([]string, dbutil.InClauseCap+1)
	if _, err := NewEntrypointInboundRepo(TestDB(t)).Outcomes(t.Context(), "gw", ids); err == nil {
		t.Error("Outcomes accepted more ids than one query takes")
	}
}
