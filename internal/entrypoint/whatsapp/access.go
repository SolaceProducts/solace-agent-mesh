// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"sync"
	"time"
)

const (
	senderMessagesPerMinute   = 12
	senderBurst               = 6
	maxInFlightTasksPerSender = 3
	inFlightTaskTTL           = 15 * time.Minute
)

const backpressureNoticeWindow = time.Minute

type senderGate struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	inFlight map[string]map[string]time.Time

	lastSweep time.Time

	now func() time.Time
}

func (g *senderGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *senderGate) admitMessage(sender string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	if g.buckets == nil {
		g.buckets = make(map[string]*tokenBucket)
	}
	bucket, ok := g.buckets[sender]
	if !ok {
		g.prune(now)
		if len(g.buckets) >= senderGateKeyCap {
			return false
		}
		bucket = &tokenBucket{tokens: senderBurst, last: now}
		g.buckets[sender] = bucket
	}
	return bucket.take(now)
}

func (g *senderGate) admitTask(sender string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.liveTasks(sender, g.clock())) < maxInFlightTasksPerSender
}

func (g *senderGate) taskStarted(sender, taskID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight == nil {
		g.inFlight = make(map[string]map[string]time.Time)
	}
	if g.inFlight[sender] == nil {
		g.inFlight[sender] = make(map[string]time.Time)
	}
	g.inFlight[sender][taskID] = g.clock()
}

func (g *senderGate) taskFinished(sender, taskID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.inFlight[sender], taskID)
	if len(g.inFlight[sender]) == 0 {
		delete(g.inFlight, sender)
	}
}

func (g *senderGate) finishTask(taskID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for sender, tasks := range g.inFlight {
		if _, ok := tasks[taskID]; !ok {
			continue
		}
		delete(tasks, taskID)
		if len(tasks) == 0 {
			delete(g.inFlight, sender)
		}
	}
}

func (g *senderGate) liveTaskIDs(sender string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	tasks := g.liveTasks(sender, g.clock())
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	return ids
}

func (g *senderGate) runningTasks(sender string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.liveTasks(sender, g.clock()))
}

func (g *senderGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.buckets, g.inFlight = nil, nil
	g.lastSweep = time.Time{}
}

func (g *senderGate) liveTasks(sender string, now time.Time) map[string]time.Time {
	tasks := g.inFlight[sender]
	for id, started := range tasks {
		if now.Sub(started) > inFlightTaskTTL {
			delete(tasks, id)
		}
	}
	return tasks
}

const senderGateKeyCap = 1024

const senderGateSweepInterval = 5 * time.Second

func (g *senderGate) prune(now time.Time) {
	if len(g.buckets) < senderGateKeyCap {
		return
	}
	if !g.lastSweep.IsZero() && now.Sub(g.lastSweep) < senderGateSweepInterval {
		return
	}
	g.lastSweep = now
	g.expireAbandonedTasksOfAllSenders(now)
	for sender, bucket := range g.buckets {
		if bucket.refilled(now) && len(g.inFlight[sender]) == 0 {
			delete(g.buckets, sender)
		}
	}
}

func (g *senderGate) expireAbandonedTasksOfAllSenders(now time.Time) {
	for sender := range g.inFlight {
		if len(g.liveTasks(sender, now)) == 0 {
			delete(g.inFlight, sender)
		}
	}
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (b *tokenBucket) take(now time.Time) bool {
	b.refill(now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *tokenBucket) refilled(now time.Time) bool {
	b.refill(now)
	return b.tokens >= senderBurst
}

func (b *tokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.tokens = min(senderBurst, b.tokens+elapsed.Minutes()*senderMessagesPerMinute)
	b.last = now
}

func (a *Adapter) admitTask(sender string) bool {
	if a.senders.admitTask(sender) {
		return true
	}
	ids := a.senders.liveTaskIDs(sender)
	if a.outbound == nil || len(ids) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), taskItemTimeout)
	defer cancel()
	ended, err := a.outbound.TasksWithFinalRow(ctx, a.gatewayID(), sender, ids)
	if err != nil {
		a.logger.Warn("whatsapp task limit check could not read the send table", "error", err)
		return false
	}
	for _, id := range ended {
		a.senders.taskFinished(sender, id)
	}
	return a.senders.admitTask(sender)
}

func (a *Adapter) admitMessageRate(msg *inboundMessage) bool {
	if a.senders.admitMessage(msg.senderID()) {
		return true
	}
	a.refuse(msg, &a.burstySenders, backpressureNoticeWindow, refusalRateLimited, msgTooManyMessages,
		"whatsapp inbound message refused: sender exceeded the per-sender message rate")
	return false
}

func (a *Adapter) refuse(msg *inboundMessage, notices *warnThrottle, window time.Duration, reason, notice, logMsg string, extra ...any) {
	if a.logRefusal(msg, notices, window, reason, logMsg, extra...) {
		a.reply("", msg.senderID(), notice)
	}
	a.auditRefused(msg, reason)
}

func (a *Adapter) refuseAndAlwaysReply(msg *inboundMessage, notices *warnThrottle, window time.Duration, reason, notice, logMsg string, extra ...any) {
	a.logRefusal(msg, notices, window, reason, logMsg, extra...)
	a.reply("", msg.senderID(), notice)
	a.auditRefused(msg, reason)
}

func (a *Adapter) logRefusal(msg *inboundMessage, notices *warnThrottle, window time.Duration, reason, logMsg string, extra ...any) bool {
	fields := []any{"wamid", msg.ID, "type", msg.Type, "sender", maskSenderID(msg.senderID())}
	fields = append(fields, extra...)
	switch warn, suppressed, saturated := notices.shouldWarn(msg.senderID(), window); {
	case warn:
		a.logger.Warn(logMsg, append(fields, "suppressedSinceLast", suppressed)...)
		return true
	case saturated:
		a.logger.Debug(logMsg, fields...)
		if w, dropped, _ := a.saturatedNotices.shouldWarn(reason, window); w {
			a.logger.Warn("whatsapp refusal notices are saturated; further senders are being refused silently",
				"refusalReason", reason, "trackedSenders", warnThrottleKeyCap, "droppedSinceLast", dropped)
		}
	default:
		a.logger.Debug(logMsg, fields...)
	}
	return false
}

func createsTask(msgType string) bool {
	switch msgType {
	case "text", "image", "document", "video", "audio":
		return true
	}
	return false
}
