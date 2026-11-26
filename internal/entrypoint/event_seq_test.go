// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type logCapture struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	logger *slog.Logger
}

type syncWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func newLogCapture() *logCapture {
	c := &logCapture{}
	w := &syncWriter{mu: &c.mu, buf: &c.buf}
	c.logger = slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return c
}

func (c *logCapture) lines() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for line := range strings.SplitSeq(c.buf.String(), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (c *logCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *logCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}

func seqProps(publisherID string, seq uint64) map[string]string {
	return map[string]string{
		a2a.PropPublisherID: publisherID,
		a2a.PropEventSeq:    strconv.FormatUint(seq, 10),
	}
}

func TestEventSeqTracker_InOrderEventsAreSilent(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	for i := uint64(1); i <= 5; i++ {
		tracker.Observe("task-A", "trace-1", seqProps("agent-1", i))
	}
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("in-order events should not log, got: %v", got)
	}
}

func TestEventSeqTracker_GapLogsError(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 2))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 5))

	lines := cap.lines()
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d: %v", len(lines), lines)
	}
	rec := lines[0]
	if rec["level"] != "ERROR" {
		t.Fatalf("expected ERROR level, got %v", rec["level"])
	}
	if rec["taskID"] != "task-A" {
		t.Fatalf("taskID = %v, want task-A", rec["taskID"])
	}
	if rec["traceID"] != "trace-1" {
		t.Fatalf("traceID = %v, want trace-1", rec["traceID"])
	}
	if rec["publisherID"] != "agent-1" {
		t.Fatalf("publisherID = %v, want agent-1", rec["publisherID"])
	}
	if float64(rec["gap"].(float64)) != 2 {
		t.Fatalf("gap = %v, want 2", rec["gap"])
	}
	cap.reset()
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 6))
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("post-gap +1 should not re-alarm, got: %v", got)
	}
}

func TestEventSeqTracker_DuplicateLogsWarn(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 2))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 2))

	lines := cap.lines()
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d: %v", len(lines), lines)
	}
	if lines[0]["level"] != "WARN" {
		t.Fatalf("expected WARN, got %v", lines[0]["level"])
	}
	cap.reset()
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 3))
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("post-dup +1 should not re-alarm, got: %v", got)
	}
}

func TestEventSeqTracker_OutOfOrderLogsWarn(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 5))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 3))
	lines := cap.lines()
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines, got %d: %v", len(lines), lines)
	}
	if lines[0]["level"] != "ERROR" {
		t.Fatalf("first log should be gap ERROR, got %v", lines[0]["level"])
	}
	if lines[1]["level"] != "WARN" {
		t.Fatalf("second log should be out-of-order WARN, got %v", lines[1]["level"])
	}
}

func TestEventSeqTracker_StreamResetLogsDebug(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	for i := uint64(1); i <= 5; i++ {
		tracker.Observe("task-A", "trace-1", seqProps("agent-1", i))
	}
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))

	lines := cap.lines()
	if len(lines) != 1 {
		t.Fatalf("want 1 log line, got %d: %v", len(lines), lines)
	}
	rec := lines[0]
	if rec["level"] != "DEBUG" {
		t.Fatalf("expected DEBUG stream-reset, got %v", rec["level"])
	}
	if msg, _ := rec["msg"].(string); !strings.Contains(msg, "publisher stream reset") {
		t.Fatalf("msg = %v, want publisher stream reset", rec["msg"])
	}
	if rec["lastSeq"].(float64) != 5 || rec["currentSeq"].(float64) != 1 {
		t.Fatalf("lastSeq/currentSeq = %v/%v, want 5/1", rec["lastSeq"], rec["currentSeq"])
	}
	cap.reset()
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 2))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 3))
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("post-reset +1 advance should be silent, got: %v", got)
	}
}

func TestEventSeqTracker_DuplicateOfFirstEventLogsWarn(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	lines := cap.lines()
	if len(lines) != 1 || lines[0]["level"] != "WARN" {
		t.Fatalf("dup of first event should WARN (not reset), got: %v", lines)
	}
}

func TestEventSeqTracker_AbsentFieldsAreNoOp(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "", map[string]string{"other": "value"})
	tracker.Observe("task-A", "", map[string]string{a2a.PropPublisherID: "agent-1"})
	tracker.Observe("task-A", "", map[string]string{a2a.PropEventSeq: "3"})
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("absent fields must be silent (Python parity), got: %v", got)
	}
}

func TestEventSeqTracker_TwoPublishersOnSameTaskAreIndependent(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-parent", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-child", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-child", 2))
	tracker.Observe("task-A", "trace-1", seqProps("agent-parent", 2))
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("two-publisher interleave should be silent, got: %v", got)
	}
}

func TestEventSeqTracker_ForgetResetsState(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 2))
	tracker.Forget("task-A")
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("post-forget restart should be silent, got: %v", got)
	}
}

func TestEventSeqTracker_PublisherCapWarnsOnce(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	for i := range maxPublishersPerTask {
		tracker.Observe("task-A", "trace-1", seqProps("p-"+strconv.Itoa(i), 1))
	}
	if got := cap.lines(); len(got) != 0 {
		t.Fatalf("at cap should be silent, got %d lines", len(got))
	}
	tracker.Observe("task-A", "trace-1", seqProps("p-overflow-1", 1))
	tracker.Observe("task-A", "trace-1", seqProps("p-overflow-2", 1))
	tracker.Observe("task-A", "trace-1", seqProps("p-overflow-3", 1))

	lines := cap.lines()
	if len(lines) != 1 {
		t.Fatalf("expected exactly one cap warn, got %d: %v", len(lines), lines)
	}
	if lines[0]["level"] != "WARN" {
		t.Fatalf("expected WARN, got %v", lines[0]["level"])
	}
}

func TestEventSeqTracker_UnparseableSeqLogsWarn(t *testing.T) {
	cap := newLogCapture()
	tracker := NewEventSeqTracker(cap.logger)
	tracker.Observe("task-A", "trace-1", map[string]string{
		a2a.PropPublisherID: "agent-1",
		a2a.PropEventSeq:    "not-a-number",
	})
	lines := cap.lines()
	if len(lines) != 1 || lines[0]["level"] != "WARN" {
		t.Fatalf("expected one WARN for unparseable seq, got: %v", lines)
	}
}

func TestEventSeqTracker_NilReceiverIsSafe(t *testing.T) {
	var tracker *EventSeqTracker
	tracker.Observe("task-A", "trace-1", seqProps("agent-1", 1))
	tracker.Forget("task-A")
}

func TestEventSeqTracker_DrivenByEventLoop(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cap := newLogCapture()
	core.eventSeqTracker = NewEventSeqTracker(cap.logger)

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	statusTopic := a2a.GatewayStatusTopic("test-ns", "test-gw", "task-loop-1")
	publish := func(seq uint64, withProps bool) {
		t.Helper()
		event := &a2a.TaskStatusUpdateEvent{
			TaskID: "task-loop-1",
			Kind:   "status-update",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
		}
		resp := a2a.NewResponse("task-loop-1", event)
		payload, _ := json.Marshal(resp)
		msg := &broker.Message{Payload: payload}
		if withProps {
			msg.UserProperties = seqProps("agent-1", seq)
		}
		if err := brk.Publish(ctx, statusTopic, msg); err != nil {
			t.Fatalf("publish seq=%d: %v", seq, err)
		}
	}

	publish(1, true)
	publish(2, true)
	publish(5, true)
	publish(5, true)
	publish(0, false)
	publish(6, true)

	waitFor(t, 2*time.Second, func() bool {
		return len(cap.lines()) >= 2
	})
	time.Sleep(50 * time.Millisecond)

	lines := cap.lines()
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 logs (gap ERROR + duplicate WARN), got %d: %v", len(lines), lines)
	}
	if lines[0]["level"] != "ERROR" || lines[0]["taskID"] != "task-loop-1" {
		t.Fatalf("first log should be ERROR on task-loop-1, got %v", lines[0])
	}
	if lines[1]["level"] != "WARN" {
		t.Fatalf("second log should be WARN, got %v", lines[1])
	}
}
