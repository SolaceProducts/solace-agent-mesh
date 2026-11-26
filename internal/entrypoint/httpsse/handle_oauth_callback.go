// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type oauthCallbackHandler struct {
	core   *entrypoint.GatewayCore
	logger *slog.Logger
}

const fragmentBounceMarker = "__fragbounce"

func (h *oauthCallbackHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	oauthErr := q.Get("error")

	if oauthErr != "" {
		h.handleOAuthError(w, r, oauthErr, q.Get("error_description"), q.Get("error_uri"))
		return
	}

	if code == "" || state == "" {
		if code == "" && wantsHTML(r) && q.Get(fragmentBounceMarker) == "" {
			h.logger.Debug("serving OAuth fragment-recovery page", "path", r.URL.Path)
			writeFragmentBounceHTML(w)
			return
		}
		h.logger.Warn("OAuth callback missing code or state", "path", r.URL.Path)
		if wantsHTML(r) {
			writeCallbackHTMLHeaders(w)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, oauthNoResponseHTML)
			return
		}
		auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken, "Missing code or state parameter")
		return
	}

	authState, ok := h.core.TakeToolAuthState(r.Context(), state)
	if !ok {
		h.logger.Warn("OAuth callback state not found or expired", "stateLen", len(state))
		auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken, "Invalid or expired state parameter")
		return
	}

	callbackURL := fmt.Sprintf("%s?%s", r.URL.Path, r.URL.RawQuery)
	if r.URL.Host != "" {
		callbackURL = r.URL.String()
	}

	authResp := a2a.AuthResponseData{
		Type:              "auth_response",
		TargetAgent:       authState.TargetAgent,
		TargetAgentTaskID: authState.TaskID,
		UserID:            authState.UserID,
		ClientID:          authState.ClientID,
		FunctionCallID:    authState.FunctionCallID,
		Code:              code,
		CredentialKey:     authState.CredentialKey,
		UserIdentity:      nil,
		AuthResponseURI:   callbackURL,
		TaskCallStack:     authState.TaskCallStack,
		CodeVerifier:      authState.CodeVerifier,
		State:             state,
		IsCredentialSave:  true,
	}

	msg := a2a.Message{
		Role: "user",
		Parts: []a2a.Part{
			a2a.NewDataPart(authResp),
		},
	}

	if err := h.core.SubmitAuthResponse(r.Context(), authState.TargetAgent, authState.TaskID, msg, authState.UserID); err != nil {
		h.logger.Error("failed to send auth_response to agent", "agentName", authState.TargetAgent, "error", err)
		auth.WriteError(w, http.StatusInternalServerError, auth.ErrorServerConfiguration, "Failed to process authentication")
		return
	}

	h.logger.Info("OAuth callback processed, auth_response sent",
		"toolType", authState.ToolType,
		"agentName", authState.TargetAgent,
		"credentialKey", authState.CredentialKey,
	)

	writeCallbackHTMLHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, oauthSuccessHTML)
}

func writeCallbackHTMLHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
}

func (h *oauthCallbackHandler) handleOAuthError(w http.ResponseWriter, r *http.Request, errCode, errDesc, errURI string) {
	h.logger.Warn("OAuth callback returned error from authorization server",
		"error", errCode,
		"errorDescription", truncate(errDesc, 80),
		"errorUri", errURI,
		"path", r.URL.Path,
	)

	detail := errCode
	if errDesc != "" {
		detail = fmt.Sprintf("%s: %s", errCode, errDesc)
	}

	if !wantsHTML(r) {
		auth.WriteError(w, http.StatusBadRequest, auth.ErrorInvalidToken,
			"Authorization server returned an error: "+detail)
		return
	}

	writeCallbackHTMLHeaders(w)
	w.WriteHeader(http.StatusBadRequest)
	_ = oauthErrorTmpl.Execute(w, oauthErrorPage{Error: errCode, ErrorDescription: errDesc})
}

func wantsHTML(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func writeFragmentBounceHTML(w http.ResponseWriter) {
	writeCallbackHTMLHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, oauthFragmentBounceHTML)
}

const oauthSuccessHTML = `<!DOCTYPE html>
<html>
<head><title>Authentication Complete</title></head>
<body>
<h1>Authentication Successful</h1>
<p>You have been authenticated. You can close this window and return to the app.</p>
<script>window.close();</script>
</body>
</html>`

const oauthNoResponseHTML = `<!DOCTYPE html>
<html>
<head><title>Authentication Incomplete</title></head>
<body>
<h1>Authentication Incomplete</h1>
<p>No authorization code or error was received. Please close this window and start the authentication again.</p>
</body>
</html>`

const oauthFragmentBounceHTML = `<!DOCTYPE html>
<html>
<head><title>Completing Authentication…</title></head>
<body>
<p>Completing authentication…</p>
<script>
(function () {
  var hash = window.location.hash;
  var sep = window.location.search ? "&" : "?";
  var tail = (hash && hash.length > 1) ? hash.substring(1) : "__fragbounce=1";
  window.location.replace(window.location.pathname + window.location.search + sep + tail);
})();
</script>
</body>
</html>`

type oauthErrorPage struct {
	Error            string
	ErrorDescription string
}

var oauthErrorTmpl = template.Must(template.New("oauthError").Parse(`<!DOCTYPE html>
<html>
<head><title>Authorization Error</title></head>
<body>
<h1>Authorization Error</h1>
<p>The authorization server returned an error: <strong>{{.Error}}</strong></p>
{{if .ErrorDescription}}<p>{{.ErrorDescription}}</p>{{end}}
<p>Please close this window, correct the issue, and try again.</p>
</body>
</html>`))
