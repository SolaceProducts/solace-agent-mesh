// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	notifyNamespace = "test-ns"
	notifySpace     = "spaces/DMADA"
	notifyEmail     = "ada@example.com"
)

type fakeDirectMessages struct {
	mu        sync.Mutex
	direct    map[string]bool
	err       error
	failFirst []error
	asked     []string
}

func (f *fakeDirectMessages) IsDirectMessage(_ context.Context, space string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, space)
	if len(f.failFirst) > 0 {
		err := f.failFirst[0]
		f.failFirst = f.failFirst[1:]
		return false, err
	}
	if f.err != nil {
		return false, f.err
	}
	direct, ok := f.direct[space]
	if !ok {
		return false, errNoDirectMessage
	}
	return direct, nil
}

func (f *fakeDirectMessages) askedSpaces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

type proactiveRig struct {
	a          *Adapter
	chat       *fakeChat
	dms        *fakeDirectMessages
	brk        *broker.DevBroker
	scoped     *artifact.ScopedService
	logs       *logCapture
	badAddress bool
}

func newProactiveRig(t *testing.T) *proactiveRig {
	t.Helper()
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.cfg.Delivery = DeliveryHTTPS
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyNotifyService, Default: true})
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": notifyNamespace, "gateway_id": a.gatewayID})
	core.SetServices(runtime.NewRuntime(ctxHonoringBroker{brk}, nil, runtime.WithFeatureRegistry(reg), runtime.WithArtifacts(scoped)).Services())
	core.SetIdentityAuthorizer(matchedAuthorizer())
	a.core = core
	dms := &fakeDirectMessages{direct: map[string]bool{notifySpace: true}}
	a.dms = dms
	return &proactiveRig{a: a, chat: chat, dms: dms, brk: brk, scoped: scoped, logs: recordLogs(a)}
}

func (r *proactiveRig) address(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(proactiveAddress{Space: notifySpace, Email: notifyEmail})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (r *proactiveRig) deliver(t *testing.T, d a2a.NotifyDeliver) {
	t.Helper()
	if d.Address == nil {
		d.Address = r.address(t)
	}
	if d.ChannelType == "" {
		d.ChannelType = EntrypointType
	}
	r.a.handleNotifyDeliver(d)
}

func (r *proactiveRig) posted(t *testing.T) []fakeWrite {
	t.Helper()
	writes, _ := r.chat.snapshot()
	return writes
}

func apiError(code int) error { return &googleapi.Error{Code: code} }

func TestNotifyOptions_PromptsOnlyWhereClicksReachTheAdapter(t *testing.T) {
	const addOnSigner = "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"
	for name, tc := range map[string]struct {
		cfg  Config
		want bool
	}{
		"a classic app over https":                                  {Config{Delivery: DeliveryHTTPS}, true},
		"an add-on app over https with its endpoint":                {Config{Delivery: DeliveryHTTPS, AddOnServiceAccountEmail: addOnSigner, EndpointURL: "https://sam.example.com/gw/gc/events"}, true},
		"an add-on app over https with no endpoint":                 {Config{Delivery: DeliveryHTTPS, AddOnServiceAccountEmail: addOnSigner}, false},
		"pubsub, where a classic app cannot be told from an add-on": {Config{Delivery: DeliveryPubSub}, false},
	} {
		t.Run(name, func(t *testing.T) {
			a := &Adapter{cfg: &tc.cfg}
			want := entrypoint.NotifyDeliverOptions{GatewayType: "google_chat", Interactable: tc.want}
			if got := a.notifyOptions(); got != want {
				t.Errorf("notifyOptions = %+v, want %+v", got, want)
			}
		})
	}
}

func TestStartNotifyDeliverLoop_AdvertisesGoogleChat(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.startNotifyDeliverLoop()

	want := a2a.NotifyCapabilities{ChannelType: "google_chat", Notifiable: true, Interactable: true}
	if got := rig.a.core.AdvertisedNotifyCapabilities(); got == nil || *got != want {
		t.Fatalf("advertised = %+v, want %+v", got, want)
	}
}

func TestStartNotifyDeliverLoop_WithoutASpaceReaderAdvertisesNothing(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.dms = nil
	rig.a.startNotifyDeliverLoop()
	if got := rig.a.core.AdvertisedNotifyCapabilities(); got != nil {
		t.Errorf("advertised = %+v, want nothing: the entrypoint could not tell a direct message from a shared space", got)
	}
}

func TestNotifyConsumer_ServesDeliveriesOverTheBroker(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.startNotifyDeliverLoop()

	payload, err := json.Marshal(a2a.NotifyDeliver{RequestID: "req-b", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, ChannelType: EntrypointType, Address: rig.address(t), Title: "Report", Message: "is ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err = rig.brk.PublishGuaranteed(t.Context(), a2a.NotifyDeliverTopic(notifyNamespace, EntrypointType, "req-b"), &broker.Message{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	writes, _ := rig.chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	if writes[0].space != notifySpace || writes[0].text != "Report\nis ready" {
		t.Errorf("delivery = %+v, want %q in %s", writes[0], "Report\nis ready", notifySpace)
	}
}

func TestPublishPresence(t *testing.T) {
	message := func(space string, dm bool) *inboundEvent {
		return &inboundEvent{Kind: kindMessage, Addressed: true, IsDM: dm, SpaceName: space, SenderName: "users/1", SenderEmail: "Ada@Example.com", Text: "hi"}
	}
	for name, tc := range map[string]struct {
		ev            *inboundEvent
		wantPublished bool
	}{
		"a direct message is recorded":     {ev: message(notifySpace, true), wantPublished: true},
		"a shared space is never recorded": {ev: message("spaces/TEAM", false)},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			presence, err := rig.brk.CreateDirectSubscription()
			if err != nil {
				t.Fatal(err)
			}
			if err = rig.brk.Subscribe(presence, a2a.NotifyPresenceSubscription(notifyNamespace)); err != nil {
				t.Fatal(err)
			}
			sender := rig.a.authorizeSender(t.Context(), tc.ev).Identity
			if sender == nil {
				t.Fatal("the sender was not identified")
			}

			rig.a.publishPresence(t.Context(), tc.ev, sender)

			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			msg, err := rig.brk.Receive(ctx, presence)
			if !tc.wantPublished {
				if err == nil {
					t.Fatalf("presence = %s, want none", msg.Payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("no presence was published: %v", err)
			}
			var p a2a.NotifyPresence
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				t.Fatal(err)
			}
			var addr proactiveAddress
			if err := json.Unmarshal(p.Address, &addr); err != nil {
				t.Fatal(err)
			}
			if p.ChannelType != EntrypointType || addr != (proactiveAddress{Space: notifySpace, Email: "ada@example.com"}) {
				t.Errorf("presence = %+v with address %+v, want %s at %s", p, addr, EntrypointType, notifySpace)
			}
		})
	}
}

type fullLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *fullLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *fullLog) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, b.String())
	return nil
}

func (l *fullLog) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *fullLog) WithGroup(string) slog.Handler      { return l }

func TestProactiveFailuresNeverLogTheErrorTextOrTheEmail(t *testing.T) {
	leaky := &googleapi.Error{Code: 503, Message: "users/" + notifyEmail + " leaked-detail"}
	for name, setup := range map[string]func(*proactiveRig){
		"a space that cannot be checked":       func(r *proactiveRig) { r.dms.err = leaky },
		"a message chat refuses":               func(r *proactiveRig) { r.chat.failures = []error{&permanentError{err: leaky}} },
		"a domain no longer allowed":           func(r *proactiveRig) { r.a.cfg.AllowedEmailDomains = []string{"corp.example.org"} },
		"a space that is not a direct message": func(r *proactiveRig) { r.dms.direct[notifySpace] = false },
		"an address that cannot be read":       func(r *proactiveRig) { r.badAddress = true },
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			logs := &fullLog{}
			rig.a.logger = slog.New(logs)
			rig.a.writer.logger = rig.a.logger
			setup(rig)

			d := a2a.NotifyDeliver{RequestID: "req-x", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Title: "Report"}
			if rig.badAddress {
				d.Address = json.RawMessage(`{"space":"nope","email":"ada@example.com"}`)
			}
			rig.deliver(t, d)

			if len(logs.lines) == 0 {
				t.Fatal("nothing was logged")
			}
			for _, line := range logs.lines {
				if strings.Contains(line, notifyEmail) || strings.Contains(line, "leaked-detail") {
					t.Errorf("a log line carries the person's email or the error text: %q", line)
				}
			}
		})
	}
}

func TestDeliverNotice(t *testing.T) {
	long := strings.Repeat("word ", 2000)
	for name, tc := range map[string]struct {
		d         a2a.NotifyDeliver
		wantTexts []string
	}{
		"a title over its message": {a2a.NotifyDeliver{Title: "Report", Message: "is ready"}, []string{"Report\nis ready"}},
		"a message alone":          {a2a.NotifyDeliver{Message: "is ready"}, []string{"is ready"}},
		"a title alone":            {a2a.NotifyDeliver{Title: "Report"}, []string{"Report"}},
		"markdown in chat's form":  {a2a.NotifyDeliver{Message: "this is **done**, see [the log](https://example.com/log)"}, []string{"this is *done*, see <https://example.com/log|the log>"}},
		"nothing to say":           {a2a.NotifyDeliver{}, []string{msgNotifyDefault}},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			tc.d.Mode = a2a.NotifyModeNotify
			rig.deliver(t, tc.d)
			var got []string
			for _, w := range rig.posted(t) {
				if w.space != notifySpace || w.thread != "" {
					t.Errorf("write = %+v, want a new message in %s", w, notifySpace)
				}
				got = append(got, w.text)
			}
			if strings.Join(got, "|") != strings.Join(tc.wantTexts, "|") {
				t.Errorf("messages = %q, want %q", got, tc.wantTexts)
			}
		})
	}
	t.Run("a long message is split into messages chat accepts", func(t *testing.T) {
		rig := newProactiveRig(t)
		rig.deliver(t, a2a.NotifyDeliver{Mode: a2a.NotifyModeNotify, Message: long})
		writes := rig.posted(t)
		if len(writes) < 3 {
			t.Fatalf("messages = %d, want the text split over several", len(writes))
		}
		for _, w := range writes {
			if n := len([]rune(w.text)); n > maxMessageRunes {
				t.Errorf("a message holds %d runes, want at most %d", n, maxMessageRunes)
			}
		}
	})
}

func TestDeliver_RefusesWhatIsNotTheAppsDirectMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		setup   func(*proactiveRig)
		address string
	}{
		"a shared space":                   {setup: func(r *proactiveRig) { r.dms.direct[notifySpace] = false }},
		"a direct message that is gone":    {setup: func(r *proactiveRig) { delete(r.dms.direct, notifySpace) }},
		"credentials chat refused":         {setup: func(r *proactiveRig) { r.dms.err = fmt.Errorf("%w: %w", errChatRefused, apiError(401)) }},
		"a lookup chat cannot answer":      {setup: func(r *proactiveRig) { r.dms.err = apiError(503) }},
		"an address that is not a space":   {address: `{"space":"spaces/x/../../y","email":"ada@example.com"}`},
		"an address with no person":        {address: `{"space":"spaces/DMADA","email":"not an email"}`},
		"an address that is not an object": {address: `"spaces/DMADA"`},
		"an address outside the domains":   {setup: func(r *proactiveRig) { r.a.cfg.AllowedEmailDomains = []string{"corp.example.org"} }},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			if tc.setup != nil {
				tc.setup(rig)
			}
			d := a2a.NotifyDeliver{RequestID: "req-x", Mode: a2a.NotifyModeNotify, Title: "Report"}
			if tc.address != "" {
				d.Address = json.RawMessage(tc.address)
			}
			rig.deliver(t, d)
			if writes := rig.posted(t); len(writes) != 0 {
				t.Errorf("writes = %+v, want nothing sent", writes)
			}
		})
	}
}

func inputDeliver(requestID string) a2a.NotifyDeliver {
	return a2a.NotifyDeliver{RequestID: requestID, Mode: a2a.NotifyModeInput, UserID: notifyEmail, Surface: json.RawMessage(askSurfaceJSON)}
}

func TestDeliverInput_IsACardRoutedToTheNotificationService(t *testing.T) {
	rig := newProactiveRig(t)
	rig.deliver(t, inputDeliver("req-9"))

	writes := rig.posted(t)
	if len(writes) != 1 || writes[0].cards != 1 || writes[0].cardButtons == 0 {
		t.Fatalf("writes = %+v, want one card with buttons", writes)
	}
	function, params := buttonOnCards(t, rig.chat.currentCards(writes[0].name), hilSubmitLabel)
	if function != actionHILSubmit {
		t.Errorf("the button invokes %q, want %q on a classic app", function, actionHILSubmit)
	}
	want := map[string]string{paramRequestID: "req-9", paramNotify: notifyRoutedValue, paramRecipient: notifyEmail, buttonActionParam: actionHILSubmit}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("button parameter %s = %q, want %q", k, params[k], v)
		}
	}
	if task, ok := params[paramTaskID]; ok {
		t.Errorf("the button names task %q: a proactive prompt has none", task)
	}
}

func TestDeliverInput_AddOnButtonsPointAtTheEndpoint(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.AddOnServiceAccountEmail = "service-123456789@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"
	rig.a.cfg.EndpointURL = "https://sam.example.com/gw/gc/events"
	rig.deliver(t, inputDeliver("req-9"))

	writes := rig.posted(t)
	if len(writes) != 1 {
		t.Fatalf("writes = %+v, want one", writes)
	}
	function, params := buttonOnCards(t, rig.chat.currentCards(writes[0].name), hilSubmitLabel)
	if function != rig.a.cfg.EndpointURL || params[buttonActionParam] != actionHILSubmit {
		t.Errorf("the button invokes %q carrying %q, want the endpoint URL carrying %q", function, params[buttonActionParam], actionHILSubmit)
	}
}

func TestDeliverInput_PointsAtTheWebUIWhereButtonsCannotBeAnswered(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.Delivery = DeliveryPubSub
	rig.deliver(t, inputDeliver("req-9"))

	writes := rig.posted(t)
	if len(writes) != 1 || writes[0].cardButtons != 0 || !strings.Contains(writes[0].cardText, "web UI") {
		t.Errorf("writes = %+v, want one card with no buttons that points at the web UI", writes)
	}
}

func TestDeliverInput_UnreadableSurfaceSendsNothing(t *testing.T) {
	rig := newProactiveRig(t)
	d := inputDeliver("req-9")
	d.Surface = json.RawMessage(`{"not":"a surface"`)
	rig.deliver(t, d)
	if writes := rig.posted(t); len(writes) != 0 {
		t.Errorf("writes = %+v, want nothing", writes)
	}
}

func (r *proactiveRig) subscribeReplies(t *testing.T, requestID string) broker.Queue {
	t.Helper()
	q, err := r.brk.CreateQueue("notify-replies-"+requestID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.brk.Subscribe(q, a2a.NotifyReplyTopic(notifyNamespace, requestID)); err != nil {
		t.Fatal(err)
	}
	return q
}

func (r *proactiveRig) nextReply(t *testing.T, q broker.Queue, wait time.Duration) (a2a.UserInputResponseData, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	msg, err := r.brk.Receive(ctx, q)
	if err != nil || msg == nil {
		return a2a.UserInputResponseData{}, false
	}
	msg.Ack()
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("the reply is not a user input response: %v", err)
	}
	return resp, true
}

func (r *proactiveRig) promptClick(t *testing.T, requestID, label, email string) *inboundEvent {
	t.Helper()
	r.deliver(t, inputDeliver(requestID))
	writes := r.posted(t)
	if len(writes) == 0 {
		t.Fatal("no prompt was delivered")
	}
	last := writes[len(writes)-1]
	function, params := buttonOnCards(t, r.chat.currentCards(last.name), label)
	params[actionMethodParam] = function
	return &inboundEvent{
		Kind: kindCardClick, Addressed: true, IsDM: true, SpaceName: notifySpace, MessageName: last.name,
		SenderName: "users/1", SenderEmail: email, Action: params,
	}
}

func TestNotifyPromptClick_AnswersTheNotificationService(t *testing.T) {
	rig := newProactiveRig(t)
	replies := rig.subscribeReplies(t, "req-9")
	click := rig.promptClick(t, "req-9", hilSubmitLabel, notifyEmail)
	click.FormInputs = map[string][]string{"hil_0": {"o1"}}

	if res := rig.a.process(t.Context(), click); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	resp, ok := rig.nextReply(t, replies, 5*time.Second)
	if !ok {
		t.Fatal("no answer reached the notification service's reply topic")
	}
	if resp.RequestID != "req-9" || resp.Status != "answered" || resp.Data["answers"] == nil {
		t.Errorf("reply = %+v, want req-9 answered with the form's answers", resp)
	}
	rig.chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		for _, x := range w {
			if x.op == "update" && x.name == click.MessageName && x.text == msgHILSubmitted && x.cards == 0 {
				return true
			}
		}
		return false
	})
}

func TestNotifyPromptClick_CancelIsAnsweredAsCancelled(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.Delivery = DeliveryHTTPS
	replies := rig.subscribeReplies(t, "req-c")
	rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-c", Mode: a2a.NotifyModeInput, UserID: notifyEmail, Surface: json.RawMessage(cancellableSurfaceJSON)})
	writes := rig.posted(t)
	function, params := buttonOnCards(t, rig.chat.currentCards(writes[0].name), cancelButtonLabel)
	params[actionMethodParam] = function
	click := &inboundEvent{Kind: kindCardClick, Addressed: true, IsDM: true, SpaceName: notifySpace, MessageName: writes[0].name, SenderName: "users/1", SenderEmail: notifyEmail, Action: params}

	if res := rig.a.process(t.Context(), click); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	if resp, ok := rig.nextReply(t, replies, 5*time.Second); !ok || resp.Status != "cancelled" || resp.RequestID != "req-c" {
		t.Errorf("reply = %+v (arrived: %v), want req-c cancelled", resp, ok)
	}
}

func TestNotifyPromptClick_RefusesAnyoneElse(t *testing.T) {
	rig := newProactiveRig(t)
	replies := rig.subscribeReplies(t, "req-9")
	click := rig.promptClick(t, "req-9", hilSubmitLabel, "bob@example.com")

	if res := rig.a.process(t.Context(), click); res.outcome != outcomeDeclined {
		t.Fatalf("outcome = %v, want declined", res.outcome)
	}
	rig.chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourPrompt) })
	if resp, ok := rig.nextReply(t, replies, 300*time.Millisecond); ok {
		t.Errorf("reply = %+v, want none from someone it was not sent to", resp)
	}
}

func TestNotifyPromptClick_IgnoresRoutingItDoesNotRender(t *testing.T) {
	for name, tamper := range map[string]func(map[string]string){
		"both a task and the notification service": func(p map[string]string) { p[paramTaskID] = tid("some-task") },
		"no request":                func(p map[string]string) { delete(p, paramRequestID) },
		"another value of the flag": func(p map[string]string) { p[paramNotify] = "yes" },
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			replies := rig.subscribeReplies(t, "req-9")
			click := rig.promptClick(t, "req-9", hilSubmitLabel, notifyEmail)
			tamper(click.Action)

			if res := rig.a.process(t.Context(), click); res.outcome != outcomeIgnored {
				t.Errorf("outcome = %v, want ignored", res.outcome)
			}
			if resp, ok := rig.nextReply(t, replies, 300*time.Millisecond); ok {
				t.Errorf("reply = %+v, want none", resp)
			}
		})
	}
}

func TestNotifyPromptClick_ASenderTheGateRefusesIsNotAnswered(t *testing.T) {
	rig := newProactiveRig(t)
	replies := rig.subscribeReplies(t, "req-9")
	click := rig.promptClick(t, "req-9", hilSubmitLabel, notifyEmail)
	rig.a.core.SetIdentityAuthorizer(&stubAuthorizer{})

	if res := rig.a.process(t.Context(), click); res.outcome == outcomeSubmitted {
		t.Errorf("outcome = %v, want the click refused at the identity gate", res.outcome)
	}
	if resp, ok := rig.nextReply(t, replies, 300*time.Millisecond); ok {
		t.Errorf("reply = %+v, want none", resp)
	}
}

func TestNotifyPromptClick_ABrokerFailureIsRetried(t *testing.T) {
	rig := newProactiveRig(t)
	click := rig.promptClick(t, "req-9", hilSubmitLabel, notifyEmail)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	res := rig.a.clickNotifyHIL(cancelled, click)
	if res.outcome == outcomeSubmitted || res.decline != msgHILSubmitFailed {
		t.Errorf("result = %+v, want a retry that tells the person the answer did not go through", res)
	}
}

func TestDeliverArtifacts(t *testing.T) {
	seed := func(t *testing.T, rig *proactiveRig, owner, name string) a2a.NotifyArtifact {
		t.Helper()
		loc := rig.scoped.Resolve("agent", owner, "sess-1", name)
		if _, err := rig.scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2"), "text/csv"); err != nil {
			t.Fatal(err)
		}
		return a2a.NotifyArtifact{AgentName: "agent", UserID: owner, SessionID: "sess-1", Filename: name, MimeType: "text/csv"}
	}
	notice := func(t *testing.T, rig *proactiveRig, arts ...a2a.NotifyArtifact) {
		t.Helper()
		rig.deliver(t, a2a.NotifyDeliver{RequestID: "req-a", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, Message: "done", Artifacts: arts})
	}

	t.Run("without drive the files are named with a pointer to the web UI", func(t *testing.T) {
		rig := newProactiveRig(t)
		notice(t, rig, seed(t, rig, notifyEmail, "sales.csv"))
		_, texts := rig.chat.snapshot()
		if len(texts) != 2 || !strings.Contains(texts[1], "sales.csv") {
			t.Errorf("messages = %q, want the notice and then one naming sales.csv", texts)
		}
	})

	t.Run("with drive each file is shared with the person and linked", func(t *testing.T) {
		rig := newProactiveRig(t)
		shared := make(chan string, 2)
		rig.a.cfg.ArtifactDriveFolderID = "folder-0123456789"
		rig.a.cfg.ArtifactDriveAccessDays = 14
		rig.a.cfg.DriveMaxArtifactBytes = defaultMaxDriveArtifactBytes
		rig.a.drive = &fakeDrive{share: func(_ context.Context, _, email string, _ time.Time) error { shared <- email; return nil }}
		rig.a.driveSlots = make(chan struct{}, 1)
		notice(t, rig, seed(t, rig, notifyEmail, "sales.csv"))

		select {
		case email := <-shared:
			if email != notifyEmail {
				t.Errorf("the file was shared with %q, want %q", email, notifyEmail)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the file was never shared")
		}
		_, texts := rig.chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
			return len(texts) == 2 && strings.Contains(texts[1], driveFileLinkPrefix)
		})
		if len(texts) != 2 {
			t.Errorf("messages = %q, want the notice and then a Drive link", texts)
		}
	})

	t.Run("a file of someone else is never handed over", func(t *testing.T) {
		rig := newProactiveRig(t)
		notice(t, rig, seed(t, rig, "bob@example.com", "secret.csv"))
		_, texts := rig.chat.snapshot()
		if len(texts) != 1 {
			t.Errorf("messages = %q, want only the notice", texts)
		}
	})

	t.Run("a shared principal's file is never handed over", func(t *testing.T) {
		rig := newProactiveRig(t)
		owner := rbac.DefaultSystemUserSub
		d := a2a.NotifyDeliver{RequestID: "req-a", Mode: a2a.NotifyModeNotify, UserID: owner, Message: "done", Address: rig.address(t), Artifacts: []a2a.NotifyArtifact{seed(t, rig, owner, "system.csv")}}
		rig.a.handleNotifyDeliver(d)
		_, texts := rig.chat.snapshot()
		if len(texts) != 1 {
			t.Errorf("messages = %q, want only the notice", texts)
		}
	})

	t.Run("more files than the limit are named, not handed over", func(t *testing.T) {
		rig := newProactiveRig(t)
		rig.a.cfg.ArtifactDriveFolderID = "folder-0123456789"
		rig.a.cfg.ArtifactDriveAccessDays = 14
		rig.a.cfg.DriveMaxArtifactBytes = defaultMaxDriveArtifactBytes
		drive := &fakeDrive{}
		rig.a.drive = drive
		rig.a.driveSlots = make(chan struct{}, 1)
		var arts []a2a.NotifyArtifact
		for i := 0; i < maxDriveArtifactsPerTask+2; i++ {
			arts = append(arts, seed(t, rig, notifyEmail, fmt.Sprintf("f%d.csv", i)))
		}
		notice(t, rig, arts...)
		_, texts := rig.chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
			return len(texts) == 3 && drive.count("share") == maxDriveArtifactsPerTask
		})
		if !anyTextContains(texts, "f5.csv") || !anyTextContains(texts, "f6.csv") {
			t.Errorf("messages = %q, want the two files left over named", texts)
		}
	})
}

func TestDirectMessageError(t *testing.T) {
	for name, tc := range map[string]struct {
		in   error
		want error
	}{
		"not found":    {apiError(404), errNoDirectMessage},
		"forbidden":    {apiError(403), errNoDirectMessage},
		"unauthorized": {apiError(401), errChatRefused},
	} {
		t.Run(name, func(t *testing.T) {
			if got := directMessageError(tc.in); !errors.Is(got, tc.want) {
				t.Errorf("directMessageError(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	for _, code := range []int{400, 429, 500, 503} {
		if got := directMessageError(apiError(code)); errors.Is(got, errNoDirectMessage) || errors.Is(got, errChatRefused) {
			t.Errorf("a %d was classified as %v, want it left as a failure that may pass on a retry", code, got)
		}
	}
}

func TestDirectMessageFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		wantReason string
		wantLevel  slog.Level
	}{
		"a space the app cannot read":  {errNoDirectMessage, reasonNoConversation, slog.LevelWarn},
		"credentials chat refused":     {errChatRefused, reasonChatRefused, slog.LevelError},
		"a failure that may pass":      {apiError(503), reasonUnavailable, slog.LevelWarn},
		"a wrapped space that is gone": {fmt.Errorf("outer: %w", errNoDirectMessage), reasonNoConversation, slog.LevelWarn},
	} {
		t.Run(name, func(t *testing.T) {
			if reason, level := directMessageFailure(tc.err); reason != tc.wantReason || level != tc.wantLevel {
				t.Errorf("directMessageFailure = %q at %v, want %q at %v", reason, level, tc.wantReason, tc.wantLevel)
			}
		})
	}
}

const cancellableSurfaceJSON = `{
  "surfaceId":"req-c",
  "components":[
    {"id":"q0-label","component":"Text","text":{"literalString":"Proceed?"},"variant":"h3"},
    {"id":"cancel-text","component":"Text","text":{"literalString":"Cancel"}},
    {"id":"cancel-btn","component":"Button","child":"cancel-text","variant":"secondary",
      "action":{"event":{"name":"cancel","context":{}}}}
  ],
  "dataModel":{}
}`
