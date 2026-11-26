// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func notifyEnabledCore(t *testing.T, brk broker.Broker) *entrypoint.GatewayCore {
	t.Helper()
	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "slack-gw"})
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyNotifyService, Default: true})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg)).Services())
	return core
}

func TestSlackNotifyWiring_AdvertisesAndResolves(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := notifyEnabledCore(t, brk)
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("alice@example.com", &goslack.User{ID: "U1", TeamID: "T1"})
	a := &SlackGatewayAdapter{core: core, logger: slog.Default(), api: api}

	a.startNotifyDeliverLoop(t.Context())
	want := a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true, Interactable: true, ResolvesAddress: true}
	if got := core.AdvertisedNotifyCapabilities(); got == nil || *got != want {
		t.Fatalf("advertised = %+v, want %+v", got, want)
	}

	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := brk.Request(t.Context(), a2a.NotifyResolveTopic("test-ns", "slack"), &broker.Message{
		Payload: payload, ReplyTo: a2a.NotifyResolveReplyTopic("test-ns", "r1"),
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("no answer to a resolve request: %v", err)
	}
	var resp a2a.NotifyResolveResponse
	if err := json.Unmarshal(reply.Payload, &resp); err != nil || string(resp.Address) != `{"team_id":"T1","user_id":"U1"}` {
		t.Fatalf("reply = %s (%v), want the user's Slack address", reply.Payload, err)
	}
}

func TestSlackResolveNotifyAddress(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("alice@example.com", &goslack.User{ID: "U1", TeamID: "T1"})
	api.SeedUserByEmail("gone@x.com", &goslack.User{ID: "U2", TeamID: "T1", Deleted: true})
	api.SeedUserByEmail("bot@x.com", &goslack.User{ID: "U3", TeamID: "T1", IsBot: true})
	a := &SlackGatewayAdapter{logger: slog.Default(), api: api}
	resolve := func(email string) a2a.NotifyResolveResponse {
		return a.resolveNotifyAddress(t.Context(), a2a.NotifyResolveRequest{RequestID: "r", UserID: "u", Email: email})
	}

	if got := resolve("alice@example.com"); got.Error != "" || string(got.Address) != `{"team_id":"T1","user_id":"U1"}` {
		t.Errorf("found user = %+v", got)
	}
	for _, email := range []string{"nobody@x.com", "gone@x.com", "bot@x.com"} {
		if got := resolve(email); got.Error != a2a.NotifyResolveNoUser || len(got.Address) != 0 {
			t.Errorf("%s = %+v, want no_user", email, got)
		}
	}
	api.SetNextError("GetUserByEmail", errors.New("missing_scope"))
	if got := resolve("alice@example.com"); got.Error != a2a.NotifyResolveMissingScope {
		t.Errorf("missing scope = %+v", got)
	}
	api.SetNextError("GetUserByEmail", errors.New("ratelimited"))
	if got := resolve("alice@example.com"); got.Error != a2a.NotifyResolveUnavailable {
		t.Errorf("other failure = %+v", got)
	}
	for _, code := range []string{"invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive", "no_permission", "not_allowed_token_type", "access_denied", "team_access_not_granted", "org_login_required"} {
		api.SetNextError("GetUserByEmail", errors.New(code))
		if got := resolve("alice@example.com"); got.Error != a2a.NotifyResolveMissingScope {
			t.Errorf("%s = %+v, want the administrator-action reason", code, got)
		}
	}
	api.SeedUserByEmail("blank@x.com", &goslack.User{})
	if got := resolve("blank@x.com"); got.Error != a2a.NotifyResolveNoUser || len(got.Address) != 0 {
		t.Errorf("an answer with no user id = %+v, want no_user, not an empty address", got)
	}
}

func TestSlackResolveNotifyAddress_ProvesTheDMCanBeOpened(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("alice@example.com", &goslack.User{ID: "U1", TeamID: "T1"})
	a := &SlackGatewayAdapter{logger: slog.Default(), api: api}
	resolve := func() a2a.NotifyResolveResponse {
		return a.resolveNotifyAddress(t.Context(), a2a.NotifyResolveRequest{RequestID: "r", UserID: "u", Email: "alice@example.com"})
	}

	if got := resolve(); got.Error != "" || string(got.Address) != `{"team_id":"T1","user_id":"U1"}` {
		t.Fatalf("a reachable account = %+v", got)
	}
	if !slices.ContainsFunc(api.Calls(), func(call slackapi.RecordedCall) bool {
		return call.Method == "OpenDMChannel" && call.Params["user"] == "U1"
	}) {
		t.Fatalf("the lookup never opened the direct message with the resolved account U1: %+v", api.Calls())
	}

	cases := map[string]string{
		"missing_scope":    a2a.NotifyResolveNoDirectMessage,
		"user_disabled":    a2a.NotifyResolveNoUser,
		"cannot_dm_bot":    a2a.NotifyResolveNoUser,
		"token_revoked":    a2a.NotifyResolveMissingScope,
		"internal_error":   a2a.NotifyResolveUnavailable,
		"ratelimited":      a2a.NotifyResolveUnavailable,
		"user_not_found":   a2a.NotifyResolveNoUser,
		"not_allowed_dm":   a2a.NotifyResolveUnavailable,
		"access_denied":    a2a.NotifyResolveNoDirectMessage,
		"account_inactive": a2a.NotifyResolveMissingScope,
	}
	for code, want := range cases {
		api.SetNextError("OpenDMChannel", errors.New(code))
		if got := resolve(); got.Error != want || len(got.Address) != 0 {
			t.Errorf("OpenDMChannel failing with %s = %+v, want reason %q and no address", code, got, want)
		}
	}
}

func TestSlackResolveNotifyAddress_OpensNoDMWhenThereIsNoAccount(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("gone@example.com", &goslack.User{ID: "U2", TeamID: "T1", Deleted: true})
	api.SeedUserByEmail("bot@example.com", &goslack.User{ID: "U3", TeamID: "T1", IsBot: true})
	api.SeedUserByEmail("blank@example.com", &goslack.User{})
	a := &SlackGatewayAdapter{logger: slog.Default(), api: api}

	for _, email := range []string{"nobody@example.com", "gone@example.com", "bot@example.com", "blank@example.com"} {
		a.resolveNotifyAddress(t.Context(), a2a.NotifyResolveRequest{RequestID: "r", UserID: "u", Email: email})
	}
	api.SetNextError("GetUserByEmail", errors.New("ratelimited"))
	a.resolveNotifyAddress(t.Context(), a2a.NotifyResolveRequest{RequestID: "r", UserID: "u", Email: "nobody@example.com"})

	for _, call := range api.Calls() {
		if call.Method == "OpenDMChannel" {
			t.Errorf("opened a direct message although no account was found: %+v", call)
		}
	}
}

func TestSlackDMFailure(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantLevel  slog.Level
	}{
		{"no permission to send direct messages", goslack.SlackErrorResponse{Err: "missing_scope"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"the same, wrapped", fmt.Errorf("open: %w", goslack.SlackErrorResponse{Err: "missing_scope"}), a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"account disabled", goslack.SlackErrorResponse{Err: "user_disabled"}, a2a.NotifyResolveNoUser, slog.LevelDebug},
		{"no such user", goslack.SlackErrorResponse{Err: "user_not_found"}, a2a.NotifyResolveNoUser, slog.LevelDebug},
		{"a bot", goslack.SlackErrorResponse{Err: "cannot_dm_bot"}, a2a.NotifyResolveNoUser, slog.LevelDebug},
		{"a token type that may not", goslack.SlackErrorResponse{Err: "not_allowed_token_type"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"no permission", goslack.SlackErrorResponse{Err: "no_permission"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"access denied", goslack.SlackErrorResponse{Err: "access_denied"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"an account the token cannot see", goslack.SlackErrorResponse{Err: "user_not_visible"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"a restricted account", goslack.SlackErrorResponse{Err: "restricted_action"}, a2a.NotifyResolveNoDirectMessage, slog.LevelWarn},
		{"revoked token", goslack.SlackErrorResponse{Err: "token_revoked"}, a2a.NotifyResolveMissingScope, slog.LevelError},
		{"unknown code", goslack.SlackErrorResponse{Err: "internal_error"}, a2a.NotifyResolveUnavailable, slog.LevelWarn},
	}
	for _, tc := range cases {
		reason, level := slackDMFailure(tc.err)
		if reason != tc.wantReason || level != tc.wantLevel {
			t.Errorf("%s: (%q, %v), want (%q, %v)", tc.name, reason, level, tc.wantReason, tc.wantLevel)
		}
	}
}

func TestSlackResolveFailure(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
		wantLevel  slog.Level
	}{
		{"no such user", goslack.SlackErrorResponse{Err: "users_not_found"}, a2a.NotifyResolveNoUser, slog.LevelDebug},
		{"no such user, wrapped", fmt.Errorf("lookup: %w", goslack.SlackErrorResponse{Err: "users_not_found"}), a2a.NotifyResolveNoUser, slog.LevelDebug},
		{"missing scope", goslack.SlackErrorResponse{Err: "missing_scope"}, a2a.NotifyResolveMissingScope, slog.LevelWarn},
		{"revoked token", goslack.SlackErrorResponse{Err: "token_revoked"}, a2a.NotifyResolveMissingScope, slog.LevelError},
		{"rate limited", errors.New("slack rate limit exceeded, retry after 30s"), a2a.NotifyResolveUnavailable, slog.LevelWarn},
		{"unknown code", goslack.SlackErrorResponse{Err: "internal_error"}, a2a.NotifyResolveUnavailable, slog.LevelWarn},
		{"a code only mentioned in other text", errors.New("Post https://slack.com/api/users.lookupByEmail?hint=users_not_found: timeout"), a2a.NotifyResolveUnavailable, slog.LevelWarn},
	}
	for _, tc := range cases {
		reason, level := slackResolveFailure(tc.err)
		if reason != tc.wantReason || level != tc.wantLevel {
			t.Errorf("%s: (%q, %v), want (%q, %v)", tc.name, reason, level, tc.wantReason, tc.wantLevel)
		}
	}
}

func TestSlackPublishPresence_OnlyWhileTheResolverIsUnbound(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	q, err := brk.CreateQueue("presence-sink", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyPresenceSubscription("test-ns")); err != nil {
		t.Fatal(err)
	}
	core := notifyEnabledCore(t, brk)
	a := &SlackGatewayAdapter{core: core, logger: slog.Default(), api: slackapi.NewInMemorySlackAPI()}
	published := func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil || msg == nil {
			return false
		}
		msg.Ack()
		return true
	}

	core.StartNotifyDeliverConsumer(t.Context(), notifyDeliverOptions, slog.Default(), func(a2a.NotifyDeliver) {})
	a.publishPresence(t.Context(), "u1", "u1@x.com", slackProactiveAddress{TeamID: "T1", UserID: "U1"})
	if !published() {
		t.Fatal("no presence published while the resolver is not bound")
	}

	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), a.resolveNotifyAddress)
	a.publishPresence(t.Context(), "u1", "u1@x.com", slackProactiveAddress{TeamID: "T1", UserID: "U1"})
	if published() {
		t.Fatal("presence published although the gateway resolves addresses itself")
	}
}

func TestSlackNotifyDeliverOptions(t *testing.T) {
	want := entrypoint.NotifyDeliverOptions{GatewayType: "slack", Interactable: true, ApproverSets: true}
	if notifyDeliverOptions != want {
		t.Errorf("notifyDeliverOptions = %+v, want %+v", notifyDeliverOptions, want)
	}
}

func slackAddr(t *testing.T) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(slackProactiveAddress{ChannelID: "C123", UserID: "U1", TeamID: "T1"})
	return b
}

func TestSlackStartNotifyDeliverLoop_DisabledNoop(t *testing.T) {
	a, _, _ := newHandlerTestAdapter(t)
	a.startNotifyDeliverLoop(a.ctx)
}

func TestSlackProactiveDeliver_PostsHILAndNotice(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-h", Mode: a2a.NotifyModeInput, ChannelType: "slack",
		Address: slackAddr(t), Surface: json.RawMessage(askSurfaceJSON),
	})
	if calls.Posts() != 1 {
		t.Fatalf("HIL deliver should post once, got %d", calls.Posts())
	}

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-n", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: slackAddr(t), Title: "Heads up", Message: "report ready",
	})
	if calls.Posts() != 2 {
		t.Fatalf("notice deliver should post again, got %d", calls.Posts())
	}
	if got := calls.OpenDMUsers(); len(got) == 0 || got[len(got)-1] != "U1" {
		t.Fatalf("expected a DM opened for U1, got %v", got)
	}
	if ch := calls.LastPostChannel(); ch != "D-U1" {
		t.Fatalf("post targeted %q, want the opened DM D-U1 (never the last-seen channel C123)", ch)
	}
}

func TestSlackProactiveDeliver_DMOpenFailureSkips(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	calls.FailOpenDM()
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-dmfail", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: slackAddr(t), Title: "Heads up", Message: "report ready",
	})
	if calls.Posts() != 0 {
		t.Fatalf("DM-open failure must skip out-of-band delivery, got %d posts", calls.Posts())
	}
}

func TestSlackProactiveDeliver_AddressLessDropped(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-bad", Mode: a2a.NotifyModeInput, ChannelType: "slack",
		Address: json.RawMessage(`{}`), Surface: json.RawMessage(askSurfaceJSON),
	})
	if calls.Posts() != 0 {
		t.Fatalf("address-less command must not post, got %d", calls.Posts())
	}
}

func TestSlackProactiveDeliver_HILBadSurfaceDropped(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-bs", Mode: a2a.NotifyModeInput, ChannelType: "slack",
		Address: slackAddr(t), Surface: json.RawMessage(`123`),
	})
	if calls.Posts() != 0 {
		t.Fatalf("bad surface must not post, got %d", calls.Posts())
	}
}

func TestSlackProactiveNotice_TextVariants(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "r1", Mode: a2a.NotifyModeNotify, ChannelType: "slack", Address: slackAddr(t), Title: "T", Message: "M"})
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "r2", Mode: a2a.NotifyModeNotify, ChannelType: "slack", Address: slackAddr(t)})
	if calls.Posts() != 2 {
		t.Fatalf("both notices should post, got %d", calls.Posts())
	}
}

func TestSlackProactiveNotice_ConvertsMarkdown(t *testing.T) {
	cases := []struct {
		name    string
		convert bool
		title   string
		message string
		want    string
	}{
		{"converted", true, "Scheduled task: Report", "- **Headers**: levels 1-6", "Scheduled task: Report\n- *Headers*: levels 1-6"},
		{"a link is left as written", true, "Scheduled task: Report", "See [the report](https://example.com/r) for **details**.", "Scheduled task: Report\nSee [the report](https://example.com/r) for *details*."},
		{"a Markdown link in a shared chat's title keeps its URL visible", true, "Ada shared a chat with you", "\"[Click here](https://evil.example)\"\nhttps://sam.example/share/1", "Ada shared a chat with you\n\"[Click here](https://evil.example)\"\nhttps://sam.example/share/1"},
		{"a URL with parentheses is not cut short", true, "Scheduled task: Report", "See [Wiki](https://en.wikipedia.org/wiki/Go_(language)).", "Scheduled task: Report\nSee [Wiki](https://en.wikipedia.org/wiki/Go_(language))."},
		{"left as written", false, "Scheduled task: Report", "- **Headers**: levels 1-6", "Scheduled task: Report\n- **Headers**: levels 1-6"},
		{"plain text is unchanged", true, "Scheduled task: Report", "Revenue grew 5% this week.", "Scheduled task: Report\nRevenue grew 5% this week."},
		{"the title is posted as written", true, "**Q3** [r](http://x.io)", "**Done**", "**Q3** [r](http://x.io)\n*Done*"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, calls, _ := newHandlerTestAdapter(t)
			a.cfg.CorrectMarkdown = c.convert
			a.handleNotifyDeliver(a2a.NotifyDeliver{
				RequestID: "req-md", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
				Address: slackAddr(t), Title: c.title, Message: c.message,
			})
			if got := lastPostedText(t, calls); got != c.want {
				t.Errorf("posted %q, want %q", got, c.want)
			}
		})
	}
}

func TestSlackProactiveNotice_ConvertsThenChunks(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.CorrectMarkdown = true
	long := strings.Repeat("**two words** ", slackMaxMessageRunes)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-long-md", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: slackAddr(t), Title: "Result", Message: long,
	})
	if calls.Posts() < 2 {
		t.Fatalf("a body over the Slack limit should be split into multiple posts, got %d", calls.Posts())
	}
	for i, got := range calls.PostTexts() {
		if strings.Contains(got, "**") {
			t.Errorf("post %d still carries literal asterisks: %q", i, got)
		}
	}
}

func TestSlackProactiveNotice_ChunksLongBody(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	long := strings.Repeat("word ", slackMaxMessageRunes)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-long", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: slackAddr(t), Title: "Result", Message: long,
	})
	if calls.Posts() < 2 {
		t.Fatalf("a body over the Slack limit should be split into multiple posts, got %d", calls.Posts())
	}
}

func TestSlackProactiveNotice_ArtifactsNoServiceGraceful(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-art", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: slackAddr(t), Title: "Report", Message: "done",
		Artifacts: []a2a.NotifyArtifact{{AgentName: "A", UserID: "u", SessionID: "s", Filename: "chart.png"}},
	})
	if calls.Posts() < 1 {
		t.Fatalf("notice with artifacts should still post its text, got %d posts", calls.Posts())
	}
}

func TestSlackProactiveHIL_DegradeAndNoTitle(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)

	long := strings.Repeat("x", 80)
	oversized := `{"surfaceId":"s","components":[
	  {"id":"q0","component":"ChoicePicker","variant":"mutuallyExclusive","options":[{"label":{"literalString":"l"},"value":"` + long + `"}],"value":{"path":"/answers/q0"}},
	  {"id":"submit-text","component":"Text","text":{"literalString":"Submit"}},
	  {"id":"submit-btn","component":"Button","child":"submit-text","action":{"event":{"name":"submit","context":{}}}}],"dataModel":{}}`
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "req-os", Mode: a2a.NotifyModeInput, ChannelType: "slack", Address: slackAddr(t), Surface: json.RawMessage(oversized)})
	if calls.Posts() != 1 {
		t.Fatalf("oversized surface should post one web-UI notice, got %d posts", calls.Posts())
	}
	if text := calls.PostTexts()[0]; !strings.Contains(text, "web UI") {
		t.Errorf("degraded prompt text = %q, want a pointer to the web UI", text)
	}
	if blocks := calls.LastPostBlocks(); strings.Contains(blocks, HILInputActionPrefix) {
		t.Errorf("degraded prompt carries buttons: %s", blocks)
	}

	noTitle := `{"surfaceId":"s","components":[
	  {"id":"submit-text","component":"Text","text":{"literalString":"OK"}},
	  {"id":"submit-btn","component":"Button","child":"submit-text","action":{"event":{"name":"submit","context":{}}}}],"dataModel":{}}`
	a.handleNotifyDeliver(a2a.NotifyDeliver{RequestID: "req-nt", Mode: a2a.NotifyModeInput, ChannelType: "slack", Address: slackAddr(t), Surface: json.RawMessage(noTitle)})
	if calls.Posts() != 2 {
		t.Fatalf("title-less HIL should post once with the default header, got %d posts in all", calls.Posts())
	}
}

func TestSlackProactive_PublishReplyErrorIsBestEffort(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	_ = brk.Disconnect(context.Background())
	_ = a.publishNotifyReply(a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r", Status: "answered"})
}

func TestSlackProactiveReply_PublishFailureNotifiesUser(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	s := parseSurfaceT(t)
	blocks, _ := a.buildHILBlocks(s, "req-pf", "", "", true)
	submitVal := submitButtonValue(t, blocks, "submit-btn")
	_ = brk.Disconnect(context.Background())

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.1"},
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
			},
		},
	}
	before := calls.Posts()
	a.handleHILInput(callback, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitVal})
	if calls.Posts() <= before {
		t.Fatal("a failed reply publish should post a corrective message to the user")
	}
}

func TestSlackProactiveReply_CancelRoutesToNotify(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	s := parseSurfaceT(t)
	blocks, _ := a.buildHILBlocks(s, "req-c", "", "", true)
	cancelVal := submitButtonValue(t, blocks, "cancel-btn")

	q, err := brk.CreateQueue("notify-cancel", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplyTopic("test-ns", "req-c")); err != nil {
		t.Fatal(err)
	}

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.1"},
	}
	a.handleHILInput(callback, &goslack.BlockAction{ActionID: HILInputActionPrefix + "cancel-btn", Value: cancelVal})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
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

func TestSlackProactiveReply_RoutesToNotify(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	s := parseSurfaceT(t)
	blocks, ok := a.buildHILBlocks(s, "req-p", "", "", true)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	submitVal := submitButtonValue(t, blocks, "submit-btn")

	q, err := brk.CreateQueue("notify-reply", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplyTopic("test-ns", "req-p")); err != nil {
		t.Fatal(err)
	}

	callback := goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: "C123", MessageTs: "1700000000.1"},
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
			},
		},
	}
	a.handleHILInput(callback, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitVal})

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
