// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

var _ stateDispatcherHandler = (*StateDispatcher)(nil)

type stateVerifier interface {
	VerifyStateToken(ctx context.Context, raw string) (*trust.StateClaims, error)
}

type StateDispatcher struct {
	sv     stateVerifier
	logger *slog.Logger
}

func NewStateDispatcher(sv stateVerifier, logger *slog.Logger) *StateDispatcher {
	return &StateDispatcher{
		sv:     sv,
		logger: logger.With("flow", "oauth_dispatcher"),
	}
}

func (d *StateDispatcher) Handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stateRaw := q.Get("state")
	idpCode := q.Get("code")
	idpErr := q.Get("error")
	idpErrDesc := q.Get("error_description")

	claims, err := d.sv.VerifyStateToken(r.Context(), stateRaw)
	if err != nil {
		d.logger.Warn("state-dispatch: state JWT verification failed", "error", err)
		writeOpaqueError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	u, err := url.Parse(claims.TargetLinkURI)
	if err != nil || u.Host == "" {
		d.logger.Warn("state-dispatch: target_link_uri unparseable after verify", "uri", claims.TargetLinkURI)
		writeOpaqueError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	qOut := u.Query()
	if idpErr != "" {
		qOut.Set("error", idpErr)
		qOut.Set("error_description", idpErrDesc)
	} else if idpCode != "" {
		qOut.Set("code", idpCode)
	}
	qOut.Set("state", stateRaw)
	u.RawQuery = qOut.Encode()

	d.logger.Info("state-dispatch: dispatching to finish",
		"targetGatewayID", claims.Issuer,
		"targetLinkURI", claims.TargetLinkURI,
		"hadIDPError", idpErr != "")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func writeOpaqueError(w http.ResponseWriter, status int, errCode string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]string{"error": errCode, "error_description": ""}
	_ = json.NewEncoder(w).Encode(body)
}
