// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "testing"

func TestRecordAuthLogin(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())

	RecordAuthLogin("gw-1", AuthResultSuccess, "oidc_login")
	RecordAuthLogin("gw-1", AuthResultFailure, "oidc_state_mismatch")

	if !rec.HasCounter("auth.login",
		"gateway", "gw-1", "result", AuthResultSuccess, "reason", "oidc_login") {
		t.Error("expected auth.login{result=success, reason=oidc_login}")
	}
	if !rec.HasCounter("auth.login",
		"gateway", "gw-1", "result", AuthResultFailure, "reason", "oidc_state_mismatch") {
		t.Error("expected auth.login{result=failure, reason=oidc_state_mismatch}")
	}
	if got := rec.CounterSum("auth.login", "gateway", "gw-1"); got != 2 {
		t.Errorf("auth.login count = %d, want 2", got)
	}
}

func TestRecordAuthTokenValidationFailure(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())

	RecordAuthTokenValidationFailure("gw-1", "expired_token")

	if !rec.HasCounter("auth.token.validation",
		"gateway", "gw-1", "result", AuthResultFailure, "reason", "expired_token") {
		t.Error("expected auth.token.validation{result=failure, reason=expired_token}")
	}
	if rec.HasCounter("auth.token.validation", "result", AuthResultSuccess) {
		t.Error("auth.token.validation must never carry result=success")
	}
}

func TestRecordAuth_NoProvider(t *testing.T) {
	RecordAuthLogin("gw-1", AuthResultSuccess, "oidc_login")
	RecordAuthTokenValidationFailure("gw-1", "invalid_token")
}
