// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	typingTestAgent   = "Builder"
	typingTestSender  = "15551239000"
	typingTestRefresh = 10 * time.Millisecond
	typingQuietPeriod = 15 * typingTestRefresh
)

func isReadStatus(b []byte) bool {
	var s outboundReadStatus
	return json.Unmarshal(b, &s) == nil && s.Status == "read"
}

func (m *waMock) sentSoFar() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.raw)
}

func (m *waMock) holdTypingPosts(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	m.mu.Lock()
	m.typingGate = gate
	m.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

func sendLog(raw [][]byte) []string {
	out := make([]string, 0, len(raw))
	for _, b := range raw {
		var status outboundReadStatus
		if json.Unmarshal(b, &status) == nil && status.Status == "read" && status.TypingIndicator != nil {
			out = append(out, "typing:"+status.MessageID)
			continue
		}
		var msg outboundTextMessage
		_ = json.Unmarshal(b, &msg)
		if msg.Type == "text" {
			out = append(out, "text:"+msg.Text.Body)
			continue
		}
		out = append(out, msg.Type)
	}
	return out
}

func typingAfter(log []string, marker string) int {
	idx := -1
	for i := len(log) - 1; i >= 0; i-- {
		if log[i] == marker {
			idx = i
			break
		}
	}
	if idx < 0 {
		return -1
	}
	n := 0
	for _, entry := range log[idx+1:] {
		if strings.HasPrefix(entry, "typing:") {
			n++
		}
	}
	return n
}

func newTypingRig(t *testing.T, refresh time.Duration, logger ...*slog.Logger) (*Adapter, *waMock) {
	t.Helper()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, typingTestAgent, logger...)
	a.typingRefresh = refresh
	allowEverySubmit(t, a, typingTestAgent)
	t.Cleanup(a.stopTyping)
	return a, mock
}

func startTypingTask(t *testing.T, a *Adapter, wamid string) string {
	t.Helper()
	before := liveTaskIDs(a, typingTestSender)
	value, msg := textMessage(wamid, typingTestSender, "do the thing")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("deliver(%s) settled %v, want settleAck", wamid, got)
	}
	for _, id := range liveTaskIDs(a, typingTestSender) {
		if !slices.Contains(before, id) {
			return id
		}
	}
	t.Fatalf("deliver(%s) submitted no task", wamid)
	return ""
}

func waitForLog(t *testing.T, mock *waMock, why string, cond func(log []string) bool) []string {
	t.Helper()
	var log []string
	if !waitFor(5*time.Second, func() bool {
		log = sendLog(mock.sentSoFar())
		return cond(log)
	}) {
		t.Fatalf("%s; sends were %q", why, log)
	}
	return log
}

func assertTypingEndsWith(t *testing.T, mock *waMock, marker, why string) {
	t.Helper()
	waitForLog(t, mock, "waiting for "+marker, func(log []string) bool { return slices.Contains(log, marker) })
	time.Sleep(typingQuietPeriod)
	log := sendLog(mock.rawSends())
	if n := typingAfter(log, marker); n != 0 {
		t.Fatalf("%s: %d typing indicator(s) posted after %q; sends were %q", why, n, marker, log)
	}
}

func TestTyping_RefreshedUntilTheTaskEnds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		end    func(a *Adapter, taskID string)
		marker string
	}{
		{
			name: "final answer",
			end: func(a *Adapter, taskID string) {
				a.OnFinalResponse(t.Context(), taskID, completedTask("all done"), nil)
			},
			marker: "text:all done",
		},
		{
			name: "task error",
			end: func(a *Adapter, taskID string) {
				a.OnError(t.Context(), taskID, &a2a.Error{Code: -32000, Message: "boom"}, nil)
			},
			marker: "text:" + msgAgentError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, mock := newTypingRig(t, typingTestRefresh)
			taskID := startTypingTask(t, a, "wamid.long")

			waitForLog(t, mock, "a task outliving Meta's expiry must have its indicator re-posted",
				func(log []string) bool {
					return len(log) >= 4 && !slices.ContainsFunc(log, func(e string) bool { return e != "typing:wamid.long" })
				})

			tc.end(a, taskID)
			assertTypingEndsWith(t, mock, tc.marker, "a finished task must stop its indicator")
		})
	}
}

func TestTyping_ReshownAfterAuthorization(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	taskID := startTypingTask(t, a, "wamid.auth")

	a.OnStatusUpdate(t.Context(), taskID, authRequiredEvent("https://idp.example/authorize"), nil)
	assertTypingEndsWith(t, mock, "interactive", "a task waiting on the user's consent must not show typing")

	a.OnStatusUpdate(t.Context(), taskID, authResultEvent(true), nil)
	waitForLog(t, mock, "the indicator must come back after the authorization follow-up",
		func(log []string) bool { return typingAfter(log, "text:"+authSuccessMessage) > 0 })

	a.OnFinalResponse(t.Context(), taskID, completedTask("created the ticket"), nil)
	assertTypingEndsWith(t, mock, "text:created the ticket", "the final answer must end the indicator")
}

func TestTyping_StaysOffAfterFailedAuthorization(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	taskID := startTypingTask(t, a, "wamid.denied")

	a.OnStatusUpdate(t.Context(), taskID, authRequiredEvent("https://idp.example/authorize"), nil)
	a.OnStatusUpdate(t.Context(), taskID, authResultEvent(false), nil)
	assertTypingEndsWith(t, mock, "text:"+authFailureMessage,
		"the user was told to try again, so nothing may look like it is still working")
}

func TestTyping_PausedForAQuestionAndResumedByTheAnswer(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	taskID := startTypingTask(t, a, "wamid.ask")

	a.OnStatusUpdate(t.Context(), taskID, inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	pending := waitPendingPrompt(t, a, typingTestSender)
	assertTypingEndsWith(t, mock, "interactive", "a task waiting on the user's answer must not show typing")

	value, reply := interactiveReply(typingTestSender, "wamid.answer", "opt:1")
	reply.Context = &messageContext{From: typingTestSender, ID: pending.PromptWamid}
	value.Messages[0].Context = reply.Context
	if got := a.deliver(value, reply); got != settleAck {
		t.Fatalf("answer settled %v, want settleAck", got)
	}
	waitForLog(t, mock, "answering the question must bring the indicator back on the answer",
		func(log []string) bool {
			at := slices.Index(log, "interactive")
			return at >= 0 && slices.Contains(log[at:], "typing:wamid.answer")
		})

	a.OnFinalResponse(t.Context(), taskID, completedTask("you picked blue"), nil)
	assertTypingEndsWith(t, mock, "text:you picked blue", "the final answer must end the indicator")
}

func TestTyping_ReshownAfterAMidTaskNotice(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, 0)
	startTypingTask(t, a, "wamid.working")

	sticker := &inboundMessage{From: typingTestSender, ID: "wamid.sticker", Type: "sticker"}
	a.deliver(&webhookChangeValue{Messages: []inboundMessage{*sticker}}, sticker)

	waitForLog(t, mock, "a notice sent mid-task clears the indicator, so it must be re-shown",
		func(log []string) bool { return typingAfter(log, "text:"+msgUnsupportedType) > 0 })
}

func TestTyping_SiblingTaskKeepsTheIndicator(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, 0)
	first := startTypingTask(t, a, "wamid.first")
	second := startTypingTask(t, a, "wamid.second")

	a.OnFinalResponse(t.Context(), first, completedTask("first done"), nil)
	waitForLog(t, mock, "the sibling task still working must re-show the indicator",
		func(log []string) bool { return typingAfter(log, "text:first done") > 0 })

	a.OnFinalResponse(t.Context(), second, completedTask("second done"), nil)
	assertTypingEndsWith(t, mock, "text:second done", "the last task's answer must end the indicator")
}

func TestTyping_FinalAnswerWaitsForTheIndicatorInFlight(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, 0)
	release := mock.holdTypingPosts(t)

	value, msg := textMessage("wamid.race", typingTestSender, "do the thing")
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		a.deliver(value, msg)
	}()
	waitForLog(t, mock, "the indicator POST must be in flight", func(log []string) bool {
		return slices.Equal(log, []string{"typing:wamid.race"})
	})
	taskID := liveTaskIDs(a, typingTestSender)[0]

	a.OnFinalResponse(t.Context(), taskID, completedTask("the answer"), nil)
	time.Sleep(typingQuietPeriod)
	if log := sendLog(mock.sentSoFar()); len(log) != 1 {
		t.Fatalf("the answer was sent while the indicator was still in flight; sends were %q", log)
	}

	release()
	<-delivered
	waitForLog(t, mock, "the answer must follow the indicator", func(log []string) bool {
		return slices.Equal(log, []string{"typing:wamid.race", "text:the answer"})
	})
}

type pauseAtAudit struct {
	wamid    string
	reached  chan struct{}
	release  chan struct{}
	once     sync.Once
	released sync.Once
}

func newPauseAtAudit(t *testing.T, wamid string) *pauseAtAudit {
	t.Helper()
	p := &pauseAtAudit{wamid: wamid, reached: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(p.resume)
	return p
}

func (p *pauseAtAudit) resume() { p.released.Do(func() { close(p.release) }) }

func (p *pauseAtAudit) Enabled(context.Context, slog.Level) bool { return true }
func (p *pauseAtAudit) WithAttrs([]slog.Attr) slog.Handler       { return p }
func (p *pauseAtAudit) WithGroup(string) slog.Handler            { return p }

func (p *pauseAtAudit) Handle(_ context.Context, r slog.Record) error {
	if r.Message != auditMsg {
		return nil
	}
	matched := false
	r.Attrs(func(attr slog.Attr) bool {
		matched = attr.Key == "wamid" && attr.Value.String() == p.wamid
		return !matched
	})
	if matched {
		p.once.Do(func() { close(p.reached) })
		<-p.release
	}
	return nil
}

func (p *pauseAtAudit) waitReached(t *testing.T) {
	t.Helper()
	select {
	case <-p.reached:
	case <-time.After(5 * time.Second):
		t.Fatalf("the inbound worker never reached the audit record for %s", p.wamid)
	}
}

func deliverAsync(a *Adapter, value *webhookChangeValue, msg *inboundMessage) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.deliver(value, msg)
	}()
	return done
}

func TestTyping_TaskAnsweredBeforeTheIndicatorIsShown(t *testing.T) {
	t.Parallel()
	pause := newPauseAtAudit(t, "wamid.fast")
	a, mock := newTypingRig(t, typingTestRefresh, slog.New(pause))

	value, msg := textMessage("wamid.fast", typingTestSender, "quick one")
	delivered := deliverAsync(a, value, msg)
	pause.waitReached(t)
	a.OnFinalResponse(t.Context(), liveTaskIDs(a, typingTestSender)[0], completedTask("instant answer"), nil)
	pause.resume()
	<-delivered

	assertTypingEndsWith(t, mock, "text:instant answer", "an answer that beat the indicator must still end it")
}

func TestTyping_QuestionAskedBeforeTheResumedIndicatorIsShown(t *testing.T) {
	t.Parallel()
	pause := newPauseAtAudit(t, "wamid.answer")
	a, mock := newTypingRig(t, typingTestRefresh, slog.New(pause))
	taskID := startTypingTask(t, a, "wamid.ask")

	a.OnStatusUpdate(t.Context(), taskID, inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	pending := waitPendingPrompt(t, a, typingTestSender)

	value, reply := interactiveReply(typingTestSender, "wamid.answer", "opt:1")
	reply.Context = &messageContext{From: typingTestSender, ID: pending.PromptWamid}
	value.Messages[0].Context = reply.Context
	delivered := deliverAsync(a, value, reply)
	pause.waitReached(t)
	a.OnStatusUpdate(t.Context(), taskID, inputNeededEvent(t, "req-2", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	pause.resume()
	<-delivered

	waitForLog(t, mock, "the follow-up question must be sent", func(log []string) bool {
		return len(slices.DeleteFunc(slices.Clone(log), func(e string) bool { return e != "interactive" })) == 2
	})
	assertTypingEndsWith(t, mock, "interactive", "a question asked before the resumed indicator showed must not get one")
}

func TestTyping_PausedForAQuestionThatCannotBeRendered(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	taskID := startTypingTask(t, a, "wamid.unrenderable")

	a.OnStatusUpdate(t.Context(), taskID, inputNeededEvent(t, "req-1", json.RawMessage(`"not a surface"`)), nil)
	time.Sleep(typingQuietPeriod)
	settled := len(mock.sentSoFar())
	time.Sleep(typingQuietPeriod)
	if got := len(mock.sentSoFar()); got != settled {
		t.Fatalf("indicator POSTs = %d while the agent waits on an unrenderable question, want %d", got, settled)
	}
}

func workingEvent() *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
}

func typingTaskIDs(a *Adapter) []string {
	a.typing.mu.Lock()
	defer a.typing.mu.Unlock()
	conv := a.typing.convs[typingTestSender]
	if conv == nil {
		return nil
	}
	ids := make([]string, 0, len(conv.tasks))
	for id := range conv.tasks {
		ids = append(ids, id)
	}
	return ids
}

func TestTyping_IdleLifetimeIsPerTask(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	a.typingLifetime = 50 * typingTestRefresh
	silent := startTypingTask(t, a, "wamid.silent")
	streaming := startTypingTask(t, a, "wamid.streaming")

	stop := make(chan struct{})
	streamed := make(chan struct{})
	go func() {
		defer close(streamed)
		ticker := time.NewTicker(typingTestRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.OnStatusUpdate(t.Context(), streaming, workingEvent(), nil)
			}
		}
	}()

	if !waitFor(5*time.Second, func() bool { return slices.Equal(typingTaskIDs(a), []string{streaming}) }) {
		close(stop)
		t.Fatalf("tracked tasks = %v, want only the streaming one once %s went silent past its lifetime", typingTaskIDs(a), silent)
	}
	before := len(mock.sentSoFar())
	if !waitFor(5*time.Second, func() bool { return len(mock.sentSoFar()) > before }) {
		close(stop)
		t.Fatal("releasing the silent task must not stop the indicator its streaming sibling still holds")
	}

	close(stop)
	<-streamed
	if !waitFor(5*time.Second, func() bool { return typingTaskIDs(a) == nil }) {
		t.Fatalf("tracked tasks = %v after the stream went quiet past its lifetime, want none", typingTaskIDs(a))
	}
}

func TestTyping_TaskWaitingOnTheUserIsReleased(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	a.typingLifetime = 10 * typingTestRefresh
	taskID := startTypingTask(t, a, "wamid.consent")

	a.OnStatusUpdate(t.Context(), taskID, authRequiredEvent("https://idp.example/authorize"), nil)
	if !waitFor(5*time.Second, func() bool { return typingTaskIDs(a) == nil }) {
		t.Fatalf("tracked tasks = %v, want none once the waiting task outlived its lifetime", typingTaskIDs(a))
	}
	if n := typingAfter(sendLog(mock.rawSends()), "interactive"); n != 0 {
		t.Fatalf("%d indicator(s) posted under the Authorize button", n)
	}
}

func TestTyping_StopCancelsTheRefresh(t *testing.T) {
	t.Parallel()
	a, mock := newTypingRig(t, typingTestRefresh)
	startTypingTask(t, a, "wamid.shutdown")
	waitForLog(t, mock, "the refresh must be running", func(log []string) bool { return len(log) >= 2 })

	a.stopTyping()
	time.Sleep(typingQuietPeriod)
	settled := len(mock.sentSoFar())
	time.Sleep(typingQuietPeriod)
	if got := len(mock.sentSoFar()); got != settled {
		t.Fatalf("indicator POSTs = %d after stop, want %d", got, settled)
	}
}
