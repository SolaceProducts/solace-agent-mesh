// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func newClockedSessionTestCore(t *testing.T) (*NotifyCore, *auth.Subject, *fakeClock) {
	t.Helper()
	core, session := newSessionTestCore(t)
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	core.adoptions.clock = clock.now
	return core, session, clock
}

func seedLoginRow(t *testing.T, core *NotifyCore, title string) {
	t.Helper()
	if _, err := core.inbox.Add(t.Context(), benLogin, title, "b", nil, "test", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func rowsUnderLogin(t *testing.T, core *NotifyCore) int {
	t.Helper()
	n, err := core.inbox.CountByUser(t.Context(), benLogin, store.InboxFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func aliasOf(t *testing.T, core *NotifyCore, login string) string {
	t.Helper()
	got, err := core.db.PlatformUserIDFor(t.Context(), login)
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	return got
}

func TestNotifyCore_AdoptionIsNotRepeatedUntilTheRecheck(t *testing.T) {
	core, _, clock := newClockedSessionTestCore(t)
	listInbox(t, core)
	seedLoginRow(t, core, "late")

	clock.t = clock.t.Add(5*time.Minute - time.Second)
	listInbox(t, core)
	if n := rowsUnderLogin(t, core); n != 1 {
		t.Fatalf("rows under the login id inside the recheck = %d, want 1 left in place", n)
	}

	clock.t = clock.t.Add(2 * time.Second)
	if got := listInbox(t, core); len(got) != 1 || got[0].Title != "late" {
		t.Fatalf("inbox after the recheck = %+v, want the late row moved in", got)
	}
}

func TestNotifyCore_FailedAdoptionIsRetriedAfterTheRetryInterval(t *testing.T) {
	core, _, clock := newClockedSessionTestCore(t)
	ctx := t.Context()
	var ddl string
	if err := core.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'notify_user_aliases'`).Scan(&ddl); err != nil {
		t.Fatalf("read alias table: %v", err)
	}
	if _, err := core.db.ExecContext(ctx, `DROP TABLE notify_user_aliases`); err != nil {
		t.Fatalf("break alias store: %v", err)
	}
	listInbox(t, core)
	if _, err := core.db.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("restore alias store: %v", err)
	}
	seedLoginRow(t, core, "pending")

	clock.t = clock.t.Add(time.Minute - time.Second)
	listInbox(t, core)
	if got := aliasOf(t, core, benLogin); got != "" {
		t.Fatalf("alias inside the retry interval = %q, want none: a failing store is retried at most once a minute", got)
	}

	clock.t = clock.t.Add(2 * time.Second)
	if got := listInbox(t, core); len(got) != 1 || got[0].Title != "pending" {
		t.Fatalf("inbox after the retry interval = %+v, want the pending row moved in", got)
	}
	if got := aliasOf(t, core, benLogin); got != benKey {
		t.Fatalf("alias after the retry interval = %q, want %q", got, benKey)
	}
}

func TestNotifyCore_ALoginSeenWithANewPlatformIDIsReadoptedAtOnce(t *testing.T) {
	const otherUUID = "0192f3a4-0000-7000-8000-0000000000cc"
	core, session, _ := newClockedSessionTestCore(t)
	listInbox(t, core)

	*session = *auth.NewTestSubject(auth.SubjectFields{UserID: benLogin, PlatformUserID: otherUUID})
	seedLoginRow(t, core, "for the new holder")
	got := listInbox(t, core)

	if alias := aliasOf(t, core, benLogin); alias != otherUUID {
		t.Fatalf("alias = %q, want the new users.id %q without waiting for the recheck", alias, otherUUID)
	}
	if len(got) != 1 || got[0].Title != "for the new holder" {
		t.Fatalf("new holder's inbox = %+v, want the login-keyed row", got)
	}
}

func TestNotifyCore_ARefusedAdoptionStaysRefusedUntilTheRecheck(t *testing.T) {
	const target = "not-a-uuid-platform-id"
	core, session, clock := newClockedSessionTestCore(t)
	*session = *auth.NewTestSubject(auth.SubjectFields{UserID: target, PlatformUserID: benUUID})
	ctx := t.Context()
	if err := core.db.RecordAlias(ctx, "victim@example.com", target); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	listInbox(t, core)
	if _, err := core.db.ExecContext(ctx, `DELETE FROM notify_user_aliases WHERE login_id = 'victim@example.com'`); err != nil {
		t.Fatalf("clear alias: %v", err)
	}

	clock.t = clock.t.Add(5*time.Minute - time.Second)
	listInbox(t, core)
	if got := aliasOf(t, core, target); got != "" {
		t.Fatalf("alias inside the recheck = %q, want the refusal remembered", got)
	}

	clock.t = clock.t.Add(2 * time.Second)
	listInbox(t, core)
	if got := aliasOf(t, core, target); got != benKey {
		t.Fatalf("alias after the recheck = %q, want %q once the login id is no longer an alias target", got, benKey)
	}
}

func newSessionTestCore(t *testing.T) (*NotifyCore, *auth.Subject) {
	t.Helper()
	session := auth.NewTestSubject(auth.SubjectFields{})
	*session = *ben
	return newIdentityTestCore(t, session), session
}
