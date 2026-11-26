// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	queueRefusedWarnLog = "the orphan-adoption queue is full; replies and status messages for calls held by another replica were not queued. A call whose reply no replica adopts is resolved as timed out by the expiry reaper"
	queueRefusedDebug   = "the orphan-adoption queue is full; this message was not queued"
	gaveUpErrorLog      = "giving up on recording an orphaned reply; the expiry reaper will resolve the call"
	droppedNotifyLog    = "notify: dropped a HIL response that could not be queued for recording; the task's holder may still apply it, otherwise the task waits out its input timeout"
	noPendingInfoLog    = "notify: HIL response with no matching pending request; ignoring"
	answerRetryDropLog  = "could not queue a user's answer for another recording attempt; the task's holder may still apply it, otherwise the task waits out its input timeout"
)

type orphanQueueSizes struct {
	bulk, answers int
}

func sizedOrphanQueues(agent *Agent, sizes orphanQueueSizes) {
	agent.orphanAdoptions = make(chan orphanAdoption, sizes.bulk)
	agent.orphanAnswers = make(chan orphanAdoption, sizes.answers)
}

func fillBulkQueue(agent *Agent) {
	for len(agent.orphanAdoptions) < cap(agent.orphanAdoptions) {
		agent.orphanAdoptions <- orphanAdoption{corrID: "filler", source: "filler"}
	}
}

func recordsAtLevel(logs *syncLogBuffer, msg, level string) int {
	n := 0
	for _, rec := range logs.records(msg) {
		if rec["level"] == level {
			n++
		}
	}
	return n
}

func TestUserAnswerIsQueuedWhenPeerRepliesStatusesAndRemoteRepliesFillTheirQueue(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	for i := range 3 * 192 {
		id := fmt.Sprintf("corr-for-another-replica-%d", i)
		switch i % 3 {
		case 0:
			agent.resumeOrphanPeerResponse(id, peerReplyMessage(t, "peer finished"))
		case 1:
			agent.noteOrphanPeerStatus(id)
		default:
			agent.resumeOrphanRemoteToolReply(id, []byte(`{}`))
		}
	}

	got := agent.recordAnswerForTaskHeldElsewhere(regionAnswer(), hilSenderUser(questionOwnerID))

	if diff := cmp.Diff(map[string]int{"bulk": 192, "answers": 1}, map[string]int{"bulk": len(agent.orphanAdoptions), "answers": len(agent.orphanAnswers)}); diff != "" {
		t.Errorf("queue depths after the flood and one answer (-want +got):\n%s", diff)
	}
	if !got {
		t.Error("recordAnswerForTaskHeldElsewhere() = false after a flood of peer and remote traffic, want the answer queued")
	}
}

func TestFanOutQueueTakesItsLastSlotAndRefusesTheNextReply(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	type outcome struct{ Accepted, FirstRefused, Queued int }
	got := outcome{FirstRefused: -1}
	for i := range 200 {
		id := fmt.Sprintf("corr-%d", i)
		var taken bool
		if i%2 == 0 {
			taken = agent.resumeOrphanPeerResponse(id, peerReplyMessage(t, "peer finished"))
		} else {
			taken = agent.resumeOrphanRemoteToolReply(id, []byte(`{}`))
		}
		if taken {
			got.Accepted++
		} else if got.FirstRefused < 0 {
			got.FirstRefused = i
		}
	}
	got.Queued = len(agent.orphanAdoptions)

	if diff := cmp.Diff(outcome{Accepted: 192, FirstRefused: 192, Queued: 192}, got); diff != "" {
		t.Errorf("fan-out intake over 200 replies (-want +got):\n%s", diff)
	}
}

func TestAnswerQueueTakesItsLastSlotAndRefusesTheNextAnswer(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	accepted := 0
	for range 65 {
		if agent.recordAnswerForTaskHeldElsewhere(regionAnswer(), hilSenderUser(questionOwnerID)) {
			accepted++
		}
	}

	if diff := cmp.Diff(map[string]int{"accepted": 64, "queued": 64}, map[string]int{"accepted": accepted, "queued": len(agent.orphanAnswers)}); diff != "" {
		t.Errorf("answer intake over 65 answers (-want +got):\n%s", diff)
	}
}

func TestQueuedAnswersAreTakenBeforeEarlierQueuedFanOutWork(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	agent.resumeOrphanPeerResponse("sub-first", peerReplyMessage(t, "peer finished"))
	agent.resumeOrphanRemoteToolReply("remote-second", []byte(`{}`))
	agent.noteOrphanPeerStatus("sub-third")
	agent.recordAnswerForTaskHeldElsewhere(regionAnswer(), hilSenderUser(questionOwnerID))

	var got []string
	for range 4 {
		work, ok := agent.nextOrphanWork(t.Context())
		if !ok {
			t.Fatal("nextOrphanWork() reported shutdown with work queued")
		}
		got = append(got, work.source)
	}

	if diff := cmp.Diff([]string{"user answer", "peer response", "remote tool reply", "peer status"}, got); diff != "" {
		t.Errorf("order work was taken in (-want +got):\n%s", diff)
	}
}

func TestRetryOfAFanOutReplyAtTheQueueLimitIsRefusedWithoutAnErrorLog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &syncLogBuffer{}
		agent := checkpointedAgent(t, session.NewMemoryStore())
		capturingLogs(logs)(agent)
		sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
		fillBulkQueue(agent)

		agent.retryOrphanAdoption(agent.remoteToolReplyAdoption("corr-retried", []byte(`{}`)))
		time.Sleep(time.Second)
		synctest.Wait()

		got := map[string]int{
			"queued":         len(agent.orphanAdoptions),
			"gave up errors": len(logs.records(gaveUpErrorLog)),
			"refusal warns":  recordsAtLevel(logs, queueRefusedWarnLog, "WARN"),
			"refusal debugs": recordsAtLevel(logs, queueRefusedDebug, "DEBUG"),
		}
		if diff := cmp.Diff(map[string]int{"queued": 192, "gave up errors": 0, "refusal warns": 1, "refusal debugs": 1}, got); diff != "" {
			t.Errorf("retry of a reply at the queue limit (-want +got):\n%s", diff)
		}
	})
}

func TestRetryOfAFanOutReplyBelowTheQueueLimitIsQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agent := checkpointedAgent(t, session.NewMemoryStore())
		sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})

		agent.retryOrphanAdoption(agent.remoteToolReplyAdoption("corr-retried", []byte(`{}`)))
		time.Sleep(time.Second)
		synctest.Wait()

		if diff := cmp.Diff(1, len(agent.orphanAdoptions)); diff != "" {
			t.Errorf("fan-out queue depth after a retry with room (-want +got):\n%s", diff)
		}
	})
}

func TestRetryOfAUserAnswerIsQueuedWhileTheFanOutQueueIsFull(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	fillBulkQueue(agent)
	answer := orphanAdoption{corrID: askRequestID, source: "user answer", userAnswer: true}

	agent.requeueOrphanAdoption(answer)

	if diff := cmp.Diff(1, len(agent.orphanAnswers)); diff != "" {
		t.Errorf("answer queue depth after a retry (-want +got):\n%s", diff)
	}
}

func TestRetryOfAUserAnswerWithNoRoomWarnsThatTheAnswerWasDropped(t *testing.T) {
	logs := &syncLogBuffer{}
	agent := checkpointedAgent(t, session.NewMemoryStore())
	capturingLogs(logs)(agent)
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 1})
	agent.orphanAnswers <- orphanAdoption{corrID: "already-queued", userAnswer: true}

	agent.requeueOrphanAdoption(orphanAdoption{corrID: askRequestID, source: "user answer", userAnswer: true, attempts: 2})

	got := map[string]int{
		"queued":       len(agent.orphanAnswers),
		"drop warns":   recordsAtLevel(logs, answerRetryDropLog, "WARN"),
		"fan-out warn": len(logs.records(queueRefusedWarnLog)),
		"error logs":   len(logs.records(gaveUpErrorLog)),
	}
	if diff := cmp.Diff(map[string]int{"queued": 1, "drop warns": 1, "fan-out warn": 0, "error logs": 0}, got); diff != "" {
		t.Errorf("retry of an answer with no room (-want +got):\n%s", diff)
	}
}

func TestRefusedFanOutRepliesLogDebugEachAndWarnOncePerInterval(t *testing.T) {
	logs := &syncLogBuffer{}
	agent := checkpointedAgent(t, session.NewMemoryStore())
	capturingLogs(logs)(agent)
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 64})
	fillBulkQueue(agent)

	for i := range 5 {
		agent.resumeOrphanRemoteToolReply(fmt.Sprintf("corr-shed-%d", i), []byte(`{}`))
	}

	warns := logs.records(queueRefusedWarnLog)
	if diff := cmp.Diff(map[string]int{"debug": 5, "warn": 1}, map[string]int{"debug": recordsAtLevel(logs, queueRefusedDebug, "DEBUG"), "warn": len(warns)}); diff != "" {
		t.Fatalf("shed logs for 5 refused replies (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"level": "WARN", "refused": float64(1), "latestSource": "remote tool reply", "queueDepth": float64(192), "queueCapacity": float64(192)},
		map[string]any{"level": warns[0]["level"], "refused": warns[0]["refused"], "latestSource": warns[0]["latestSource"], "queueDepth": warns[0]["queueDepth"], "queueCapacity": warns[0]["queueCapacity"]}); diff != "" {
		t.Errorf("shed warning fields (-want +got):\n%s", diff)
	}
}

func TestRefusalWarningsCarryTheCountSinceTheLastOne(t *testing.T) {
	type verdict struct {
		Warn    bool
		Refused int
	}
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var log orphanRefusalLog
	var got []verdict
	for _, offset := range []time.Duration{0, time.Second, 59 * time.Second, 61 * time.Second, 62 * time.Second} {
		warn, refused := log.note(start.Add(offset))
		got = append(got, verdict{warn, refused})
	}

	want := []verdict{{true, 1}, {false, 0}, {false, 0}, {true, 3}, {false, 0}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("note() verdicts at 0s, 1s, 59s, 61s, 62s (-want +got):\n%s", diff)
	}
}

func TestPeerStatusRefusedForALackOfRoomIsLoggedAtDebug(t *testing.T) {
	logs := &syncLogBuffer{}
	agent := checkpointedAgent(t, session.NewMemoryStore())
	capturingLogs(logs)(agent)
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 1, answers: 64})
	fillBulkQueue(agent)

	agent.noteOrphanPeerStatus("sub-status")

	if diff := cmp.Diff(1, recordsAtLevel(logs, queueRefusedDebug, "DEBUG")); diff != "" {
		t.Errorf("debug records for a refused status (-want +got):\n%s", diff)
	}
}

func TestNotifyAnswerThatCannotBeQueuedIsReportedAsDroppedNotAsUnmatched(t *testing.T) {
	logs := &syncLogBuffer{}
	agent := checkpointedAgent(t, session.NewMemoryStore())
	capturingLogs(logs)(agent)
	sizedOrphanQueues(agent, orphanQueueSizes{bulk: 192, answers: 1})
	agent.orphanAnswers <- orphanAdoption{corrID: "already-queued", userAnswer: true}
	msg := notifyAnswer(t, regionAnswer())
	msg.UserProperties = a2a.EncodeUserProperties(&a2a.UserProperties{TraceID: "trace-notify-1"})

	agent.handleNotifyHILResponse(msg)

	drops := logs.records(droppedNotifyLog)
	if diff := cmp.Diff(map[string]int{"drops": 1, "unmatched infos": 0}, map[string]int{"drops": len(drops), "unmatched infos": len(logs.records(noPendingInfoLog))}); diff != "" {
		t.Fatalf("records for a notify answer with no room in the answer queue (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]any{"level": "WARN", "requestID": askRequestID, "traceID": "trace-notify-1"},
		map[string]any{"level": drops[0]["level"], "requestID": drops[0]["requestID"], "traceID": drops[0]["traceID"]}); diff != "" {
		t.Errorf("drop warning fields (-want +got):\n%s", diff)
	}
}

func TestNotifyAnswerWithDurableResumeOffIsStillReportedAsUnmatched(t *testing.T) {
	logs := &syncLogBuffer{}
	agent, _, _ := newTestAgent()
	capturingLogs(logs)(agent)

	agent.handleNotifyHILResponse(&broker.Message{Payload: []byte(`{"type":"user_input_response","request_id":"ghost","status":"answered"}`)})

	if diff := cmp.Diff(map[string]int{"drops": 0, "unmatched infos": 1}, map[string]int{"drops": len(logs.records(droppedNotifyLog)), "unmatched infos": len(logs.records(noPendingInfoLog))}); diff != "" {
		t.Errorf("records for an unmatched notify answer with durable resume off (-want +got):\n%s", diff)
	}
}
