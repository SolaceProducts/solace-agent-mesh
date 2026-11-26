// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	chat "google.golang.org/api/chat/v1"
)

type spaceThrottle struct {
	interval time.Duration

	mu   sync.Mutex
	next map[string]time.Time
}

const maxThrottledSpaces = 1024

func newSpaceThrottle(interval time.Duration) *spaceThrottle {
	return &spaceThrottle{interval: interval, next: make(map[string]time.Time)}
}

func (t *spaceThrottle) release(space string, slot, prev time.Time, hadPrev bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.next[space].Equal(slot.Add(t.interval)) {
		return
	}
	if hadPrev {
		t.next[space] = prev
		return
	}
	delete(t.next, space)
}

func (t *spaceThrottle) wait(ctx context.Context, space string) error {
	if t.interval <= 0 {
		return ctx.Err()
	}
	t.mu.Lock()
	now := time.Now()
	slot := now
	prev, hadPrev := t.next[space]
	if prev.After(now) {
		slot = prev
	}
	t.next[space] = slot.Add(t.interval)
	if len(t.next) > maxThrottledSpaces {
		for s, at := range t.next {
			if at.Before(now) {
				delete(t.next, s)
			}
		}
	}
	t.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.release(space, slot, prev, hadPrev)
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type chatWriter struct {
	api        chatAPI
	throttle   *spaceThrottle
	logger     *slog.Logger
	retryDelay func(attempt int, hint time.Duration) time.Duration
}

func newChatWriter(api chatAPI, throttle *spaceThrottle, logger *slog.Logger) *chatWriter {
	return &chatWriter{api: api, throttle: throttle, logger: logger, retryDelay: backoffDelay}
}

func backoffDelay(attempt int, hint time.Duration) time.Duration {
	if hint > 0 {
		return min(hint, outboundRetryCap)
	}
	base := min(outboundRetryBase<<min(attempt-1, 8), outboundRetryCap)
	return base + rand.N(outboundRetryJitter)
}

func (w *chatWriter) create(ctx context.Context, space, thread string, content messageContent) (string, error) {
	var name string
	err := w.withRetry(ctx, space, "create", func() error {
		var err error
		name, err = w.api.CreateMessage(ctx, space, thread, content)
		return err
	})
	return name, err
}

func (w *chatWriter) updateOnce(ctx context.Context, space, name string, content messageContent, replaceCards bool) error {
	if err := w.throttle.wait(ctx, space); err != nil {
		return fmt.Errorf("waiting for the space's write slot: %w", err)
	}
	return w.api.UpdateMessage(ctx, name, content, replaceCards)
}

func (w *chatWriter) update(ctx context.Context, space, name string, content messageContent, replaceCards bool) error {
	return w.withRetry(ctx, space, "update", func() error {
		return w.api.UpdateMessage(ctx, name, content, replaceCards)
	})
}

func (w *chatWriter) withRetry(ctx context.Context, space, op string, write func() error) error {
	return w.retry(ctx, space, op, write, true)
}

func (w *chatWriter) readWithRetry(ctx context.Context, op string, read func() error) error {
	return w.retry(ctx, "", op, read, false)
}

func (w *chatWriter) retry(ctx context.Context, space, op string, call func() error, throttled bool) error {
	failed := "google chat read failed; retrying"
	if throttled {
		failed = "google chat write failed; retrying"
	}
	var err error
	for attempt := 1; ; attempt++ {
		if throttled {
			if waitErr := w.throttle.wait(ctx, space); waitErr != nil {
				return waitErr
			}
		}
		err = call()
		if err == nil || isTerminalWriteError(err) {
			return err
		}
		if attempt >= outboundRetryAttempts {
			if throttled {
				w.logger.Error("google chat write failed after retries", append([]any{"step", op, "attempts", attempt}, causeFields(primaryKeys, err)...)...)
			}
			return err
		}
		var hint time.Duration
		if rl, ok := errors.AsType[*rateLimitedError](err); ok {
			hint = rl.retryAfter
		}
		delay := w.retryDelay(attempt, hint)
		w.logger.Warn(failed, append([]any{"step", op, "attempt", attempt, "retryIn_ms", delay.Milliseconds()}, causeFields(primaryKeys, err)...)...)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isTerminalWriteError(err error) bool {
	if errors.Is(err, errMessageGone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	_, permanent := errors.AsType[*permanentError](err)
	return permanent
}

type replyOp struct {
	kind     replyOpKind
	text     string
	lead     string
	cards    []*chat.CardWithId
	done     chan drainResult
	fallback *promptFallback
	signIn   bool
	terminal bool
	release  func()
}

type promptFallback struct {
	halt string
	stop func(error)
}

type drainResult int

const (
	drainUnfinished drainResult = iota
	drainWritten
	drainIncomplete
)

type replyOpKind int

const (
	opPlaceholder replyOpKind = iota
	opProgress
	opAppend
	opReplace
	opNotice
	opHalt
	opFlush
	opCard
	opPrompt
	opClearControls
	opConclude
	opResolveSignIn
	opRerender
)

type replyQueue struct {
	space       string
	thread      string
	writer      *chatWriter
	logger      *slog.Logger
	onAnchors   func(queueAnchors)
	adopt       func(ctx context.Context) (queueAnchors, bool)
	controls    []*chat.CardWithId
	citations   *citationBook
	signInLinks func(ctx context.Context, known []string) ([]string, bool)

	ops       chan replyOp
	stopCh    chan struct{}
	doneCh    chan struct{}
	once      sync.Once
	abandoned atomic.Bool

	lookahead         *replyOp
	inFlight          *replyOp
	messages          []string
	committed         []string
	segment           int
	controlsMessage   string
	signInMessages    []string
	signInOutcomes    map[string]string
	signInSettled     map[string]bool
	prompted          bool
	text              string
	hasAnswer         bool
	dirty             bool
	lastFlush         time.Time
	incompleteNoticed bool
	pendingNotices    []string
	noticeLost        bool
}

type queueAnchors struct {
	Status   string
	Controls string
	SignIn   []string
	Prompted bool
}

func newReplyQueue(space, thread string, writer *chatWriter, logger *slog.Logger, start queueAnchors, onAnchors func(queueAnchors)) *replyQueue {
	q := &replyQueue{
		space:           space,
		thread:          thread,
		writer:          writer,
		logger:          logger,
		onAnchors:       onAnchors,
		controlsMessage: start.Controls,
		signInMessages:  slices.Clone(start.SignIn),
		prompted:        start.Prompted,
		ops:             make(chan replyOp, replyQueueOpsCap),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
	if start.Status != "" {
		q.messages = []string{start.Status}
		q.committed = []string{""}
	}
	go q.run()
	return q
}

func (q *replyQueue) anchors() queueAnchors {
	a := queueAnchors{Controls: q.controlsMessage, SignIn: slices.Clone(q.signInMessages), Prompted: q.prompted}
	if q.segment < len(q.messages) {
		a.Status = q.messages[q.segment]
	}
	return a
}

func (q *replyQueue) reportAnchors() {
	if q.onAnchors != nil {
		q.onAnchors(q.anchors())
	}
}

func (q *replyQueue) send(op replyOp) {
	select {
	case <-q.stopCh:
		q.dropped(op)
		return
	default:
	}
	select {
	case q.ops <- op:
		select {
		case <-q.stopCh:
			<-q.doneCh
			q.dropBuffered()
		default:
		}
	case <-q.stopCh:
		q.dropped(op)
	}
}

func (q *replyQueue) dropped(op replyOp) {
	if q.abandoned.Load() {
		return
	}
	if op.fallback != nil && op.fallback.stop != nil {
		q.logger.Warn("google chat prompt dropped: the reply queue closed before posting it")
	}
	if op.release != nil {
		op.release()
	}
}

func (q *replyQueue) Placeholder(text string) { q.send(replyOp{kind: opPlaceholder, text: text}) }

func (q *replyQueue) Progress(text string) { q.send(replyOp{kind: opProgress, text: text}) }

func (q *replyQueue) Append(delta string) { q.send(replyOp{kind: opAppend, text: delta}) }

func (q *replyQueue) Replace(text string) { q.send(replyOp{kind: opReplace, text: text}) }

func (q *replyQueue) Rerender() { q.send(replyOp{kind: opRerender}) }

func (q *replyQueue) Notice(text string) { q.send(replyOp{kind: opNotice, text: text}) }

func (q *replyQueue) Halt(text string) { q.send(replyOp{kind: opHalt, text: text}) }

func (q *replyQueue) Card(fallbackText string, cards []*chat.CardWithId) {
	q.send(replyOp{kind: opCard, text: fallbackText, cards: cards})
}

func (q *replyQueue) Prompt(lead, fallbackText string, cards []*chat.CardWithId) {
	q.send(replyOp{kind: opPrompt, lead: lead, text: fallbackText, cards: cards})
}

func (q *replyQueue) PromptOrStop(lead, fallbackText string, cards []*chat.CardWithId, fallback promptFallback, release func()) {
	q.send(replyOp{kind: opPrompt, lead: lead, text: fallbackText, cards: cards, fallback: &fallback, release: release})
}

func (q *replyQueue) PromptSignIn(lead, fallbackText string, cards []*chat.CardWithId, fallback promptFallback, release func()) {
	q.send(replyOp{kind: opPrompt, lead: lead, text: fallbackText, cards: cards, fallback: &fallback, signIn: true, release: release})
}

func (q *replyQueue) ResolveSignIn(outcome string) {
	q.send(replyOp{kind: opResolveSignIn, text: outcome})
}

func (q *replyQueue) RetireSignIn(outcome string) {
	q.send(replyOp{kind: opResolveSignIn, text: outcome, terminal: true})
}

func (q *replyQueue) ClearControls() { q.send(replyOp{kind: opClearControls}) }

func (q *replyQueue) Conclude(fallbackText string, cards []*chat.CardWithId) {
	q.send(replyOp{kind: opConclude, text: fallbackText, cards: cards})
}

func (q *replyQueue) Drain(timeout time.Duration) drainResult {
	done := make(chan drainResult, 1)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case q.ops <- replyOp{kind: opFlush, done: done}:
	case <-q.stopCh:
		return drainUnfinished
	case <-timer.C:
		return drainUnfinished
	}
	select {
	case res := <-done:
		return res
	case <-q.doneCh:
		return drainUnfinished
	case <-timer.C:
		return drainUnfinished
	}
}

func (q *replyQueue) Close() {
	q.once.Do(func() { close(q.stopCh) })
	<-q.doneCh
}

func (q *replyQueue) Abandon() {
	q.abandoned.Store(true)
	q.Close()
}

func (q *replyQueue) run() {
	defer close(q.doneCh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-q.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer q.recoverRun()

	timer := time.NewTimer(textFlushInterval)
	defer timer.Stop()
	for {
		if op := q.lookahead; op != nil {
			q.lookahead = nil
			if q.stopped() {
				q.dropped(*op)
				q.dropBuffered()
				return
			}
			q.handleTracked(ctx, *op)
			continue
		}
		select {
		case <-q.stopCh:
			q.dropBuffered()
			return
		case op := <-q.ops:
			q.handleTracked(ctx, op)
		case <-timer.C:
			if q.dirty {
				q.absorbText()
				q.flush(ctx)
			}
			timer.Reset(textFlushInterval)
		}
	}
}

func (q *replyQueue) stopped() bool {
	select {
	case <-q.stopCh:
		return true
	default:
		return false
	}
}

func (q *replyQueue) handleTracked(ctx context.Context, op replyOp) {
	q.inFlight = &op
	q.handle(ctx, op)
	q.inFlight = nil
}

func (q *replyQueue) recoverRun() {
	r := recover()
	if r == nil {
		return
	}
	q.logger.Error("google chat reply queue panicked; the task's remaining writes are given up",
		"panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
	q.once.Do(func() { close(q.stopCh) })
	if op := q.inFlight; op != nil {
		q.inFlight = nil
		q.dropped(*op)
	}
	q.dropBuffered()
}

func (q *replyQueue) dropBuffered() {
	if op := q.lookahead; op != nil {
		q.lookahead = nil
		q.dropped(*op)
	}
	for {
		select {
		case op := <-q.ops:
			q.dropped(op)
		default:
			return
		}
	}
}

func (q *replyQueue) handle(ctx context.Context, op replyOp) {
	switch op.kind {
	case opPlaceholder:
		if len(q.messages) == 0 {
			q.writeStatus(ctx, op.text)
		}
	case opProgress:
		if !q.hasAnswer {
			q.writeStatus(ctx, op.text)
		}
	case opAppend, opRerender:
		q.applyText(op)
		if q.dirty && time.Since(q.lastFlush) >= textFlushInterval {
			q.absorbText()
			q.flush(ctx)
		}
	case opReplace:
		q.applyText(op)
		q.absorbText()
		q.flush(ctx)
	case opNotice:
		q.notice(ctx, op.text)
	case opHalt:
		q.halt(ctx, op.text)
	case opFlush:
		q.flush(ctx)
		q.noticeIfIncomplete(ctx)
		q.postPendingNotices(ctx)
		op.done <- q.drainResult()
	case opCard:
		q.flush(ctx)
		if _, err := q.postCards(ctx, op.text, op.cards); err != nil {
			q.logger.Warn("google chat card could not be posted", "error", err)
		}
	case opPrompt:
		q.prompt(ctx, op)
	case opClearControls:
		q.flush(ctx)
		q.clearControls(ctx)
	case opConclude:
		q.flush(ctx)
		q.conclude(ctx, op)
	case opResolveSignIn:
		q.resolveSignIn(ctx, op.text, op.terminal)
	}
}

func (q *replyQueue) applyText(op replyOp) bool {
	switch op.kind {
	case opAppend:
		q.text += op.text
		q.markAnswer()
	case opReplace:
		q.text = op.text
		q.markAnswer()
	case opRerender:
		if q.text != "" {
			q.dirty = true
		}
	default:
		return false
	}
	return true
}

func (q *replyQueue) absorbText() {
	for q.lookahead == nil {
		select {
		case op := <-q.ops:
			if !q.applyText(op) {
				q.lookahead = &op
			}
		default:
			return
		}
	}
}

func (q *replyQueue) drainResult() drainResult {
	if q.dirty || q.noticeLost {
		return drainIncomplete
	}
	return drainWritten
}

func (q *replyQueue) notice(ctx context.Context, text string) {
	q.flush(ctx)
	switch {
	case !q.hasAnswer:
		if !q.writeStatus(ctx, text) {
			q.noticeLost = true
		}
	case q.dirty:
		q.pendingNotices = append(q.pendingNotices, text)
	default:
		q.postStandalone(ctx, text)
	}
}

func (q *replyQueue) halt(ctx context.Context, text string) {
	if q.hasAnswer {
		q.notice(ctx, text)
		return
	}
	q.text = text
	q.markAnswer()
	q.flush(ctx)
}

var errReplyQueueClosed = errors.New("google chat reply queue closed before the prompt was posted")

func (q *replyQueue) prompt(ctx context.Context, op replyOp) {
	if !q.postPrompt(ctx, op) {
		q.dropped(op)
		return
	}
	if op.release != nil {
		op.release()
	}
}

func (q *replyQueue) postPrompt(ctx context.Context, op replyOp) bool {
	q.flush(ctx)
	q.noticeIfIncomplete(ctx)
	q.postPendingNotices(ctx)
	if !q.hasAnswer && q.segment < len(q.messages) {
		q.writeStatus(ctx, op.lead)
	}
	name, err := q.postCards(ctx, op.text, op.cards)
	if err != nil {
		switch {
		case ctx.Err() != nil && q.abandoned.Load():
			return false
		case op.fallback == nil || op.fallback.stop == nil:
			q.logger.Warn("google chat prompt could not be posted", "error", err)
		case ctx.Err() != nil:
			op.fallback.stop(errReplyQueueClosed)
		default:
			q.halt(ctx, op.fallback.halt)
			op.fallback.stop(err)
		}
		return true
	}
	q.startSegment()
	q.prompted = true
	if op.signIn {
		if name == "" {
			q.logger.Warn("google chat sign-in link was posted without a message name, so it cannot be replaced later")
		} else {
			q.signInMessages = append(q.signInMessages, name)
		}
	}
	q.reportAnchors()
	return true
}

func (q *replyQueue) resolveSignIn(ctx context.Context, outcome string, terminal bool) {
	links := q.outstandingSignIns(ctx)
	if len(links) == 0 {
		if !terminal && len(q.signInMessages) == 0 {
			q.logger.Debug("google chat sign-in result with no outstanding link")
		}
		if len(q.signInMessages) > 0 {
			q.signInMessages = nil
			q.reportAnchors()
		}
		return
	}
	var unresolved []string
	for _, name := range links {
		linkOutcome := cmp.Or(q.signInOutcomes[name], outcome)
		err := q.writer.update(ctx, q.space, name, messageContent{Cards: signInOutcomeCard(linkOutcome)}, true)
		if err == nil || errors.Is(err, errMessageGone) {
			q.settleSignIn(name)
			continue
		}
		q.keepSignIn(name, linkOutcome)
		unresolved = append(unresolved, name)
		switch {
		case errors.Is(err, context.Canceled) || ctx.Err() != nil:
			q.logger.Debug("google chat sign-in link not replaced: the queue stopped", "messageName", name)
		case terminal:
			q.logger.Error("google chat sign-in link left in place after the task ended", "messageName", name, "error", err)
		default:
			q.logger.Warn("google chat sign-in link could not be replaced with its outcome", "messageName", name, "error", err)
		}
	}
	q.signInMessages = unresolved
	q.reportAnchors()
}

func (q *replyQueue) settleSignIn(name string) {
	if q.signInSettled == nil {
		q.signInSettled = make(map[string]bool)
	}
	q.signInSettled[name] = true
	delete(q.signInOutcomes, name)
}

func (q *replyQueue) keepSignIn(name, outcome string) {
	if q.signInOutcomes == nil {
		q.signInOutcomes = make(map[string]string)
	}
	q.signInOutcomes[name] = outcome
}

func (q *replyQueue) outstandingSignIns(ctx context.Context) []string {
	links := slices.Clone(q.signInMessages)
	if q.signInLinks != nil {
		if outstanding, ok := q.signInLinks(ctx, links); ok {
			links = outstanding
		}
	}
	return slices.DeleteFunc(links, func(name string) bool { return q.signInSettled[name] })
}

func (q *replyQueue) noticeIfIncomplete(ctx context.Context) {
	if q.dirty && !q.incompleteNoticed {
		q.incompleteNoticed = true
		q.postStandalone(ctx, msgReplyIncomplete)
	}
}

func (q *replyQueue) postPendingNotices(ctx context.Context) {
	for _, text := range q.pendingNotices {
		q.postStandalone(ctx, text)
	}
	q.pendingNotices = nil
}

func (q *replyQueue) markAnswer() {
	q.dirty = true
	if q.text != "" {
		q.hasAnswer = true
	}
}

func (q *replyQueue) startSegment() {
	q.segment = len(q.messages)
	q.text = ""
	q.hasAnswer = false
	q.dirty = false
}

func (q *replyQueue) writeStatus(ctx context.Context, text string) bool {
	return q.writeAt(ctx, q.segment, text)
}

func (q *replyQueue) flush(ctx context.Context) {
	defer func() { q.lastFlush = time.Now() }()
	if !q.dirty {
		return
	}
	q.dirty = false
	for i, chunk := range splitForChat(q.citations.renderAnswer(q.text), maxMessageRunes) {
		at := q.segment + i
		if at < len(q.committed) && q.committed[at] == chunk {
			continue
		}
		if !q.writeAt(ctx, at, chunk) {
			q.dirty = true
			return
		}
	}
	q.postPendingNotices(ctx)
}

func (q *replyQueue) postStandalone(ctx context.Context, text string) {
	if _, err := q.writer.create(ctx, q.space, q.thread, messageContent{Text: text}); err != nil {
		q.noticeLost = true
		q.logger.Warn("google chat notice could not be posted", "error", err)
	}
}

func (q *replyQueue) postCards(ctx context.Context, fallbackText string, cards []*chat.CardWithId) (string, error) {
	return q.writer.create(ctx, q.space, q.thread, messageContent{FallbackText: fallbackText, Cards: cards})
}

func (q *replyQueue) clearControls(ctx context.Context) {
	if q.controlsMessage == "" {
		return
	}
	err := q.writer.update(ctx, q.space, q.controlsMessage, messageContent{}, true)
	if err != nil && !errors.Is(err, errMessageGone) {
		q.logger.Warn("google chat status message controls could not be cleared", "error", err)
	}
}

func (q *replyQueue) conclude(ctx context.Context, op replyOp) {
	last := ""
	if q.segment < len(q.messages) && !q.dirty {
		last = q.messages[len(q.messages)-1]
	}
	if last != q.controlsMessage {
		q.clearControls(ctx)
	}
	if last != "" {
		err := q.writer.update(ctx, q.space, last, messageContent{Cards: op.cards}, true)
		if err == nil {
			if last == q.controlsMessage {
				q.controlsMessage = ""
			}
			return
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			q.logger.Debug("google chat cards were not added to the answer: the reply queue stopped")
			return
		}
		if errors.Is(err, errMessageGone) {
			q.logger.Info("google chat answer message was deleted; posting the cards instead")
		} else {
			q.logger.Warn("google chat cards could not be added to the answer; posting them instead", "error", err)
		}
		if last == q.controlsMessage {
			q.clearControls(ctx)
		}
	}
	if _, err := q.postCards(ctx, op.text, op.cards); err != nil {
		q.logger.Warn("google chat card could not be posted", "error", err)
	}
}

func (q *replyQueue) adoptAnchors(ctx context.Context) {
	adopt := q.adopt
	q.adopt = nil
	if adopt == nil || len(q.messages) > 0 || q.prompted {
		return
	}
	found, ok := adopt(ctx)
	if !ok || found.Status == "" {
		return
	}
	q.messages = []string{found.Status}
	q.committed = []string{""}
	q.controlsMessage = cmp.Or(q.controlsMessage, found.Controls)
}

func (q *replyQueue) writeAt(ctx context.Context, i int, text string) bool {
	if text == "" {
		return true
	}
	if i == q.segment {
		q.adoptAnchors(ctx)
	}
	if i < len(q.messages) {
		err := q.writer.update(ctx, q.space, q.messages[i], messageContent{Text: text}, false)
		if err == nil {
			q.committed[i] = text
			return true
		}
		if !errors.Is(err, errMessageGone) {
			q.logger.Warn("google chat message update failed", "messageIndex", i, "error", err)
			return false
		}
		q.logger.Info("google chat message was deleted; posting a new one", "messageIndex", i)
	}
	content := messageContent{Text: text}
	carriesControls := len(q.messages) == 0 && q.controlsMessage == "" && len(q.controls) > 0
	if carriesControls {
		content.Cards = q.controls
	}
	name, err := q.writer.create(ctx, q.space, q.thread, content)
	if err != nil {
		q.logger.Warn("google chat message create failed", "messageIndex", i, "error", err)
		return false
	}
	if i < len(q.messages) {
		q.messages[i], q.committed[i] = name, text
	} else {
		q.messages = append(q.messages, name)
		q.committed = append(q.committed, text)
	}
	if carriesControls {
		q.controlsMessage = name
	}
	if i == q.segment || carriesControls {
		q.reportAnchors()
	}
	return true
}
