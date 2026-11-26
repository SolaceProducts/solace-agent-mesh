// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func TestMessageQueue_StartStop(t *testing.T) {
	q := NewMessageQueue("test", "C123", "1234.5678", nil, false, nil)

	q.logger = testLogger()

	q.Start()

	done := make(chan struct{})
	go func() {
		q.Stop(5 * time.Second)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not complete within timeout")
	}
}

func TestMessageQueue_WaitDrain(t *testing.T) {
	q := NewMessageQueue("test", "C123", "1234.5678", nil, false, nil)
	q.logger = testLogger()
	q.Start()

	done := make(chan struct{})
	go func() {
		q.WaitDrain()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitDrain did not complete")
	}

	q.Stop(5 * time.Second)
}

func TestMessageQueue_ConcurrentStop(t *testing.T) {
	q := NewMessageQueue("test", "C123", "1234.5678", nil, false, nil)
	q.logger = testLogger()
	q.Start()

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			q.Stop(2 * time.Second)
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Stop did not complete within timeout")
	}
}

func TestBuildExternalKey(t *testing.T) {
	tests := []struct {
		channelID string
		threadTS  string
		want      string
	}{
		{"C123", "1234.5678", "C123:1234.5678"},
		{"C456", "", "C456:_"},
		{"C789", "9999.0000", "C789:9999.0000"},
	}
	for _, tt := range tests {
		got := buildExternalKey(tt.channelID, tt.threadTS)
		if got != tt.want {
			t.Errorf("buildExternalKey(%q, %q) = %q, want %q", tt.channelID, tt.threadTS, got, tt.want)
		}
	}
}

func TestStripBotMention(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<@U12345> hello", "hello"},
		{"<@W99999> test message", "test message"},
		{"no mention here", "no mention here"},
		{"<@U12345>", ""},
	}
	for _, tt := range tests {
		got := stripBotMention(tt.input)
		if got != tt.want {
			t.Errorf("stripBotMention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"日本語テスト", 4, "日..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
		if utf8.RuneCountInString(got) > tt.n {
			t.Errorf("truncate(%q, %d) = %q exceeds %d runes", tt.input, tt.n, got, tt.n)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", tt.input, tt.n, got)
		}
	}
}

func TestOperationTags(t *testing.T) {
	ops := []struct {
		op  Operation
		tag string
	}{
		{TextUpdateOp{}, "text_update"},
		{FileUploadOp{}, "file_upload"},
		{MessagePostOp{}, "message_post"},
		{MessageUpdateOp{}, "message_update"},
		{MessageDeleteOp{}, "message_delete"},
		{FinalizeOp{}, "finalize"},
		{stopSignal{}, "stop"},
	}
	for _, tt := range ops {
		if got := tt.op.opTag(); got != tt.tag {
			t.Errorf("%T.opTag() = %q, want %q", tt.op, got, tt.tag)
		}
	}
}

func testLogger() *slog.Logger {
	return slog.Default()
}

var _ = slog.Default

func TestSplitForSlack(t *testing.T) {
	if h, tl := splitForSlack("hello", 100); h != "hello" || tl != "" {
		t.Fatalf("short input: got head=%q tail=%q", h, tl)
	}

	body := strings.Repeat("a", 90) + "\n" + strings.Repeat("b", 50)
	h, tl := splitForSlack(body, 100)
	if got := utf8.RuneCountInString(h); got > 100 {
		t.Errorf("head %d runes, want <= 100", got)
	}
	if !strings.HasSuffix(h, "\n") {
		t.Errorf("expected break on newline; head ended %q", h[len(h)-1:])
	}
	if h+tl != body {
		t.Errorf("reassembly mismatch")
	}

	hard := strings.Repeat("x", 250)
	h, tl = splitForSlack(hard, 100)
	if got := utf8.RuneCountInString(h); got != 100 {
		t.Errorf("hard cut head %d runes, want 100", got)
	}
	if h+tl != hard {
		t.Errorf("reassembly mismatch on hard cut")
	}
}

func TestMessageQueue_RollsOverLongText(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-1", "C1", "1.0", api, false, testLogger())
	q.Start()

	long := strings.Repeat("lorem ipsum dolor sit amet\n", 2000)
	q.Send(TextUpdateOp{Text: long})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	msgs := api.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected long response split across >=2 messages, got %d", len(msgs))
	}
	total := 0
	for _, m := range msgs {
		n := utf8.RuneCountInString(m.Text)
		if n > slackMaxMessageRunes {
			t.Errorf("message exceeds Slack cap: %d > %d runes", n, slackMaxMessageRunes)
		}
		total += n
	}
	if want := utf8.RuneCountInString(long); total != want {
		t.Errorf("split changed text: %d runes across messages, want %d", total, want)
	}

	posts := 0
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" {
			posts++
		}
	}
	if posts < 2 {
		t.Errorf("expected >=2 PostMessage calls (rollover), got %d", posts)
	}
}

func TestMessageQueue_KeepsWhitespaceOnlyTextForTheNextText(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-sep", "C1", "1.0", api, false, testLogger())
	q.Start()

	q.Send(TextUpdateOp{Text: "\n\n"})
	q.WaitDrain()
	if msgs := api.Messages(); len(msgs) != 0 {
		t.Fatalf("whitespace-only text posted %d messages, want none", len(msgs))
	}

	q.Send(TextUpdateOp{Text: "Here is the table."})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	msgs := api.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "Here is the table.") {
		t.Errorf("messages = %+v, want one message with the next text", msgs)
	}
}

func TestMessageQueue_RollsOverLongMarkdown(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-md", "C1", "1.0", api, true, testLogger())
	q.Start()

	long := strings.Repeat("---\n", 12500)
	q.Send(TextUpdateOp{Text: long})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	msgs := api.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected markdown-expanded response split across >=2 messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if n := utf8.RuneCountInString(m.Text); n > slackMaxMessageRunes {
			t.Errorf("rendered message exceeds cap: %d > %d runes", n, slackMaxMessageRunes)
		}
	}
}

func TestSlackEditSize_MatchesSlackUpdateLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		text   string
		accept bool
	}{
		{"4000 letters", strings.Repeat("a", 4000), true},
		{"800 ampersands", strings.Repeat("&", 800), true},
		{"801 ampersands", strings.Repeat("&", 801), false},
		{"1000 less-than signs", strings.Repeat("<", 1000), true},
		{"1001 less-than signs", strings.Repeat("<", 1001), false},
		{"1333 em dashes", strings.Repeat("—", 1333), true},
		{"1334 em dashes", strings.Repeat("—", 1334), false},
		{"1333 box lines", strings.Repeat("─", 1333), true},
		{"401 emoji", strings.Repeat("😀", 401), false},
		{"1333 arrows", strings.Repeat("→", 1333), true},
		{"1333 check marks", strings.Repeat("✓", 1333), true},
		{"1334 greater-or-equal signs", strings.Repeat("≥", 1334), false},
	} {
		if got := slackEditSize(tc.text) <= 4000; got != tc.accept {
			t.Errorf("%s: slackEditSize = %d, within Slack's 4000 = %v, want %v", tc.name, slackEditSize(tc.text), got, tc.accept)
		}
	}
}

func TestMessageQueue_SplitsTextSlackMeasuresAboveItsLength(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-dash", "C1", "1.0", api, false, testLogger())
	q.Start()

	long := strings.Repeat("— stage & step —\n", 600)
	q.Send(TextUpdateOp{Text: long})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	for _, m := range api.Messages() {
		if n := slackEditSize(m.Text); n > slackMaxEditBytes {
			t.Errorf("message measures %d to Slack, over the %d cap", n, slackMaxEditBytes)
		}
	}
	if joined := joinedMessages(api); joined != long {
		t.Errorf("split changed the text: %d runes delivered, want %d", utf8.RuneCountInString(joined), utf8.RuneCountInString(long))
	}
}

func TestMessageQueue_RetriesAPartSlackRefusesAsTooLong(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-retry", "C1", "1.0", api, false, testLogger())
	q.Start()

	q.Send(TextUpdateOp{Text: "intro\n"})
	q.WaitDrain()
	api.SetNextError("UpdateMessage", errors.New("msg_too_long"))
	rest := strings.Repeat("lorem ipsum dolor\n", 150)
	q.Send(TextUpdateOp{Text: rest})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	if joined := joinedMessages(api); joined != "intro\n"+rest {
		t.Errorf("refused part lost text: %d runes delivered, want %d", utf8.RuneCountInString(joined), utf8.RuneCountInString("intro\n"+rest))
	}
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" && strings.Contains(c.Text, "could not be delivered") {
			t.Error("a part that fit after a smaller split posted a delivery-gap notice")
		}
	}
}

type testItems struct {
	mu sync.Mutex
	m  map[string]string
}

func newTestItems() *testItems { return &testItems{m: map[string]string{}} }

func (t *testItems) put(item, value string) { t.mu.Lock(); defer t.mu.Unlock(); t.m[item] = value }

func (t *testItems) delete(item string) { t.mu.Lock(); defer t.mu.Unlock(); delete(t.m, item) }

func (t *testItems) get(item string) string { t.mu.Lock(); defer t.mu.Unlock(); return t.m[item] }

func (t *testItems) answers() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return answerMessages(t.m)
}

func takeoverQueue(api *slackapi.InMemorySlackAPI, items *testItems) *MessageQueue {
	q := NewMessageQueue("task-take", "C1", "1.0", api, false, testLogger())
	q.record = items.put
	q.forget = items.delete
	return q
}

func fillAnswerMessages(t *testing.T, api *slackapi.InMemorySlackAPI, items *testItems, text string) []string {
	t.Helper()
	first := takeoverQueue(api, items)
	first.Start()
	first.Send(TextUpdateOp{Text: text})
	first.WaitDrain()
	first.Stop(5 * time.Second)
	return items.answers()
}

func joinedMessages(api *slackapi.InMemorySlackAPI) string {
	msgs := api.Messages()
	slices.SortFunc(msgs, func(a, b slackapi.StoredMessage) int { return strings.Compare(a.TS, b.TS) })
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Text)
	}
	return b.String()
}

func TestMessageQueue_TakeoverRewritesTheAnswerMessagesInOrder(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	answer := strings.Repeat("lorem ipsum dolor sit amet\n", 400)
	filled := fillAnswerMessages(t, api, items, answer[:9000])
	if len(filled) < 3 {
		t.Fatalf("setup: the first instance filled %d messages, want at least 3", len(filled))
	}

	next := takeoverQueue(api, items)
	next.resume(&resumedQueue{answerTS: items.answers()})
	next.Start()
	next.Send(TextUpdateOp{Text: answer})
	next.Send(answerDoneOp{})
	next.WaitDrain()
	next.Stop(5 * time.Second)

	if got := joinedMessages(api); got != answer {
		t.Errorf("thread holds %d runes, want the answer once (%d runes)", utf8.RuneCountInString(got), utf8.RuneCountInString(answer))
	}
	if got := items.answers(); len(got) != len(api.Messages()) || !slices.Equal(got[:len(filled)], filled) {
		t.Errorf("answer items = %v, want the filled messages %v followed by the new ones, %d in all", got, filled, len(api.Messages()))
	}
}

func TestMessageQueue_TakeoverDeletesAnswerMessagesTheFinalAnswerDoesNotNeed(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	if filled := fillAnswerMessages(t, api, items, strings.Repeat("lorem ipsum dolor sit amet\n", 400)); len(filled) < 2 {
		t.Fatalf("setup: the first instance filled %d messages, want at least 2", len(filled))
	}

	next := takeoverQueue(api, items)
	next.resume(&resumedQueue{answerTS: items.answers()})
	next.Start()
	next.Send(TextUpdateOp{Text: "short final answer"})
	next.Send(answerDoneOp{})
	next.WaitDrain()
	next.Stop(5 * time.Second)

	if msgs := api.Messages(); len(msgs) != 1 || msgs[0].Text != "short final answer" {
		t.Errorf("messages = %+v, want the final answer alone", msgs)
	}
	if got := items.answers(); len(got) != 1 {
		t.Errorf("answer items = %v, want one", got)
	}
}

func TestMessageQueue_TakeoverWithoutTextKeepsThePartialAnswer(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	partial := strings.Repeat("lorem ipsum dolor sit amet\n", 400)
	filled := fillAnswerMessages(t, api, items, partial)

	next := takeoverQueue(api, items)
	next.resume(&resumedQueue{answerTS: items.answers()})
	next.startNewMessage()
	if next.currentMsgTS != filled[0] {
		t.Fatalf("current message = %q before any text, want the first answer message %q", next.currentMsgTS, filled[0])
	}
	next.Start()
	next.Send(answerDoneOp{})
	next.WaitDrain()
	next.Stop(5 * time.Second)

	if got := joinedMessages(api); got != partial {
		t.Errorf("partial answer changed: %d runes in the thread, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(partial))
	}
}

func TestMessageQueue_RolloverHeadFailureNotifies(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("PostMessage", errors.New("rate_limited"))
	q := NewMessageQueue("task-fail", "C1", "1.0", api, false, testLogger())
	q.Start()

	long := strings.Repeat("lorem ipsum dolor\n", 3000)
	q.Send(TextUpdateOp{Text: long})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	gapNoticed := false
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" && strings.Contains(c.Text, "could not be delivered") {
			gapNoticed = true
		}
	}
	if !gapNoticed {
		t.Error("expected a delivery-gap notice after head-post failure, found none")
	}
}

func TestMessageQueue_HeadFrozenAfterRollover(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-stream", "C1", "1.0", api, false, testLogger())
	q.Start()

	chunk := strings.Repeat("a", 10000) + "\n"
	for range 5 {
		q.Send(TextUpdateOp{Text: chunk})
		q.WaitDrain()
	}
	q.Stop(5 * time.Second)

	calls := api.Calls()
	var postTS []string
	secondPostIdx := -1
	for i, c := range calls {
		if c.Method == "PostMessage" {
			postTS = append(postTS, c.ResponseTS)
			if len(postTS) == 2 {
				secondPostIdx = i
			}
		}
	}
	if len(postTS) < 2 {
		t.Fatalf("expected streaming response to roll over to >=2 posted messages, got %d", len(postTS))
	}
	headTS := postTS[0]
	for i, c := range calls {
		if i > secondPostIdx && c.Method == "UpdateMessage" && c.ResponseTS == headTS {
			t.Errorf("head message %s was updated after rollover; it should be frozen", headTS)
		}
	}
	for _, m := range api.Messages() {
		if n := utf8.RuneCountInString(m.Text); n > slackMaxMessageRunes {
			t.Errorf("message exceeds cap: %d > %d runes", n, slackMaxMessageRunes)
		}
	}
}

func TestIsMsgTooLong(t *testing.T) {
	if !isMsgTooLong(errors.New("msg_too_long")) {
		t.Error("msg_too_long should match")
	}
	if isMsgTooLong(errors.New("rate_limited")) {
		t.Error("unrelated error should not match")
	}
	if isMsgTooLong(nil) {
		t.Error("nil should not match")
	}
}

func TestMessageQueue_MsgTooLongDropsBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-blk", "C1", "1.0", api, true, testLogger())
	q.Start()

	table := "Results:\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n"

	q.Send(TextUpdateOp{Text: table})
	q.WaitDrain()

	postHadBlocks := false
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" && c.Blocks != "" {
			postHadBlocks = true
		}
	}
	if !postHadBlocks {
		t.Fatal("setup: first PostMessage carried no blocks; table did not render to Block Kit")
	}

	api.SetNextError("UpdateMessage", errors.New("msg_too_long"))
	q.Send(TextUpdateOp{Text: table + "| 3 | 4 |\n"})
	q.WaitDrain()

	q.Send(TextUpdateOp{Text: table + "| 3 | 4 |\n| 5 | 6 |\n"})
	q.WaitDrain()

	q.Stop(5 * time.Second)

	updates, textOnly := 0, 0
	for _, c := range api.Calls() {
		if c.Method != "UpdateMessage" {
			continue
		}
		updates++
		if c.Blocks == "" {
			textOnly++
		} else {
			t.Errorf("UpdateMessage still carried blocks after msg_too_long; blocks not dropped")
		}
	}
	if updates == 0 {
		t.Fatal("no UpdateMessage delivered after the failure — message appears frozen")
	}
	if textOnly == 0 {
		t.Error("expected a text-only UpdateMessage retry after msg_too_long")
	}
}

func TestMessageQueue_MsgTooLongDropsBlocksOnPost(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-blk-post", "C1", "1.0", api, true, testLogger())
	q.Start()

	table := "Results:\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n"
	if _, ok := BuildMessageBlocks(table); !ok {
		t.Fatal("setup: table did not render to Block Kit blocks")
	}

	api.SetNextError("PostMessage", errors.New("msg_too_long"))
	q.Send(TextUpdateOp{Text: table})
	q.WaitDrain()

	q.Send(TextUpdateOp{Text: table + "| 3 | 4 |\n"})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	posts, textOnlyPost := 0, 0
	for _, c := range api.Calls() {
		if c.Method != "PostMessage" {
			continue
		}
		posts++
		if c.Blocks == "" {
			textOnlyPost++
		} else {
			t.Errorf("recorded PostMessage carried blocks; expected the text-only retry")
		}
	}
	if posts == 0 {
		t.Fatal("no PostMessage delivered — first message frozen")
	}
	if textOnlyPost == 0 {
		t.Error("expected a text-only PostMessage retry after msg_too_long")
	}
	if len(api.Messages()) == 0 {
		t.Fatal("message not stored; currentMsgTS not set → streaming stalled")
	}
	for _, c := range api.Calls() {
		if c.Method == "UpdateMessage" && c.Blocks != "" {
			t.Errorf("UpdateMessage carried blocks after post-path drop; dropBlocks not propagated")
		}
	}
}

func TestMessageQueue_RolloverReattemptsBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-reset", "C1", "1.0", api, true, testLogger())

	blocks, ok := BuildMessageBlocks("| A | B |\n| --- | --- |\n| 1 | 2 |\n")
	if !ok {
		t.Fatal("setup: no blocks")
	}

	q.dropBlocks = true
	q.currentMsgTS = ""

	if err := q.writeCurrentMessage("fresh chunk", blocks); err != nil {
		t.Fatalf("writeCurrentMessage: %v", err)
	}
	if q.dropBlocks {
		t.Error("dropBlocks must reset to false when a fresh message starts")
	}
	sawBlockPost := false
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" && c.Blocks != "" {
			sawBlockPost = true
		}
	}
	if !sawBlockPost {
		t.Error("fresh message must re-attempt blocks after a prior message dropped them")
	}
}

func TestAnswerMessages_ReadsBothItemShapes(t *testing.T) {
	if got := answerMessages(map[string]string{itemAnswerMsgTS: "1.1"}); !slices.Equal(got, []string{"1.1"}) {
		t.Errorf("earlier release: got %v, want [1.1]", got)
	}
	if got := answerMessages(map[string]string{}); got != nil {
		t.Errorf("no answer yet: got %v, want none", got)
	}
	items := map[string]string{answerMsgItem(10): "1.10", answerMsgItem(2): "1.2", answerMsgItem(1): "1.1", itemAnswerMsgTS: "1.10"}
	if got := answerMessages(items); !slices.Equal(got, []string{"1.1", "1.2", "1.10"}) {
		t.Errorf("numbered items: got %v, want [1.1 1.2 1.10]", got)
	}
}

func TestMessageQueue_LateResumeLeavesAQueueThatStreamedItsOwnAnswer(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	q := takeoverQueue(api, items)
	q.Start()
	answer := strings.Repeat("lorem ipsum dolor sit amet\n", 400)
	q.MarkTextStreamed()
	q.Send(TextUpdateOp{Text: answer})
	q.WaitDrain()
	q.Send(resumeOp{r: &resumedQueue{answerTS: items.answers()}})
	q.Send(TextUpdateOp{Text: "the end\n"})
	q.Send(answerDoneOp{})
	q.WaitDrain()
	q.Stop(5 * time.Second)

	if got := joinedMessages(api); got != answer+"the end\n" {
		i := 0
		for i < len(got) && got[i] == (answer + "the end\n")[i] {
			i++
		}
		t.Errorf("thread differs at byte %d: %q", i, got[max(0, i-20):min(len(got), i+40)])
	}
}

func TestMessageQueue_TakeoverWhoseOverwriteFailsKeepsThePartialAnswer(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	partial := strings.Repeat("lorem ipsum dolor sit amet\n", 400)
	fillAnswerMessages(t, api, items, partial)

	next := takeoverQueue(api, items)
	next.resume(&resumedQueue{answerTS: items.answers()})
	api.SetNextError("UpdateMessage", errors.New("ratelimited"))
	next.Start()
	next.Send(TextUpdateOp{Text: "final"})
	next.Send(answerDoneOp{})
	next.WaitDrain()
	next.Stop(5 * time.Second)

	if got := joinedMessages(api); got != partial {
		t.Errorf("partial answer changed: %d runes in the thread, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(partial))
	}
}

func TestMessageQueue_UnusedAnswerMessagesStayListedUntilDeleted(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	items := newTestItems()
	filled := fillAnswerMessages(t, api, items, strings.Repeat("lorem ipsum dolor sit amet\n", 400))

	next := takeoverQueue(api, items)
	next.resume(&resumedQueue{answerTS: items.answers()})
	next.Start()
	next.Send(TextUpdateOp{Text: "short"})
	next.WaitDrain()
	api.SetNextError("DeleteMessage", errors.New("ratelimited"))
	next.Send(answerDoneOp{})
	next.WaitDrain()
	next.Stop(5 * time.Second)

	listed := items.answers()
	if !slices.Equal(listed, filled) {
		t.Errorf("after a failed delete the items list %v, want every message still there %v", listed, filled)
	}
}

func TestAnswerMessages_KeepsAnEarlierReleaseCurrentMessage(t *testing.T) {
	items := map[string]string{answerMsgItem(1): "1.1", answerMsgItem(2): "1.2", itemAnswerMsgTS: "1.3"}
	if got := answerMessages(items); !slices.Equal(got, []string{"1.1", "1.2", "1.3"}) {
		t.Errorf("got %v, want [1.1 1.2 1.3]", got)
	}
	items[itemAnswerMsgTS] = "1.1"
	if got := answerMessages(items); !slices.Equal(got, []string{"1.1", "1.2"}) {
		t.Errorf("a current message already numbered: got %v, want [1.1 1.2]", got)
	}

	recorded := newTestItems()
	q := takeoverQueue(slackapi.NewInMemorySlackAPI(), recorded)
	q.resume(&resumedQueue{answerTS: answerMessages(map[string]string{itemAnswerMsgTS: "1.1"})})
	if got := recorded.get(answerMsgItem(1)); got != "1.1" {
		t.Errorf("resume recorded %q for the first message, want the earlier release's message numbered", got)
	}
}
