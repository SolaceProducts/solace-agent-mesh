// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

const cliRedirectCookieName = "sam_cli_redirect"

const cliStateCookieName = "sam_cli_state"

const cliResponseModeValue = "cli"

const cliConfirmedQueryValue = "1"

func IsLoopbackRedirectURI(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("redirect_uri is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("redirect_uri: parse: %w", err)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("redirect_uri: must use http (got %q)", u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("redirect_uri: must not contain userinfo")
	}
	if u.Fragment != "" || strings.Contains(rawURL, "#") {
		return "", fmt.Errorf("redirect_uri: must not contain a fragment")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("redirect_uri: host missing")
	}
	if host != "localhost" {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return "", fmt.Errorf("redirect_uri: host %q is not loopback (must be 127.0.0.0/8, ::1, or localhost)", host)
		}
		if !addr.IsLoopback() {
			return "", fmt.Errorf("redirect_uri: host %q is not loopback (RFC 8252 §7.3)", host)
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func setCLIRedirectCookie(w http.ResponseWriter, redirectURI, secretKey string, secure bool) {
	signed := signState(redirectURI, secretKey)
	http.SetCookie(w, &http.Cookie{
		Name:     cliRedirectCookieName,
		Value:    signed,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func getCLIRedirectCookie(r *http.Request, secretKey string) (string, error) {
	cookie, err := r.Cookie(cliRedirectCookieName)
	if err != nil {
		return "", nil
	}
	return verifySignedState(cookie.Value, secretKey)
}

func clearCLIRedirectCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cliRedirectCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

func setCLIStateCookie(w http.ResponseWriter, state, secretKey string, secure bool) {
	signed := signState(state, secretKey)
	http.SetCookie(w, &http.Cookie{
		Name:     cliStateCookieName,
		Value:    signed,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func getCLIStateCookie(r *http.Request, secretKey string) (string, error) {
	cookie, err := r.Cookie(cliStateCookieName)
	if err != nil {
		return "", nil
	}
	return verifySignedState(cookie.Value, secretKey)
}

func clearCLIStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cliStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

func buildCLICallbackURL(redirectURI, accessToken, samAccessToken, refreshToken, state string, expiresIn int) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", fmt.Errorf("cli callback url: %w", err)
	}
	q := u.Query()
	q.Set("access_token", accessToken)
	q.Set("sam_access_token", samAccessToken)
	if refreshToken != "" {
		q.Set("refresh_token", refreshToken)
	}
	if state != "" {
		q.Set("state", state)
	}
	if expiresIn > 0 {
		q.Set("expires_in", fmt.Sprintf("%d", expiresIn))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func renderCLIConsentPage(w http.ResponseWriter, r *http.Request, redirectURI, cliState, platformLabel string) {
	continueURL := *r.URL
	q := continueURL.Query()
	q.Set("confirmed", cliConfirmedQueryValue)
	continueURL.RawQuery = q.Encode()
	continueURL.Scheme = ""
	continueURL.Host = ""

	cancelURL, _ := buildCLICancelURL(redirectURI, cliState)

	data := struct {
		ContinueHref  string
		CancelHref    string
		PlatformLabel string
		LoopbackHost  string
	}{
		ContinueHref:  continueURL.String(),
		CancelHref:    cancelURL,
		PlatformLabel: platformLabel,
		LoopbackHost:  loopbackDisplayHost(redirectURI),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	_ = cliConsentTemplate.Execute(w, data)
}

func buildCLICancelURL(redirectURI, state string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", fmt.Errorf("cli cancel url: %w", err)
	}
	q := u.Query()
	q.Set("error", "access_denied")
	q.Set("error_description", "User canceled SAM CLI sign-in")
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func loopbackDisplayHost(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return ""
	}
	return u.Host
}

var cliConsentTemplate = template.Must(template.New("cliConsent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Authorize SAM CLI</title>
<style>
body { font-family: system-ui, sans-serif; background: #f5f7fa; padding: 4em 1em; color: #1a202c; }
.card { background: white; max-width: 32em; margin: 0 auto; padding: 2em; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
h1 { font-size: 1.4em; margin-top: 0; }
.note { color: #4a5568; }
.detail { color: #4a5568; font-size: 0.9em; margin-top: 1.5em; }
.detail code { background: #edf2f7; padding: 1px 6px; border-radius: 4px; font-size: 0.95em; }
.actions { margin-top: 2em; display: flex; gap: 0.75em; flex-wrap: wrap; }
.btn { display: inline-block; padding: 0.6em 1.2em; border-radius: 6px; text-decoration: none; font-weight: 500; }
.btn-primary { background: #2b6cb0; color: white; }
.btn-primary:hover { background: #2c5282; }
.btn-secondary { background: #edf2f7; color: #2d3748; }
.btn-secondary:hover { background: #e2e8f0; }
</style>
</head>
<body>
<div class="card">
<h1>Sign in to SAM</h1>
<p class="note">The <strong>SAM CLI</strong> on this computer is requesting to sign in{{if .PlatformLabel}} to <strong>{{.PlatformLabel}}</strong>{{end}}.</p>
<p class="note">If you didn't start <code>sam auth login</code>, click Cancel.</p>
<div class="actions">
<a class="btn btn-primary" href="{{.ContinueHref}}">Continue to sign in</a>
<a class="btn btn-secondary" href="{{.CancelHref}}">Cancel</a>
</div>
{{if .LoopbackHost}}<p class="detail">Tokens will be delivered to <code>{{.LoopbackHost}}</code> on this machine.</p>{{end}}
</div>
</body>
</html>`))

type cliConfigResponse struct {
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

func (p *Provider) handleCLIConfig(w http.ResponseWriter, _ *http.Request) {
	if p.config == nil || p.config.OIDC == nil || p.config.OIDC.Issuer == "" {
		http.NotFound(w, nil)
		return
	}
	scopes := p.config.OIDC.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile", "offline_access"}
	}
	body := cliConfigResponse{
		Issuer:   p.config.OIDC.Issuer,
		ClientID: p.config.OIDC.ClientID,
		Scopes:   scopes,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		p.logger.Warn("cli-config: encode failed", "error", err)
	}
}
