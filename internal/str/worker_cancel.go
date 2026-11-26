// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func (w *Worker) activeInvocationCount() int {
	return int(w.inFlight.Load())
}

const runningCallsPollInterval = 100 * time.Millisecond

func (w *Worker) waitForRunningCalls(ctx context.Context) int {
	settled := func() bool {
		dispatchDone := !w.dispatchStarted.Load() || w.dispatchExited.Load()
		return dispatchDone && w.activeInvocationCount() == 0
	}
	if settled() {
		return 0
	}
	w.logger.Info("waiting for running tool calls before shutdown", "remaining", w.activeInvocationCount())
	ticker := time.NewTicker(runningCallsPollInterval)
	defer ticker.Stop()
	for {
		if settled() {
			return 0
		}
		select {
		case <-ctx.Done():
			return w.activeInvocationCount()
		case <-ticker.C:
		}
	}
}

func (w *Worker) registerInvocationCancel(reqID string, cancel context.CancelFunc) {
	if reqID == "" || cancel == nil {
		return
	}
	w.invocationCancels.Store(reqID, cancel)
}

func (w *Worker) unregisterInvocationCancel(reqID string) {
	if reqID == "" {
		return
	}
	w.invocationCancels.Delete(reqID)
}

func (w *Worker) cancelInvocation(corrID string) bool {
	if corrID == "" {
		return false
	}
	v, ok := w.invocationCancels.LoadAndDelete(corrID)
	if !ok {
		return false
	}
	v.(context.CancelFunc)()
	return true
}

func (w *Worker) cancelDispatchLoop(ctx context.Context) {
	for {
		msg, err := w.svc.Broker().Receive(ctx, w.cancelQueue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("cancel receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		corrID := remotetool.ExtractCancelCorrID(msg.Topic)
		if corrID == "" {
			w.logger.Warn("cancel message with unparseable topic", "topic", msg.Topic)
			msg.Ack()
			continue
		}

		if w.cancelInvocation(corrID) {
			w.logger.Info("invocation cancelled by agent", "corrID", corrID, "topic", msg.Topic)
		}
		msg.Ack()
	}
}
