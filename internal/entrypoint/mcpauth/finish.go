// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"net/http"
)

func (h *Handler) HandleFinish(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.StateRedirectionEnabled {
		h.logger.Error("MCP OAuth finish: state_redirection_enabled=false")
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	if h.cfg.StateVerifier == nil {
		h.logger.Error("MCP OAuth finish: state verifier not configured")
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	q := r.URL.Query()
	stateRaw := q.Get("state")
	idpCode := q.Get("code")
	idpErr := q.Get("error")
	idpErrDesc := q.Get("error_description")

	claims, err := h.cfg.StateVerifier.VerifyStateToken(r.Context(), stateRaw)
	if err != nil {
		h.logger.Error("MCP OAuth finish: state JWT verification failed", "error", err)
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	if claims.Issuer != h.cfg.GatewayID {
		h.logger.Error("MCP OAuth finish: iss mismatch",
			"expected", h.cfg.GatewayID, "got", claims.Issuer)
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	sd, err := h.store.takeStateByVerifierHash(r.Context(), claims.GwPKCEVerifierHash)
	if err != nil {
		h.logger.Error("MCP OAuth finish: read authorization state failed", "error", err)
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "")
		return
	}
	if sd == nil {
		h.logger.Error("MCP OAuth finish: no matching state",
			"hashPrefix", safePrefix(claims.GwPKCEVerifierHash, 8))
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	if idpErr != "" {
		redirectClientError(w, r, sd.ClientRedirectURI, sd.ClientState,
			idpErr, idpErrDesc)
		return
	}
	if idpCode == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}

	h.completeMCPFlow(w, r, sd, idpCode, h.cfg.OAuthDispatcherURL)
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
