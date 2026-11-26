// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var undeliverableChannelTypes = []string{"web", "webhook", "connector", "email", "Slack", "slack/x", "slack.>", ""}

func TestChannelsFor_MapsConfirmationAndSkipsUndeliverableTypes(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, "trent", "slack", `{"channel_id":"CANDIDATE"}`)
	confirmChannel(t, core, "trent", "teams", `{"conversation_id":"CONFIRMED"}`)
	seedPresence(t, core, "trent", "teams", `{"conversation_id":"NEWER-CANDIDATE"}`)
	for _, ct := range undeliverableChannelTypes[:len(undeliverableChannelTypes)-1] {
		seedPresence(t, core, "trent", ct, `{"channel_id":"X"}`)
	}

	got, err := core.ChannelsFor(t.Context(), "trent")
	if err != nil {
		t.Fatalf("ChannelsFor: %v", err)
	}
	want := []LearnedChannel{
		{ChannelType: "slack", Status: AddressUnconfirmed},
		{ChannelType: "teams", Address: `{"conversation_id":"CONFIRMED"}`, Status: AddressConfirmed},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ChannelsFor = %+v, want %+v", got, want)
	}
}

func TestChannelsFor_ReturnsStoreErrors(t *testing.T) {
	core := newTestCore(t, true, nil)
	if err := core.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if _, err := core.ChannelsFor(t.Context(), "trent"); err == nil {
		t.Fatal("a store failure must surface, not read as a user seen nowhere")
	}
}

func TestDeliver_UnconfirmedChannelIsSkippedThroughTheRealDirectory(t *testing.T) {
	core := newTestCore(t, true, nil)
	sink := deliverSink(t, core.svc.Broker(), "unconfirmed-sink", core.cfg.Namespace, "slack")
	seedPresence(t, core, "trent", "slack", `{"channel_id":"C1"}`)

	recs := core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi", Message: "m"})

	var slackRecs []DeliveryRecord
	for _, r := range recs {
		if r.ChannelType == "slack" {
			slackRecs = append(slackRecs, r)
		}
	}
	if len(slackRecs) != 1 || slackRecs[0].Status != "skipped" || slackRecs[0].Error != "channel not confirmed by the user" {
		t.Fatalf("slack records = %+v, want one skipped as not confirmed", slackRecs)
	}
	if d := sink(200 * time.Millisecond); d != nil {
		t.Fatalf("nothing may be published to an unconfirmed channel, got %+v", d)
	}
}

func TestPresence_DropsChannelTypesNoGatewayCanDeliverTo(t *testing.T) {
	core, _, _ := startRouterCore(t)
	addr := json.RawMessage(`{"channel_id":"C1"}`)
	for _, ct := range undeliverableChannelTypes {
		payload, err := json.Marshal(a2a.NotifyPresence{UserID: "trent", ChannelType: ct, Address: addr})
		if err != nil {
			t.Fatal(err)
		}
		core.handlePresence(t.Context(), &broker.Message{Payload: payload})
	}
	rows, err := core.channels.ListByUser(t.Context(), "trent")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("undeliverable presence types were stored: %+v", rows)
	}
}

func TestPublishDeliver_RefusesAChannelTypeThatIsNotATopicToken(t *testing.T) {
	core := newTestCore(t, true, nil)
	for _, ct := range []string{"slack/x", "slack.>", "Slack", ""} {
		if err := core.PublishDeliver(t.Context(), ct, a2a.NotifyDeliver{RequestID: "r"}); err == nil {
			t.Errorf("PublishDeliver(%q) = nil, want a refusal", ct)
		}
	}
}

func TestHTTP_ChannelConfirm_StartRefusesUndeliverableTypes(t *testing.T) {
	core := newTestCore(t, true, nil)
	for _, ct := range []string{"email", "web", "Slack"} {
		rr := startConfirm(t, core, ct)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), msgChannelTypeNotConfirmable) {
			t.Errorf("start %q = %d %s, want 400 %q", ct, rr.Code, rr.Body.String(), msgChannelTypeNotConfirmable)
		}
	}
}
