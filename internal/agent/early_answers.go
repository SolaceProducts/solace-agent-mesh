// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const earlyAnswerGrace = time.Minute

type earlySlotKey struct {
	taskID    string
	requestID string
}

type earlyAnswerSlot struct {
	ownerUserID string
	expiresAt   time.Time
	answer      *a2a.UserInputResponseData
	sender      hilSender
}

type earlyAnswers struct {
	mu    sync.Mutex
	slots map[earlySlotKey]*earlyAnswerSlot

	now func() time.Time

	beforeStash     func()
	afterOwnerCheck func()
}

func (e *earlyAnswers) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

func (e *earlyAnswers) pruneExpired() {
	now := e.clock()
	for key, slot := range e.slots {
		if slot.answer == nil && now.After(slot.expiresAt) {
			delete(e.slots, key)
		}
	}
}

func (e *earlyAnswers) expect(taskID, requestID, ownerUserID string, timeout time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneExpired()
	if e.slots == nil {
		e.slots = make(map[earlySlotKey]*earlyAnswerSlot)
	}
	key := earlySlotKey{taskID: taskID, requestID: requestID}
	expiresAt := e.clock().Add(timeout + earlyAnswerGrace)
	if existing := e.slots[key]; existing != nil {
		if expiresAt.After(existing.expiresAt) {
			existing.expiresAt = expiresAt
		}
		return
	}
	e.slots[key] = &earlyAnswerSlot{ownerUserID: ownerUserID, expiresAt: expiresAt}
}

func (e *earlyAnswers) stashFrom(resp *a2a.UserInputResponseData, sender hilSender) (taskID string, found, refused bool) {
	if e.beforeStash != nil {
		e.beforeStash()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneExpired()
	accepted := false
	for key, slot := range e.slots {
		if key.requestID != resp.RequestID {
			continue
		}
		if !found {
			taskID, found = key.taskID, true
		}
		if !sender.mayAnswer(slot.ownerUserID) {
			continue
		}
		if e.afterOwnerCheck != nil {
			e.afterOwnerCheck()
		}
		if slot.answer == nil {
			slot.answer, slot.sender = resp, sender
		}
		accepted = true
	}
	return taskID, found, found && !accepted
}

type takenEarlySlot struct {
	requestID string
	slot      *earlyAnswerSlot
}

func (e *earlyAnswers) takeTask(taskID string) []takenEarlySlot {
	e.mu.Lock()
	defer e.mu.Unlock()
	var taken []takenEarlySlot
	for key, slot := range e.slots {
		if key.taskID == taskID {
			taken = append(taken, takenEarlySlot{requestID: key.requestID, slot: slot})
			delete(e.slots, key)
		}
	}
	return taken
}

func (e *earlyAnswers) takeRequests(taskID string, requestIDs []string) []takenEarlySlot {
	e.mu.Lock()
	defer e.mu.Unlock()
	var taken []takenEarlySlot
	for _, requestID := range requestIDs {
		key := earlySlotKey{taskID: taskID, requestID: requestID}
		if slot, ok := e.slots[key]; ok {
			taken = append(taken, takenEarlySlot{requestID: requestID, slot: slot})
			delete(e.slots, key)
		}
	}
	return taken
}

func (e *earlyAnswers) forget(taskID, requestID string) *earlyAnswerSlot {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := earlySlotKey{taskID: taskID, requestID: requestID}
	slot := e.slots[key]
	delete(e.slots, key)
	return slot
}

func (a *Agent) expectEarlyAnswer(taskID, requestID, ownerUserID string, timeout time.Duration) {
	a.early.expect(taskID, requestID, ownerUserID, timeout)
}

func (a *Agent) stashEarlyAnswer(resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	taskID, found, refused := a.early.stashFrom(resp, sender)
	switch {
	case !found:
		return hilUnmatched
	case refused:
		warnControlResponseRefused(a.logger.With("requestID", resp.RequestID, "taskID", taskID), a2a.SignalUserInputResponse, sender)
		return hilRefused
	}
	a.logger.Debug("holding an answer for a task that has not paused on it yet", "taskID", taskID, "requestIDBytes", len(resp.RequestID))
	return hilDelivered
}

func (a *Agent) forgetEarlyAnswer(state *TaskExecutionState, requestID string) {
	if slot := a.early.forget(state.TaskID, requestID); slot != nil && slot.answer != nil {
		a.logger.Warn("dropping an early answer because its request was refused",
			"traceID", state.UserProps.TraceID, "taskID", state.TaskID, "requestIDBytes", len(requestID))
	}
}

func (a *Agent) forgetEarlyAnswers(taskID string) {
	for _, taken := range a.early.takeTask(taskID) {
		if taken.slot.answer != nil {
			a.logger.Debug("dropping an early answer because its task ended before it paused",
				"taskID", taskID, "requestIDBytes", len(taken.requestID))
		}
	}
}

func (a *Agent) applyEarlyAnswers(taskID string, requestIDs []string) {
	for _, taken := range a.early.takeRequests(taskID, requestIDs) {
		if taken.slot.answer == nil {
			continue
		}
		if a.deliverHILResponseToWaitOfTask(taskID, taken.slot.answer, taken.slot.sender) == hilUnmatched {
			a.logger.Warn("an answer that arrived before its task paused matches no paused call",
				"taskID", taskID, "requestIDBytes", len(taken.requestID))
		}
	}
}
