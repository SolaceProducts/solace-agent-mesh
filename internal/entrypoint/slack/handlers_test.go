// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func newTestSocketClient(t *testing.T) (*socketmode.Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-test"})
	})
	srv := httptest.NewServer(mux)
	api := goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/"))
	client := socketmode.New(api)
	return client, srv
}

func TestRegisterSocketHandlers_ContextCancelExitsGoroutine(t *testing.T) {
	t.Parallel()
	client, srv := newTestSocketClient(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	a := &SlackGatewayAdapter{ctx: ctx, cancel: cancel, logger: testLogger()}
	a.registerSocketHandlers(client)
	cancel()

	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler goroutine did not exit after context cancellation")
	}
}

func TestRegisterSocketHandlers_ClosedChannelExitsGoroutine(t *testing.T) {
	t.Parallel()
	client, srv := newTestSocketClient(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := &SlackGatewayAdapter{ctx: ctx, cancel: cancel, logger: testLogger()}
	a.registerSocketHandlers(client)
	close(client.Events)

	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler goroutine did not exit after Events channel close")
	}
}

func TestRegisterSocketHandlers_MultipleEventsProcessed(t *testing.T) {
	t.Parallel()
	client, srv := newTestSocketClient(t)
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	a := &SlackGatewayAdapter{ctx: ctx, cancel: cancel, logger: testLogger()}
	a.registerSocketHandlers(client)

	events := []socketmode.Event{
		{Type: socketmode.EventTypeHello, Request: &socketmode.Request{Type: "hello"}},
		{Type: socketmode.EventTypeDisconnect, Request: &socketmode.Request{Type: "disconnect"}},
		{Type: socketmode.EventType("unknown"), Request: nil},
		{Type: socketmode.EventType("unknown_with_env"), Request: &socketmode.Request{EnvelopeID: "e1"}},
		{Type: socketmode.EventTypeHello, Request: &socketmode.Request{Type: "hello"}},
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for _, evt := range events {
			client.Events <- evt
		}
	})
	wg.Wait()

	time.Sleep(100 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after processing mixed events")
	}
}

func TestBuildApprovalResultBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		original       []goslack.Block
		resultText     string
		wantBlockCount int
	}{
		{
			name: "FiltersActions",
			original: []goslack.Block{
				goslack.NewContextBlock("header", goslack.NewTextBlockObject(goslack.MarkdownType, "Header", false, false)),
				goslack.NewActionBlock("actions",
					goslack.NewButtonBlockElement("approve", "yes",
						goslack.NewTextBlockObject(goslack.PlainTextType, "Approve", false, false))),
				goslack.NewSectionBlock(
					goslack.NewTextBlockObject(goslack.MarkdownType, "Details", false, false), nil, nil),
			},
			resultText:     "*APPROVED* by <@U123>",
			wantBlockCount: 3,
		},
		{
			name:           "EmptyOriginal",
			original:       nil,
			resultText:     "*DENIED* by <@U456>",
			wantBlockCount: 1,
		},
		{
			name: "MultipleActions",
			original: []goslack.Block{
				goslack.NewContextBlock("ctx", goslack.NewTextBlockObject(goslack.MarkdownType, "Info", false, false)),
				goslack.NewActionBlock("actions1",
					goslack.NewButtonBlockElement("a1", "v1",
						goslack.NewTextBlockObject(goslack.PlainTextType, "B1", false, false))),
				goslack.NewActionBlock("actions2",
					goslack.NewButtonBlockElement("a2", "v2",
						goslack.NewTextBlockObject(goslack.PlainTextType, "B2", false, false))),
			},
			resultText:     "Done",
			wantBlockCount: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := buildApprovalResultBlocks(tc.original, tc.resultText)
			if len(result) != tc.wantBlockCount {
				t.Fatalf("got %d blocks, want %d", len(result), tc.wantBlockCount)
			}
			for _, b := range result {
				if b.BlockType() == goslack.MBTAction {
					t.Error("action block should have been removed")
				}
			}
			lastText := extractBlockText(t, result[len(result)-1])
			if lastText != tc.resultText {
				t.Errorf("result text = %q, want %q", lastText, tc.resultText)
			}
		})
	}
}

type ephemeralCall struct{ Channel, User, Text string }

func (c *slackAPICalls) Ephemerals() []ephemeralCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ephemeralCall(nil), c.ephemerals...)
}

type updateCall struct {
	Channel, TS, Blocks string
}

func (c *slackAPICalls) LastUpdate() updateCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastUpdate
}

type slackAPICalls struct {
	mu                 sync.Mutex
	ephemerals         []ephemeralCall
	postCalls          int
	updateCalls        int
	deleteCalls        int
	lastUpdateText     string
	lastUpdate         updateCall
	lastPostBlocks     string
	lastPostText       string
	postTexts          []string
	postThreadTSs      []string
	failBlockPosts     atomic.Bool
	failAllPosts       atomic.Bool
	noProfileEmail     atomic.Bool
	lastPostChannel    string
	openDMUsers        []string
	failOpenDM         bool
	updateTextByTS     map[string]string
	sessionStatusCalls []sessionStatusCall
	failSessionStatus  atomic.Bool
}

type sessionStatusCall struct {
	Channel  string
	ThreadTS string
	Status   string
}

func (c *slackAPICalls) SessionStatuses() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sessionStatusCalls))
	for i, call := range c.sessionStatusCalls {
		out[i] = call.Status
	}
	return out
}

func (c *slackAPICalls) SessionStatusCalls() []sessionStatusCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sessionStatusCall(nil), c.sessionStatusCalls...)
}

func (c *slackAPICalls) Posts() int { c.mu.Lock(); defer c.mu.Unlock(); return c.postCalls }

func (c *slackAPICalls) PostTexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.postTexts...)
}

func (c *slackAPICalls) PostThreadTSs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.postThreadTSs...)
}

func (c *slackAPICalls) LastPostChannel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPostChannel
}

func (c *slackAPICalls) OpenDMUsers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.openDMUsers...)
}

func (c *slackAPICalls) FailOpenDM() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failOpenDM = true
}
func (c *slackAPICalls) Updates() int { c.mu.Lock(); defer c.mu.Unlock(); return c.updateCalls }
func (c *slackAPICalls) Deletes() int { c.mu.Lock(); defer c.mu.Unlock(); return c.deleteCalls }
func (c *slackAPICalls) LastUpdateText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastUpdateText
}

func (c *slackAPICalls) UpdateTextForTS(ts string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.updateTextByTS[ts]
}

func (c *slackAPICalls) LastPostBlocks() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPostBlocks
}

type recordingGWAdapter struct{}

func (r *recordingGWAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}
func (r *recordingGWAdapter) Init(_ context.Context, _ *entrypoint.GatewayCore) error { return nil }
func (r *recordingGWAdapter) Start(_ context.Context) error                           { return nil }
func (r *recordingGWAdapter) Stop(_ context.Context) error                            { return nil }
func (r *recordingGWAdapter) OnStatusUpdate(_ context.Context, _ string, _ *a2a.TaskStatusUpdateEvent, _ []byte) {
}
func (r *recordingGWAdapter) OnArtifactUpdate(_ context.Context, _ string, _ *a2a.TaskArtifactUpdateEvent, _ []byte) {
}
func (r *recordingGWAdapter) OnFinalResponse(_ context.Context, _ string, _ *a2a.Task, _ []byte) {}
func (r *recordingGWAdapter) OnError(_ context.Context, _ string, _ *a2a.Error, _ []byte)        {}
func (r *recordingGWAdapter) OnAgentDiscovered(_ *a2a.AgentCard)                                 {}
func (r *recordingGWAdapter) OnAgentRemoved(_ string)                                            {}

func newHandlerTestAdapter(t *testing.T) (*SlackGatewayAdapter, *slackAPICalls, *broker.DevBroker) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	calls := &slackAPICalls{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.noProfileEmail.Load() {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"real_name": "No Email"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"email": "test@test.com"}})
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if calls.failAllPosts.Load() || (calls.failBlockPosts.Load() && r.FormValue("blocks") != "") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_blocks"})
			return
		}
		calls.mu.Lock()
		calls.postCalls++
		calls.lastPostBlocks = r.FormValue("blocks")
		calls.lastPostText = r.FormValue("text")
		calls.postTexts = append(calls.postTexts, r.FormValue("text"))
		calls.postThreadTSs = append(calls.postThreadTSs, r.FormValue("thread_ts"))
		calls.lastPostChannel = r.FormValue("channel")
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": r.FormValue("channel"), "ts": "1700000000.000010"})
	})
	mux.HandleFunc("/conversations.open", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := r.FormValue("users")
		calls.mu.Lock()
		calls.openDMUsers = append(calls.openDMUsers, user)
		fail := calls.failOpenDM
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"id": "D-" + user}})
	})
	mux.HandleFunc("/chat.postEphemeral", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls.mu.Lock()
		calls.ephemerals = append(calls.ephemerals, ephemeralCall{Channel: r.FormValue("channel"), User: r.FormValue("user"), Text: r.FormValue("text")})
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message_ts": "1700000000.000020"})
	})
	mux.HandleFunc("/chat.update", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls.mu.Lock()
		calls.updateCalls++
		calls.lastUpdateText = r.FormValue("text")
		calls.lastUpdate = updateCall{Channel: r.FormValue("channel"), TS: r.FormValue("ts"), Blocks: r.FormValue("blocks")}
		if calls.updateTextByTS == nil {
			calls.updateTextByTS = make(map[string]string)
		}
		calls.updateTextByTS[r.FormValue("ts")] = r.FormValue("text")
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000010"})
	})
	mux.HandleFunc("/chat.delete", func(w http.ResponseWriter, _ *http.Request) {
		calls.mu.Lock()
		calls.deleteCalls++
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000010"})
	})
	mux.HandleFunc("/agents.sessions.setStatus", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls.mu.Lock()
		calls.sessionStatusCalls = append(calls.sessionStatusCalls, sessionStatusCall{
			Channel:  r.FormValue("channel_id"),
			ThreadTS: r.FormValue("thread_ts"),
			Status:   r.FormValue("status"),
		})
		calls.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if calls.failSessionStatus.Load() {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not_supported"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	for _, path := range []string{"/chat.startStream", "/chat.appendStream", "/chat.stopStream"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "1700000000.000010"})
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	api := slackapi.NewSlackAPIAdapter(goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/")), "xoxb-test",
		slackapi.WithDisabledRateLimiting(), slackapi.WithAPIBaseURL(srv.URL+"/"))
	a := &SlackGatewayAdapter{
		core:             core,
		cfg:              &SlackConfig{RunAs: "default"},
		api:              api,
		emailCache:       NewEmailCache(api, 0, testLogger()),
		hilDedup:         runtime.NewMapCache(),
		inFlightMentions: make(map[string]mentionSlot),
		logger:           testLogger(),
		ctx:              ctx,
		cancel:           cancel,
		queues:           make(map[string]*MessageQueue),
	}
	return a, calls, brk
}

func TestHandleMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		ev        *slackevents.MessageEvent
		addAgent  bool
		wantPosts int
	}{
		{
			name:      "UsesTimestampAsThreadTS",
			ev:        &slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "C1", Text: "new thread", TimeStamp: "1700000010.000001"},
			addAgent:  true,
			wantPosts: 0,
		},
		{
			name:      "CommandInMessage",
			ev:        &slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "C1", Text: "!help", TimeStamp: "1700000080.000001"},
			wantPosts: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			if tc.addAgent {
				a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
			}
			a.HandleMessage(tc.ev, "T1")
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}

func TestHandleMessage_DM_SetsSessionProcessing(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U1", Channel: "C1", Text: "hello",
		TimeStamp: "1700000010.000001",
	}, "T1")

	if got := calls.SessionStatuses(); !slices.Equal(got, []string{"processing"}) {
		t.Errorf("agents.sessions.setStatus calls = %v, want [processing]", got)
	}
}

func TestHandleMessage_DM_SessionStatusFailureDoesNotBlockDelivery(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
	a.cfg.InitialStatusMessage = "Got it, working on it..."
	calls.failSessionStatus.Store(true)

	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U1", Channel: "C1", Text: "hello",
		TimeStamp: "1700000010.000001",
	}, "T1")

	if calls.Posts() != 1 {
		t.Errorf("posts = %d, want 1 (delivery must proceed despite the setStatus failure)", calls.Posts())
	}
}

func TestHandleAppMention_NeverAcquiresSessionStatus(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	a.HandleAppMention(&slackevents.AppMentionEvent{
		User: "U1", Channel: "C1", Text: "<@UBOT> hello",
		TimeStamp: "1700000010.000001",
	}, "T1")

	if got := calls.SessionStatuses(); len(got) != 0 {
		t.Errorf("agents.sessions.setStatus calls = %v, want none (channel mention is never a DM)", got)
	}
}

func TestHandleMessage_DM_AcquireThenRelease_OrderedPair(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U1", Channel: "C1", Text: "hello",
		TimeStamp: "1700000010.000001",
	}, "T1")

	taskIDs := a.core.Tasks().AllByAgent("TestAgent")
	if len(taskIDs) != 1 {
		t.Fatalf("tasks for TestAgent = %v, want exactly 1", taskIDs)
	}
	taskID := taskIDs[0]

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{ID: taskID, Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}, nil)

	if got := calls.SessionStatuses(); !slices.Equal(got, []string{"processing", "active"}) {
		t.Errorf("agents.sessions.setStatus calls = %v, want [processing active]", got)
	}
	for _, call := range calls.SessionStatusCalls() {
		if call.Channel != "C1" || call.ThreadTS != "1700000010.000001" {
			t.Errorf("call %+v: channel/threadTS mismatch, want C1/1700000010.000001", call)
		}
	}
}

func TestHandleMessageOnlyRespondsToDMs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		channelType   string
		threadTS      string
		wantSubmitted bool
	}{
		{name: "DirectMessage", channelType: "im", wantSubmitted: true},
		{name: "DirectMessageThreadReply", channelType: "im", threadTS: "1700000000.000001", wantSubmitted: true},
		{name: "ChannelMessage", channelType: "channel", wantSubmitted: false},
		{name: "ChannelThreadReply", channelType: "channel", threadTS: "1700000000.000001", wantSubmitted: false},
		{name: "PrivateGroupMessage", channelType: "group", wantSubmitted: false},
		{name: "MultipartyDM", channelType: "mpim", wantSubmitted: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			a.cfg.InitialStatusMessage = "thinking..."
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

			ev := &slackevents.MessageEvent{
				User:            "U1",
				Channel:         "C1",
				Text:            "hello",
				TimeStamp:       "1700000010.000001",
				ThreadTimeStamp: tc.threadTS,
				ChannelType:     tc.channelType,
			}
			a.HandleMessage(ev, "T1")

			gotSubmitted := calls.Posts() > 0
			if gotSubmitted != tc.wantSubmitted {
				t.Errorf("submitted = %v, want %v (posts=%d)", gotSubmitted, tc.wantSubmitted, calls.Posts())
			}
		})
	}
}

func TestBotIdentityFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		botID         string
		user          string
		subType       string
		resolved      bool
		wantSubmitted bool
	}{
		{name: "PureBotMessage_xoxb", botID: "B1", user: "", resolved: true, wantSubmitted: false},
		{name: "BotMessageSubtype", botID: "", user: "U1", subType: "bot_message", resolved: true, wantSubmitted: false},
		{name: "UserViaApp_xoxp", botID: "B1", user: "U1", resolved: true, wantSubmitted: true},
		{name: "PlainUser", botID: "", user: "U1", resolved: true, wantSubmitted: true},
		{name: "NoIdentity", botID: "", user: "", resolved: true, wantSubmitted: true},
		{name: "SelfAuthored_byUserID", botID: "B1", user: "U-bot", resolved: true, wantSubmitted: false},
		{name: "SelfAuthored_byBotID", botID: "B-self", user: "U-weird", resolved: true, wantSubmitted: false},
		{name: "FailClosed_xoxpDropped", botID: "B1", user: "U1", resolved: false, wantSubmitted: false},
		{name: "FailClosed_plainUserSubmits", botID: "", user: "U1", resolved: false, wantSubmitted: true},
	}
	for _, tc := range tests {
		t.Run("Message_"+tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			a.cfg.InitialStatusMessage = "thinking..."
			if tc.resolved {
				a.botUserID = "U-bot"
				a.botID = "B-self"
				a.identityResolved = true
			}
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

			ev := &slackevents.MessageEvent{
				BotID:       tc.botID,
				User:        tc.user,
				SubType:     tc.subType,
				ChannelType: "im",
				Channel:     "C1",
				Text:        "hello",
				TimeStamp:   "1700000010.000001",
			}
			a.HandleMessage(ev, "T1")

			if got := calls.Posts() > 0; got != tc.wantSubmitted {
				t.Errorf("message submitted = %v, want %v (posts=%d)", got, tc.wantSubmitted, calls.Posts())
			}
		})
	}
}

func TestHandleAppMentionBotIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		botID         string
		user          string
		resolved      bool
		wantSubmitted bool
	}{
		{name: "PureBot", botID: "B1", user: "", resolved: true, wantSubmitted: false},
		{name: "UserViaApp_xoxp", botID: "B1", user: "U1", resolved: true, wantSubmitted: true},
		{name: "PlainUser", botID: "", user: "U1", resolved: true, wantSubmitted: true},
		{name: "SelfAuthored_byUserID", botID: "B1", user: "U-bot", resolved: true, wantSubmitted: false},
		{name: "SelfAuthored_byBotID", botID: "B-self", user: "U-weird", resolved: true, wantSubmitted: false},
		{name: "FailClosed_xoxpDropped", botID: "B1", user: "U1", resolved: false, wantSubmitted: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			a.cfg.InitialStatusMessage = "thinking..."
			if tc.resolved {
				a.botUserID = "U-bot"
				a.botID = "B-self"
				a.identityResolved = true
			}
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

			ev := &slackevents.AppMentionEvent{
				BotID:     tc.botID,
				User:      tc.user,
				Channel:   "C1",
				Text:      "<@UBOT> hi",
				TimeStamp: "1700000020.000001",
			}
			a.HandleAppMention(ev, "T1")

			if got := calls.Posts() > 0; got != tc.wantSubmitted {
				t.Errorf("mention submitted = %v, want %v (posts=%d)", got, tc.wantSubmitted, calls.Posts())
			}
		})
	}
}

func TestHandleMessageWithFiles(t *testing.T) {
	t.Parallel()
	a, _, _ := newHandlerTestAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ev := &slackevents.MessageEvent{
		ChannelType: "im",
		User:        "U1",
		Channel:     "C1",
		Text:        "here is a file",
		TimeStamp:   "1700000099.000001",
	}
	a.HandleMessageWithFiles(ev, "T1", []TestFileInfo{
		{ID: "F1", Name: "test.txt", MimeType: "text/plain", URLPrivateDownload: "https://files.slack.com/test.txt"},
	})
}

func TestHandleAppMention(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		ev        *slackevents.AppMentionEvent
		addAgent  bool
		wantPosts int
	}{
		{
			name:      "SkipsPureBotMention",
			ev:        &slackevents.AppMentionEvent{BotID: "B999", Channel: "C1", Text: "hi", TimeStamp: "t1"},
			wantPosts: 0,
		},
		{
			name:     "StripsBotMention",
			ev:       &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> tell me a joke", TimeStamp: "1700000020.000001"},
			addAgent: true,
		},
		{
			name:      "CommandInMention",
			ev:        &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> !artifacts", TimeStamp: "1700000090.000001"},
			wantPosts: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			if tc.addAgent {
				a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
			}
			a.HandleAppMention(tc.ev, "T1")
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}

func TestHandleAppMentionStillRespondsInChannelThread(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ev := &slackevents.AppMentionEvent{
		User:            "U1",
		Channel:         "C1",
		Text:            "<@UBOT> what's up",
		TimeStamp:       "1700000200.000001",
		ThreadTimeStamp: "1700000100.000001",
	}
	a.HandleAppMention(ev, "T1")

	if calls.Posts() == 0 {
		t.Error("app_mention in a channel thread was ignored; want a submitted task")
	}
}

func TestHandleAppMention_EditWhileInFlight_Dropped(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	original := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> summarize", TimeStamp: "1700000300.000001"}
	a.HandleAppMention(original, "T1")
	if got := calls.Posts(); got != 1 {
		t.Fatalf("after original mention: posts = %d, want 1 (one task submitted)", got)
	}

	edit := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> summarize!", TimeStamp: "1700000300.000001", Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000305.000000"}}
	a.HandleAppMention(edit, "T1")
	if got := calls.Posts(); got != 1 {
		t.Fatalf("editing while the original is in flight: posts = %d, want 1 (re-fired app_mention must be dropped)", got)
	}

	addedMention := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> now look", TimeStamp: "1700000400.000002", Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000405.000000"}}
	a.HandleAppMention(addedMention, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("mention on a different message: posts = %d, want 2 (new message ts must be handled)", got)
	}
}

func TestHandleAppMention_EditAfterCompletion_ReAnswered(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	original := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> summarize", TimeStamp: "1700000600.000001"}
	a.HandleAppMention(original, "T1")
	if got := calls.Posts(); got != 1 {
		t.Fatalf("original mention: posts = %d, want 1", got)
	}

	ids := a.core.Tasks().AllByAgent("TestAgent")
	if len(ids) != 1 {
		t.Fatalf("registered tasks = %d, want 1", len(ids))
	}
	a.cleanupTask(ids[0])

	edit := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> actually, translate it", TimeStamp: "1700000600.000001", Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000650.000000"}}
	a.HandleAppMention(edit, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("edit after completion: posts = %d, want 2 (a completed message's edit is answered afresh)", got)
	}
}

func TestHandleAppMention_TrackingIsPerChannel(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: "1700000500.000001"}, "T1")
	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C2", Text: "<@UBOT> hi", TimeStamp: "1700000500.000001"}, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("same ts in two channels: posts = %d, want 2 (per-channel key must not collide)", got)
	}
}

func TestHandleAppMention_EmptyTimeStampBypassesTracking(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> a", TimeStamp: ""}, "T1")
	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> b", TimeStamp: ""}, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("empty-ts mentions: posts = %d, want 2 (must not be tracked onto one key)", got)
	}
}

func TestHandleAppMention_SubmitFailureReleasesSlot(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."

	ts := "1700000700.000001"
	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts}, "T1")
	if got := calls.Posts(); got != 1 {
		t.Fatalf("no-agent submit: posts = %d, want 1 (error message)", got)
	}

	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
	edit := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts, Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000705.000000"}}
	a.HandleAppMention(edit, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("retry after a failed submit: posts = %d, want 2 (slot must have been released)", got)
	}
}

func TestHandleAppMention_IsRunningBackstopReclaims(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ts := "1700000800.000001"
	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts}, "T1")
	ids := a.core.Tasks().AllByAgent("TestAgent")
	if len(ids) != 1 {
		t.Fatalf("registered tasks = %d, want 1", len(ids))
	}
	a.core.Tasks().Remove(ids[0])

	edit := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts, Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000805.000000"}}
	a.HandleAppMention(edit, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("edit after task vanished: posts = %d, want 2 (IsRunning=false must reclaim the slot)", got)
	}
}

func TestHandleAppMention_StaleSlotReclaimedByAgeCap(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ts := "1700000900.000001"
	key := "C1:" + ts
	a.HandleAppMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts}, "T1")

	a.mentionMu.Lock()
	slot := a.inFlightMentions[key]
	slot.reservedAt = slot.reservedAt.Add(-2 * mentionSlotMaxAge)
	a.inFlightMentions[key] = slot
	a.mentionMu.Unlock()

	edit := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: ts, Edited: &slackevents.Edited{User: "U1", TimeStamp: "1700000905.000000"}}
	a.HandleAppMention(edit, "T1")
	if got := calls.Posts(); got != 2 {
		t.Fatalf("edit after slot aged out: posts = %d, want 2 (age cap must reclaim a never-terminating task's slot)", got)
	}
}

func TestHandleAppMention_ConcurrentSameMessage_OneSubmit(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	a.cfg.InitialStatusMessage = "thinking..."
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ev := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: "1700001000.000001"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.HandleAppMention(ev, "T1") }()
	}
	wg.Wait()
	if got := calls.Posts(); got != 1 {
		t.Fatalf("8 concurrent deliveries of the same message: posts = %d, want 1", got)
	}
}

func TestHandleCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		text        string
		addAgent    bool
		wantHandled bool
		wantPosts   int
	}{
		{"Help", "!help", true, true, 1},
		{"HelpNoAgents", "!help", false, true, 1},
		{"Artifacts", "!artifacts", false, true, 1},
		{"UnknownReturnsFalse", "!unknown", false, false, 0},
		{"NotACommand", "hello world", false, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			if tc.addAgent {
				a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "AlphaAgent"})
			}
			handled := a.handleCommand("C1", "t1", "U1", "T1", tc.text)
			if handled != tc.wantHandled {
				t.Errorf("handleCommand(%q) = %v, want %v", tc.text, handled, tc.wantHandled)
			}
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}

func TestHandleInteractive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		evt   func(t *testing.T) socketmode.Event
		setup func(a *SlackGatewayAdapter)
	}{
		{
			name: "CancelAction",
			evt: func(_ *testing.T) socketmode.Event {
				return socketmode.Event{
					Type: socketmode.EventTypeInteractive,
					Data: goslack.InteractionCallback{
						ActionCallback: goslack.ActionCallbacks{
							BlockActions: []*goslack.BlockAction{
								{ActionID: CancelButtonActionID, Value: "task-cancel-1"},
							},
						},
					},
					Request: &socketmode.Request{EnvelopeID: "env-1"},
				}
			},
			setup: func(a *SlackGatewayAdapter) {
				_ = a.core.Tasks().Register("task-cancel-1", "Agent", "U1", entrypoint.TaskIdentity{})
			},
		},
		{
			name: "NonInteractiveData",
			evt: func(_ *testing.T) socketmode.Event {
				return socketmode.Event{
					Type:    socketmode.EventTypeInteractive,
					Data:    "not-a-callback",
					Request: &socketmode.Request{EnvelopeID: "env-5"},
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _, _ := newHandlerTestAdapter(t)
			if tc.setup != nil {
				tc.setup(a)
			}
			client, srv := newTestSocketClient(t)
			defer srv.Close()
			a.handleInteractive(client, tc.evt(t))
		})
	}
}

func TestHandleInteractive_ToolAuthLinkButton_NoOp(t *testing.T) {
	t.Parallel()
	a, calls, _ := newHandlerTestAdapter(t)
	client, srv := newTestSocketClient(t)
	defer srv.Close()

	a.handleInteractive(client, socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{{ActionID: toolAuthActionID}},
			},
		},
		Request: &socketmode.Request{EnvelopeID: "env-toolauth"},
	})

	if calls.Posts() != 0 || calls.Updates() != 0 || calls.Deletes() != 0 {
		t.Errorf("tool-auth link click caused side effects: posts=%d updates=%d deletes=%d, want all 0",
			calls.Posts(), calls.Updates(), calls.Deletes())
	}
}

func TestHandleInteractive_ToolAuthLinkButton_NotLoggedAsUnhandled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		actionID     string
		wantUnhandle bool
	}{
		{name: "ToolAuthAuthorize", actionID: toolAuthActionID, wantUnhandle: false},
		{name: "StaleButtonFromOlderVersion", actionID: "button_from_older_version", wantUnhandle: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _, _ := newHandlerTestAdapter(t)
			logs := &lockedBuffer{}
			a.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			client, srv := newTestSocketClient(t)
			defer srv.Close()

			a.handleInteractive(client, socketmode.Event{
				Type: socketmode.EventTypeInteractive,
				Data: goslack.InteractionCallback{
					ActionCallback: goslack.ActionCallbacks{
						BlockActions: []*goslack.BlockAction{{ActionID: tc.actionID}},
					},
				},
				Request: &socketmode.Request{EnvelopeID: "env-unhandled"},
			})

			if got := strings.Contains(logs.String(), "unhandled slack block action"); got != tc.wantUnhandle {
				t.Errorf("unhandled-action log present = %v, want %v; logs: %s", got, tc.wantUnhandle, logs.String())
			}
		})
	}
}

func TestHandleInteractive_DownloadAction_NotImplemented(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{})
	mock := newSlackHTTPMock(t)
	client := goslack.New("xoxb-test", goslack.OptionAPIURL(mock.server.URL+"/"))
	a.api = slackapi.NewSlackAPIAdapter(client, "xoxb-test", slackapi.WithDisabledRateLimiting())
	a.ctx = t.Context()
	a.queues = make(map[string]*MessageQueue)
	a.hilDedup = runtime.NewMapCache()

	smClient, smSrv := newTestSocketClient(t)
	defer smSrv.Close()

	payload := `{"filename":"report.csv","version":1}`
	callback := goslack.InteractionCallback{
		Container: goslack.Container{ChannelID: "C1", ThreadTs: "t1"},
		ActionCallback: goslack.ActionCallbacks{
			BlockActions: []*goslack.BlockAction{
				{ActionID: DownloadArtifactActionID, Value: payload},
			},
		},
	}
	evt := socketmode.Event{
		Type:    socketmode.EventTypeInteractive,
		Data:    callback,
		Request: &socketmode.Request{EnvelopeID: "env-4"},
	}
	a.handleInteractive(smClient, evt)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.posts()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mock.posts()) != 1 {
		t.Errorf("expected 1 post for not-implemented, got %d", len(mock.posts()))
	}
}

func TestInvalidPayloadReturnsEarly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		callFn func(a *SlackGatewayAdapter, cb goslack.InteractionCallback, action *goslack.BlockAction)
	}{
		{"Download", func(a *SlackGatewayAdapter, cb goslack.InteractionCallback, action *goslack.BlockAction) {
			a.handleDownloadAction(cb, action)
		}},
		{"HILInput", func(a *SlackGatewayAdapter, cb goslack.InteractionCallback, action *goslack.BlockAction) {
			a.handleHILInput(cb, action)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			cb := goslack.InteractionCallback{
				User:      goslack.User{ID: "U999"},
				Container: goslack.Container{ChannelID: "C1", MessageTs: "t1"},
			}
			tc.callFn(a, cb, &goslack.BlockAction{Value: "bad{json"})
			if calls.Posts()+calls.Updates() != 0 {
				t.Errorf("invalid payload should produce 0 API calls, got %d posts + %d updates",
					calls.Posts(), calls.Updates())
			}
		})
	}
}

func TestHandleSlashCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		data      any
		wantPosts int
	}{
		{"Artifacts", goslack.SlashCommand{Command: "/artifacts", ChannelID: "C1", UserID: "U1"}, 1},
		{"Help", goslack.SlashCommand{Command: "/help", ChannelID: "C1", UserID: "U1"}, 1},
		{"InvalidData", "not-a-slash-command", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			client, srv := newTestSocketClient(t)
			defer srv.Close()
			evt := socketmode.Event{
				Type:    socketmode.EventTypeSlashCommand,
				Data:    tc.data,
				Request: &socketmode.Request{EnvelopeID: "env-slash-" + tc.name},
			}
			a.handleSlashCommand(client, evt)
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}

func TestHandleSlashCommand_DecodedFromSocketModePayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		payload        string
		wantEnterprise bool
	}{
		{"FlagOmitted", `{"command":"/help","channel_id":"C1","user_id":"U1"}`, false},
		{"FlagNull", `{"command":"/help","channel_id":"C1","user_id":"U1","is_enterprise_install":null}`, false},
		{"FlagBool", `{"command":"/help","channel_id":"C1","user_id":"U1","is_enterprise_install":true}`, true},
		{"FlagString", `{"command":"/help","channel_id":"C1","user_id":"U1","is_enterprise_install":"true"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cmd goslack.SlashCommand
			if err := json.Unmarshal([]byte(tc.payload), &cmd); err != nil {
				t.Fatalf("decode slash command payload: %v", err)
			}
			if cmd.IsEnterpriseInstall != tc.wantEnterprise {
				t.Errorf("IsEnterpriseInstall = %v, want %v", cmd.IsEnterpriseInstall, tc.wantEnterprise)
			}

			a, calls, _ := newHandlerTestAdapter(t)
			client, srv := newTestSocketClient(t)
			defer srv.Close()
			a.handleSlashCommand(client, socketmode.Event{
				Type:    socketmode.EventTypeSlashCommand,
				Data:    cmd,
				Request: &socketmode.Request{EnvelopeID: "env-slash-decoded-" + tc.name},
			})
			if calls.Posts() != 1 {
				t.Errorf("posts = %d, want 1", calls.Posts())
			}
		})
	}
}

func TestHandlerEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		run       func(a *SlackGatewayAdapter, calls *slackAPICalls)
		wantPosts int
	}{
		{
			name: "CancelAction_UnknownTask",
			run: func(a *SlackGatewayAdapter, _ *slackAPICalls) {
				a.handleCancelAction(goslack.InteractionCallback{}, &goslack.BlockAction{Value: "unknown-task-id"})
			},
		},
		{
			name: "DownloadAction_NoArtifactService",
			run: func(a *SlackGatewayAdapter, _ *slackAPICalls) {
				cb := goslack.InteractionCallback{Container: goslack.Container{ChannelID: "C1", ThreadTs: "t1"}}
				a.handleDownloadAction(cb, &goslack.BlockAction{Value: `{"filename":"f.txt","version":1}`})
			},
			wantPosts: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			tc.run(a, calls)
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}

func TestHandleEventsAPI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		data      any
		wantPosts int
	}{
		{
			name:      "InvalidData",
			data:      "not-events-api",
			wantPosts: 0,
		},
		{
			name: "MessageEvent",
			data: slackevents.EventsAPIEvent{
				TeamID: "T-team",
				InnerEvent: slackevents.EventsAPIInnerEvent{
					Type: string(slackevents.Message),
					Data: &slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "C1", Text: "!help", TimeStamp: "1700000100.000001"},
				},
			},
			wantPosts: 1,
		},
		{
			name: "AppMentionEvent",
			data: slackevents.EventsAPIEvent{
				TeamID: "T-team",
				InnerEvent: slackevents.EventsAPIInnerEvent{
					Type: string(slackevents.AppMention),
					Data: &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> !artifacts", TimeStamp: "1700000100.000002"},
				},
			},
			wantPosts: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, calls, _ := newHandlerTestAdapter(t)
			client, srv := newTestSocketClient(t)
			defer srv.Close()
			evt := socketmode.Event{
				Type:    socketmode.EventTypeEventsAPI,
				Data:    tc.data,
				Request: &socketmode.Request{EnvelopeID: "env-events-" + tc.name},
			}
			a.handleEventsAPI(client, evt)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && calls.Posts() < tc.wantPosts {
				time.Sleep(10 * time.Millisecond)
			}
			if tc.wantPosts == 0 {
				time.Sleep(100 * time.Millisecond)
			}
			if calls.Posts() != tc.wantPosts {
				t.Errorf("posts = %d, want %d", calls.Posts(), tc.wantPosts)
			}
		})
	}
}
