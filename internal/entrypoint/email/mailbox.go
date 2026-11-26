// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type retryPolicy struct {
	initial  time.Duration
	max      time.Duration
	attempts int
}

var defaultRetry = retryPolicy{initial: time.Second, max: time.Minute, attempts: 10}

const giveUpReplyBodyText = "Your message could not be processed. Please try again later, or contact the entrypoint operator if the problem persists."

type mailboxRun struct {
	term uint64
	done chan struct{}
}

var _ entrypoint.LeaderIngressAdapter = (*EmailAdapter)(nil)

func (a *EmailAdapter) RunIngress(ctx context.Context, term uint64) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if a.ctx == nil || a.ctx.Err() != nil || ctx.Err() != nil {
		return
	}
	prev := a.run
	if prev != nil && prev.term == term && !closed(prev.done) {
		return
	}
	run := &mailboxRun{term: term, done: make(chan struct{})}
	a.run = run
	runCtx, cancel := context.WithCancel(ctx)
	stopAfter := context.AfterFunc(a.ctx, cancel)
	a.wg.Go(func() {
		defer close(run.done)
		defer cancel()
		defer stopAfter()
		if prev != nil {
			<-prev.done
		}
		a.logger.LogAttrs(runCtx, slog.LevelInfo, "leading: fetching the mailbox",
			slog.String("eventType", "email.mailbox_lead_started"),
			slog.Uint64("term", term),
		)
		if err := a.openMailbox(runCtx); err != nil {
			a.logger.LogAttrs(runCtx, slog.LevelWarn, "mailbox could not be opened; trying again on the next leader tick",
				slog.String("eventType", "email.mailbox_open_failed"),
				slog.Uint64("term", term),
				slog.String("error", sanitizeForLog(err.Error())),
			)
			return
		}
		a.runDriverSupervisor(runCtx)
		a.closeMailbox()
		reason := "the leader term ended"
		if a.ctx.Err() != nil {
			reason = "the entrypoint stopped"
		}
		a.logger.LogAttrs(context.Background(), slog.LevelInfo, "mailbox closed",
			slog.String("eventType", "email.mailbox_closed"),
			slog.Uint64("term", term),
			slog.String("reason", reason),
		)
	})
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (a *EmailAdapter) openMailbox(ctx context.Context) error {
	if err := a.probeForDriverSelection(ctx); err != nil {
		return err
	}
	a.inFlight.reset()
	a.queue = a.newQueue()
	driver, err := a.selectDriver()
	if err != nil {
		return err
	}
	a.setDriver(driver)
	a.queue.Start(ctx)
	a.logger.LogAttrs(ctx, slog.LevelInfo, "email mailbox opened",
		slog.String("eventType", "email.mailbox_opened"),
		slog.Int("workerCount", a.cfg.WorkerCount),
		slog.Int("queueCapacity", a.cfg.QueueCapacity),
		slog.Int("poll_interval_seconds", a.cfg.PollIntervalSeconds),
		slog.String("policyMode", string(a.cfg.PolicyMode)),
		slog.String("deliveryMode", string(a.cfg.DeliveryMode)),
		slog.String("driver", fmt.Sprintf("%T", a.currentDriver())),
		slog.Bool("internalTrustEnabled", a.cfg.TrustedInternalTenantID != ""),
		slog.String("internalTrustTenantID", a.cfg.TrustedInternalTenantID),
		slog.Int("allowlistSize", len(a.cfg.AllowedSenders)),
		slog.Int("mimeAllowlistSize", len(a.cfg.MIMEAllowlist)),
		slog.Int("mimeDenylistSize", len(a.cfg.MIMEDenylist)),
	)
	return nil
}

func (a *EmailAdapter) closeMailbox() {
	ctx := context.Background()
	_ = a.queue.Stop(ctx)
	_ = a.provider.Disconnect(ctx)
}

func (a *EmailAdapter) newQueue() *MessageQueue {
	return NewMessageQueue(QueueConfig{
		Capacity: a.cfg.QueueCapacity,
		Workers:  a.cfg.WorkerCount,
		Key:      func(m RawMessage) string { return threadKey(m.RFC822) },
		Handler:  a.handleQueued,
		Logger:   a.logger,
	})
}

func (a *EmailAdapter) handleQueued(ctx context.Context, m RawMessage) {
	policy := a.retry
	if policy.attempts == 0 {
		policy = defaultRetry
	}
	result := a.worker.Handle(ctx, m)
	wait := policy.initial
	for attempt := 1; result == HandleRetry && attempt < policy.attempts; attempt++ {
		if backoff.Sleep(ctx, wait) != nil {
			a.inFlight.Done(m.UID)
			return
		}
		wait = min(2*wait, policy.max)
		result = a.worker.Handle(ctx, m)
	}
	if result == HandleRetry {
		if ctx.Err() != nil {
			a.inFlight.Done(m.UID)
			return
		}
		a.giveUp(ctx, m, policy.attempts)
	}
	a.currentDriver().Ack(ctx, m.UID)
}

func (a *EmailAdapter) giveUp(ctx context.Context, m RawMessage, attempts int) {
	a.logger.LogAttrs(ctx, slog.LevelError, "mail not processed after every attempt; telling the sender",
		slog.String("eventType", "email.retries_exhausted"),
		slog.String("msgUID", string(m.UID)),
		slog.Int("attempts", attempts),
	)
	if a.replier == nil {
		return
	}
	n, err := NormalizeWithOpts(m.RFC822, NormalizeOpts{TrustedAuthservIDs: a.cfg.TrustedAuthservIDs})
	if err != nil {
		return
	}
	trust := EvaluateInternalTrust(n.RawHeaders, a.cfg.TrustedInternalTenantID, a.cfg.TrustedAuthservIDs)
	replyTo := resolveReplyTo(ctx, n, trust, a.logger)
	if err := a.replier.Send(ctx, ReplyRequest{
		To:                 replyTo.Address,
		Subject:            n.Subject,
		BodyText:           giveUpReplyBodyText,
		OriginalMessageID:  n.MessageID,
		OriginalReferences: n.References,
	}); err != nil {
		a.logReplySendError(ctx, "give-up notice send failed", "email.give_up_notice_failed", err,
			slog.String("to", maskEmail(replyTo.Address)),
		)
		return
	}
	a.markGivenUp(ctx, n.MessageID, m.UID)
}

func (a *EmailAdapter) markGivenUp(ctx context.Context, messageID string, uid ProviderUID) {
	if !a.items.enabled() {
		return
	}
	taskID := emailTaskID(a.core.GatewayID(), messageID, uid)
	if !a.core.Tasks().Holds(taskID) {
		return
	}
	if recorded, err := a.mailRecorded(ctx, messageID); err != nil || recorded {
		return
	}
	a.items.claim(ctx, taskID, itemReplySent)
}
