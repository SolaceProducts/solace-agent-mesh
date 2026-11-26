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

func TestSignAndVerifyValue(t *testing.T) {
	const key = "super-secret"

	cases := []string{
		"short",
		"",
		"value with . dots . in . it",
		"opaque IdP token: eyJhbGciOiJIUzI1NiJ9.foo.bar",
		strings.Repeat("x", 4096),
	}

	for _, v := range cases {
		signed := signValue(v, key)
		got, err := verifySignedValue(signed, key)
		if err != nil {
			t.Fatalf("verify %q: %v", v, err)
		}
		if got != v {
			t.Errorf("roundtrip: got %q, want %q", got, v)
		}
	}
}

func TestVerifySignedValue_Rejects(t *testing.T) {
	signed := signValue("abc", "k1")

	if _, err := verifySignedValue(signed, "k2"); err == nil {
		t.Error("must reject when key changes")
	}
	if _, err := verifySignedValue("no-dot", "k1"); err == nil {
		t.Error("must reject malformed value")
	}
	parts := strings.SplitN(signed, ".", 2)
	tampered := "ZnJlc2g" + "." + parts[1]
	if _, err := verifySignedValue(tampered, "k1"); err == nil {
		t.Error("must reject tampered payload")
	}
	if _, err := verifySignedValue(parts[0]+".!!!not-base64!!!", "k1"); err == nil {
		t.Error("must reject signature that is not valid base64")
	}
	if _, err := verifySignedValue(parts[0]+".", "k1"); err == nil {
		t.Error("must reject empty signature")
	}
}

func TestSetGetClearSignedCookie(t *testing.T) {
	const key = "signing-key"
	rec := httptest.NewRecorder()
	setSignedCookie(rec, cookieOpts{
		Name:     "sam_access_token",
		MaxAge:   3600,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}, "opaque-idp-token", key)

	resp := rec.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != "sam_access_token" || c.MaxAge != 3600 || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attrs wrong: %+v", c)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	got, err := getSignedCookie(req, "sam_access_token", key)
	if err != nil {
		t.Fatalf("getSignedCookie: %v", err)
	}
	if got != "opaque-idp-token" {
		t.Errorf("got %q, want opaque-idp-token", got)
	}

	rec2 := httptest.NewRecorder()
	clearSignedCookie(rec2, "sam_access_token", true)
	deleteCookies := rec2.Result().Cookies()
	if len(deleteCookies) != 1 || deleteCookies[0].MaxAge != -1 {
		t.Fatalf("expected delete cookie with MaxAge=-1, got %+v", deleteCookies)
	}
}
