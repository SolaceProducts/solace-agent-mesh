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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker/migration"
)

const (
	ingressMoveBudget                    = 2 * time.Minute
	ingressMoveRecheck                   = 3 * time.Minute
	ingressAgentWait                     = 30 * time.Second
	ingressReprobeEveryProvisionCooldown = 5 * time.Second
)

type loopTimer struct {
	timer *time.Timer
	C     <-chan time.Time
}

func (l *loopTimer) arm(d time.Duration) {
	l.stop()
	l.timer = time.NewTimer(d)
	l.C = l.timer.C
}

func (l *loopTimer) armed() bool {
	return l.C != nil
}

func (l *loopTimer) stop() {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.C = nil
}

func (a *Adapter) onIngressBindState(state broker.BindState) {
	active := state == broker.BindActive
	a.ingressActive.Store(active)
	a.logger.Info("whatsapp ingress queue bind state changed", "queue", a.ingressQueueName(), "state", state.String())
	if !active {
		a.clearIngressDown()
		a.cancelMove()
	}
	a.kickOwner()
}

func (a *Adapter) kickOwner() {
	select {
	case a.ingressKick <- struct{}{}:
	default:
	}
}

func (a *Adapter) ingressQueueName() string {
	return a2a.WhatsAppIngressQueueNameV2(a.core.Namespace(), a.core.GatewayID())
}

func (a *Adapter) runIngressOwner() {
	var recheck, reprobe loopTimer
	defer recheck.stop()
	defer reprobe.stop()
	for {
		if a.ctx.Err() != nil {
			return
		}
		switch want := a.ingressActive.Load(); {
		case want && !a.ownsIngress():
			if again := a.takeIngress(); again {
				recheck.arm(cmp.Or(a.moveRecheck, ingressMoveRecheck))
			}
		case !want && a.ownsIngress():
			recheck.stop()
			ctx, cancel := context.WithTimeout(a.ctx, adapterStopBudget)
			a.releaseIngress(ctx)
			cancel()
		}
		switch {
		case !a.ingressUnbound():
			reprobe.stop()
		case !reprobe.armed():
			reprobe.arm(cmp.Or(a.reprobeInterval, ingressReprobeEveryProvisionCooldown))
		}
		select {
		case <-a.ctx.Done():
			return
		case <-a.ingressKick:
		case <-recheck.C:
			recheck.stop()
			if a.ingressActive.Load() && a.ownsIngress() && a.moveOldIngressQueue() {
				recheck.arm(cmp.Or(a.moveRecheck, ingressMoveRecheck))
			}
		case <-reprobe.C:
			reprobe.stop()
			a.reprobeIngressBindingOnOwnerLoop()
		}
	}
}

func (a *Adapter) ingressUnbound() bool {
	return a.ingressDown.Load() && !a.ingressActive.Load() && !a.ownsIngress()
}

func (a *Adapter) reprobeIngressBindingOnOwnerLoop() {
	bound := a.probeIngressBinding()
	if !bound || a.ctx.Err() != nil {
		return
	}
	a.logger.Info("whatsapp ingress queue bound", "queue", a.ingressQueueName())
	a.clearIngressDown()
}

func (a *Adapter) ownsIngress() bool {
	a.ownerMu.Lock()
	defer a.ownerMu.Unlock()
	return a.ingressOwned
}

func (a *Adapter) takeIngress() (again bool) {
	a.ownerMu.Lock()
	defer a.ownerMu.Unlock()
	if a.ingressOwned || a.ingressQueue == nil {
		return false
	}
	a.ingressOwned = true
	a.startSender()
	ctx := a.startIngressWorkers()
	a.awaitDefaultAgent(ctx)
	again = a.moveOldIngressQueue()
	a.startIngressDispatch(ctx)
	a.logger.Info("whatsapp ingress active on this instance", "queue", a.ingressQueueName())
	return again
}

func (a *Adapter) awaitDefaultAgent(ctx context.Context) {
	name := a.cfg.DefaultAgentName
	if name == "" {
		return
	}
	deadline := time.Now().Add(cmp.Or(a.agentWait, ingressAgentWait))
	for {
		if !a.ingressActive.Load() || !a.core.AgentRegistry().ResolveByDefaultName(name).Misconfigured {
			return
		}
		if time.Now().After(deadline) {
			a.logger.Warn("whatsapp default agent not discovered before the ingress queue is consumed", "agent", name)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (a *Adapter) releaseIngress(ctx context.Context) {
	a.ownerMu.Lock()
	defer a.ownerMu.Unlock()
	if !a.ingressOwned {
		return
	}
	a.ingressOwned = false
	a.stopIngressConsumer()
	a.stopTyping()
	a.senders.reset()
	if !a.stopSender(ctx) {
		a.logger.Warn("whatsapp sender still finishing after the ingress bind was lost")
	}
	a.logger.Info("whatsapp ingress released on this instance", "queue", a.ingressQueueName())
}

func (a *Adapter) moveOldIngressQueue() (again bool) {
	brk := a.brokerOrNil()
	if brk == nil || a.ingressCtx == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(a.ingressCtx, cmp.Or(a.moveBudget, ingressMoveBudget))
	a.setMoveCancel(cancel)
	defer func() {
		a.setMoveCancel(nil)
		cancel()
	}()
	if !a.ingressActive.Load() {
		return true
	}
	namespace, gatewayID := a.core.Namespace(), a.core.GatewayID()
	found, err := migration.MoveQueue(ctx, brk, migration.MovedQueue{
		Queue:  a2a.WhatsAppIngressQueueName(namespace, gatewayID),
		Topics: []string{ingressTopic(namespace, gatewayID)},
		Idle:   a.moveIdle,
	}, a.handOverAndAwait, a.logger)
	if err != nil {
		if a.ingressCtx.Err() == nil && a.ingressActive.Load() {
			a.logger.Warn("whatsapp ingress queue move cut short; the rest waits for the re-check", "error", err)
		}
		return true
	}
	return found
}

func (a *Adapter) setMoveCancel(cancel context.CancelFunc) {
	a.moveMu.Lock()
	defer a.moveMu.Unlock()
	a.moveCancel = cancel
}

func (a *Adapter) cancelMove() {
	a.moveMu.Lock()
	defer a.moveMu.Unlock()
	if a.moveCancel != nil {
		a.moveCancel()
	}
}

func (a *Adapter) handOverAndAwait(ctx context.Context, msg *broker.Message) {
	settled := make(chan struct{})
	var once sync.Once
	done := func() { once.Do(func() { close(settled) }) }
	tracked := broker.NewDeliveredMessage(msg.ID, msg.Topic, msg.Payload,
		func() { msg.Ack(); done() },
		func() { msg.Nack(); done() })
	tracked.UserProperties = msg.UserProperties
	tracked.ReplyTo = msg.ReplyTo
	tracked.CorrelationID = msg.CorrelationID
	tracked.Redelivered = msg.Redelivered
	tracked.ReplicationGroupMessageID = msg.ReplicationGroupMessageID
	if !a.routeToWorker(ctx, tracked) {
		msg.Nack()
		return
	}
	select {
	case <-settled:
	case <-ctx.Done():
	}
}
