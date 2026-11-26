// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const devLoginEmail = "dev.user@example.com"

func advertise(t *testing.T, core *NotifyCore, caps a2a.NotifyCapabilities) {
	t.Helper()
	core.capabilities.Observe(gatewayCard(caps.ChannelType+"-gw", &caps), "i1")
}

func advertiseResolving(t *testing.T, core *NotifyCore, channelType string) {
	t.Helper()
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: channelType, Notifiable: true, ResolvesAddress: true})
}

type resolvingGateway struct {
	mu       sync.Mutex
	requests []a2a.NotifyResolveRequest
	answer   func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse
}

func (g *resolvingGateway) received() []a2a.NotifyResolveRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.requests)
}

func startResolvingGateway(t *testing.T, core *NotifyCore, channelType string, answer func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse) *resolvingGateway {
	t.Helper()
	brk := core.svc.Broker()
	q, err := brk.CreateQueue("resolver-"+channelType, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create resolve queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.NotifyResolveSubscription(core.cfg.Namespace, channelType)); err != nil {
		t.Fatalf("subscribe resolve: %v", err)
	}
	g := &resolvingGateway{answer: answer}
	go func() {
		for {
			msg, err := brk.Receive(t.Context(), q)
			if err != nil {
				return
			}
			var req a2a.NotifyResolveRequest
			if json.Unmarshal(msg.Payload, &req) != nil {
				msg.Ack()
				continue
			}
			g.mu.Lock()
			g.requests = append(g.requests, req)
			g.mu.Unlock()
			payload, _ := json.Marshal(g.answer(req))
			_ = brk.Publish(context.Background(), msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
			msg.Ack()
		}
	}()
	return g
}

func slackAddress(slackUser string) a2a.NotifyResolveResponse {
	return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"team_id":"T1","user_id":"` + slackUser + `"}`)}
}

func TestHTTP_ResolvedChannel_ConfirmedWithoutEverMessagingTheBot(t *testing.T) {
	t.Setenv("SAM_DEV_USER_EMAIL", devLoginEmail)
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")
	gw := startResolvingGateway(t, core, "slack", func(req a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return slackAddress("U-ALICE")
	})
	sink := deliverSink(t, core.svc.Broker(), "slack-sink", core.cfg.Namespace, "slack")

	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || ch.Confirmed || ch.Pending {
		t.Fatalf("slack entry = %+v, want an unconfirmed offer", ch)
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}

	reqs := gw.received()
	if len(reqs) != 1 || reqs[0].Email != devLoginEmail || reqs[0].UserID != devUser || reqs[0].RequestID == "" {
		t.Fatalf("resolve requests = %+v, want one for the caller's login email", reqs)
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "slack", `{"team_id":"T1","user_id":"U-ALICE"}`)
	if got := confirmedAddress(t, core, devUser, "slack"); got != `{"team_id":"T1","user_id":"U-ALICE"}` {
		t.Fatalf("confirmed address = %q", got)
	}

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("second turn on = %d; body=%s", rr.Code, rr.Body.String())
	}
	if d := sink(300 * time.Millisecond); d != nil {
		t.Fatalf("a second notice was sent for the same resolved address: %+v", d)
	}
}

func TestHTTP_ResolvedChannel_ExplainsWhyAnAddressWasNotFound(t *testing.T) {
	cases := []struct {
		name       string
		resp       a2a.NotifyResolveResponse
		wantStatus int
		wantText   string
	}{
		{"no account", a2a.NotifyResolveResponse{Error: a2a.NotifyResolveNoUser}, http.StatusBadRequest, "could not find a Slack account for your login email"},
		{"missing permission", a2a.NotifyResolveResponse{Error: a2a.NotifyResolveMissingScope}, http.StatusBadRequest, "not allowed to look up Slack users by email"},
		{"cannot send a direct message", a2a.NotifyResolveResponse{Error: a2a.NotifyResolveNoDirectMessage}, http.StatusBadRequest, "cannot send you a direct message"},
		{"lookup failed", a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable}, http.StatusBadGateway, "could not reach Slack"},
		{"a reason this notify does not know", a2a.NotifyResolveResponse{Error: "something_new"}, http.StatusBadGateway, "could not reach Slack"},
		{"empty answer", a2a.NotifyResolveResponse{}, http.StatusBadGateway, "could not look up your account right now"},
		{"null address", a2a.NotifyResolveResponse{Address: json.RawMessage(`null`)}, http.StatusBadGateway, "could not look up your account right now"},
		{"empty object address", a2a.NotifyResolveResponse{Address: json.RawMessage(`{}`)}, http.StatusBadGateway, "could not look up your account right now"},
		{"non-object address", a2a.NotifyResolveResponse{Address: json.RawMessage(`"U1"`)}, http.StatusBadGateway, "could not look up your account right now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			core := newTestCore(t, true, nil)
			advertiseResolving(t, core, "slack")
			startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse { return tc.resp })
			sink := deliverSink(t, core.svc.Broker(), "slack-sink", core.cfg.Namespace, "slack")

			rr := startConfirm(t, core, "slack")
			var apiErr struct{ Message string }
			if err := json.Unmarshal(rr.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if rr.Code != tc.wantStatus || !strings.Contains(apiErr.Message, tc.wantText) {
				t.Fatalf("start = %d %q, want %d containing %q", rr.Code, apiErr.Message, tc.wantStatus, tc.wantText)
			}
			if sink(200*time.Millisecond) != nil {
				t.Error("a notice was sent although no address was found")
			}
			if uc, err := core.channels.GetByType(t.Context(), devUser, "slack"); err != nil || uc != nil {
				t.Errorf("row = %+v (%v), want none", uc, err)
			}
		})
	}
}

func TestHTTP_ResolvedChannel_NoGatewayAnswering(t *testing.T) {
	prev := resolveTimeout
	resolveTimeout = 150 * time.Millisecond
	t.Cleanup(func() { resolveTimeout = prev })
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")

	rr := startConfirm(t, core, "slack")
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "entry point is not responding") {
		t.Fatalf("start = %d %s, want 503 saying the entry point is not responding", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ResolvedChannel_LogsEachFailureAtItsOwnLevel(t *testing.T) {
	const lookupFailed = "notify resolve: address lookup failed"
	prev := resolveTimeout
	resolveTimeout = 150 * time.Millisecond
	t.Cleanup(func() { resolveTimeout = prev })

	t.Run("no answer in time", func(t *testing.T) {
		core, logs := capturingCore(t)
		advertiseResolving(t, core, "slack")
		if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
		}
		if rec := logLine(t, logs.String(), lookupFailed); rec["level"] != "WARN" || rec["requestID"] == "" {
			t.Errorf("record = %v, want a warning naming the request", rec)
		}
	})

	t.Run("gateway cannot look the user up", func(t *testing.T) {
		core, logs := capturingCore(t)
		advertiseResolving(t, core, "slack")
		startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
			return a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable}
		})
		if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
		}
		rec := logLine(t, logs.String(), lookupFailed)
		if rec["level"] != "WARN" || rec["requestID"] == "" || !strings.Contains(rec["error"].(string), "known: true") {
			t.Errorf("record = %v, want a warning with the request id and the gateway's reason", rec)
		}
	})

	t.Run("gateway answers a reason this notify does not know", func(t *testing.T) {
		core, logs := capturingCore(t)
		advertiseResolving(t, core, "slack")
		startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
			return a2a.NotifyResolveResponse{Error: "something_new"}
		})
		if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
		}
		rec := logLine(t, logs.String(), lookupFailed)
		if rec["level"] != "WARN" || !strings.Contains(rec["error"].(string), "known: false") || strings.Contains(logs.String(), "something_new") {
			t.Errorf("record = %v, want a warning that says the reason is unknown without repeating it", rec)
		}
	})

	t.Run("gateway breaks the contract", func(t *testing.T) {
		core, logs := capturingCore(t)
		advertiseResolving(t, core, "slack")
		startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
			return a2a.NotifyResolveResponse{Address: json.RawMessage(`null`)}
		})
		if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
			t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
		}
		if rec := logLine(t, logs.String(), lookupFailed); rec["level"] != "ERROR" {
			t.Errorf("record = %v, want an error for an answer that breaks the contract", rec)
		}
	})

	t.Run("caller went away", func(t *testing.T) {
		core, logs := capturingCore(t)
		advertiseResolving(t, core, "slack")
		ctx, cancel := context.WithCancel(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: devUser, Email: devLoginEmail})))
		cancel()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/notify/channels/slack/confirm", nil).WithContext(ctx)
		req.SetPathValue("channelType", "slack")
		core.handleChannelConfirmStart(httptest.NewRecorder(), req)
		if rec := logLine(t, logs.String(), lookupFailed); rec["level"] != "DEBUG" {
			t.Errorf("record = %v, want a debug line for a caller who left", rec)
		}
	})
}

func TestHTTP_ResolvedChannel_NeedsABareLoginEmail(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")
	gw := startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse { return slackAddress("U1") })

	for _, email := range []string{"", "jdoe", "Jane <jane@example.com>", "<jane@example.com>", `"Jane" <jane@example.com>`, " jane@example.com", "jane@example.com ", "a@example.com, b@example.com"} {
		rr := callAs(core.handleChannelConfirmStart, http.MethodPost, "/api/v1/notify/channels/slack/confirm", "slack", "",
			auth.NewTestSubject(auth.SubjectFields{UserID: "u1", Email: email}))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), msgNoLoginEmail) {
			t.Errorf("login email %q: start = %d %s, want 400 %q", email, rr.Code, rr.Body.String(), msgNoLoginEmail)
		}
	}
	if got := gw.received(); len(got) != 0 {
		t.Errorf("the gateway was asked %+v for an unusable email", got)
	}
}

func TestHTTP_PresenceChannel_StillUsesTheRecordedAddress(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true})
	sink := deliverSink(t, core.svc.Broker(), "teams-sink", core.cfg.Namespace, "teams")

	if rr := startConfirm(t, core, "teams"); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Send a message to the Agent Mesh application in Microsoft Teams first.") {
		t.Fatalf("start before any message = %d %s, want the message-the-bot error", rr.Code, rr.Body.String())
	}
	seedPresence(t, core, devUser, "teams", `{"conversation_id":"C1"}`)
	if rr := startConfirm(t, core, "teams"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d %s", rr.Code, rr.Body.String())
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "teams", `{"conversation_id":"C1"}`)
}

func TestPresence_IgnoredForChannelsThatResolveAddresses(t *testing.T) {
	core, _, _ := startRouterCore(t)
	advertiseResolving(t, core, "slack")
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true})
	for _, ct := range []string{"slack", "teams"} {
		payload, err := json.Marshal(a2a.NotifyPresence{UserID: "alice", ChannelType: ct, Address: json.RawMessage(`{"user_id":"U1"}`)})
		if err != nil {
			t.Fatal(err)
		}
		core.handlePresence(t.Context(), &broker.Message{Payload: payload})
	}
	if uc, err := core.channels.GetByType(t.Context(), "alice", "slack"); err != nil || uc != nil {
		t.Errorf("slack row = %+v (%v), want none", uc, err)
	}
	if uc, err := core.channels.GetByType(t.Context(), "alice", "teams"); err != nil || uc == nil {
		t.Errorf("teams row = %+v (%v), want the presence row", uc, err)
	}
}

func TestHTTP_Channels_ListsOnlyWhatAnEntryPointOffers(t *testing.T) {
	t.Setenv("SAM_DEV_USER_EMAIL", devLoginEmail)
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"user_id":"U-OLD"}`)
	confirmChannel(t, core, devUser, "teams", `{"conversation_id":"T1"}`)

	types := func() []string {
		var got []string
		for _, c := range listChannels(t, core) {
			got = append(got, c.ChannelType)
		}
		slices.Sort(got)
		return got
	}
	if got := types(); !slices.Equal(got, []string{"teams"}) {
		t.Fatalf("with no entry point advertising, listed = %v, want only the confirmed teams", got)
	}

	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	if got := types(); !slices.Equal(got, []string{"slack", "teams"}) {
		t.Fatalf("with slack advertising, listed = %v, want the slack row back", got)
	}

	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: false})
	if got := types(); !slices.Equal(got, []string{"teams"}) {
		t.Fatalf("with slack declining notifications, listed = %v", got)
	}

	advertiseResolving(t, core, "slack")
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "discord", Notifiable: true})
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "webhook", Notifiable: true})
	if got := types(); !slices.Equal(got, []string{"discord", "slack", "teams"}) {
		t.Fatalf("listed = %v, want every advertised channel (never a built-in like webhook), and the confirmed teams", got)
	}

	rr := do(core, http.MethodGet, "/api/v1/notify/channels?userId=someone-else", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), `"slack"`) || strings.Contains(rr.Body.String(), `"discord"`) {
		t.Fatalf("another user's listing = %d %s, want none of the caller's offers", rr.Code, rr.Body.String())
	}
}

func TestHTTP_Channels_OfferedToACallerWithoutALoginEmail(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true})

	rr := callAs(core.handleChannelsList, http.MethodGet, "/api/v1/notify/channels", "", "", auth.NewTestSubject(auth.SubjectFields{UserID: "u1", Email: "jdoe"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[UserChannelDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range page.Data {
		got = append(got, c.ChannelType)
	}
	if !slices.Equal(got, []string{"slack", "teams"}) {
		t.Fatalf("listed = %v, want slack and teams", got)
	}
}

func TestHTTP_Channels_OfferedToACallerKeyedByUsersID(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")

	caller := auth.NewTestSubject(auth.SubjectFields{UserID: "u1", Email: "u1@example.com", PlatformUserID: "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01"})
	rr := callAs(core.handleChannelsList, http.MethodGet, "/api/v1/notify/channels", "", "", caller)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[UserChannelDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ChannelType != "slack" {
		t.Fatalf("listed = %+v, want slack offered to its own caller", page.Data)
	}
}

func TestHTTP_Channels_KeepsARowWhoseCodeIsStillRedeemable(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true})
	seedPresence(t, core, devUser, "teams", `{"conversation_id":"C1"}`)
	issueLegacyCode(t, core, devUser, "teams")

	core.capabilities.Remove("teams-gw", "i1")
	ch := findChannel(listChannels(t, core), "teams")
	if ch == nil || !ch.Pending {
		t.Fatalf("teams entry = %+v, want the pending row while its code is redeemable", ch)
	}
}

func TestHTTP_ResolvedChannel_IgnoresARequestSuppliedAddress(t *testing.T) {
	t.Setenv("SAM_DEV_USER_EMAIL", devLoginEmail)
	core := newTestCore(t, true, nil)
	advertiseResolving(t, core, "slack")
	gw := startResolvingGateway(t, core, "slack", func(a2a.NotifyResolveRequest) a2a.NotifyResolveResponse { return slackAddress("U-ALICE") })
	sink := deliverSink(t, core.svc.Broker(), "slack-sink", core.cfg.Namespace, "slack")

	sent := time.Now()
	rr := do(core, http.MethodPost, "/api/v1/notify/channels/slack/confirm?userId=eve&address=%7B%22user_id%22%3A%22U-EVE%22%7D&email=eve%40example.com", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("start naming another user = %d %s, want 403", rr.Code, rr.Body.String())
	}
	rr = do(core, http.MethodPost, "/api/v1/notify/channels/slack/confirm?address=%7B%22user_id%22%3A%22U-EVE%22%7D&email=eve%40example.com", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d %s", rr.Code, rr.Body.String())
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "slack", `{"team_id":"T1","user_id":"U-ALICE"}`)
	reqs := gw.received()
	if len(reqs) != 1 || reqs[0].Email != devLoginEmail || reqs[0].UserID != devUser {
		t.Fatalf("resolve requests = %+v, want one for the authenticated caller", reqs)
	}
	expiresAt, err := time.Parse(time.RFC3339, reqs[0].ExpiresAt)
	if err != nil {
		t.Fatalf("request expires_at %q is not an ISO 8601 timestamp: %v", reqs[0].ExpiresAt, err)
	}
	if earliest, latest := sent.Add(resolveTimeout-time.Millisecond), time.Now().Add(resolveTimeout); expiresAt.Before(earliest) || expiresAt.After(latest) {
		t.Errorf("request expires at %v, want between %v and %v: the moment the wait ends", expiresAt, earliest, latest)
	}
	if uc, err := core.channels.GetByType(t.Context(), devUser, "slack"); err != nil || uc == nil || uc.Email != devLoginEmail {
		t.Errorf("stored row = %+v (%v), want the caller's own email", uc, err)
	}
}
