// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func newStreamingQueue(api slackapi.SlackAPIAdapter) *MessageQueue {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewMessageQueue("task1", "D123", "1700000000.000001", api, true, logger)
	q.EnableStreaming(newActivityTimeline(logger), "U123", "T123", goslack.TaskDisplayModePlan, "Got it, thinking...")
	return q
}

func drive(q *MessageQueue, ops ...Operation) {
	q.Start()
	for _, op := range ops {
		q.Send(op)
	}
	q.WaitDrain()
	q.Stop(30 * time.Second)
}

func callsByMethod(calls []slackapi.RecordedCall, method string) []slackapi.RecordedCall {
	var out []slackapi.RecordedCall
	for _, c := range calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestStreaming_FullLifecycle(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	drive(q,
		ActivitySignalOp{Type: "tool_invocation_start", Data: map[string]any{
			"tool_name": "search_docs", "function_call_id": "fc1", "tool_args": map[string]any{"q": "x"},
		}},
		ActivitySignalOp{Type: "tool_result", Data: map[string]any{
			"function_call_id": "fc1", "result_data": map[string]any{"message": "5 hits"},
		}},
		TextUpdateOp{Text: "Here is the answer."},
		StreamFinalizeOp{},
	)

	calls := api.Calls()

	starts := callsByMethod(calls, "StartStream")
	if len(starts) != 1 {
		t.Fatalf("StartStream calls = %d, want 1", len(starts))
	}
	if starts[0].Params["task_display_mode"] != "plan" {
		t.Errorf("task_display_mode = %q, want plan", starts[0].Params["task_display_mode"])
	}
	if starts[0].ThreadTS != "1700000000.000001" {
		t.Errorf("thread_ts = %q, want the seed thread", starts[0].ThreadTS)
	}
	if starts[0].Params["recipient_user_id"] != "U123" || starts[0].Params["recipient_team_id"] != "T123" {
		t.Errorf("recipient params missing: %+v", starts[0].Params)
	}

	appends := callsByMethod(calls, "AppendStream")
	allChunks := ""
	for _, c := range appends {
		allChunks += c.Params["chunks"]
	}
	if !strings.Contains(allChunks, `"type":"task_update"`) {
		t.Errorf("no task_update chunk streamed: %s", allChunks)
	}
	if !strings.Contains(allChunks, "search docs") {
		t.Errorf("tool title missing from chunks: %s", allChunks)
	}
	if !strings.Contains(allChunks, `"status":"complete"`) {
		t.Errorf("tool card never completed: %s", allChunks)
	}
	if !strings.Contains(allChunks, `"type":"markdown_text"`) || !strings.Contains(allChunks, "Here is the answer.") {
		t.Errorf("answer not streamed as markdown_text: %s", allChunks)
	}

	if stops := callsByMethod(calls, "StopStream"); len(stops) != 1 {
		t.Fatalf("StopStream calls = %d, want 1", len(stops))
	}
	if calls[len(calls)-1].Method != "StopStream" {
		t.Errorf("last call = %s, want StopStream", calls[len(calls)-1].Method)
	}
	if posts := callsByMethod(calls, "PostMessage"); len(posts) != 0 {
		t.Errorf("PostMessage calls = %d, want 0 in streaming mode", len(posts))
	}
}

func TestStreaming_TableAnswerUsesBlocksChunk(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	answer := "Results:\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"
	drive(q,
		ActivitySignalOp{Type: "agent_progress_update", Data: map[string]any{"status_text": "Working"}},
		TextUpdateOp{Text: answer},
		StreamFinalizeOp{},
	)

	allChunks := ""
	for _, c := range callsByMethod(api.Calls(), "AppendStream") {
		allChunks += c.Params["chunks"]
	}
	if !strings.Contains(allChunks, `"type":"blocks"`) || !strings.Contains(allChunks, `"type":"table"`) {
		t.Errorf("table answer should stream as a blocks chunk with a table block: %s", allChunks)
	}
	if !strings.Contains(allChunks, `"type":"plan_update"`) {
		t.Errorf("agent_progress_update should stream a plan_update: %s", allChunks)
	}
}

func TestStreaming_ErrorFinalize(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	drive(q,
		ActivitySignalOp{Type: "agent_progress_update", Data: map[string]any{"status_text": "Working"}},
		StreamFinalizeOp{ErrorText: "❌ boom"},
	)

	allChunks := ""
	for _, c := range callsByMethod(api.Calls(), "AppendStream") {
		allChunks += c.Params["chunks"]
	}
	if !strings.Contains(allChunks, "boom") {
		t.Errorf("error text not streamed: %s", allChunks)
	}
	if len(callsByMethod(api.Calls(), "StopStream")) != 1 {
		t.Error("stream should be stopped after an error finalize")
	}
}

func TestStreaming_NoOutputNoStream(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	drive(q, StreamFinalizeOp{})
	if calls := api.Calls(); len(calls) != 0 {
		t.Errorf("expected no API calls for an empty task, got %d: %+v", len(calls), calls)
	}
}

func TestStreaming_StartStreamFailureFallsBack(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("StartStream", errStub)
	q := newStreamingQueue(api)
	drive(q,
		TextUpdateOp{Text: "answer text"},
		StreamFinalizeOp{},
	)
	if len(callsByMethod(api.Calls(), "PostMessage")) == 0 {
		t.Errorf("expected fallback PostMessage after StartStream failure, calls: %+v", api.Calls())
	}
}

func TestStreaming_StreamStartOpSeedsPlanTitle(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	drive(q, StreamStartOp{})

	calls := api.Calls()
	if got := len(callsByMethod(calls, "StartStream")); got != 1 {
		t.Fatalf("StartStream = %d, want 1 (immediate ack opens the stream at submit)", got)
	}
	allChunks := ""
	for _, c := range callsByMethod(calls, "AppendStream") {
		allChunks += c.Params["chunks"]
	}
	if !strings.Contains(allChunks, `"type":"plan_update"`) || !strings.Contains(allChunks, "Got it, thinking...") {
		t.Errorf("StreamStartOp should seed the plan header with the initial title: %s", allChunks)
	}
}

func TestStreaming_TimelineDisplayMode(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewMessageQueue("task1", "D123", "1700000000.000001", api, true, logger)
	q.EnableStreaming(newActivityTimeline(logger), "U123", "T123", goslack.TaskDisplayModeTimeline, "hi")
	drive(q, StreamStartOp{})

	starts := callsByMethod(api.Calls(), "StartStream")
	if len(starts) != 1 || starts[0].Params["task_display_mode"] != "timeline" {
		t.Errorf("want task_display_mode=timeline on StartStream, got %+v", starts)
	}
}

func TestStreaming_FinalizeAppendFailureFallsBack(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	q.Start()
	q.Send(StreamStartOp{})
	q.Send(TextUpdateOp{Text: "the answer"})
	q.WaitDrain()
	api.SetNextError("AppendStream", errStub)
	q.Send(StreamFinalizeOp{})
	q.WaitDrain()
	q.Stop(2 * time.Second)

	posts := callsByMethod(api.Calls(), "PostMessage")
	if len(posts) == 0 {
		t.Fatalf("finalize append failure must fall back to PostMessage so the answer isn't lost; calls: %+v", api.Calls())
	}
	joined := ""
	for _, p := range posts {
		joined += p.Text
	}
	if !strings.Contains(joined, "the answer") {
		t.Errorf("fallback PostMessage missing the answer: %q", joined)
	}
}

func TestFinalize_EmptyStatusTSPostsTerminalMessage(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewMessageQueue("task1", "D123", "thread1", api, true, logger)
	drive(q, finalizeStatus("", noticeError, "❌ Something went wrong"))

	posts := callsByMethod(api.Calls(), "PostMessage")
	if len(posts) != 1 || !strings.Contains(posts[0].Text, "went wrong") {
		t.Errorf("empty-StatusTS finalize should post the terminal message, got %+v", api.Calls())
	}
}

func allDeliveredText(calls []slackapi.RecordedCall) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(c.Text)
		b.WriteString(c.Blocks)
		b.WriteString(c.Params["chunks"])
	}
	return b.String()
}

func TestStreaming_LongAnswerNotTruncated(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	answer := "START-MARKER " + strings.Repeat("filler content sentence. ", 150) +
		" MID-MARKER " + strings.Repeat("filler content sentence. ", 150) + " END-MARKER"
	drive(q, StreamStartOp{}, TextUpdateOp{Text: answer}, StreamFinalizeOp{})

	all := allDeliveredText(api.Calls())
	for _, m := range []string{"START-MARKER", "MID-MARKER", "END-MARKER"} {
		if !strings.Contains(all, m) {
			t.Errorf("long answer missing %q — content dropped", m)
		}
	}
	streamed := ""
	for _, c := range callsByMethod(api.Calls(), "AppendStream") {
		streamed += c.Params["chunks"]
	}
	if !strings.Contains(streamed, `"type":"blocks"`) {
		t.Errorf("long answer should stream as split section blocks, not one markdown_text chunk")
	}
}

func TestStreaming_OversizedAnswerFallsBackToSplitMessages(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	answer := "START-MARKER\n" + strings.Repeat(strings.Repeat("a", 2900)+"\n", 27) +
		"MID-MARKER\n" + strings.Repeat(strings.Repeat("a", 2900)+"\n", 28) + "END-MARKER"
	drive(q, StreamStartOp{}, TextUpdateOp{Text: answer}, StreamFinalizeOp{})

	if len(callsByMethod(api.Calls(), "PostMessage")) == 0 {
		t.Fatalf("oversized answer should fall back to PostMessage split delivery; calls: %d", len(api.Calls()))
	}
	all := allDeliveredText(api.Calls())
	for _, m := range []string{"START-MARKER", "MID-MARKER", "END-MARKER"} {
		if !strings.Contains(all, m) {
			t.Errorf("oversized answer missing %q — content dropped", m)
		}
	}
}

func TestStreaming_FileUploadKeepsAnswerInStream(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	q := newStreamingQueue(api)
	drive(q,
		StreamStartOp{},
		TextUpdateOp{Text: "Here is your report."},
		FileUploadOp{Filename: "report.csv", Content: []byte("a,b\n1,2\n")},
		StreamFinalizeOp{},
	)

	streamed := ""
	for _, c := range callsByMethod(api.Calls(), "AppendStream") {
		streamed += c.Params["chunks"]
	}
	if !strings.Contains(streamed, "Here is your report.") {
		t.Errorf("answer should be delivered in the stream, chunks: %s", streamed)
	}
	for _, p := range callsByMethod(api.Calls(), "PostMessage") {
		if strings.Contains(p.Text, "Here is your report.") {
			t.Errorf("answer leaked to a plain PostMessage instead of the stream: %q", p.Text)
		}
	}
}

func TestNonStreaming_LongAnswerSplitsNotTruncated(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewMessageQueue("task1", "D123", "thread1", api, true, logger)
	answer := "START-MARKER " + strings.Repeat("word ", 2000) + " END-MARKER"
	drive(q, TextUpdateOp{Text: answer})

	all := allDeliveredText(api.Calls())
	if !strings.Contains(all, "START-MARKER") || !strings.Contains(all, "END-MARKER") {
		t.Errorf("long answer truncated: startPresent=%v endPresent=%v",
			strings.Contains(all, "START-MARKER"), strings.Contains(all, "END-MARKER"))
	}
	posts := callsByMethod(api.Calls(), "PostMessage")
	if len(posts) < 2 {
		t.Errorf("expected the long answer to split across multiple messages, got %d", len(posts))
	}
	for _, c := range api.Calls() {
		if c.Method == "PostMessage" || c.Method == "UpdateMessage" {
			if n := utf8.RuneCountInString(c.Text); n > slackMaxMessageRunes {
				t.Errorf("%s text = %d runes, exceeds cap %d (would be msg_too_long)", c.Method, n, slackMaxMessageRunes)
			}
		}
	}
}

func TestNonStreaming_WithinCapUpdateErrorPreservesContent(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewMessageQueue("task1", "D123", "thread1", api, false, logger)
	q.Start()
	q.Send(TextUpdateOp{Text: "first"})
	q.WaitDrain()
	api.SetNextError("UpdateMessage", errStub)
	q.Send(TextUpdateOp{Text: " second"})
	q.WaitDrain()
	q.Send(TextUpdateOp{Text: " third"})
	q.WaitDrain()
	q.Stop(30 * time.Second)

	all := allDeliveredText(api.Calls())
	if !strings.Contains(all, "first second third") {
		t.Errorf("a within-cap update error lost content; delivered: %q", all)
	}
	if strings.Contains(all, "could not be delivered") {
		t.Errorf("within-cap update error must not post a delivery-gap marker (content is preserved)")
	}
}

type stubErr struct{}

func (stubErr) Error() string { return "stub error" }

var errStub = stubErr{}
