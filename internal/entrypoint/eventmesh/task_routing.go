// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"time"
)

func (a *EventMeshAdapter) findHandler(name string) *EventHandlerConfig {
	for _, h := range a.cfg.EventHandlers {
		if h.Name == name {
			return h
		}
	}
	return nil
}

const (
	routingReadTimeout = 2 * time.Second
	answeredHold       = time.Minute
)

func (a *EventMeshAdapter) takePending(ctx context.Context, taskID string) (*pendingTask, bool, error) {
	if _, busy := a.answering.LoadOrStore(taskID, struct{}{}); busy {
		return nil, false, nil
	}
	release := func() { a.answering.Delete(taskID) }
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), routingReadTimeout)
	r, found, err := a.ingress.routing(readCtx, taskID)
	cancel()
	if err != nil || !found {
		release()
		return nil, false, err
	}
	a.ingress.dropRouting(taskID)
	handler := a.findHandler(r.HandlerName)
	if handler == nil {
		release()
		a.logger.Warn("answer dropped: the task's handler is no longer configured", "taskID", taskID, "handler", r.HandlerName)
		return nil, false, nil
	}
	time.AfterFunc(answeredHold, release)
	return &pendingTask{
		handlerCfg:            handler,
		forwardContext:        r.ForwardContext,
		deferredACK:           r.DeferredACK,
		userID:                r.UserID,
		sentStructuredRequest: r.SentStructuredRequest,
		inboundID:             r.InboundID,
	}, true, nil
}
