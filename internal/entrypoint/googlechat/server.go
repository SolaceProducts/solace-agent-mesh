// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxEventBytes          = 1 << 20
	eventsPath             = "/events"
	unauthenticatedLogWait = time.Minute
)

func (a *Adapter) eventsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+eventsPath, a.handleEvent)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if a.spool != nil && a.spool.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (a *Adapter) handleEvent(w http.ResponseWriter, r *http.Request) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || bearer == "" {
		a.refuseUnauthenticated(w, "missing_bearer", nil)
		return
	}
	if err := a.verifier.Verify(r.Context(), bearer); err != nil {
		if errors.Is(err, errSignerCertsUnavailable) {
			a.refuseUnverifiable(w, err)
			return
		}
		a.refuseUnauthenticated(w, "invalid_token", err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxEventBytes+1))
	if err != nil {
		a.refuseUndecodable(w, len(body), err)
		return
	}
	if len(body) > maxEventBytes {
		a.refuseUndecodable(w, len(body), errEventTooLarge)
		return
	}
	if !json.Valid(body) {
		a.refuseUndecodable(w, len(body), errInvalidJSON)
		return
	}
	ev, err := parseEvent(body)
	if err != nil {
		a.refuseUndecodable(w, len(body), err)
		return
	}
	if err := a.spool.Publish(r.Context(), body, ev.SenderName); err != nil {
		a.logger.Warn("google chat HTTPS event could not be spooled; answering 503", "error", err)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

var (
	errInvalidJSON   = errors.New("event body is not valid JSON")
	errEventTooLarge = errors.New("event body exceeds the size limit")
)

func (a *Adapter) refuseUndecodable(w http.ResponseWriter, payloadLen int, err error) {
	if a.badEventNotices.allow("undecodable", unauthenticatedLogWait) {
		a.logger.Warn("google chat HTTPS event could not be decoded; refusing it", "payloadLen", payloadLen, "error", err)
	}
	a.meter().Counter(metricAckedWithoutTask, 1, "gatewayId", a.gatewayID, "reason", "undecodable")
	http.Error(w, "bad request", http.StatusBadRequest)
}

func (a *Adapter) refuseUnverifiable(w http.ResponseWriter, err error) {
	if a.unauthenticatedNotices.allow("signer_certs_unavailable", unauthenticatedLogWait) {
		a.logger.Warn("google chat signer certs unavailable; answering 503", "error", err)
	}
	http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
}

func (a *Adapter) refuseUnauthenticated(w http.ResponseWriter, reason string, err error) {
	if a.unauthenticatedNotices.allow(reason, unauthenticatedLogWait) {
		a.logger.Warn("google chat HTTPS request rejected: not signed by Google Chat", "reason", reason, "error", err)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
