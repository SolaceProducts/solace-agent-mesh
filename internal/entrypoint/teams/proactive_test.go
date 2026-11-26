// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestTeamsNotifyWiring_PresenceMatchesAdvertisedChannel(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	presence, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(presence, a2a.NotifyPresenceSubscription("test-ns")); subErr != nil {
		t.Fatal(subErr)
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "teams-gw"})
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyNotifyService, Default: true})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg)).Services())
	a := &TeamsAdapter{core: core, logger: slog.Default(), ctx: t.Context()}

	a.startNotifyDeliverLoop()
	want := a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true, Interactable: true}
	if got := core.AdvertisedNotifyCapabilities(); got == nil || *got != want {
		t.Fatalf("advertised = %+v, want %+v", got, want)
	}

	a.publishPresence(t.Context(), "trent", "trent@x.com", teamsProactiveAddress{ServiceURL: "https://smba.trafficmanager.net/", ConversationID: "a:1", FromID: "29:a"})
	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, presence)
	if err != nil {
		t.Fatalf("no presence published: %v", err)
	}
	var p a2a.NotifyPresence
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.ChannelType != want.ChannelType {
		t.Fatalf("presence channel type = %q, want the advertised %q", p.ChannelType, want.ChannelType)
	}
}

func TestTeamsNotifyDeliverOptions(t *testing.T) {
	want := entrypoint.NotifyDeliverOptions{GatewayType: "teams", Interactable: true}
	if notifyDeliverOptions != want {
		t.Errorf("notifyDeliverOptions = %+v, want %+v", notifyDeliverOptions, want)
	}
}

func teamsAddr(t *testing.T, a *TeamsAdapter) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(teamsProactiveAddress{ServiceURL: a.botClient.apiBaseURL, ConversationID: "conv-1", FromID: "29:U1", AadObjectID: "aad-U1"})
	return b
}

func TestTeamsStartNotifyDeliverLoop_DisabledNoop(t *testing.T) {
	a, _, _ := newHILTeamsAdapter(t)
	a.startNotifyDeliverLoop()
}

func TestTeamsProactiveDeliver_PostsHILAndNotice(t *testing.T) {
	a, _, posts := newHILTeamsAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-h", Mode: a2a.NotifyModeInput, ChannelType: "teams",
		Address: teamsAddr(t, a), Surface: json.RawMessage(teamsAskSurfaceJSON),
	})
	if posts.Load() == 0 {
		t.Fatal("HIL deliver should post an Adaptive Card")
	}
	before := posts.Load()

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-n", Mode: a2a.NotifyModeNotify, ChannelType: "teams",
		Address: teamsAddr(t, a), Title: "Heads up", Message: "report ready",
	})
	if posts.Load() <= before {
		t.Fatal("notice deliver should post a message")
	}
}

func TestTeamsProactiveDeliver_AddressLessDropped(t *testing.T) {
	a, _, posts := newHILTeamsAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-bad", Mode: a2a.NotifyModeInput, ChannelType: "teams",
		Address: json.RawMessage(`{}`), Surface: json.RawMessage(teamsAskSurfaceJSON),
	})
	if posts.Load() != 0 {
		t.Fatalf("address-less command must not post, got %d", posts.Load())
	}
}

func TestTeamsProactiveDeliver_DMCreateFailureSkips(t *testing.T) {
	a, _, _ := newHILTeamsAdapter(t)
	var sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth2"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case strings.HasSuffix(r.URL.Path, "/v3/conversations"):
			w.WriteHeader(http.StatusForbidden)
		default:
			sends.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
		}
	}))
	t.Cleanup(srv.Close)
	a.botClient.apiBaseURL = srv.URL

	addr, _ := json.Marshal(teamsProactiveAddress{ServiceURL: srv.URL, ConversationID: "conv-1", FromID: "29:U1", AadObjectID: "aad-U1"})
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-dmfail", Mode: a2a.NotifyModeNotify, ChannelType: "teams",
		Address: json.RawMessage(addr), Title: "Heads up", Message: "report ready",
	})
	if sends.Load() != 0 {
		t.Fatalf("DM-create failure must skip out-of-band delivery, got %d sends", sends.Load())
	}
}

func TestTeamsProactiveDeliver_TargetsCreatedDMNotLastSeenConversation(t *testing.T) {
	a, _, _ := newHILTeamsAdapter(t)
	var activityPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth2"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case strings.HasSuffix(r.URL.Path, "/v3/conversations"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "dm-conv-1"})
		default:
			activityPath.Store(r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
		}
	}))
	t.Cleanup(srv.Close)
	a.botClient.apiBaseURL = srv.URL

	addr, _ := json.Marshal(teamsProactiveAddress{ServiceURL: srv.URL, ConversationID: "conv-1", FromID: "29:U1", TenantID: "tid-1"})
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-dm", Mode: a2a.NotifyModeNotify, ChannelType: "teams",
		Address: json.RawMessage(addr), Title: "Heads up", Message: "report ready",
	})
	path, _ := activityPath.Load().(string)
	if path == "" {
		t.Fatal("expected an activity send")
	}
	if !strings.Contains(path, "/conversations/dm-conv-1/") {
		t.Errorf("activity sent to %q, want the created DM conversation dm-conv-1", path)
	}
	if strings.Contains(path, "/conversations/conv-1/") {
		t.Errorf("activity leaked to the last-seen conversation conv-1: %q", path)
	}
}

func TestTeamsProactiveDeliver_EmptyConvIDSkips(t *testing.T) {
	a, _, _ := newHILTeamsAdapter(t)
	var sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth2"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case strings.HasSuffix(r.URL.Path, "/v3/conversations"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": ""})
		default:
			sends.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
		}
	}))
	t.Cleanup(srv.Close)
	a.botClient.apiBaseURL = srv.URL

	addr, _ := json.Marshal(teamsProactiveAddress{ServiceURL: srv.URL, ConversationID: "conv-1", FromID: "29:U1", TenantID: "tid-1"})
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-emptyid", Mode: a2a.NotifyModeNotify, ChannelType: "teams",
		Address: json.RawMessage(addr), Title: "Heads up", Message: "report ready",
	})
	if sends.Load() != 0 {
		t.Fatalf("empty conversation id must skip out-of-band delivery, got %d sends", sends.Load())
	}
}

func TestTeamsProactiveDeliver_HILBadSurfaceDropped(t *testing.T) {
	a, _, posts := newHILTeamsAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-bs", Mode: a2a.NotifyModeInput, ChannelType: "teams",
		Address: teamsAddr(t, a), Surface: json.RawMessage(`123`),
	})
	if posts.Load() != 0 {
		t.Fatalf("bad surface must not post, got %d", posts.Load())
	}
}

func TestTeamsProactiveNotice_TextVariants(t *testing.T) {
	a, _, posts := newHILTeamsAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "r1", Mode: a2a.NotifyModeNotify, ChannelType: "teams", Address: teamsAddr(t, a), Title: "T", Message: "M"})
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "r2", Mode: a2a.NotifyModeNotify, ChannelType: "teams", Address: teamsAddr(t, a)})
	if posts.Load() != 2 {
		t.Fatalf("both notices should post, got %d", posts.Load())
	}
}

func TestTeamsProactive_PublishReplyErrorIsBestEffort(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	_ = brk.Disconnect(context.Background())
	_ = a.publishNotifyReply(a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r", Status: "answered"})
}

func TestTeamsProactiveReply_PublishFailureNotifiesUser(t *testing.T) {
	a, brk, posts := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-pf", "", "", true)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o1"
	value, _ := json.Marshal(submit)
	_ = brk.Disconnect(context.Background())

	act := &Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}, ServiceURL: a.botClient.apiBaseURL}
	act.Conversation.ID = "conv-1"
	before := posts.Load()
	a.handleHILSubmit(act)
	if posts.Load() <= before {
		t.Fatal("a failed reply publish should post a corrective message to the user")
	}
}

func TestTeamsProactiveReply_CancelRoutesToNotify(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-c", "", "", true)
	cancel := submitActionData(t, card, HILCancelAction)
	value, _ := json.Marshal(cancel)

	q, err := brk.CreateQueue("teams-notify-cancel", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplyTopic("test-ns", "req-c")); err != nil {
		t.Fatal(err)
	}

	a.handleHILCancel(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}})

	ctx, ccl := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccl()
	msg, rerr := brk.Receive(ctx, q)
	if rerr != nil {
		t.Fatalf("expected a cancelled reply on the notify reply topic: %v", rerr)
	}
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp.RequestID != "req-c" || resp.Status != "cancelled" {
		t.Fatalf("reply = %+v, want req-c/cancelled routed to notify", resp)
	}
}

func TestTeamsProactiveReply_RoutesToNotify(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	s := parseTeamsSurfaceT(t)
	card, _ := a.buildHILCard(s, "req-p", "", "", true)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o1"
	value, _ := json.Marshal(submit)

	q, err := brk.CreateQueue("teams-notify-reply", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplyTopic("test-ns", "req-p")); err != nil {
		t.Fatal(err)
	}

	a.handleHILSubmit(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: "U1"}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, rerr := brk.Receive(ctx, q)
	if rerr != nil {
		t.Fatalf("expected a reply on the notify reply topic: %v", rerr)
	}
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp.RequestID != "req-p" || resp.Status != "answered" {
		t.Fatalf("reply = %+v, want req-p/answered routed to notify", resp)
	}
}
