// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func loginOnlyRequest(id string) a2a.NotifyRequest {
	return a2a.NotifyRequest{RequestID: id, RecipientUserID: benLogin, Mode: a2a.NotifyModeNotify, Title: id, Message: "m"}
}

func putCategory(t *testing.T, core *NotifyCore, category string, enabled bool) {
	t.Helper()
	body := `{"category":"` + category + `","enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`
	rr := httptest.NewRecorder()
	core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/v1/notify/preferences/categories", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT category = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func publishRequest(t *testing.T, core *NotifyCore, req a2a.NotifyRequest) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	core.handleMessage(t.Context(), &broker.Message{Topic: a2a.NotifyRequestTopic("test-ns", req.RecipientUserID), Payload: payload})
}

func TestNotifyCore_LoginOnlySendHonoursAMuteSetUnderThePlatformID(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	putCategory(t, core, CategoryScheduledTask, false)

	req := loginOnlyRequest("muted")
	req.Category = CategoryScheduledTask
	publishRequest(t, core, req)

	if got := listInbox(t, core); len(got) != 0 {
		t.Fatalf("a login-only send must honour the user's mute, inbox = %+v", got)
	}
	if has, _ := core.db.HasRowsFor(t.Context(), benLogin); has {
		t.Fatal("a muted login-only send must write nothing under the login id either")
	}
}

func TestNotifyCore_LoginOnlyAndBothIDSendsShareDedupKeys(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	listInbox(t, core)

	first := loginOnlyRequest("first")
	first.Dedup = "budget-warn"
	publishRequest(t, core, first)
	second := loginOnlyRequest("second")
	second.Dedup = "budget-warn"
	second.RecipientPlatformUserID = benUUID
	publishRequest(t, core, second)

	if got := listInbox(t, core); len(got) != 1 || got[0].Title != "first" {
		t.Fatalf("inbox = %+v, want one row for the shared dedup key", got)
	}
}

func TestNotifyCore_PlatformIDOnlyRequestReachesTheUser(t *testing.T) {
	core := newIdentityTestCore(t, ben)

	publishRequest(t, core, a2a.NotifyRequest{RequestID: "p", RecipientPlatformUserID: benUUID, Mode: a2a.NotifyModeNotify, Message: "m"})

	if got := listInbox(t, core); len(got) != 1 {
		t.Fatalf("inbox = %+v, want the platform-addressed row", got)
	}
}

func TestNotifyCore_RequestsNamingNoUsableRecipientAreDropped(t *testing.T) {
	core := newIdentityTestCore(t, ben)

	publishRequest(t, core, a2a.NotifyRequest{RequestID: "none", Mode: a2a.NotifyModeNotify, Message: "m"})
	publishRequest(t, core, a2a.NotifyRequest{
		RequestID: "bad-id", RecipientUserID: benLogin, RecipientPlatformUserID: "not-a-uuid",
		Mode: a2a.NotifyModeNotify, Message: "m",
	})

	if got := listInbox(t, core); len(got) != 0 {
		t.Fatalf("inbox = %+v, want nothing delivered", got)
	}
	if has, _ := core.db.HasRowsFor(t.Context(), "not-a-uuid"); has {
		t.Fatal("a malformed platform user id must not key any row")
	}
}

func TestNotifyCore_AdminOverrideByLoginIDReadsThePlatformKeyedInbox(t *testing.T) {
	admin := auth.NewTestSubject(auth.SubjectFields{UserID: "admin@example.com", Scopes: []string{rbac.ScopeNotifyReadOthers}})
	core := newIdentityTestCore(t, admin)
	if err := core.db.RecordAlias(t.Context(), benLogin, benKey); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID, Mode: a2a.NotifyModeNotify, Message: "m",
	})

	code, page := getJSON[NotificationDTO](t, core, "/api/v1/notify/inbox?userId="+benLogin)

	if code != http.StatusOK || len(page.Data) != 1 {
		t.Fatalf("admin read by login id = %d %+v, want the user's platform-keyed row", code, page.Data)
	}
}

func TestNotifyCore_AdoptionRecordsNoAliasWithoutADistinctPlatformID(t *testing.T) {
	for name, caller := range map[string]*auth.Subject{
		"no platform id":              auth.NewTestSubject(auth.SubjectFields{UserID: benLogin}),
		"login id is the platform id": auth.NewTestSubject(auth.SubjectFields{UserID: benKey, PlatformUserID: benUUID}),
	} {
		t.Run(name, func(t *testing.T) {
			core := newIdentityTestCore(t, caller)
			listInbox(t, core)

			if got, _ := core.db.PlatformUserIDFor(t.Context(), caller.UserID()); got != "" {
				t.Fatalf("alias for %q = %q, want none", caller.UserID(), got)
			}
		})
	}
}

func TestNotifyCore_AdoptionFailureStillServesTheRequest(t *testing.T) {
	core := newIdentityTestCore(t, ben)
	if _, err := core.db.ExecContext(t.Context(), `DROP TABLE notify_user_aliases`); err != nil {
		t.Fatalf("break alias store: %v", err)
	}

	if got := listInbox(t, core); len(got) != 0 {
		t.Fatalf("inbox = %+v, want empty", got)
	}
}

func TestNotifyCore_AUsersIDAsTheLoginIDMovesNothing(t *testing.T) {
	const attackerUUID = "0192f3a4-0000-7000-8000-0000000000bb"
	attacker := auth.NewTestSubject(auth.SubjectFields{UserID: benKey, PlatformUserID: attackerUUID})
	core := newIdentityTestCore(t, attacker)
	ctx := t.Context()
	if _, err := core.inbox.Add(ctx, benKey, "ben's", "b", nil, "test", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := listInbox(t, core); len(got) != 0 {
		t.Fatalf("attacker inbox = %+v, want empty", got)
	}
	if n, _ := core.inbox.CountByUser(ctx, benKey, store.InboxFilter{}); n != 1 {
		t.Fatalf("rows under the victim's users.id = %d, want 1", n)
	}
	if got, _ := core.db.PlatformUserIDFor(ctx, benKey); got != "" {
		t.Fatalf("alias for the victim's users.id = %q, want none", got)
	}
}

func TestNotifyCore_ALoginIDThatIsAnAliasTargetMovesNothing(t *testing.T) {
	const target = "not-a-uuid-platform-id"
	core := newIdentityTestCore(t, auth.NewTestSubject(auth.SubjectFields{UserID: target, PlatformUserID: benUUID}))
	ctx := t.Context()
	if err := core.db.RecordAlias(ctx, "victim@example.com", target); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := core.inbox.Add(ctx, target, "victim's", "b", nil, "test", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	listInbox(t, core)

	if n, _ := core.inbox.CountByUser(ctx, target, store.InboxFilter{}); n != 1 {
		t.Fatalf("rows under the alias target = %d, want 1", n)
	}
	if got, _ := core.db.PlatformUserIDFor(ctx, target); got != "" {
		t.Fatalf("alias for the alias target = %q, want none", got)
	}
}
