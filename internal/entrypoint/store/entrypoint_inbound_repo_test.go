// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

const inboundLease = time.Hour

func inboundState(t *testing.T, db *DB, gatewayID, messageID string) (state, payload string) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(),
		`SELECT state, payload FROM entrypoint_inbound_messages WHERE gateway_id = ? AND message_id = ?`,
		gatewayID, messageID).Scan(&state, &payload); err != nil {
		t.Fatalf("read inbound row: %v", err)
	}
	return state, payload
}

func TestEntrypointInboundRepo_ClaimLifecycle(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()

	first, err := repo.Claim(ctx, "slack", "C1:1.1", `{"text":"hi"}`, inboundLease)
	if err != nil || first.Outcome != ClaimAcquired || first.Attempts != 0 {
		t.Fatalf("first claim = %+v, %v; want acquired", first, err)
	}
	if again, err := repo.Claim(ctx, "slack", "C1:1.1", `{"text":"hi"}`, inboundLease); err != nil || again.Outcome != ClaimInFlight {
		t.Fatalf("claim while held = %+v, %v; want in flight", again, err)
	}
	if other, err := repo.Claim(ctx, "teams", "C1:1.1", "", inboundLease); err != nil || other.Outcome != ClaimAcquired {
		t.Fatalf("same id on another entrypoint = %+v, %v; want acquired", other, err)
	}

	if ok, err := repo.MarkDone(ctx, "slack", "C1:1.1", first.Token); err != nil || !ok {
		t.Fatalf("MarkDone = %v, %v", ok, err)
	}
	if state, payload := inboundState(t, db, "slack", "C1:1.1"); state != InboundDone || payload != "" {
		t.Fatalf("row after done = %q, %q; want done with no payload", state, payload)
	}
	if dup, err := repo.Claim(ctx, "slack", "C1:1.1", `{"text":"hi"}`, 0); err != nil || dup.Outcome != ClaimDuplicate {
		t.Fatalf("claim after done = %+v, %v; want duplicate", dup, err)
	}
}

func TestEntrypointInboundRepo_LapsedClaimTakenOver(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()

	first, err := repo.Claim(ctx, "slack", "C1:2.2", "p1", inboundLease)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Claim(ctx, "slack", "C1:2.2", "p2", 0)
	if err != nil || second.Outcome != ClaimAcquired || second.Attempts != 1 || second.Token <= first.Token {
		t.Fatalf("takeover = %+v, %v; want acquired with one attempt and a greater token", second, err)
	}
	if _, payload := inboundState(t, db, "slack", "C1:2.2"); payload != "p1" {
		t.Fatalf("payload after takeover = %q, want the stored one", payload)
	}
	if ok, err := repo.MarkDone(ctx, "slack", "C1:2.2", first.Token); err != nil || ok {
		t.Fatalf("MarkDone with the lost token = %v, %v; want false", ok, err)
	}
	if ok, err := repo.MarkDone(ctx, "slack", "C1:2.2", second.Token); err != nil || !ok {
		t.Fatalf("MarkDone with the current token = %v, %v; want true", ok, err)
	}
}

func TestEntrypointInboundRepo_ClaimHasOneWinner(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			c, err := repo.Claim(t.Context(), "slack", "C1:3.3", "p", inboundLease)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if c.Outcome == ClaimAcquired {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claim winners = %d, want 1", wins.Load())
	}
}

func TestEntrypointInboundRepo_TakeLapsed(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()

	done, _ := repo.Claim(ctx, "slack", "C1:done", "d", inboundLease)
	if _, err := repo.MarkDone(ctx, "slack", "C1:done", done.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, "slack", "C1:lost", "lost-payload", inboundLease); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, "teams", "A1", "teams-payload", inboundLease); err != nil {
		t.Fatal(err)
	}

	if held, err := repo.TakeLapsed(ctx, "slack", inboundLease, 10); err != nil || len(held) != 0 {
		t.Fatalf("TakeLapsed inside the lease = %+v, %v; want none", held, err)
	}
	taken, err := repo.TakeLapsed(ctx, "slack", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(taken) != 1 || taken[0].MessageID != "C1:lost" || taken[0].Payload != "lost-payload" || taken[0].Attempts != 1 {
		t.Fatalf("TakeLapsed = %+v; want the one lapsed slack message", taken)
	}
	if ok, err := repo.MarkDone(ctx, "slack", "C1:lost", taken[0].Token); err != nil || !ok {
		t.Fatalf("MarkDone after TakeLapsed = %v, %v", ok, err)
	}
	if again, err := repo.TakeLapsed(ctx, "slack", 0, 10); err != nil || len(again) != 0 {
		t.Fatalf("TakeLapsed after done = %+v, %v; want none", again, err)
	}
}

func TestEntrypointInboundRepo_TakeLapsedHasOneWinner(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES ('slack', 'C1:4.4', 'processing', 'p', 1, 0, 1)`); err != nil {
		t.Fatal(err)
	}

	var taken atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			got, err := repo.TakeLapsed(t.Context(), "slack", time.Minute, 10)
			if err != nil {
				t.Errorf("TakeLapsed: %v", err)
				return
			}
			taken.Add(int32(len(got)))
		})
	}
	wg.Wait()
	if taken.Load() != 1 {
		t.Fatalf("messages taken = %d, want 1", taken.Load())
	}
}

func TestEntrypointInboundRepo_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES ('slack', 'old-1', 'done', '', 1, 0, 1), ('teams', 'old-2', 'processing', 'p', 2, 3, 2),
		        ('slack', 'retaken', 'processing', 'p', 99999999999999, 5, 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, "slack", "new", "p", inboundLease); err != nil {
		t.Fatal(err)
	}

	n, err := repo.DeleteOlderThan(ctx, time.UnixMilli(10), 1)
	if err != nil || n != 3 {
		t.Fatalf("DeleteOlderThan = %d, %v; want 3, including the row retaken recently", n, err)
	}
	if state, _ := inboundState(t, db, "slack", "new"); state != InboundProcessing {
		t.Fatalf("recent row state = %q, want kept", state)
	}
}

const entrypointInboundMigration = "entrypoint_inbound_messages"

func entrypointInboundMigrationBody(t *testing.T, db *DB, version int64) {
	t.Helper()
	ctx := t.Context()
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	if _, err := NewEntrypointInboundRepo(db).Claim(ctx, "slack", "C1:1.1", "p", inboundLease); err != nil {
		t.Fatalf("write after the migration: %v", err)
	}
	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM entrypoint_inbound_messages`); err == nil {
		t.Fatal("entrypoint_inbound_messages survived the down migration")
	}
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

func TestEntrypointInboundMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "entrypoint_inbound.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	version := migrationVersion(t, sqliteMigrations(), entrypointInboundMigration)
	if err := db.MigrateUpTo(t.Context(), version-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	entrypointInboundMigrationBody(t, db, version)
}

func TestEntrypointInboundMigration_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	version := migrationVersion(t, postgresMigrations(), entrypointInboundMigration)
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_entrypoint_inbound", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(t.Context(), version-1)
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)
	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entrypointInboundMigrationBody(t, db, version)
}

func TestEntrypointInboundRepo_ClosedDatabase(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()
	if _, err := repo.Claim(ctx, "slack", "m", "p", inboundLease); err == nil {
		t.Error("Claim on a closed database")
	}
	if _, err := repo.MarkDone(ctx, "slack", "m", 1); err == nil {
		t.Error("MarkDone on a closed database")
	}
	if _, err := repo.TakeLapsed(ctx, "slack", 0, 10); err == nil {
		t.Error("TakeLapsed on a closed database")
	}
	if _, err := repo.DeleteOlderThan(ctx, time.Now(), 10); err == nil {
		t.Error("DeleteOlderThan on a closed database")
	}
	if _, _, err := repo.Renew(ctx, "slack", "m", 1); err == nil {
		t.Error("Renew on a closed database")
	}
	if _, err := repo.Record(ctx, "email", "m"); err == nil {
		t.Error("Record on a closed database")
	}
	if _, err := repo.Recorded(ctx, "email", "m"); err == nil {
		t.Error("Recorded on a closed database")
	}
	if err := repo.Forget(ctx, "email", "m"); err == nil {
		t.Error("Forget on a closed database")
	}
}

func TestEntrypointInboundRepo_Renew(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()
	claim, err := repo.Claim(ctx, "slack", "C1:5.5", "p", inboundLease)
	if err != nil {
		t.Fatal(err)
	}
	lapsed := time.Now().Add(-inboundLease - time.Minute).UnixMilli()
	if _, err = db.ExecContext(ctx, `UPDATE entrypoint_inbound_messages SET claimed_at = ?`, lapsed); err != nil {
		t.Fatal(err)
	}
	if _, held, renewErr := repo.Renew(ctx, "slack", "C1:5.5", claim.Token); renewErr != nil || held {
		t.Fatalf("Renew with a token the row no longer has = %v, %v; want false", held, renewErr)
	}
	renewed, held, err := repo.Renew(ctx, "slack", "C1:5.5", lapsed)
	if err != nil || !held || renewed <= lapsed {
		t.Fatalf("Renew with the lapsed claim's token = %d, %v, %v; want a later token", renewed, held, err)
	}
	if taken, takeErr := repo.TakeLapsed(ctx, "slack", inboundLease, 10); takeErr != nil || len(taken) != 0 {
		t.Fatalf("TakeLapsed after the renew = %+v, %v; want nothing", taken, takeErr)
	}
	if done, doneErr := repo.MarkDone(ctx, "slack", "C1:5.5", lapsed); doneErr != nil || done {
		t.Fatalf("MarkDone with the token before the renew = %v, %v; want false", done, doneErr)
	}
	if done, doneErr := repo.MarkDone(ctx, "slack", "C1:5.5", renewed); doneErr != nil || !done {
		t.Fatalf("MarkDone with the renewed token = %v, %v; want true", done, doneErr)
	}
	if _, held, renewErr := repo.Renew(ctx, "slack", "C1:5.5", renewed); renewErr != nil || held {
		t.Fatalf("Renew after done = %v, %v; want false", held, renewErr)
	}
}

func TestEntrypointInboundRepo_Record(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)
	ctx := t.Context()

	if seen, err := repo.Recorded(ctx, "email", "<m1@x>"); err != nil || seen {
		t.Fatalf("Recorded before = %v, %v; want false", seen, err)
	}
	if wrote, err := repo.Record(ctx, "email", "<m1@x>"); err != nil || !wrote {
		t.Fatalf("first Record = %v, %v; want true", wrote, err)
	}
	if wrote, err := repo.Record(ctx, "email", "<m1@x>"); err != nil || wrote {
		t.Fatalf("second Record = %v, %v; want false", wrote, err)
	}
	if seen, err := repo.Recorded(ctx, "email", "<m1@x>"); err != nil || !seen {
		t.Fatalf("Recorded after = %v, %v; want true", seen, err)
	}
	if state, payload := inboundState(t, db, "email", "<m1@x>"); state != InboundDone || payload != "" {
		t.Fatalf("row = %q, %q; want done with no payload", state, payload)
	}
	if seen, err := repo.Recorded(ctx, "other", "<m1@x>"); err != nil || seen {
		t.Fatalf("Recorded on another entrypoint = %v, %v; want false", seen, err)
	}

	if err := repo.Forget(ctx, "email", "<m1@x>"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if err := repo.Forget(ctx, "email", "<m1@x>"); err != nil {
		t.Fatalf("Forget of an absent row: %v", err)
	}
	if wrote, err := repo.Record(ctx, "email", "<m1@x>"); err != nil || !wrote {
		t.Fatalf("Record after Forget = %v, %v; want true", wrote, err)
	}

	if _, err := repo.Claim(ctx, "slack", "C1:9.9", "p", inboundLease); err != nil {
		t.Fatal(err)
	}
	if wrote, err := repo.Record(ctx, "slack", "C1:9.9"); err != nil || wrote {
		t.Fatalf("Record over a claimed row = %v, %v; want false", wrote, err)
	}
	if state, _ := inboundState(t, db, "slack", "C1:9.9"); state != InboundProcessing {
		t.Fatalf("claimed row state = %q, want it untouched", state)
	}
}

func TestEntrypointInboundRepo_RecordHasOneWinner(t *testing.T) {
	db := newTestDB(t)
	repo := NewEntrypointInboundRepo(db)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			wrote, err := repo.Record(t.Context(), "email", "<race@x>")
			if err != nil {
				t.Errorf("Record: %v", err)
				return
			}
			if wrote {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("record winners = %d, want 1", wins.Load())
	}
}
