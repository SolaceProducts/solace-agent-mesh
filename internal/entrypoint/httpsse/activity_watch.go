// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	activityWatchAnnounceInterval = 10 * time.Second
	activityWatchTTL              = 3 * activityWatchAnnounceInterval
)

type remoteWatches struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
	ttl   time.Duration
}

func (w *remoteWatches) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *remoteWatches) touch(taskID string) {
	ttl := w.ttl
	if ttl == 0 {
		ttl = activityWatchTTL
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.until == nil {
		w.until = make(map[string]time.Time)
	}
	w.until[taskID] = w.clock().Add(ttl)
}

func (w *remoteWatches) watched(taskID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	lapses, ok := w.until[taskID]
	if !ok {
		return false
	}
	if w.clock().After(lapses) {
		delete(w.until, taskID)
		return false
	}
	return true
}

func (w *remoteWatches) prune() {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.clock()
	for taskID, lapses := range w.until {
		if now.After(lapses) {
			delete(w.until, taskID)
		}
	}
}

func (a *HTTPSSEAdapter) watchingTask(taskID string) bool {
	return (a.sseManager != nil && a.sseManager.HasSubscribers(taskID)) || a.remoteWatches.watched(taskID)
}

func (a *HTTPSSEAdapter) announceActivityWatch(taskID string) {
	if a.streamPub != nil {
		a.streamPub.PublishTaskStreamRecord(taskID, entrypoint.StreamRecord{EventType: entrypoint.ActivityWatchRecord})
	}
}

func (a *HTTPSSEAdapter) announceHeldStreams() {
	if a.sseManager != nil {
		for _, taskID := range a.sseManager.HeldTasksWithSubscribers() {
			a.announceActivityWatch(taskID)
		}
	}
	a.remoteWatches.prune()
}

func (a *HTTPSSEAdapter) startActivityWatch(ctx context.Context) {
	if a.activityWatchCancel != nil {
		return
	}
	every := a.activityWatchEvery
	if every <= 0 {
		every = activityWatchAnnounceInterval
	}
	loopCtx, cancel := context.WithCancel(ctx)
	a.activityWatchCancel = cancel
	a.activityWatchWG.Go(func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				a.announceHeldStreams()
			}
		}
	})
}

func (a *HTTPSSEAdapter) stopActivityWatch() {
	if a.activityWatchCancel == nil {
		return
	}
	a.activityWatchCancel()
	a.activityWatchWG.Wait()
	a.activityWatchCancel = nil
}

type forwardedActivity struct{ a *HTTPSSEAdapter }

func (f forwardedActivity) OnActivityEvent(ctx context.Context, taskID, topic string, payload map[string]any) {
	f.a.OnActivityEvent(ctx, taskID, topic, payload)
}

func (f forwardedActivity) WatchingTask(taskID string) bool { return f.a.watchingTask(taskID) }

func (a *HTTPSSEAdapter) ForwardedActivity() entrypoint.ActivityEventEmitter {
	return forwardedActivity{a}
}

var (
	_ entrypoint.ActivityWatchers      = forwardedActivity{}
	_ entrypoint.ForwardedActivityView = (*HTTPSSEAdapter)(nil)
)
