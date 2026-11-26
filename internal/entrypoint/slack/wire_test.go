// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func roundTripBlocks(t *testing.T, blocks []goslack.Block) goslack.Blocks {
	t.Helper()
	wire, err := json.Marshal(goslack.Blocks{BlockSet: blocks})
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	var decoded goslack.Blocks
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal blocks %s: %v", wire, err)
	}
	if len(decoded.BlockSet) != len(blocks) {
		t.Fatalf("decoded %d blocks, want %d", len(decoded.BlockSet), len(blocks))
	}
	again, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal blocks: %v", err)
	}
	if string(again) != string(wire) {
		t.Errorf("blocks changed over a decode and encode:\n got %s\nwant %s", again, wire)
	}
	return decoded
}

func TestBlocksDecode_NativeFeedbackModal(t *testing.T) {
	t.Parallel()
	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t1", SessionID: "s1", Rating: feedbackRatingDown}, "C1", "1.0")

	decoded := roundTripBlocks(t, modal.Blocks.BlockSet)

	input, ok := decoded.BlockSet[0].(*goslack.InputBlock)
	if !ok {
		t.Fatalf("block 0 is %T, want *InputBlock", decoded.BlockSet[0])
	}
	el, ok := input.Element.(*goslack.PlainTextInputBlockElement)
	if !ok || el.ActionID != FeedbackCommentInputID || !el.Multiline || el.MaxLength != feedbackCommentMaxLength {
		t.Errorf("comment element = %#v, want the multiline comment input", input.Element)
	}
	notice, ok := decoded.BlockSet[1].(*goslack.ContextBlock)
	if !ok {
		t.Fatalf("block 1 is %T, want *ContextBlock", decoded.BlockSet[1])
	}
	if len(notice.ContextElements.Elements) != 1 {
		t.Fatalf("context elements = %d, want 1", len(notice.ContextElements.Elements))
	}
	text, ok := notice.ContextElements.Elements[0].(*goslack.TextBlockObject)
	if !ok || text.Type != goslack.MarkdownType {
		t.Errorf("context element = %#v, want a mrkdwn text object", notice.ContextElements.Elements[0])
	}
}

func TestBlocksDecode_HILBlocks(t *testing.T) {
	t.Parallel()
	a := &SlackGatewayAdapter{}
	blocks, ok := a.buildHILBlocks(parseSurfaceT(t), "req-1", "task-1", "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks returned ok=false")
	}

	decoded := roundTripBlocks(t, blocks)

	var inputs, buttons int
	for _, b := range decoded.BlockSet {
		switch blk := b.(type) {
		case *goslack.InputBlock:
			inputs++
		case *goslack.ActionBlock:
			buttons += len(blk.Elements.ElementSet)
		}
	}
	if inputs != 2 || buttons != 2 {
		t.Errorf("decoded %d input blocks and %d buttons, want 2 and 2", inputs, buttons)
	}
}

func TestBlocksDecode_StatusAndAuthPromptBlocks(t *testing.T) {
	t.Parallel()
	roundTripBlocks(t, StatusBlocks("Working..."))
	roundTripBlocks(t, ContentBlocks("**done**"))
	roundTripBlocks(t, authPromptBlocks("https://idp.example/authorize?state=s1"))
}

func TestBlocksDecode_SectionAccessories(t *testing.T) {
	t.Parallel()
	const wire = `[
		{"type":"section","text":{"type":"mrkdwn","text":"hi"},"accessory":{"type":"button","action_id":"a1","text":{"type":"plain_text","text":"Go"}}},
		{"type":"section","text":{"type":"mrkdwn","text":"hi"},"accessory":{"type":"overflow","action_id":"a2","options":[{"text":{"type":"plain_text","text":"One"},"value":"1"}]}},
		{"type":"section","text":{"type":"mrkdwn","text":"hi"},"accessory":{"type":"radio_buttons","action_id":"a3","options":[{"text":{"type":"plain_text","text":"One"},"value":"1"}]}},
		{"type":"section","text":{"type":"mrkdwn","text":"hi"},"accessory":{"type":"not_a_known_element","action_id":"a4"}},
		{"type":"not_a_known_block","block_id":"b9"}
	]`
	var blocks goslack.Blocks
	if err := json.Unmarshal([]byte(wire), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks.BlockSet) != 5 {
		t.Fatalf("decoded %d blocks, want 5", len(blocks.BlockSet))
	}
	section := func(i int) *goslack.SectionBlock {
		s, ok := blocks.BlockSet[i].(*goslack.SectionBlock)
		if !ok {
			t.Fatalf("block %d is %T, want *SectionBlock", i, blocks.BlockSet[i])
		}
		return s
	}
	if section(0).Accessory.ButtonElement == nil || section(0).Accessory.ButtonElement.ActionID != "a1" {
		t.Errorf("accessory 0 = %#v, want the button", section(0).Accessory)
	}
	if section(1).Accessory.OverflowElement == nil {
		t.Errorf("accessory 1 = %#v, want the overflow menu", section(1).Accessory)
	}
	if section(2).Accessory.RadioButtonsElement == nil {
		t.Errorf("accessory 2 = %#v, want the radio buttons", section(2).Accessory)
	}
	if section(3).Accessory.UnknownElement == nil {
		t.Errorf("accessory 3 = %#v, want an unknown element", section(3).Accessory)
	}
	if _, ok := blocks.BlockSet[4].(*goslack.UnknownBlock); !ok {
		t.Errorf("block 4 is %T, want *UnknownBlock", blocks.BlockSet[4])
	}
}

func TestBlockObjectsDecode_OptionsAndText(t *testing.T) {
	t.Parallel()
	const wire = `[
		{"type":"plain_text","text":"label"},
		{"type":"mrkdwn","text":"*bold*"},
		{"text":{"type":"plain_text","text":"One"},"value":"1"},
		{"label":{"type":"plain_text","text":"Group"},"options":[{"text":{"type":"plain_text","text":"Two"},"value":"2"}]},
		{"confirm":{"type":"plain_text","text":"Yes"},"title":{"type":"plain_text","text":"Sure?"},"text":{"type":"plain_text","text":"Really?"}}
	]`
	var objects goslack.BlockObjects
	if err := json.Unmarshal([]byte(wire), &objects); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(objects.TextObjects) != 2 || objects.TextObjects[0].Type != goslack.PlainTextType || objects.TextObjects[1].Type != goslack.MarkdownType {
		t.Errorf("text objects = %#v, want a plain_text then a mrkdwn", objects.TextObjects)
	}
	if len(objects.OptionObjects) != 1 || objects.OptionObjects[0].Value != "1" {
		t.Errorf("option objects = %#v, want the option with value 1", objects.OptionObjects)
	}
	if len(objects.OptionGroupObjects) != 1 || len(objects.OptionGroupObjects[0].Options) != 1 {
		t.Errorf("option group objects = %#v, want one group of one option", objects.OptionGroupObjects)
	}
	if len(objects.ConfirmationObjects) != 1 {
		t.Errorf("confirmation objects = %d, want 1", len(objects.ConfirmationObjects))
	}
}

func TestInteractionCallbackParse_BlockActionsFromFormBody(t *testing.T) {
	t.Parallel()
	const payload = `{"type":"block_actions","user":{"id":"U1","team_id":"T1"},"trigger_id":"trig",` +
		`"container":{"channel_id":"C1","message_ts":"1700000000.000100"},` +
		`"actions":[{"action_id":"a1","block_id":"b1","type":"button","value":"v1"}]}`
	form := url.Values{"payload": {payload}}
	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	cb, err := goslack.InteractionCallbackParse(req)
	if err != nil {
		t.Fatalf("InteractionCallbackParse: %v", err)
	}
	if cb.User.ID != "U1" || cb.Container.ChannelID != "C1" || cb.TriggerID != "trig" {
		t.Errorf("callback = %+v, want user U1 in channel C1 with trigger trig", cb)
	}
	if len(cb.ActionCallback.BlockActions) != 1 || cb.ActionCallback.BlockActions[0].Value != "v1" {
		t.Errorf("block actions = %#v, want the button with value v1", cb.ActionCallback.BlockActions)
	}

	empty := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(""))
	empty.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := goslack.InteractionCallbackParse(empty); err == nil {
		t.Error("InteractionCallbackParse accepted a request without a payload")
	}
}

func TestMsgOptionText_EscapedTextIsStableAcrossReuse(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		texts = append(texts, r.PostForm.Get("text"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1.0"}`))
	}))
	t.Cleanup(srv.Close)
	client := newSlackClient(&SlackConfig{BotToken: "xoxb-test", APIBaseURL: srv.URL + "/"})

	option := goslack.MsgOptionText("a < b & c > d", true)
	for range 3 {
		if _, _, err := client.PostMessageContext(t.Context(), "C1", option); err != nil {
			t.Fatalf("PostMessage: %v", err)
		}
	}

	const want = "a &lt; b &amp; c &gt; d"
	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 3 {
		t.Fatalf("server saw %d posts, want 3", len(texts))
	}
	for i, got := range texts {
		if got != want {
			t.Errorf("post %d text = %q, want %q", i, got, want)
		}
	}
}

func TestNewSlackClient_RetriesARetryAfterAtTheCeiling(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", strconv.Itoa(int(slackMaxRetryAfter/time.Second)))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1.0"}`))
	}))
	t.Cleanup(srv.Close)
	client := newSlackClient(&SlackConfig{BotToken: "xoxb-test", APIBaseURL: srv.URL + "/"})

	if _, _, err := client.PostMessageContext(t.Context(), "C1", goslack.MsgOptionText("hello", false)); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2: a wait of exactly slackMaxRetryAfter is still retried, so no jitter may be added to it", got)
	}
}

func TestNewSlackClient_GivesUpAfterTheRetryBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	client := newSlackClient(&SlackConfig{BotToken: "xoxb-test", APIBaseURL: srv.URL + "/"})

	_, _, err := client.PostMessageContext(t.Context(), "C1", goslack.MsgOptionText("hello", false))
	var limited *goslack.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("PostMessage error = %v, want a rate-limited error", err)
	}
	if got, want := calls.Load(), int32(slackRateLimitRetries+1); got != want {
		t.Fatalf("calls = %d, want %d", got, want)
	}
}
