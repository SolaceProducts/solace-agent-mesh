// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"context"
	"sync"
	"time"
)

const (
	typingRefreshInterval = 20 * time.Second
	typingIdleLifetime    = time.Hour
	typingPostTimeout     = 10 * time.Second
)

type typingTaskState int

const (
	typingWorking typingTaskState = iota
	typingAwaitingUser
)

type typingIndicators struct {
	mu    sync.Mutex
	convs map[string]*typingConversation
}

type typingConversation struct {
	sender  string
	wamid   string
	tasks   map[string]*typingTask
	timer   *time.Timer
	posting chan struct{}
}

type typingTask struct {
	state        typingTaskState
	lastActivity time.Time
}

func (c *typingConversation) working() bool {
	for _, task := range c.tasks {
		if task.state == typingWorking {
			return true
		}
	}
	return false
}

func (a *Adapter) typingTrack(sender, taskID, wamid string) {
	if sender == "" || taskID == "" || wamid == "" {
		return
	}
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.convs == nil {
		t.convs = make(map[string]*typingConversation)
	}
	conv := t.convs[sender]
	if conv == nil {
		conv = &typingConversation{sender: sender, tasks: make(map[string]*typingTask)}
		t.convs[sender] = conv
	}
	conv.wamid = wamid
	conv.tasks[taskID] = &typingTask{state: typingWorking, lastActivity: time.Now()}
	if conv.timer == nil {
		a.scheduleNextTyping(conv)
	}
}

func (a *Adapter) typingShow(sender string) {
	t := &a.typing
	t.mu.Lock()
	conv := t.convs[sender]
	t.mu.Unlock()
	if conv != nil {
		a.refreshTyping(conv)
	}
}

func (a *Adapter) typingAwaitingUser(sender, taskID string) {
	a.setTypingTask(sender, taskID, typingAwaitingUser)
}

func (a *Adapter) typingResumed(sender, taskID string) {
	if a.setTypingTask(sender, taskID, typingWorking) {
		a.typingAfterSend(sender)
	}
}

func (a *Adapter) typingActivity(sender, taskID string) {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	if conv := t.convs[sender]; conv != nil {
		if task := conv.tasks[taskID]; task != nil {
			task.lastActivity = time.Now()
		}
	}
}

func (a *Adapter) setTypingTask(sender, taskID string, state typingTaskState) bool {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	conv := t.convs[sender]
	if conv == nil {
		return false
	}
	task := conv.tasks[taskID]
	if task == nil {
		return false
	}
	task.state = state
	task.lastActivity = time.Now()
	return true
}

func (a *Adapter) typingFinished(sender, taskID string) {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	if conv := t.convs[sender]; conv != nil {
		delete(conv.tasks, taskID)
		a.retireTypingIfIdle(conv)
	}
}

func (a *Adapter) typingFinishedTask(taskID string) {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, conv := range t.convs {
		if _, ok := conv.tasks[taskID]; ok {
			delete(conv.tasks, taskID)
			a.retireTypingIfIdle(conv)
		}
	}
}

func (a *Adapter) typingAfterSend(sender string) {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	if conv := t.convs[sender]; conv != nil && conv.working() {
		a.scheduleTyping(conv, 0)
	}
}

func (a *Adapter) awaitTypingPost(ctx context.Context, sender string) {
	t := &a.typing
	t.mu.Lock()
	var posting chan struct{}
	if conv := t.convs[sender]; conv != nil {
		posting = conv.posting
	}
	t.mu.Unlock()
	if posting == nil {
		return
	}
	select {
	case <-posting:
	case <-ctx.Done():
	}
}

func (a *Adapter) stopTyping() {
	t := &a.typing
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, conv := range t.convs {
		if conv.timer != nil {
			conv.timer.Stop()
		}
	}
	t.convs = nil
}

func (a *Adapter) refreshTyping(conv *typingConversation) {
	t := &a.typing
	t.mu.Lock()
	for conv.posting != nil {
		inFlight := conv.posting
		t.mu.Unlock()
		<-inFlight
		t.mu.Lock()
	}
	if t.convs[conv.sender] != conv {
		t.mu.Unlock()
		return
	}
	if a.ctx.Err() != nil {
		a.dropTyping(conv)
		t.mu.Unlock()
		return
	}
	pruned := a.pruneIdleTypingTasks(conv)
	if a.retireTypingIfIdle(conv) || !conv.working() {
		a.scheduleNextTyping(conv)
		t.mu.Unlock()
		a.logPrunedTyping(pruned)
		return
	}
	posting := make(chan struct{})
	conv.posting = posting
	wamid := conv.wamid
	t.mu.Unlock()
	a.logPrunedTyping(pruned)

	a.postTyping(wamid)

	t.mu.Lock()
	defer t.mu.Unlock()
	conv.posting = nil
	close(posting)
	if t.convs[conv.sender] == conv && !a.retireTypingIfIdle(conv) {
		a.scheduleNextTyping(conv)
	}
}

func (a *Adapter) postTyping(wamid string) {
	ctx, cancel := context.WithTimeout(a.ctx, typingPostTimeout)
	defer cancel()
	if err := a.client.markReadTyping(ctx, wamid); err != nil {
		a.logger.Debug("whatsapp typing indicator failed", safeGraphErrFields(err)...)
	}
}

func (a *Adapter) logPrunedTyping(pruned int) {
	if pruned > 0 {
		a.logger.Debug("whatsapp typing indicator released idle tasks", "tasks", pruned)
	}
}

func (a *Adapter) pruneIdleTypingTasks(conv *typingConversation) int {
	lifetime := cmp.Or(a.typingLifetime, typingIdleLifetime)
	pruned := 0
	for id, task := range conv.tasks {
		if time.Since(task.lastActivity) > lifetime {
			delete(conv.tasks, id)
			pruned++
		}
	}
	return pruned
}

func (a *Adapter) scheduleNextTyping(conv *typingConversation) {
	if len(conv.tasks) == 0 {
		return
	}
	if conv.working() {
		a.scheduleTyping(conv, cmp.Or(a.typingRefresh, typingRefreshInterval))
		return
	}
	lifetime := cmp.Or(a.typingLifetime, typingIdleLifetime)
	var next time.Duration
	first := true
	for _, task := range conv.tasks {
		if until := lifetime - time.Since(task.lastActivity); first || until < next {
			next, first = until, false
		}
	}
	a.scheduleTyping(conv, max(next, 0))
}

func (a *Adapter) scheduleTyping(conv *typingConversation, d time.Duration) {
	if conv.timer == nil {
		conv.timer = time.AfterFunc(d, func() { a.refreshTyping(conv) })
		return
	}
	conv.timer.Reset(d)
}

func (a *Adapter) retireTypingIfIdle(conv *typingConversation) bool {
	if len(conv.tasks) > 0 || conv.posting != nil {
		return false
	}
	a.dropTyping(conv)
	return true
}

func (a *Adapter) dropTyping(conv *typingConversation) {
	if conv.timer != nil {
		conv.timer.Stop()
	}
	if a.typing.convs[conv.sender] == conv {
		delete(a.typing.convs, conv.sender)
	}
}
