// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stateCookieFrom(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == oidcStateCookieName {
			return c
		}
	}
	return nil
}

func issuedStateCookie(t *testing.T, p *Provider, state string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	setOIDCStateCookie(w, state, p.config.SessionSecretKey, true)
	return w.Result().Cookies()[0]
}

func TestCallbackClear_CarriesPartitionedAttributes(t *testing.T) {
	p, _ := newAuditingProvider(t, &fakeOIDCClient{validateState: true})
	if !p.cookieSecure() {
		t.Fatal("test provider must be in the secure configuration for this to be meaningful")
	}

	tampered := issuedStateCookie(t, p, "some-state")
	tampered.Value = "not-a-validly-signed-value"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code&state=some-state", nil)
	req.AddCookie(tampered)

	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)

	cleared := stateCookieFrom(rec.Result())
	if cleared == nil {
		t.Fatal("an unverifiable cookie must still be cleared")
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative", cleared.MaxAge)
	}
	if cleared.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None — the clear must match the cookie it deletes", cleared.SameSite)
	}
	if !cleared.Partitioned {
		t.Error("Partitioned must be set: a partitioned cookie is keyed by its partition, so an unpartitioned delete does not match it")
	}
	if !cleared.Secure {
		t.Error("Secure must be set alongside SameSite=None")
	}
}

func TestCallbackStateMismatch_LeavesInFlightCookieIntact(t *testing.T) {
	p, rec := newAuditingProvider(t, &fakeOIDCClient{validateState: true})

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=attacker-code&state=attacker-state", nil)
	req.AddCookie(issuedStateCookie(t, p, "victim-state"))

	res := httptest.NewRecorder()
	p.handleCallback(res, req)

	if got := stateCookieFrom(res.Result()); got != nil {
		t.Errorf("mismatch must leave the cookie alone; got Set-Cookie with MaxAge=%d", got.MaxAge)
	}
	if res.Code != http.StatusBadRequest && res.Code != http.StatusFound {
		t.Errorf("status = %d, want the request rejected", res.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCStateMismatch)
}

func TestCallbackStateMismatch_VictimCanStillCompleteLogin(t *testing.T) {
	p, _ := newAuditingProvider(t, &fakeOIDCClient{validateState: true})
	cookie := issuedStateCookie(t, p, "victim-state")

	griefing := httptest.NewRequest("GET", "/api/v1/auth/callback?code=x&state=wrong", nil)
	griefing.AddCookie(cookie)
	p.handleCallback(httptest.NewRecorder(), griefing)

	victim := httptest.NewRequest("GET", "/api/v1/auth/callback?code=victim-code&state=victim-state", nil)
	victim.AddCookie(cookie)
	res := httptest.NewRecorder()
	p.handleCallback(res, victim)

	if body := res.Body.String(); strings.Contains(body, "State parameter mismatch") {
		t.Error("the victim's state must still validate after a griefing attempt")
	}
	cleared := stateCookieFrom(res.Result())
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Error("the successful path must still clear the cookie, keeping it single-use")
	}
}
