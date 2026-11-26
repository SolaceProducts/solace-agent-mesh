// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Operation interface {
	opTag() string
}

type TextUpdateOp struct{ Text string }

func (TextUpdateOp) opTag() string { return "text_update" }

type ReplaceTextOp struct{ Text string }

func (ReplaceTextOp) opTag() string { return "replace_text" }

type StatusUpdateOp struct {
	StatusActivityID string
	Text             string
}

func (StatusUpdateOp) opTag() string { return "status_update" }

type FinalizeOp struct {
	StatusActivityID string
	Text             string
}

func (FinalizeOp) opTag() string { return "finalize" }

type DeleteOp struct {
	StatusActivityID string
}

func (DeleteOp) opTag() string { return "delete" }

type flushOp struct{ done chan struct{} }

func (flushOp) opTag() string { return "flush" }

type stopSignal struct{ discard bool }

func (stopSignal) opTag() string { return "stop" }

type MessageQueue struct {
	taskID         string
	conversationID string
	serviceURL     string
	recipientID    string
	replyToID      string

	botClient *BotClient
	logger    *slog.Logger

	feedbackEnabled bool
	minAPIInterval  time.Duration
	bufferInterval  time.Duration
	flushTimeout    time.Duration

	ops       chan Operation
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	discarded atomic.Bool

	ending atomic.Bool

	stateMu sync.RWMutex

	accumulatedText  strings.Builder
	contentMessageID string
	lastAPICallTime  time.Time
	lastFlushTime    time.Time
	sentText         string

	recordContentID func(id string)
}

func NewMessageQueue(taskID, conversationID, serviceURL, recipientID, replyToID string, botClient *BotClient, logger *slog.Logger) *MessageQueue {
	return &MessageQueue{
		taskID:         taskID,
		conversationID: conversationID,
		serviceURL:     serviceURL,
		recipientID:    recipientID,
		replyToID:      replyToID,
		botClient:      botClient,
		logger:         logger,
		minAPIInterval: 200 * time.Millisecond,
		bufferInterval: 500 * time.Millisecond,
		flushTimeout:   defaultFlushTimeout,
		ops:            make(chan Operation, 256),
		done:           make(chan struct{}),
	}
}

func (q *MessageQueue) SetMinThrottle(d time.Duration) {
	q.minAPIInterval = d
}

func (q *MessageQueue) SetBufferInterval(d time.Duration) {
	q.bufferInterval = d
}

func (q *MessageQueue) Start() {
	q.wg.Go(q.processLoop)
}

func (q *MessageQueue) Send(op Operation) {
	q.ops <- op
}

func (q *MessageQueue) markEnding() {
	q.ending.Store(true)
}

func (q *MessageQueue) endingHere() bool {
	return q.ending.Load()
}

func (q *MessageQueue) Stop(timeout time.Duration) {
	q.stop(stopSignal{}, timeout)
}

func (q *MessageQueue) Discard(timeout time.Duration) {
	q.stop(stopSignal{discard: true}, timeout)
}

func (q *MessageQueue) stop(signal stopSignal, timeout time.Duration) {
	if signal.discard {
		q.discarded.Store(true)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case q.ops <- signal:
	case <-q.done:
		return
	case <-timer.C:
		q.logger.Warn("queue stop timeout", "taskID", q.taskID)
		q.closeDone()
		return
	}
	select {
	case <-q.done:
	case <-timer.C:
		q.logger.Warn("queue stop timeout", "taskID", q.taskID)
	}
}

func (q *MessageQueue) closeDone() {
	q.closeOnce.Do(func() { close(q.done) })
}

const defaultFlushTimeout = time.Minute

const flushSizeThresholdChars = 1000

func (q *MessageQueue) Flush() {
	done := make(chan struct{})
	select {
	case q.ops <- flushOp{done: done}:
	case <-q.done:
		return
	}
	timer := time.NewTimer(q.flushTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-q.done:
	case <-timer.C:
		q.logger.Warn("queue flush timed out", "taskID", q.taskID)
	}
}

func (q *MessageQueue) flushUnsentText() {
	if q.snapshotText() == q.sentText {
		return
	}
	q.flushTextBuffer()
}

func (q *MessageQueue) processLoop() {
	defer q.closeDone()

	var flushTimer *time.Timer
	if q.bufferInterval > 0 {
		flushTimer = time.NewTimer(q.bufferInterval)
		flushTimer.Stop()
	}

	for {
		if flushTimer != nil {
			select {
			case op, ok := <-q.ops:
				if !ok {
					return
				}
				if _, isStop := op.(stopSignal); isStop {
					flushTimer.Stop()
					q.flushUnsentText()
					return
				}
				q.handleOp(op)

			case <-flushTimer.C:
				q.flushTextBuffer()
			}
		} else {
			op, ok := <-q.ops
			if !ok {
				return
			}
			if _, isStop := op.(stopSignal); isStop {
				q.flushUnsentText()
				return
			}
			q.handleOp(op)
		}
	}
}

func (q *MessageQueue) handleOp(op Operation) {
	defer func() {
		if r := recover(); r != nil {
			q.logger.Error("panic in queue processor", "taskID", q.taskID, "op", op.opTag(), "panic", r)
		}
	}()

	if q.discarded.Load() {
		if flush, ok := op.(flushOp); ok {
			close(flush.done)
		}
		return
	}

	switch o := op.(type) {
	case TextUpdateOp:
		q.handleTextUpdate(o)
	case ReplaceTextOp:
		q.replaceText(o.Text)
		q.flushTextBuffer()
	case StatusUpdateOp:
		q.handleStatusUpdate(o)
	case FinalizeOp:
		q.handleFinalize(o)
	case DeleteOp:
		q.handleDelete(o)
	case flushOp:
		q.handleFlush(o)
	default:
		q.logger.Warn("unknown queue operation", "taskID", q.taskID, "type", op.opTag())
	}
}

func (q *MessageQueue) handleTextUpdate(op TextUpdateOp) {
	combined := op.Text
	for {
		select {
		case next := <-q.ops:
			if tu, ok := next.(TextUpdateOp); ok {
				combined += tu.Text
			} else if _, isStop := next.(stopSignal); isStop {
				q.appendText(combined)
				q.flushUnsentText()
				select {
				case q.ops <- next:
				default:
				}
				return
			} else {
				q.appendText(combined)
				q.flushTextBuffer()
				q.handleOp(next)
				return
			}
		default:
			goto done
		}
	}
done:
	q.appendText(combined)

	if q.bufferInterval <= 0 {
		q.flushTextBuffer()
		return
	}

	if q.textLen() > flushSizeThresholdChars || time.Since(q.lastFlushTime) >= q.bufferInterval {
		q.flushTextBuffer()
	}
}

func (q *MessageQueue) flushTextBuffer() {
	if q.discarded.Load() {
		return
	}
	text := q.snapshotText()
	if text == "" {
		return
	}

	q.lastFlushTime = time.Now()
	q.applyRateLimit()

	ctx := context.Background()
	meta := &TeamsTaskMeta{
		ConversationID: q.conversationID,
		ServiceURL:     q.serviceURL,
	}

	if q.contentID() == "" {
		id, err := sendTeamsMessageWithFeedback(ctx, q.botClient, meta, text, nil, q.replyToID, q.feedbackEnabled)
		if err != nil {
			q.logger.Error("failed to send text message", "taskID", q.taskID, "error", err)
			return
		}
		q.setPostedContentID(id)
		q.sentText = text
		return
	}

	if err := updateTeamsMessageWithFeedback(ctx, q.botClient, meta, q.contentID(), text, nil, q.feedbackEnabled); err != nil {
		q.logger.Warn("update failed, posting new message", "taskID", q.taskID, "error", err)
		newID, postErr := sendTeamsMessageWithFeedback(ctx, q.botClient, meta, text, nil, q.replyToID, q.feedbackEnabled)
		if postErr != nil {
			q.logger.Warn("fallback POST also failed", "taskID", q.taskID, "error", postErr)
			return
		}
		q.setPostedContentID(newID)
	}
	q.sentText = text
}

func (q *MessageQueue) handleStatusUpdate(op StatusUpdateOp) {
	if op.StatusActivityID == "" {
		q.logger.Debug("status update skipped: no status activity id", "taskID", q.taskID)
		return
	}
	q.applyRateLimit()
	meta := &TeamsTaskMeta{ConversationID: q.conversationID, ServiceURL: q.serviceURL}
	if err := updateTeamsMessage(context.Background(), q.botClient, meta, op.StatusActivityID, op.Text, nil); err != nil {
		q.logger.Warn("failed to update status message", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) handleFinalize(op FinalizeOp) {
	q.flushUnsentText()

	q.applyRateLimit()
	meta := &TeamsTaskMeta{ConversationID: q.conversationID, ServiceURL: q.serviceURL}
	if op.StatusActivityID == "" {
		if _, err := sendTeamsMessage(context.Background(), q.botClient, meta, op.Text, nil, q.replyToID); err != nil {
			q.logger.Warn("failed to post terminal status message", "taskID", q.taskID, "error", err)
		}
		return
	}
	if err := updateTeamsMessage(context.Background(), q.botClient, meta, op.StatusActivityID, op.Text, nil); err != nil {
		q.logger.Warn("failed to finalize status message", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) handleDelete(op DeleteOp) {
	q.flushUnsentText()

	if op.StatusActivityID == "" {
		return
	}

	q.applyRateLimit()
	meta := &TeamsTaskMeta{ConversationID: q.conversationID, ServiceURL: q.serviceURL}
	if err := deleteTeamsMessage(context.Background(), q.botClient, meta, op.StatusActivityID); err != nil {
		q.logger.Warn("failed to delete status message", "taskID", q.taskID, "error", err)
	}
}

func (q *MessageQueue) handleFlush(op flushOp) {
	defer close(op.done)
	q.flushUnsentText()
}

func (q *MessageQueue) applyRateLimit() {
	if q.minAPIInterval <= 0 {
		return
	}
	elapsed := time.Since(q.lastAPICallTime)
	if elapsed < q.minAPIInterval {
		time.Sleep(q.minAPIInterval - elapsed)
	}
	q.lastAPICallTime = time.Now()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (q *MessageQueue) appendText(s string) {
	q.stateMu.Lock()
	q.accumulatedText.WriteString(s)
	q.stateMu.Unlock()
}

func (q *MessageQueue) textLen() int {
	q.stateMu.RLock()
	defer q.stateMu.RUnlock()
	return q.accumulatedText.Len()
}

func (q *MessageQueue) snapshotText() string {
	q.stateMu.RLock()
	defer q.stateMu.RUnlock()
	return strings.TrimSpace(q.accumulatedText.String())
}

func (q *MessageQueue) replaceText(s string) {
	q.stateMu.Lock()
	q.accumulatedText.Reset()
	q.accumulatedText.WriteString(s)
	q.stateMu.Unlock()
}

func (q *MessageQueue) setPostedContentID(id string) {
	q.setContentID(id)
	if q.recordContentID != nil {
		q.recordContentID(id)
	}
}

func (q *MessageQueue) contentID() string {
	q.stateMu.RLock()
	defer q.stateMu.RUnlock()
	return q.contentMessageID
}

func (q *MessageQueue) setContentID(id string) {
	q.stateMu.Lock()
	q.contentMessageID = id
	q.stateMu.Unlock()
}

func (q *MessageQueue) ContentMessageID() string {
	return q.contentID()
}

func (q *MessageQueue) ContentText() string {
	return q.snapshotText()
}
