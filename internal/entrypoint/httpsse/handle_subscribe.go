// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const sseHeartbeatInterval = 15 * time.Second

type subscribeHandler struct {
	sseManager *SSEManager
	eventLog   eventlog.TaskEventLog
	onHeld     func(taskID string)
}

func (h *subscribeHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "missing taskId")
		return
	}
	if !requireUUID(w, taskID, "taskId") {
		return
	}
	fromSeq, ok := h.parseFromSeq(w, r, taskID)
	if !ok {
		return
	}
	h.stream(w, r, taskID, fromSeq)
}

func (h *subscribeHandler) stream(w http.ResponseWriter, r *http.Request, taskID string, fromSeq int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	streamCtx, cancel := entrypoint.StreamContext(r)
	defer cancel()
	r = r.WithContext(streamCtx)

	liveCh, connID, exists := h.sseManager.Subscribe(taskID)
	if !exists {
		h.serveFromEventLog(w, r, taskID, fromSeq, flusher)
		return
	}
	defer func() { h.sseManager.Unsubscribe(taskID, connID) }()
	resubscribe := func() <-chan *SSEEvent {
		ch, id, ok := h.sseManager.SubscribeOpen(taskID)
		if !ok {
			return nil
		}
		connID = id
		return ch
	}

	writeSSEHeaders(w)
	_, _ = fmt.Fprint(w, ": SSE connection established\n\n")
	flusher.Flush()

	lastReplayed, finalized, err := h.replayBacklog(r.Context(), w, taskID, fromSeq, flusher)
	if err != nil {
		if r.Context().Err() == nil {
			h.writeStreamError(w, taskID, "SSE backlog replay failed; emitting terminal error frame", err, flusher)
		}
		return
	}
	if finalized {
		return
	}
	h.streamLive(r.Context(), w, taskID, liveCh, resubscribe, lastReplayed, flusher)
}

func (h *subscribeHandler) parseFromSeq(w http.ResponseWriter, r *http.Request, taskID string) (int64, bool) {
	if fromSeq, resumed := resumeFrom(r); resumed {
		return fromSeq, true
	}
	if s := r.URL.Query().Get("lastSequence"); s != "" {
		lastSeq, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid lastSequence")
			return 0, false
		}
		return seqAfter(lastSeq), true
	}
	if s := r.URL.Query().Get("lastEventTimestamp"); s != "" {
		ts, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid lastEventTimestamp")
			return 0, false
		}
		seq, err := h.eventLog.FindSequenceAfterTimestamp(r.Context(), taskID, ts)
		if err != nil {
			return 1, true
		}
		return seq, true
	}
	return 1, true
}

const lastEventIDHeader = "Last-Event-ID"

func resumeFrom(r *http.Request) (fromSeq int64, resumed bool) {
	s := r.Header.Get(lastEventIDHeader)
	if s == "" {
		return 1, false
	}
	lastSeq, err := strconv.ParseInt(s, 10, 64)
	if err != nil || lastSeq < 0 {
		return 1, false
	}
	return seqAfter(lastSeq), true
}

func seqAfter(lastSeq int64) int64 {
	if lastSeq == math.MaxInt64 {
		return math.MaxInt64
	}
	return lastSeq + 1
}

func (h *subscribeHandler) replayBacklog(ctx context.Context, w http.ResponseWriter, taskID string, fromSeq int64, flusher http.Flusher) (lastSeq int64, finalized bool, err error) {
	events, err := h.eventLog.Replay(ctx, taskID, fromSeq)
	if err != nil {
		return fromSeq - 1, false, err
	}

	lastSeq = fromSeq - 1
	var pendingArtifacts []eventlog.Event
	for _, evt := range events {
		if ctx.Err() != nil {
			return lastSeq, finalized, ctx.Err()
		}
		lastSeq = evt.Sequence
		switch evt.EventType {
		case "artifact_update":
			pendingArtifacts = append(pendingArtifacts, evt)
		case "final_response":
			pendingArtifacts = nil
			finalized = true
			writeSSEFrame(w, evt.EventType, evt.Payload, evt.Sequence)
		default:
			for _, ae := range pendingArtifacts {
				writeSSEFrame(w, ae.EventType, ae.Payload, ae.Sequence)
			}
			pendingArtifacts = nil
			writeSSEFrame(w, evt.EventType, evt.Payload, evt.Sequence)
		}
	}
	for _, ae := range pendingArtifacts {
		writeSSEFrame(w, ae.EventType, ae.Payload, ae.Sequence)
	}
	flusher.Flush()
	return lastSeq, finalized, nil
}

func (h *subscribeHandler) streamLive(ctx context.Context, w http.ResponseWriter, taskID string, live <-chan *SSEEvent, resubscribe func() <-chan *SSEEvent, lastReplayed int64, flusher http.Flusher) {
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	follow := func() (ch <-chan eventlog.Event, ok bool) {
		ch, err := h.eventLog.Subscribe(ctx, taskID, lastReplayed+1)
		if err != nil && !errors.Is(err, eventlog.ErrTaskNotFound) {
			if ctx.Err() == nil {
				h.writeStreamError(w, taskID, "SSE event log follow failed; emitting terminal error frame", err, flusher)
			}
			return nil, false
		}
		return ch, true
	}
	logCh, ok := follow()
	if !ok {
		return
	}
	sentFinal := false
	defer func() {
		if sentFinal {
			h.sseManager.Finish(taskID)
		}
	}()
	var heldFinal *SSEEvent

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case evt, open := <-logCh:
			if live != nil {
				if live = writeWaitingFrames(w, live, &heldFinal); live == nil {
					live = resubscribe()
				}
			}
			if !open {
				writeHeldFinal(w, heldFinal)
				flusher.Flush()
				if !sentFinal {
					h.finishIfCompleted(ctx, taskID)
				}
				return
			}
			if evt.Sequence > lastReplayed {
				writeSSEFrame(w, evt.EventType, evt.Payload, evt.Sequence)
				lastReplayed = evt.Sequence
			}
			sentFinal = evt.EventType == "final_response"
			flusher.Flush()
		case evt, open := <-live:
			switch {
			case !open:
				live = resubscribe()
			case evt.Sequence == 0 && evt.Final:
				heldFinal = evt
				continue
			case evt.Sequence == 0:
				writeSSEFrame(w, evt.EventType, evt.Data, 0)
				flusher.Flush()
				continue
			}
			if logCh == nil {
				if logCh, ok = follow(); !ok || (logCh == nil && live == nil) {
					writeHeldFinal(w, heldFinal)
					flusher.Flush()
					return
				}
			}
		}
	}
}

func (h *subscribeHandler) finishIfCompleted(ctx context.Context, taskID string) {
	if ctx.Err() != nil {
		return
	}
	done, err := h.eventLog.Completed(ctx, taskID)
	if err != nil {
		if ctx.Err() == nil {
			h.logSubscribeWarn(taskID, "completion check", err)
		}
		return
	}
	if done {
		h.sseManager.Finish(taskID)
	}
}

func (h *subscribeHandler) serveFromEventLog(w http.ResponseWriter, r *http.Request, taskID string, fromSeq int64, flusher http.Flusher) {
	ch, err := h.eventLog.Subscribe(r.Context(), taskID, fromSeq)
	if err != nil {
		if errors.Is(err, eventlog.ErrTaskNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
		} else {
			h.logSubscribeError(taskID, "subscribe from event log", err)
			writeError(w, http.StatusServiceUnavailable, "event log temporarily unavailable")
		}
		return
	}
	live, connID, held := h.sseManager.SubscribeHeld(taskID)
	defer func() {
		if held {
			h.sseManager.Unsubscribe(taskID, connID)
		}
	}()
	if held && h.onHeld != nil {
		h.onHeld(taskID)
	}
	resubscribe := func() <-chan *SSEEvent {
		ch, id, ok := h.sseManager.SubscribeHeld(taskID)
		if !ok {
			return nil
		}
		connID, held = id, true
		if h.onHeld != nil {
			h.onHeld(taskID)
		}
		return ch
	}

	writeSSEHeaders(w)
	_, _ = fmt.Fprint(w, ": SSE reconnection established\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	var pendingArtifacts []eventlog.Event
	var heldFinal *SSEEvent
	sentFinal := false
	defer func() {
		if sentFinal {
			h.sseManager.Finish(taskID)
		}
	}()
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case evt, open := <-live:
			if !open {
				live = resubscribe()
				continue
			}
			switch {
			case evt.Sequence == 0 && evt.Final:
				heldFinal = evt
			case evt.Sequence == 0:
				writeSSEFrame(w, evt.EventType, evt.Data, 0)
				flusher.Flush()
			}
		case evt, open := <-ch:
			if live != nil {
				if live = writeWaitingFrames(w, live, &heldFinal); live == nil {
					live = resubscribe()
				}
			}
			flusher.Flush()
			if !open {
				writeHeldFinal(w, heldFinal)
				flusher.Flush()
				if !sentFinal {
					h.finishIfCompleted(ctx, taskID)
				}
				goto done
			}
			sentFinal = evt.EventType == "final_response"
			switch evt.EventType {
			case "artifact_update":
				pendingArtifacts = append(pendingArtifacts, evt)
			case "final_response":
				pendingArtifacts = nil
				writeSSEFrame(w, evt.EventType, evt.Payload, evt.Sequence)
				flusher.Flush()
			default:
				for _, ae := range pendingArtifacts {
					writeSSEFrame(w, ae.EventType, ae.Payload, ae.Sequence)
				}
				pendingArtifacts = nil
				writeSSEFrame(w, evt.EventType, evt.Payload, evt.Sequence)
				flusher.Flush()
			}
		}
	}

done:
	for _, ae := range pendingArtifacts {
		writeSSEFrame(w, ae.EventType, ae.Payload, ae.Sequence)
	}
	if len(pendingArtifacts) > 0 {
		flusher.Flush()
	}
}

func writeWaitingFrames(w http.ResponseWriter, live <-chan *SSEEvent, held **SSEEvent) <-chan *SSEEvent {
	for live != nil {
		select {
		case evt, open := <-live:
			if !open {
				return nil
			}
			switch {
			case evt.Sequence == 0 && evt.Final:
				*held = evt
			case evt.Sequence == 0:
				writeSSEFrame(w, evt.EventType, evt.Data, 0)
			}
		default:
			return live
		}
	}
	return nil
}

func writeHeldFinal(w http.ResponseWriter, final *SSEEvent) {
	if final != nil {
		writeSSEFrame(w, final.EventType, final.Data, 0)
	}
}

func (h *subscribeHandler) logSubscribeWarn(taskID, op string, err error) {
	if h.sseManager != nil && h.sseManager.logger != nil {
		h.sseManager.logger.Warn("SSE subscribe", "op", op, "taskID", taskID, "error", err)
	}
}

func (h *subscribeHandler) logSubscribeError(taskID, op string, err error) {
	if h.sseManager != nil && h.sseManager.logger != nil {
		h.sseManager.logger.Error("SSE subscribe", "op", op, "taskID", taskID, "error", err)
	}
}

func (h *subscribeHandler) writeStreamError(w http.ResponseWriter, taskID, msg string, err error, flusher http.Flusher) {
	errID := idgen.New()
	if h.sseManager != nil && h.sseManager.logger != nil {
		h.sseManager.logger.Error(msg, "taskID", taskID, "errorId", errID, "error", err)
	}
	payload := fmt.Appendf(nil,
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"failed to load conversation history","data":{"errorId":%q}}}`,
		errID)
	writeSSEFrame(w, "final_response", payload, 0)
	flusher.Flush()
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func writeSSEFrame(w http.ResponseWriter, eventType string, data []byte, sequence int64) {
	if sequence > 0 {
		_, _ = fmt.Fprintf(w, "id: %d\n", sequence)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}
