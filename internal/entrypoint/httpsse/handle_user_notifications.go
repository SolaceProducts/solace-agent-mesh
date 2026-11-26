// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const userNotifKeepalive = 30 * time.Second

type userNotificationsHandler struct {
	core       *entrypoint.GatewayCore
	sseManager *SSEManager
	logger     *slog.Logger
}

func (h *userNotificationsHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	writeSSEHeaders(w)

	_, callerEmail := auth.UserIDFromContext(r.Context())
	ch, remove := h.sseManager.AddUserNotificationQueueForKeys(userID, strings.ToLower(strings.TrimSpace(callerEmail)), callerPlatformUserID(r))
	defer remove()

	_, _ = fmt.Fprint(w, ": notification stream established\n\n")
	flusher.Flush()

	budgetStatus := budgetStatusPusher{r: r, core: h.core}
	budgetChanged, err := budgetStatus.push(w, flusher)
	if err != nil {
		return
	}

	keepalive := time.NewTicker(userNotifKeepalive)
	defer keepalive.Stop()

	ctx, cancel := entrypoint.StreamContext(r)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-budgetChanged:
			if budgetChanged, err = budgetStatus.push(w, flusher); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case payload, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", payload.Event, payload.Data); err != nil {
				if h.logger != nil {
					h.logger.Debug("user notification write failed; client disconnected",
						"userID", userID, "error", err)
				}
				return
			}
			flusher.Flush()
		}
	}
}

const budgetStatusEvent = "budget_status"

type budgetStatusPusher struct {
	r    *http.Request
	core *entrypoint.GatewayCore
	sent []byte
}

func (p *budgetStatusPusher) push(w http.ResponseWriter, flusher http.Flusher) (<-chan struct{}, error) {
	gate := p.core.BudgetGate()
	if gate == nil {
		return nil, nil
	}
	changed := gate.Changed()
	data, err := json.Marshal(callerBudgetStatus(p.r, p.core))
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, p.sent) {
		return changed, nil
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", budgetStatusEvent, data); err != nil {
		return nil, err
	}
	flusher.Flush()
	p.sent = data
	return changed, nil
}

func callerPlatformUserID(r *http.Request) string {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok {
		return ""
	}
	return strings.ToLower(id.PlatformUserID())
}
