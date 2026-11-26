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

func writtenCookie(t *testing.T, write func(http.ResponseWriter)) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	write(w)
	got := w.Result().Cookies()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 Set-Cookie, got %d", len(got))
	}
	return got[0]
}

func TestSetOIDCStateCookie_FramingAttributes(t *testing.T) {
	tests := []struct {
		name            string
		secure          bool
		wantSameSite    http.SameSite
		wantPartitioned bool
	}{
		{"secure deployment is framable", true, http.SameSiteNoneMode, true},
		{"dev mode falls back to lax", false, http.SameSiteLaxMode, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := writtenCookie(t, func(w http.ResponseWriter) {
				setOIDCStateCookie(w, "some-state", "secret", tt.secure)
			})

			if c.Name != oidcStateCookieName {
				t.Errorf("Name = %q, want %q", c.Name, oidcStateCookieName)
			}
			if c.SameSite != tt.wantSameSite {
				t.Errorf("SameSite = %v, want %v", c.SameSite, tt.wantSameSite)
			}
			if c.Partitioned != tt.wantPartitioned {
				t.Errorf("Partitioned = %v, want %v", c.Partitioned, tt.wantPartitioned)
			}
			if c.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", c.Secure, tt.secure)
			}
			if !c.HttpOnly {
				t.Error("HttpOnly must stay set")
			}
			if got, err := verifySignedState(c.Value, "secret"); err != nil || got != "some-state" {
				t.Errorf("value must carry the signed state; got %q err %v", got, err)
			}
		})
	}
}

func TestClearOIDCStateCookie_MirrorsSetAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		set := writtenCookie(t, func(w http.ResponseWriter) {
			setOIDCStateCookie(w, "some-state", "secret", secure)
		})
		cleared := writtenCookie(t, func(w http.ResponseWriter) {
			clearOIDCStateCookie(w, secure)
		})

		if cleared.Name != set.Name {
			t.Errorf("secure=%v: Name = %q, want %q", secure, cleared.Name, set.Name)
		}
		if cleared.Path != set.Path {
			t.Errorf("secure=%v: Path = %q, want %q", secure, cleared.Path, set.Path)
		}
		if cleared.SameSite != set.SameSite {
			t.Errorf("secure=%v: SameSite = %v, want %v", secure, cleared.SameSite, set.SameSite)
		}
		if cleared.Partitioned != set.Partitioned {
			t.Errorf("secure=%v: Partitioned = %v, want %v", secure, cleared.Partitioned, set.Partitioned)
		}
		if cleared.Secure != set.Secure {
			t.Errorf("secure=%v: Secure = %v, want %v", secure, cleared.Secure, set.Secure)
		}
		if cleared.Value != "" {
			t.Errorf("secure=%v: Value = %q, want empty", secure, cleared.Value)
		}
		if cleared.MaxAge >= 0 {
			t.Errorf("secure=%v: MaxAge = %d, want negative", secure, cleared.MaxAge)
		}
	}
}

func TestSignAndVerifyState(t *testing.T) {
	const key = "state-secret"

	for _, state := range []string{generateState(), "plain-state", ""} {
		signed := signState(state, key)
		got, err := verifySignedState(signed, key)
		if err != nil {
			t.Fatalf("verify %q: %v", state, err)
		}
		if got != state {
			t.Errorf("roundtrip: got %q, want %q", got, state)
		}
	}
}

func TestVerifySignedState_Rejects(t *testing.T) {
	signed := signState("some-state", "k1")

	if _, err := verifySignedState(signed, "k2"); err == nil {
		t.Error("must reject when key changes")
	}
	if _, err := verifySignedState("no-dot", "k1"); err == nil {
		t.Error("must reject malformed value")
	}
	parts := strings.SplitN(signed, ".", 2)
	if _, err := verifySignedState("other-state."+parts[1], "k1"); err == nil {
		t.Error("must reject tampered state")
	}
	if _, err := verifySignedState(parts[0]+".!!!not-base64!!!", "k1"); err == nil {
		t.Error("must reject signature that is not valid base64")
	}
	if _, err := verifySignedState(parts[0]+".", "k1"); err == nil {
		t.Error("must reject empty signature")
	}
}
