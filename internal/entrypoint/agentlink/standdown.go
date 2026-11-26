// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	defaultSettleWait  = 10 * time.Second
	settlePublishers   = 8
	maxFlushWait       = 2 * time.Second
	flushShareOfSettle = 3
	answerGiveUpAfter  = 30 * time.Minute

	droppedWhileStandingDown   = "standing down"
	droppedFromALostConnection = "connection no longer the agent's"
	unlistedFrameType          = "(unknown)"

	droppedAFrame            = "agent link dropped a frame"
	endedWhatTheAgentWaitsOn = "agent link ended the prompts and peer calls the agent was waiting on, before closing its connections"
	forgotAHandedBackTask    = "agent link forgot a task whose request went back to its queue: the next serving replica delivers it again"
	gaveUpAnsweringATask     = "agent link gave up answering a task: its caller was never answered"
	stoodDown                = "agent link stood down: its agent connections are closed and its open tasks settled"
	standDownLostWork        = "agent link stand-down lost requests, could not publish answers or dropped model usage"
	droppedAResult           = "agent link dropped an agent's result while standing down: the agent sends it again when its task is delivered again"
	droppedModelUsage        = "agent link dropped an agent's model usage while standing down: SAM records no spend for it"
)

type standDownTally struct {
	failed             int
	forgotten          int
	unanswered         int
	requestsLost       int
	resultsDropped     int
	usageEventsDropped int
}

func (t standDownTally) lostWork() bool {
	return t.requestsLost > 0 || t.unanswered > 0 || t.usageEventsDropped > 0
}

func (t standDownTally) plus(other standDownTally) standDownTally {
	return standDownTally{
		failed:             t.failed + other.failed,
		forgotten:          t.forgotten + other.forgotten,
		unanswered:         t.unanswered + other.unanswered,
		requestsLost:       t.requestsLost + other.requestsLost,
		resultsDropped:     t.resultsDropped + other.resultsDropped,
		usageEventsDropped: t.usageEventsDropped + other.usageEventsDropped,
	}
}

type departure struct {
	closeCode       int
	closeReason     string
	unanswered      taskFailure
	requestLost     taskFailure
	requestsEndHere bool
}

var theTermEnded = departure{
	closeCode:   websocket.CloseServiceRestart,
	closeReason: closedForHandover,
	unanswered:  failedByStandDown,
	requestLost: failedWithItsRequestLost,
}

func (s *agentState) standDown(settle context.Context, leaving departure) standDownTally {
	s.stopTakingInput(leaving)
	defer s.takeInputAgain()
	s.awaitQuiet(settle, "frames still being handled", func() int { return s.framesInFlight })
	s.stopDispatchingWithin(settle)
	peerCancelsDone := s.endWhatTheAgentWaitsOn(settle)
	for _, c := range s.connections() {
		c.closeWithoutWaiting(leaving.closeCode, leaving.closeReason)
		s.removeConn(c)
	}
	s.awaitQuiet(settle, "requests still being given back", func() int { return s.giveBacksInFlight })
	tally := s.settleTasksLeftBehind(settle, leaving)
	peerCancelsDone()
	s.forgetDescription()
	tally.resultsDropped, tally.usageEventsDropped = s.droppedWhileSettling()
	return tally
}

func (s *agentState) endWhatTheAgentWaitsOn(settle context.Context) (peerCancelsDone func()) {
	s.mu.Lock()
	prompts := make([]string, 0, len(s.asks))
	for requestID := range s.asks {
		prompts = append(prompts, requestID)
	}
	peerTaskIDs := make([]string, 0, len(s.subtasks))
	subtaskIDs := make([]string, 0, len(s.subtasks))
	for subtaskID, b := range s.subtasks {
		b.cancelled = true
		subtaskIDs = append(subtaskIDs, subtaskID)
		peerTaskIDs = append(peerTaskIDs, b.peerTaskID)
	}
	s.mu.Unlock()
	for _, requestID := range prompts {
		s.closePrompt(requestID, answerCanceled)
	}
	for _, subtaskID := range subtaskIDs {
		s.finishPeerCall(subtaskID, nil)
	}
	s.awaitQuiet(settle, "peer results still being queued", func() int { return s.peerResultsInFlight })
	peerCancelsDone = s.cancelPeerCallsWithin(settle, peerTaskIDs)
	s.flushConnectionsWithin(settle)
	if len(prompts)+len(subtaskIDs) > 0 {
		s.a.logger.Debug(endedWhatTheAgentWaitsOn, "agentName", s.cfg.Name, "prompts", len(prompts), "peerCalls", len(subtaskIDs))
	}
	return peerCancelsDone
}

func (s *agentState) cancelPeerCallsWithin(settle context.Context, peerTaskIDs []string) (done func()) {
	var cancelling sync.WaitGroup
	cancelling.Go(func() {
		var publishing sync.WaitGroup
		free := make(chan struct{}, settlePublishers)
		for _, peerTaskID := range peerTaskIDs {
			free <- struct{}{}
			publishing.Go(func() {
				defer func() { <-free }()
				s.router.CancelWithPropagationWithin(settle, peerTaskID, s.ns())
			})
		}
		publishing.Wait()
	})
	return cancelling.Wait
}

func (s *agentState) flushConnectionsWithin(settle context.Context) {
	budget := maxFlushWait
	if deadline, bounded := settle.Deadline(); bounded {
		budget = min(budget, time.Until(deadline)/flushShareOfSettle)
	}
	flush, cancel := context.WithTimeout(settle, budget)
	defer cancel()
	var flushing sync.WaitGroup
	for _, c := range s.connections() {
		flushing.Go(func() { c.flushWithin(flush) })
	}
	flushing.Wait()
}

func (s *agentState) stopTakingInput(leaving departure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settling, s.requestsEndHere = true, leaving.requestsEndHere
	s.resultsDropped, s.usageEventsDropped = 0, 0
}

func (s *agentState) droppedWhileSettling() (results, usageEvents int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resultsDropped, s.usageEventsDropped
}

func (s *agentState) takeInputAgain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settling, s.requestsEndHere = false, false
}

func (s *agentState) takesFrameFrom(c *conn, f inFrame) bool {
	s.mu.Lock()
	dropped := s.frameDropReasonLocked(c)
	if dropped == "" {
		s.framesInFlight++
	}
	s.mu.Unlock()
	if dropped == "" {
		return true
	}
	answered := false
	if dropped == droppedWhileStandingDown {
		answered = answerDroppedFrame(c, f)
		s.reportDroppedWork(f)
	}
	s.a.logger.Debug(droppedAFrame, "agentName", s.cfg.Name, "connectionID", c.id,
		"frameType", listedFrameType(f.Type), "reason", dropped, "answered", answered)
	return false
}

func (s *agentState) reportDroppedWork(f inFrame) {
	switch f.Type {
	case typeResult:
		s.mu.Lock()
		s.resultsDropped++
		s.mu.Unlock()
		s.a.logger.Warn(droppedAResult, "agentName", s.cfg.Name, "taskID", boundedRef(f.TaskID), "traceID", s.traceIDOf(f.TaskID))
	case typeEvent:
		usage := usageOfEvent(f.Event)
		if usage == nil {
			return
		}
		s.mu.Lock()
		s.usageEventsDropped++
		s.mu.Unlock()
		s.a.logger.Warn(droppedModelUsage, "agentName", s.cfg.Name, "taskID", boundedRef(f.TaskID), "traceID", s.traceIDOf(f.TaskID),
			"inputTokens", usage.Input, "outputTokens", usage.Output, "cachedInputTokens", usage.CachedInput, "reasoningTokens", usage.Reasoning)
	}
}

func usageOfEvent(raw json.RawMessage) *eventUsage {
	var ev agentEvent
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Kind != eventModelFinished {
		return nil
	}
	return ev.Usage
}

func (s *agentState) traceIDOf(taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.tasks[taskID]; b != nil {
		return a2a.DecodeUserProperties(b.props).TraceID
	}
	return ""
}

func answerDroppedFrame(c *conn, f inFrame) (answered bool) {
	switch f.Type {
	case typePrompt:
		if !validTopicLevel(f.RequestID) {
			return false
		}
		asked := &openPrompt{taskID: boundedRef(f.TaskID), approval: f.Approval != nil, questions: len(f.Questions)}
		return c.send(answerFrame(f.RequestID, asked, answerCanceled, nil))
	case typePeerCall:
		if !validTopicLevel(f.SubtaskID) {
			return false
		}
		return c.send(canceledPeerResult(f.SubtaskID))
	default:
		return false
	}
}

func canceledPeerResult(subtaskID string) outFrame {
	result := peerOutcome(&subtaskBinding{cancelled: true}, nil)
	result.Type, result.SubtaskID = typePeerResult, subtaskID
	return result
}

func (s *agentState) frameDropReasonLocked(c *conn) string {
	switch {
	case s.settling:
		return droppedWhileStandingDown
	case s.conns[c.id] != c:
		return droppedFromALostConnection
	default:
		return ""
	}
}

func listedFrameType(frameType string) string {
	switch frameType {
	case typeConsume, typeAck, typeNack, typeDescribe, typeRetire, typeEvent, typePrompt, typeResult, typePeerCall, typePeerCancel:
		return frameType
	default:
		return unlistedFrameType
	}
}

func (s *agentState) frameHandled() {
	s.mu.Lock()
	s.framesInFlight--
	s.mu.Unlock()
	s.signalQuiet()
}

func (s *agentState) givingBack() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.giveBacksInFlight++
}

func (s *agentState) gaveBack() {
	s.mu.Lock()
	s.giveBacksInFlight--
	s.mu.Unlock()
	s.signalQuiet()
}

func (s *agentState) signalQuiet() {
	select {
	case s.quiet <- struct{}{}:
	default:
	}
}

func (s *agentState) awaitQuiet(settle context.Context, waitingFor string, inFlight func() int) {
	for {
		s.mu.Lock()
		left := inFlight()
		s.mu.Unlock()
		if left == 0 {
			return
		}
		select {
		case <-s.quiet:
		case <-settle.Done():
			s.a.logger.Warn("agent link stand-down went on without waiting longer", "agentName", s.cfg.Name, "waitingFor", waitingFor, "inFlight", left)
			return
		}
	}
}

const dispatchLoopsPerAgent = 1

func (s *agentState) stopDispatchingWithin(settle context.Context) {
	select {
	case <-s.stopDispatching():
	case <-settle.Done():
		s.a.logger.Warn("agent link stand-down went on without waiting longer", "agentName", s.cfg.Name, "waitingFor", "dispatch to stop",
			"inFlight", dispatchLoopsPerAgent)
	}
	s.acknowledgeSettledRequestsInHand()
}

func (s *agentState) acknowledgeSettledRequestsInHandWhenStandingDown() {
	s.mu.Lock()
	settling := s.settling
	s.mu.Unlock()
	if settling {
		s.acknowledgeSettledRequestsInHand()
	}
}

func (s *agentState) acknowledgeSettledRequestsInHand() {
	s.mu.Lock()
	var settled []*broker.Message
	for _, c := range s.conns {
		for delivery, handed := range c.inflight {
			if b := s.tasks[handed.taskID]; s.requestsEndHere || (b != nil && (b.final || b.cancelled)) {
				delete(c.inflight, delivery)
				settled = append(settled, handed.request)
			}
		}
	}
	s.mu.Unlock()
	for _, request := range settled {
		request.Ack()
	}
}

func (s *agentState) settleTasksLeftBehind(settle context.Context, leaving departure) standDownTally {
	toFail, toForget := s.tasksLeftBehind(leaving)
	tally := standDownTally{forgotten: len(toForget)}
	var failed, unanswered atomic.Int32
	var settling sync.WaitGroup
	free := make(chan struct{}, settlePublishers)
	settleEach := func(settleOne func()) {
		free <- struct{}{}
		settling.Go(func() {
			defer func() { <-free }()
			settleOne()
		})
	}
	for _, taskID := range toForget {
		settleEach(func() { s.forgetTask(settle, taskID) })
	}
	for taskID, failure := range toFail {
		if failure == leaving.requestLost && !leaving.requestsEndHere {
			tally.requestsLost++
		}
		settleEach(func() {
			s.failOpenTask(settle, taskID, failure, everyOpenTask)
			s.releaseTaskWithin(settle, taskID)
			switch s.leftForTheSweepIfUnanswered(taskID, failure) {
			case answeredHere:
				failed.Add(1)
			case leftForTheSweep:
				unanswered.Add(1)
			case noLongerBound:
			}
		})
	}
	settling.Wait()
	tally.failed, tally.unanswered = int(failed.Load()), int(unanswered.Load())
	return tally
}

func everyOpenTask(*taskBinding) bool { return true }

func (s *agentState) tasksLeftBehind(leaving departure) (toFail map[string]taskFailure, toForget []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	toFail = map[string]taskFailure{}
	for taskID, b := range s.tasks {
		switch {
		case b.final:
			b.passedOverAsAnswered = true
		case b.unanswered != nil:
			toFail[taskID] = *b.unanswered
		case b.cancelled || b.acknowledged || leaving.requestsEndHere:
			toFail[taskID] = leaving.unanswered
		case b.requestLost:
			toFail[taskID] = leaving.requestLost
		default:
			toForget = append(toForget, taskID)
		}
	}
	return toFail, toForget
}

func (s *agentState) forgetTask(ctx context.Context, taskID string) {
	s.mu.Lock()
	b := s.tasks[taskID]
	var traceID string
	if b != nil {
		traceID = a2a.DecodeUserProperties(b.props).TraceID
	}
	s.mu.Unlock()
	if b == nil {
		return
	}
	s.a.logger.Debug(forgotAHandedBackTask, "agentName", s.cfg.Name, "taskID", taskID, "traceID", traceID)
	s.dropTask(ctx, taskID)
}

func (s *agentState) dropTask(ctx context.Context, taskID string) {
	s.mu.Lock()
	b := s.tasks[taskID]
	var failingSince time.Time
	if b != nil {
		failingSince = b.answerFailingSince
	}
	s.mu.Unlock()
	s.endTaskWithin(ctx, taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if b != nil && s.tasks[taskID] == b && !b.final && b.answerFailingSince.Equal(failingSince) {
		delete(s.tasks, taskID)
	}
}

type settledTask int

const (
	answeredHere settledTask = iota
	leftForTheSweep
	noLongerBound
)

func (s *agentState) leftForTheSweepIfUnanswered(taskID string, failure taskFailure) settledTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[taskID]
	switch {
	case b == nil:
		return noLongerBound
	case b.final:
		return answeredHere
	}
	b.unanswered, b.deadline = &failure, s.a.now()
	return leftForTheSweep
}

func (s *agentState) reopenUnanswered(b *taskBinding, leftAs taskFailure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reopenLocked(b) && b.unanswered == nil {
		b.unanswered, b.deadline = &leftAs, s.a.now()
	}
}

func (s *agentState) reopenLocked(b *taskBinding) (passedOverAsAnswered bool) {
	passedOverAsAnswered = b.passedOverAsAnswered
	b.final, b.passedOverAsAnswered = false, false
	return passedOverAsAnswered
}

func (s *agentState) reopenWithItsRequestGoingBack(b *taskBinding) (passedOverAsAnswered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reopenLocked(b)
}

func (s *agentState) forgetUnlessItsRequestWasLost(ctx context.Context, taskID string) {
	s.mu.Lock()
	b := s.tasks[taskID]
	handedBack := b != nil && !b.final && b.unanswered == nil
	if handedBack && b.requestLost {
		b.unanswered, b.deadline = &failedWithItsRequestLost, s.a.now()
		handedBack = false
	}
	s.mu.Unlock()
	if handedBack {
		s.forgetTask(ctx, taskID)
	}
}

func (s *agentState) answerNotPublished(ctx context.Context, taskID string, b *taskBinding, answer taskFailure, traceID string, err error) {
	s.mu.Lock()
	now := s.a.now()
	if b.answerFailingSince.IsZero() {
		b.answerFailingSince = now
	}
	failingFor := now.Sub(b.answerFailingSince)
	standingDown := s.settling
	s.mu.Unlock()
	said := []any{"agentName", s.cfg.Name, "taskID", taskID, "traceID", traceID, "answerKind", answer.kind,
		"failing_for_ms", failingFor.Milliseconds(), "gives_up_after_ms", answerGiveUpAfter.Milliseconds(), "error", err}
	if standingDown || failingFor < answerGiveUpAfter {
		s.a.logger.Warn(answer.publishFailedLog, said...)
		return
	}
	s.a.logger.Error(gaveUpAnsweringATask, said...)
	s.dropTask(ctx, taskID)
}
