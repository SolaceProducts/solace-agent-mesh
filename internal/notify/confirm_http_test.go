// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const devUser = "sam_dev_user"

func listChannels(t *testing.T, core *NotifyCore) []UserChannelDTO {
	t.Helper()
	rr := do(core, http.MethodGet, "/api/v1/notify/channels", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list channels = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []UserChannelDTO `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	return resp.Data
}

func findChannel(dtos []UserChannelDTO, channelType string) *UserChannelDTO {
	for i := range dtos {
		if dtos[i].ChannelType == channelType {
			return &dtos[i]
		}
	}
	return nil
}

func seedPresence(t *testing.T, core *NotifyCore, userID, channelType, address string) {
	t.Helper()
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: userID, ChannelType: channelType, Address: address}); err != nil {
		t.Fatalf("seed presence: %v", err)
	}
}

func confirmedAddress(t *testing.T, core *NotifyCore, userID, channelType string) string {
	t.Helper()
	learned, err := core.ChannelsFor(t.Context(), userID)
	if err != nil {
		t.Fatalf("ChannelsFor: %v", err)
	}
	for _, lc := range learned {
		if lc.ChannelType == channelType && lc.Status == AddressConfirmed {
			return lc.Address
		}
	}
	return ""
}

const legacyCode = "ABCD-EFGH"

func issueLegacyCode(t *testing.T, core *NotifyCore, userID, channelType string) string {
	t.Helper()
	if err := core.channels.StartConfirmation(t.Context(), userID, channelType, "{}",
		hashVerificationCode(userID, channelType, legacyCode), time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("issue legacy code: %v", err)
	}
	return legacyCode
}

func assertTurnedOnNotice(t *testing.T, d *a2a.NotifyDeliver, userID, channelType, address string) {
	t.Helper()
	if d == nil {
		t.Fatal("expected a notice that notifications are on")
	}
	if string(d.Address) != address || d.ChannelType != channelType || d.UserID != userID || d.Mode != a2a.NotifyModeNotify {
		t.Fatalf("notice = %+v, want a plain notification for %s on %s at %s", d, userID, channelType, address)
	}
	if d.Title != "SAM notifications are on" || strings.Contains(d.Message, "http") || strings.Contains(d.Message, "code") {
		t.Fatalf("notice = %q / %q, want the turned-on text without a code or link", d.Title, d.Message)
	}
	if d.RequestID == "" {
		t.Fatal("notice has no request id")
	}
}

func startConfirm(t *testing.T, core *NotifyCore, channelType string) *httptest.ResponseRecorder {
	t.Helper()
	return do(core, http.MethodPost, "/api/v1/notify/channels/"+channelType+"/confirm", "")
}

func verifyCode(core *NotifyCore, channelType, code string) *httptest.ResponseRecorder {
	return do(core, http.MethodPost, "/api/v1/notify/channels/"+channelType+"/confirm/verify", `{"code":"`+code+`"}`)
}

func callAs(handler http.HandlerFunc, method, target, channelType, body string, id *auth.Subject) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("channelType", channelType)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestHTTP_ChannelConfirm_TurnsTheChannelOnInOneStep(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	candidate := `{"channel_id":"C1","user_id":"U1"}`
	seedPresence(t, core, devUser, "slack", candidate)

	rr := startConfirm(t, core, "slack")
	if rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var turnedOn UserChannelDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &turnedOn); err != nil || turnedOn.ChannelType != "slack" || !turnedOn.Confirmed || turnedOn.Pending || turnedOn.ConfirmedAt == "" {
		t.Fatalf("turn on response = %+v (%v), want confirmed with confirmedAt", turnedOn, err)
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "slack", candidate)

	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || !ch.Confirmed || ch.Pending {
		t.Fatalf("after turning on: %+v, want confirmed and not pending", ch)
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != candidate {
		t.Fatalf("confirmed address = %q, want the candidate", addr)
	}
}

func TestHTTP_ChannelConfirm_TurningOnAgainSendsNothing(t *testing.T) {
	core := newTestCore(t, true, nil)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("first turn on = %d", rr.Code)
	}
	if d := sink(2 * time.Second); d == nil {
		t.Fatal("no notice for the first turn on")
	}
	rr := startConfirm(t, core, "slack")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"confirmed":true`) {
		t.Fatalf("second turn on = %d %s, want 200 confirmed", rr.Code, rr.Body.String())
	}
	if d := sink(300 * time.Millisecond); d != nil {
		t.Fatalf("a second notice was sent: %+v", d)
	}
}

func TestHTTP_ChannelConfirm_NoticeHasNoLinkWhenAConfirmBaseURLIsSet(t *testing.T) {
	core := newTestCore(t, true, map[string]any{"notify": map[string]any{"confirm_base_url": "https://sam.example.com/#/notifications/confirm"}})
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "slack", `{"channel_id":"C1"}`)
}

func TestHTTP_ChannelConfirm_TurningOnClearsACodeEvenWhenAlreadyOnAtTheAddress(t *testing.T) {
	core := newTestCore(t, true, nil)
	address := `{"channel_id":"C1"}`
	confirmChannel(t, core, devUser, "slack", address)
	seedPresence(t, core, devUser, "slack", address)
	issueLegacyCode(t, core, devUser, "slack")

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}
	if uc, err := core.channels.GetByType(t.Context(), devUser, "slack"); err != nil || uc == nil || uc.CodeOutstandingAt(time.Now().UnixMilli()) || !uc.Confirmed() {
		t.Fatalf("row = %+v (%v), want confirmed with the code dropped", uc, err)
	}
}

func TestHTTP_ChannelConfirm_ANewAddressIsTurnedOnAndAnnounced(t *testing.T) {
	core := newTestCore(t, true, nil)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	confirmChannel(t, core, devUser, "slack", `{"channel_id":"OLD"}`)
	fresh := `{"channel_id":"NEW"}`
	seedPresence(t, core, devUser, "slack", fresh)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "slack", fresh)
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != fresh {
		t.Fatalf("confirmed address = %q, want the new address", addr)
	}
}

func TestHTTP_ChannelConfirm_TurningOnSupersedesAnOutstandingCode(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	code := issueLegacyCode(t, core, devUser, "slack")

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}
	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || !ch.Confirmed || ch.Pending || ch.Locked {
		t.Fatalf("after turning on: %+v, want confirmed with no code outstanding", ch)
	}
	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"confirmed":true`) {
		t.Fatalf("redeeming the superseded code = %d %s, want 200 confirmed", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_StartRejectsCallerChosenAddress(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "teams", `{"service_url":"https://smba.trafficmanager.net/","from_id":"29:a"}`)

	rr := do(core, http.MethodPost, "/api/v1/notify/channels/teams/confirm",
		`{"address":{"service_url":"https://attacker.example","from_id":"x"}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("start with an address body = %d, want 400", rr.Code)
	}
	if uc, _ := core.channels.GetByType(t.Context(), devUser, "teams"); uc == nil || uc.Confirmed() {
		t.Fatalf("a rejected request must not turn the channel on, got %+v", uc)
	}
	if rr := do(core, http.MethodPost, "/api/v1/notify/channels/teams/confirm", `{}`); rr.Code != http.StatusOK {
		t.Fatalf("turn on with an empty object = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

type rowDroppingBroker struct {
	broker.Broker
	core *NotifyCore
}

func (b *rowDroppingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if _, err := b.core.db.ExecContext(ctx, `DELETE FROM notify_user_channels`); err != nil {
		return err
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func TestHTTP_ChannelConfirm_CandidateGoneBeforeConfirm(t *testing.T) {
	for _, tc := range []struct{ channelType, name string }{
		{"slack", "Slack"},
		{"teams", "Microsoft Teams"},
	} {
		t.Run(tc.channelType, func(t *testing.T) {
			ctx := t.Context()
			brk := broker.NewDevBroker()
			if err := brk.Connect(ctx); err != nil {
				t.Fatalf("connect broker: %v", err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(ctx) })
			core := NewNotifyCore(map[string]any{
				"namespace":    "test-ns",
				"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
			}, NotifyExtensions{})
			rt := runtime.NewRuntime(&rowDroppingBroker{Broker: brk, core: core}, nil, runtime.WithLogger(slog.Default()))
			core.SetServices(rt.Services())
			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(func() { _ = core.Stop(ctx) })
			seedPresence(t, core, devUser, tc.channelType, `{"conversation_id":"C1"}`)

			rr := startConfirm(t, core, tc.channelType)

			want := fmt.Sprintf(`"message":"Unable to enable notifications because Agent Mesh has not received a message from this user in %s. Send a message to the Agent Mesh application in %s first."`, tc.name, tc.name)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
				t.Fatalf("turn on after the row went away = %d %s, want 400 naming %s", rr.Code, rr.Body.String(), tc.name)
			}
		})
	}
}

func TestHTTP_ChannelConfirm_GoogleChatStartWithoutPresence(t *testing.T) {
	core := newTestCore(t, true, nil)
	rr := startConfirm(t, core, "google_chat")
	want := `"message":"Unable to enable notifications because Agent Mesh has not received a message from this user in Google Chat. Send a message to the Agent Mesh application in Google Chat first."`
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("start with no candidate = %d %s, want 400 telling the user to message the application in Google Chat", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_StartWithoutPresence(t *testing.T) {
	core := newTestCore(t, true, nil)
	rr := startConfirm(t, core, "teams")
	want := `"message":"Unable to enable notifications because Agent Mesh has not received a message from this user in Microsoft Teams. Send a message to the Agent Mesh application in Microsoft Teams first."`
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("start with no candidate = %d %s, want 400 telling the user to message the application in Microsoft Teams", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_UnknownChannelType(t *testing.T) {
	core := newTestCore(t, true, nil)
	if rr := startConfirm(t, core, "email"); rr.Code != http.StatusBadRequest {
		t.Fatalf("start email = %d, want 400", rr.Code)
	}
	if rr := verifyCode(core, "email", "ABCD-EFGH"); rr.Code != http.StatusBadRequest {
		t.Fatalf("verify email = %d, want 400", rr.Code)
	}
}

func TestHTTP_ChannelConfirm_StartRefusedWhenGatewaysDecline(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	core.capabilities.Observe(gatewayCard("slack-gw", &a2a.NotifyCapabilities{ChannelType: "slack"}), "slack-pod")

	rr := startConfirm(t, core, "slack")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), msgChannelNotConfirmable) {
		t.Fatalf("start on a declining channel = %d %s, want 400 %q", rr.Code, rr.Body.String(), msgChannelNotConfirmable)
	}
	if uc, _ := core.channels.GetByType(t.Context(), devUser, "slack"); uc == nil || uc.Confirmed() {
		t.Fatalf("a refused turn on must leave the channel off, got %+v", uc)
	}
}

func TestHTTP_ChannelConfirm_StartBeforeAnyGatewayAdvertises(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	if got := core.capabilities.Capability("slack"); got.Advertised {
		t.Fatalf("precondition: nothing advertised, got %+v", got)
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on before any gateway card = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_CodeRedeemableAfterGatewayStopsAcceptingNotifications(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	code := issueLegacyCode(t, core, devUser, "slack")
	core.capabilities.Observe(gatewayCard("slack-gw", &a2a.NotifyCapabilities{ChannelType: "slack"}), "slack-pod")

	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusOK {
		t.Fatalf("verify after the gateway stopped accepting notifications = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_SendFailureKeepsWorkingChannel(t *testing.T) {
	core := newTestCore(t, true, nil)
	working := `{"channel_id":"WORKING"}`
	confirmChannel(t, core, devUser, "slack", working)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"MOVED"}`)
	if err := core.svc.Broker().Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
		t.Fatalf("turn on with a failing publish = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != working {
		t.Fatalf("a failed turn on must keep delivery on %q, got %q", working, addr)
	}
}

func TestHTTP_ChannelConfirm_FailedTurnOnKeepsAnEarlierCode(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	held := issueLegacyCode(t, core, devUser, "slack")

	if err := core.svc.Broker().Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
		t.Fatalf("turn on with a failing publish = %d, want 502", rr.Code)
	}
	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || ch.Confirmed || !ch.Pending {
		t.Fatalf("after a failed turn on: %+v, want not confirmed with the code still outstanding", ch)
	}
	if rr := verifyCode(core, "slack", held); rr.Code != http.StatusOK {
		t.Fatalf("the earlier code must still work after a failed turn on: %d %s", rr.Code, rr.Body.String())
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != "{}" {
		t.Fatalf("redeeming the earlier code confirmed %q, want the address the code was issued for", addr)
	}
}

func TestHTTP_ChannelConfirm_FailedFirstTurnOnLeavesTheChannelOff(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)

	if err := core.svc.Broker().Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
		t.Fatalf("turn on with a failing publish = %d, want 502", rr.Code)
	}
	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || ch.Confirmed || ch.Pending {
		t.Fatalf("after a failed first turn on: %+v, want off", ch)
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != "" {
		t.Fatalf("a failed first turn on confirmed %q, want nothing", addr)
	}
}

type cancelAfterPublish struct {
	broker.Broker
	cancel context.CancelFunc
}

func (b cancelAfterPublish) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	err := b.Broker.PublishGuaranteed(ctx, topic, msg)
	b.cancel()
	return err
}

func TestHTTP_ChannelConfirm_ClientGivingUpAfterTheNoticeStillTurnsTheChannelOn(t *testing.T) {
	core := newTestCore(t, true, nil)
	candidate := `{"channel_id":"C1","user_id":"U1"}`
	seedPresence(t, core, devUser, "slack", candidate)

	ctx, cancel := context.WithCancel(auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: devUser, Email: devLoginEmail, Authenticated: true})))
	defer cancel()
	core.svc = runtime.NewRuntime(cancelAfterPublish{Broker: core.svc.Broker(), cancel: cancel}, nil).Services()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify/channels/slack/confirm", nil).WithContext(ctx)
	req.SetPathValue("channelType", "slack")
	core.handleChannelConfirmStart(httptest.NewRecorder(), req)

	if addr := confirmedAddress(t, core, devUser, "slack"); addr != candidate {
		t.Fatalf("confirmed address = %q after the client gave up, want %q", addr, candidate)
	}
}

const msgCodeExpiredBody = `"message":"that code has expired; turn the channel on instead"`

func TestHTTP_ChannelConfirm_VerifyWithNoRow(t *testing.T) {
	core := newTestCore(t, true, nil)
	rr := verifyCode(core, "teams", "ABCD-EFGH")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), msgCodeExpiredBody) {
		t.Fatalf("verify with no row = %d %s, want 400", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_ResubmitAfterSuccessIsIdempotent(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", "{}")
	code := issueLegacyCode(t, core, devUser, "slack")

	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusOK {
		t.Fatalf("first verify = %d", rr.Code)
	}
	rr := verifyCode(core, "slack", code)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"confirmed":true`) {
		t.Fatalf("resubmitting a code that already succeeded = %d %s, want 200 confirmed", rr.Code, rr.Body.String())
	}
}

func TestHTTP_Channels_ListShowsLockedCode(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	seedPresence(t, core, devUser, "slack", "{}")
	issueLegacyCode(t, core, devUser, "slack")
	for range maxVerificationAttempts {
		verifyCode(core, "slack", "ZZZZ-ZZZZ")
	}
	ch := findChannel(listChannels(t, core), "slack")
	if ch == nil || !ch.Locked || ch.Pending {
		t.Fatalf("after %d wrong codes the channel must read locked and not pending, got %+v", maxVerificationAttempts, ch)
	}
}

func TestHTTP_ChannelConfirm_StartNeverUsesAnotherUsersAddress(t *testing.T) {
	core := newTestCore(t, true, nil)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "slack")
	seedPresence(t, core, "bob", "slack", `{"channel_id":"BOB"}`)

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadRequest {
		t.Fatalf("start for a user with no presence = %d, want 400", rr.Code)
	}
	if d := sink(500 * time.Millisecond); d != nil {
		t.Fatalf("no notice may be sent, got %+v", d)
	}
}

func TestHTTP_ChannelConfirm_VerifyRejections(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", "{}")
	issueLegacyCode(t, core, devUser, "slack")

	if rr := verifyCode(core, "slack", "ZZZZ-ZZZZ"); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"message":"that code is not correct; turn the channel on instead"`) {
		t.Fatalf("wrong code = %d %s", rr.Code, rr.Body.String())
	}
	if rr := verifyCode(core, "slack", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty code = %d, want 400", rr.Code)
	}
	if rr := do(core, http.MethodPost, "/api/v1/notify/channels/slack/confirm/verify", `{"code":"X","token":"y"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", rr.Code)
	}
	for range maxVerificationAttempts {
		verifyCode(core, "slack", "ZZZZ-ZZZZ")
	}
	if rr := verifyCode(core, "slack", "ZZZZ-ZZZZ"); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"message":"too many incorrect attempts; turn the channel on instead"`) {
		t.Fatalf("after the attempt limit = %d %s, want a lockout message", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_VerifyExpiredCode(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", "{}")
	const code = "ABCD-EFGH"
	if err := core.channels.StartConfirmation(t.Context(), devUser, "slack", "{}",
		hashVerificationCode(devUser, "slack", code), time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), msgCodeExpiredBody) {
		t.Fatalf("expired code = %d %s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelConfirm_IsSelfOnly(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, "bob", "slack", "{}")
	admin := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:send_others", "notify:_:read_others"}})

	if rr := callAs(core.handleChannelConfirmStart, http.MethodPost, "/api/v1/notify/channels/slack/confirm?userId=bob", "slack", "", admin); rr.Code != http.StatusForbidden {
		t.Fatalf("start for another user = %d, want 403", rr.Code)
	}
	if rr := callAs(core.handleChannelConfirmVerify, http.MethodPost, "/api/v1/notify/channels/slack/confirm/verify?userId=bob", "slack", `{"code":"ABCD-EFGH"}`, admin); rr.Code != http.StatusForbidden {
		t.Fatalf("verify for another user = %d, want 403", rr.Code)
	}
	if uc, _ := core.channels.GetByType(t.Context(), "bob", "slack"); uc == nil || uc.CodeOutstandingAt(time.Now().UnixMilli()) || uc.Confirmed() {
		t.Fatalf("bob's channel must be untouched, got %+v", uc)
	}
	if rr := callAs(core.handleChannelConfirmStart, http.MethodPost, "/api/v1/notify/channels/slack/confirm", "slack", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("start without an identity = %d, want 401", rr.Code)
	}
}

func TestHTTP_Channels_ListCrossUserNeedsReadOthers(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	seedPresence(t, core, "bob", "slack", "{}")

	plain := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:send_others"}})
	if rr := callAs(core.handleChannelsList, http.MethodGet, "/api/v1/notify/channels?userId=bob", "", "", plain); rr.Code != http.StatusForbidden {
		t.Fatalf("list another user without read-others = %d, want 403", rr.Code)
	}
	reader := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:read_others"}})
	rr := callAs(core.handleChannelsList, http.MethodGet, "/api/v1/notify/channels?userId=bob", "", "", reader)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"channelType":"slack"`) {
		t.Fatalf("list another user with read-others = %d %s", rr.Code, rr.Body.String())
	}
}

func TestHTTP_Channels_ListOmitsExpiredPending(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	seedPresence(t, core, devUser, "slack", "{}")
	if err := core.channels.StartConfirmation(t.Context(), devUser, "slack", "{}", "h", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("StartConfirmation: %v", err)
	}
	if ch := findChannel(listChannels(t, core), "slack"); ch == nil || ch.Pending {
		t.Fatalf("an expired code must not show as pending, got %+v", ch)
	}
}

func TestHTTP_ChannelConfirm_TeamsIsTurnedOnAtTheRecordedAddress(t *testing.T) {
	core := newTestCore(t, true, nil)
	sink := deliverSink(t, core.svc.Broker(), "confirm-sink", core.cfg.Namespace, "teams")
	candidate := `{"service_url":"https://smba.trafficmanager.net/","from_id":"29:a"}`
	seedPresence(t, core, devUser, "teams", candidate)

	if rr := startConfirm(t, core, "teams"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d %s", rr.Code, rr.Body.String())
	}
	assertTurnedOnNotice(t, sink(2*time.Second), devUser, "teams", candidate)
	if addr := confirmedAddress(t, core, devUser, "teams"); addr != candidate {
		t.Fatalf("confirmed address = %q, want the teams candidate", addr)
	}
}

func TestHTTP_Channels_EmptyListIsAnArray(t *testing.T) {
	core := newTestCore(t, true, nil)
	rr := do(core, http.MethodGet, "/api/v1/notify/channels", "")
	if !strings.Contains(rr.Body.String(), `"data":[]`) {
		t.Fatalf("an empty list must serialise as an array, got %s", rr.Body.String())
	}
}

func TestHTTP_Channels_UnconfirmedOmitsConfirmedAt(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", "{}")
	if rr := do(core, http.MethodGet, "/api/v1/notify/channels", ""); strings.Contains(rr.Body.String(), "confirmedAt") {
		t.Fatalf("an unconfirmed channel must not carry confirmedAt: %s", rr.Body.String())
	}
}

func TestHTTP_Channels_NoDBReturns503(t *testing.T) {
	core := newTestCore(t, false, nil)
	if rr := do(core, http.MethodGet, "/api/v1/notify/channels", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("list (no db) = %d, want 503", rr.Code)
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("start (no db) = %d, want 503", rr.Code)
	}
	if rr := verifyCode(core, "slack", "ABCD-EFGH"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("verify (no db) = %d, want 503", rr.Code)
	}
	if rr := disableChannel(core, "slack"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("disable (no db) = %d, want 503", rr.Code)
	}
}

func disableChannel(core *NotifyCore, channelType string) *httptest.ResponseRecorder {
	return do(core, http.MethodPost, "/api/v1/notify/channels/"+channelType+"/disable", "")
}

func TestHTTP_ChannelDisable_TurnsTheChannelOffAndKeepsTheUsersChoices(t *testing.T) {
	core := newTestCore(t, true, nil)
	advertise(t, core, a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true})
	sink := deliverSink(t, core.svc.Broker(), "disable-sink", core.cfg.Namespace, "slack")
	candidate := `{"channel_id":"C1","user_id":"U1"}`
	seedPresence(t, core, devUser, "slack", candidate)
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	sink(2 * time.Second)
	if code, body := putCells(core, cell(CategoryChatShare, "slack", false)); code != http.StatusOK {
		t.Fatalf("turn a cell off = %d %s", code, body)
	}

	rr := disableChannel(core, "slack")

	if rr.Code != http.StatusOK {
		t.Fatalf("turn off = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var off UserChannelDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &off); err != nil || off.ChannelType != "slack" || off.Confirmed || off.Pending || off.ConfirmedAt != "" {
		t.Fatalf("turn off response = %+v (%v), want an unconfirmed slack channel", off, err)
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != "" {
		t.Fatalf("confirmed address = %q after turning off, want none", addr)
	}
	for _, ch := range getMatrix(t, core).Channels {
		if ch.ChannelType == "slack" {
			t.Fatalf("the matrix still has a slack column after turning the channel off: %+v", ch)
		}
	}
	if rec := deliverRecord(t, core, CategoryScheduledTask, "slack"); rec.Status != "skipped" {
		t.Fatalf("delivery to a channel that was turned off = %+v, want skipped", rec)
	}

	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on again = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	sink(2 * time.Second)
	m := getMatrix(t, core)
	if c := cellState(t, m, CategoryChatShare, "slack"); c.Enabled {
		t.Errorf("the choice made before turning off was lost: %+v", c)
	}
	if c := cellState(t, m, CategoryScheduledTask, "slack"); !c.Enabled {
		t.Errorf("a type the user never changed is off after turning the channel back on: %+v", c)
	}
}

func confirmFor(t *testing.T, core *NotifyCore, userID, channelType string) {
	t.Helper()
	address := `{"id":"` + channelType + `"}`
	seedPresence(t, core, userID, channelType, address)
	if err := core.channels.ConfirmAddress(t.Context(), userID, channelType, address); err != nil {
		t.Fatalf("confirm %s: %v", channelType, err)
	}
}

func TestHTTP_ChannelDisable_AChannelThatIsAlreadyOffStaysOff(t *testing.T) {
	core := newTestCore(t, true, nil)
	assertOff := func(when string) {
		t.Helper()
		rr := disableChannel(core, "slack")
		var dto UserChannelDTO
		if err := json.Unmarshal(rr.Body.Bytes(), &dto); rr.Code != http.StatusOK || err != nil || dto.ChannelType != "slack" || dto.Confirmed {
			t.Fatalf("turn off %s = %d %s (%v), want 200 and an unconfirmed slack channel", when, rr.Code, rr.Body.String(), err)
		}
	}
	assertOff("with no row")
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	assertOff("with a presence row")
	assertOff("again")
	confirmFor(t, core, devUser, "slack")
	assertOff("a confirmed channel")
	assertOff("a confirmed channel again")
}

func TestHTTP_ChannelDisable_TurnsOffOnlyTheRequestedChannel(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmFor(t, core, devUser, "slack")
	confirmFor(t, core, devUser, "teams")

	rr := disableChannel(core, "teams")

	var dto UserChannelDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); rr.Code != http.StatusOK || err != nil || dto.ChannelType != "teams" || dto.Confirmed {
		t.Fatalf("turn off teams = %d %s (%v), want an unconfirmed teams channel", rr.Code, rr.Body.String(), err)
	}
	if addr := confirmedAddress(t, core, devUser, "teams"); addr != "" {
		t.Errorf("teams confirmed address = %q after turning it off, want none", addr)
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr == "" {
		t.Error("slack was turned off by turning teams off")
	}

	if rr := disableChannel(core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn off slack = %d, want 200", rr.Code)
	}
	if addr := confirmedAddress(t, core, devUser, "slack"); addr != "" {
		t.Errorf("slack confirmed address = %q after turning it off, want none", addr)
	}
}

func TestHTTP_ChannelDisable_StoreFailureIsReportedNotSwallowed(t *testing.T) {
	core := newTestCore(t, true, nil)
	confirmFor(t, core, devUser, "slack")
	if err := core.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	if rr := disableChannel(core, "slack"); rr.Code != http.StatusInternalServerError {
		t.Fatalf("turn off with a failing store = %d %s, want 500", rr.Code, rr.Body.String())
	}
}

func TestHTTP_ChannelDisable_DropsAnOutstandingCode(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)
	held := issueLegacyCode(t, core, devUser, "slack")

	if rr := disableChannel(core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn off = %d, want 200", rr.Code)
	}

	if rr := verifyCode(core, "slack", held); rr.Code != http.StatusBadRequest {
		t.Fatalf("redeeming a code after the channel was turned off = %d, want 400", rr.Code)
	}
}

func TestHTTP_ChannelDisable_Rejections(t *testing.T) {
	core := newTestCore(t, true, nil)
	seedPresence(t, core, "bob", "slack", "{}")
	if err := core.channels.ConfirmAddress(t.Context(), "bob", "slack", "{}"); err != nil {
		t.Fatalf("confirm bob's channel: %v", err)
	}
	admin := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: []string{"notify:_:send_others", "notify:_:read_others"}})

	if rr := disableChannel(core, "email"); rr.Code != http.StatusBadRequest {
		t.Fatalf("a channel type that cannot be turned off = %d, want 400", rr.Code)
	}
	if rr := do(core, http.MethodPost, "/api/v1/notify/channels/slack/disable", `{"userId":"bob"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("a request with a body = %d, want 400", rr.Code)
	}
	if rr := callAs(core.handleChannelDisable, http.MethodPost, "/api/v1/notify/channels/slack/disable?userId=bob", "slack", "", admin); rr.Code != http.StatusForbidden {
		t.Fatalf("turn off for another user = %d, want 403", rr.Code)
	}
	if rr := callAs(core.handleChannelDisable, http.MethodPost, "/api/v1/notify/channels/slack/disable", "slack", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("turn off without an identity = %d, want 401", rr.Code)
	}
	if uc, _ := core.channels.GetByType(t.Context(), "bob", "slack"); uc == nil || !uc.Confirmed() {
		t.Fatalf("bob's channel must be untouched, got %+v", uc)
	}
}
