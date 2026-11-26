// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

type recordedSignals struct {
	sent []a2a.NotifyInboxCreated
	err  error
}

func (r *recordedSignals) SignalInboxCreated(_ context.Context, created a2a.NotifyInboxCreated) error {
	r.sent = append(r.sent, created)
	return r.err
}

func TestDispatcher_SignalsTheInboxRowItWrote(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, benLogin)
	signals := &recordedSignals{}
	d.SetInboxSignal(signals)

	d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Title: "Budget", Message: "80%",
	})

	if len(signals.sent) != 1 {
		t.Fatalf("signals = %+v, want one", signals.sent)
	}
	rows, err := store.NewNotificationRepo(db).ListByUser(ctx, benKey, store.InboxFilter{}, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("inbox rows = %v (%v), want one", rows, err)
	}
	want := a2a.NotifyInboxCreated{RecipientUserID: benLogin, RecipientPlatformUserID: benUUID, NotificationID: rows[0].ID}
	if signals.sent[0] != want {
		t.Errorf("signal = %+v, want %+v", signals.sent[0], want)
	}
}

func TestDispatcher_SignalsNothingWhenNoInboxRowIsWritten(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, benLogin)
	signals := &recordedSignals{}
	d.SetInboxSignal(signals)
	if err := store.NewCategoryPreferenceRepo(db).SetCategory(ctx, benKey, CategoryAgentMessage, false); err != nil {
		t.Fatalf("mute: %v", err)
	}

	d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "muted", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Category: CategoryAgentMessage, Message: "m",
	})
	d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "webhook-only", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Channels: []string{"webhook"}, Message: "m",
	})

	if len(signals.sent) != 0 {
		t.Errorf("signals = %+v, want none", signals.sent)
	}
}

func TestDispatcher_AFailedSignalLeavesTheInboxDelivery(t *testing.T) {
	ctx := t.Context()
	d, _, _ := newWebhookDispatcher(t, benLogin)
	d.SetInboxSignal(&recordedSignals{err: errors.New("broker down")})

	recs := d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: benLogin, RecipientPlatformUserID: benUUID,
		Mode: a2a.NotifyModeNotify, Title: "Budget", Message: "80%",
	})

	if summarizeDeliveries(recs) != "delivered" {
		t.Errorf("deliveries = %+v, want delivered", recs)
	}
}

func TestDispatcher_ALoginAddressedRequestIsSignalledWithTheAliasedUsersID(t *testing.T) {
	ctx := t.Context()
	d, db, _ := newWebhookDispatcher(t, benLogin)
	d.SetAliases(db)
	signals := &recordedSignals{}
	d.SetInboxSignal(signals)
	if err := db.RecordAlias(ctx, benLogin, benKey); err != nil {
		t.Fatalf("record alias: %v", err)
	}

	d.Deliver(ctx, a2a.NotifyRequest{
		RequestID: "r-login", RecipientUserID: benLogin,
		Mode: a2a.NotifyModeNotify, Title: "Budget", Message: "80%",
	})

	if len(signals.sent) != 1 {
		t.Fatalf("signals = %+v, want one", signals.sent)
	}
	if got := signals.sent[0].RecipientPlatformUserID; got != benKey {
		t.Errorf("signalled users.id = %q, want the aliased %q", got, benKey)
	}
}
