// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import "testing"

type oauthAssertT interface {
	Helper()
	Errorf(format string, args ...any)
}

func (h *TestHarness) AssertOAuthState(t *testing.T) {
	t.Helper()
	h.assertOAuthStateImpl(t)
}

func (h *TestHarness) assertOAuthStateImpl(t oauthAssertT) {
	t.Helper()

	if h.scenario == nil || h.scenario.OAuthTrigger == nil {
		return
	}
	if h.oauthProvider == nil {
		return
	}

	trig := h.scenario.OAuthTrigger

	if trig.ExpectedAuthCodeGrants != nil {
		got := h.oauthProvider.AuthCodeGrantCount()
		want := *trig.ExpectedAuthCodeGrants
		if got != want {
			t.Errorf("oauth provider: authorization_code grants got %d, want %d", got, want)
		}
	}

	if trig.ExpectedRefreshGrants != nil {
		got := h.oauthProvider.RefreshGrantCount()
		want := *trig.ExpectedRefreshGrants
		if got != want {
			t.Errorf("oauth provider: refresh_token grants got %d, want %d", got, want)
		}
	}

	if trig.ExpectedErrors != nil {
		got := h.oauthProvider.ErrorCount()
		want := *trig.ExpectedErrors
		if got != want {
			t.Errorf("oauth provider: errors got %d, want %d", got, want)
		}
	}
}
