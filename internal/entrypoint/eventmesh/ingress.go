// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const (
	submitPassInterval  = time.Second
	submitPassBatch     = 64
	submitPassWorkers   = 4
	ingressWriteTimeout = 5 * time.Second
)

type storedEvent struct {
	Handler        string         `json:"handler"`
	ExternalUser   string         `json:"externalUser,omitempty"`
	Target         string         `json:"target"`
	TargetType     string         `json:"targetType"`
	Message        a2a.Message    `json:"message"`
	ForwardContext map[string]any `json:"forwardContext,omitempty"`
	UserID         string         `json:"userId,omitempty"`
	Structured     bool           `json:"structured,omitempty"`
	Deferred       bool           `json:"deferred,omitempty"`
}

func inboundKey(handler string, msg *broker.Message) string {
	if msg.ReplicationGroupMessageID != "" {
		return handler + "/" + msg.ReplicationGroupMessageID
	}
	return idgen.New()
}

func (a *EventMeshAdapter) storeEvent(ctx context.Context, logger *slog.Logger, msg *broker.Message, ev storedEvent, taskID string, policy AckPolicy) string {
	payload, err := json.Marshal(ev)
	if err != nil {
		logger.Error("event not stored: encode failed", "error", err)
		settleFailure(msg, &policy.OnFailure)
		return taxonomy.RejectInternal
	}
	key := inboundKey(ev.Handler, msg)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ingressWriteTimeout)
	inserted, err := a.ingress.store(writeCtx, key, taskID, string(payload))
	cancel()
	if err != nil {
		logger.Warn("event not stored; nacked for redelivery", "error", err)
		msg.Nack()
		return ""
	}
	if ev.Deferred {
		a.held.hold(key, msg, time.Duration(policy.TimeoutSeconds)*time.Second, policy.OnFailure)
	} else {
		msg.Ack()
	}
	if !inserted {
		logger.Debug("copy of a stored event", "messageID", key, "deferred", ev.Deferred)
		return ""
	}
	logger.Debug("event stored", "messageID", key, "taskID", taskID, "deferred", ev.Deferred)
	if !a.submitsLocally() {
		return ""
	}
	reason := a.submitStored(ctx, store.StoredInbound{MessageID: key, TaskID: taskID, Payload: string(payload)})
	if ev.Deferred {
		a.held.poll(ctx)
	}
	return reason
}

func (a *EventMeshAdapter) submitLoop(ctx context.Context) {
	ticker := time.NewTicker(submitPassInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.submitPasses(ctx)
		}
	}
}

func (a *EventMeshAdapter) submitPasses(ctx context.Context) {
	prev := ""
	for ctx.Err() == nil {
		n, oldest := a.submitPass(ctx)
		if n < submitPassBatch || oldest == prev {
			return
		}
		prev = oldest
	}
}

func (a *EventMeshAdapter) submitPass(ctx context.Context) (int, string) {
	rows, err := a.ingress.stored(ctx, submitPassBatch)
	if err != nil {
		a.logger.Warn("stored events not read", "error", err)
		return 0, ""
	}
	if len(rows) == 0 {
		return 0, ""
	}
	var wg sync.WaitGroup
	for w := range min(submitPassWorkers, len(rows)) {
		wg.Go(func() {
			for i := w; i < len(rows); i += submitPassWorkers {
				if ctx.Err() != nil {
					return
				}
				a.core.ReportSubmitRefusal(a.submitStored(ctx, rows[i]))
			}
		})
	}
	wg.Wait()
	return len(rows), rows[0].MessageID
}

func (a *EventMeshAdapter) submitStored(ctx context.Context, row store.StoredInbound) string {
	if _, busy := a.submitting.LoadOrStore(row.MessageID, struct{}{}); busy {
		return ""
	}
	defer a.submitting.Delete(row.MessageID)
	logger := a.logger.With("messageID", row.MessageID, "taskID", row.TaskID)
	var ev storedEvent
	if err := json.Unmarshal([]byte(row.Payload), &ev); err != nil {
		logger.Error("stored event cannot be decoded; rejecting it", "error", err)
		a.settleStored(ctx, logger, row, store.InboundOutcomeReject, true)
		return taxonomy.RejectInternal
	}
	logger = logger.With("handler", ev.Handler, "target", ev.Target)
	handler := a.findHandler(ev.Handler)
	if handler == nil {
		logger.Warn("stored event's handler is no longer configured; rejecting it")
		a.settleStored(ctx, logger, row, store.InboundOutcomeReject, ev.Deferred)
		return taxonomy.RejectInternal
	}
	var ext *gwauth.ExternalID
	if ev.ExternalUser != "" {
		ext = &gwauth.ExternalID{Type: gwauth.IDSub, Value: ev.ExternalUser}
	}
	caller, err := gwauth.ForEventMeshCaller(ext, handler.RunAs)
	if err != nil {
		logger.Warn(msgAuthenticationFailed, "reason", "caller_resolution_failed", "error", err)
		a.settleStored(ctx, logger, row, store.InboundOutcomeReject, ev.Deferred)
		return taxonomy.RejectForbidden
	}

	routing := taskRouting{
		HandlerName:           ev.Handler,
		ForwardContext:        ev.ForwardContext,
		DeferredACK:           ev.Deferred,
		UserID:                ev.UserID,
		SentStructuredRequest: ev.Structured,
		InboundID:             row.MessageID,
	}
	if regErr := a.core.Tasks().Register(row.TaskID, ev.Target, ev.UserID, entrypoint.TaskIdentity{}); regErr != nil {
		logger.Warn("stored event not submitted: task not recorded; retrying on the next pass", "error", regErr)
		return ""
	}
	if putErr := a.ingress.putRouting(ctx, row.TaskID, routing); putErr != nil {
		logger.Warn("stored event not submitted: routing not recorded; retrying on the next pass", "error", putErr)
		a.core.Tasks().Remove(row.TaskID)
		return ""
	}

	message := ev.Message
	if ev.Structured {
		message.ContextID = idgen.New()
	}
	err = a.core.SubmitTask(ctx, ev.Target, row.TaskID, message, caller, entrypoint.FromEvent())
	if err == nil {
		if markErr := a.ingress.markSubmitted(ctx, row.MessageID, row.TaskID, ""); markErr != nil {
			logger.Warn("submitted event not marked; it is submitted again on the next pass", "error", markErr)
		} else if !ev.Deferred {
			a.ingress.release(row.MessageID)
		}
		logger.Debug("task submitted", "targetType", ev.TargetType, "userID", ev.UserID, "deferred", ev.Deferred)
		return ""
	}

	a.core.Tasks().Remove(row.TaskID)
	a.ingress.dropRouting(row.TaskID)
	switch {
	case entrypoint.IsBudgetRefusal(err):
		logger.Debug("event dropped: a budget is spent", "budgetTier", entrypoint.BudgetRefusalTier(err))
		a.publishBudgetRefusal(ctx, logger, handler, ev.ForwardContext, err)
		a.settleStored(ctx, logger, row, store.InboundOutcomeAck, ev.Deferred)
		return entrypoint.SubmitFailureReason(err)
	case entrypoint.IsAuthorizationDenial(err) || errors.Is(err, entrypoint.ErrNoCaller) || entrypoint.IsAgentNotAvailable(err):
		entrypoint.LogSubmitFailure(logger, slog.LevelWarn, err)
		a.settleStored(ctx, logger, row, store.InboundOutcomeReject, ev.Deferred)
		if ev.Deferred && a.getAckPolicy(handler).OnFailure.Action != "ack" {
			return ""
		}
		return entrypoint.SubmitFailureReason(err)
	default:
		entrypoint.LogSubmitFailure(logger, slog.LevelWarn, err)
		logger.Warn("stored event not submitted; retrying on the next pass")
		return ""
	}
}

func (a *EventMeshAdapter) settleStored(ctx context.Context, logger *slog.Logger, row store.StoredInbound, outcome string, deferred bool) {
	if err := a.ingress.markSubmitted(ctx, row.MessageID, row.TaskID, outcome); err != nil {
		logger.Warn("refused event not settled; retrying on the next pass", "outcome", outcome, "error", err)
		return
	}
	if !deferred {
		a.ingress.release(row.MessageID)
	}
}

func (a *EventMeshAdapter) RunIngress(ctx context.Context, term uint64) {
	if !a.ingress.shared() {
		return
	}
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if a.runCtx == nil || a.runCtx.Err() != nil || ctx.Err() != nil {
		return
	}
	if a.submitRun != nil && a.submitRun.term == term && !closed(a.submitRun.done) {
		return
	}
	prev := a.submitRun
	run := &submitRun{term: term, done: make(chan struct{})}
	a.submitRun = run
	runCtx, cancel := context.WithCancel(ctx)
	stopAfter := context.AfterFunc(a.runCtx, cancel)
	a.wg.Go(func() {
		defer close(run.done)
		defer cancel()
		defer stopAfter()
		if prev != nil {
			<-prev.done
		}
		a.logger.Info("leading: submitting stored events", "term", term)
		a.submitLoop(runCtx)
	})
}

type submitRun struct {
	term uint64
	done chan struct{}
}

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (a *EventMeshAdapter) submitsLocally() bool {
	return !a.ingress.shared() || a.core == nil || !a.core.IngressOnLeader()
}
