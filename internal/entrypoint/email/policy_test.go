// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestAuthPolicy_LogOnlyAlwaysAccepts(t *testing.T) {
	t.Parallel()
	p := NewAuthPolicy(ModeLogOnly, slog.Default())
	cases := []AuthVerdict{
		{SPF: AuthPass, DKIM: AuthPass, DMARC: AuthPass},
		{SPF: AuthFail, DKIM: AuthFail, DMARC: AuthFail},
		{SPF: AuthNone, DKIM: AuthNone, DMARC: AuthNone},
	}
	for _, v := range cases {
		if got := p.Decide(t.Context(), v, InternalTrustResult{}, "msg-id"); got != PolicyAccept {
			t.Errorf("verdict %+v: got %q, want accept", v, got)
		}
	}
}

func TestAuthPolicy_RequireDMARC(t *testing.T) {
	t.Parallel()
	p := NewAuthPolicy(ModeRequireDMARC, slog.Default())
	if p.Decide(t.Context(), AuthVerdict{DMARC: AuthPass}, InternalTrustResult{}, "msg") != PolicyAccept {
		t.Error("DMARC pass should accept")
	}
	if p.Decide(t.Context(), AuthVerdict{DMARC: AuthFail}, InternalTrustResult{}, "msg") != PolicyReject {
		t.Error("DMARC fail should reject")
	}
	if p.Decide(t.Context(), AuthVerdict{DMARC: AuthNone}, InternalTrustResult{}, "msg") != PolicyReject {
		t.Error("DMARC none should reject in require_dmarc mode")
	}
}

func TestAuthPolicy_StrictRequiresAllThree(t *testing.T) {
	t.Parallel()
	p := NewAuthPolicy(ModeStrict, slog.Default())
	all := AuthVerdict{SPF: AuthPass, DKIM: AuthPass, DMARC: AuthPass}
	if p.Decide(t.Context(), all, InternalTrustResult{}, "msg") != PolicyAccept {
		t.Error("all-pass should accept")
	}
	missing := AuthVerdict{SPF: AuthPass, DKIM: AuthPass, DMARC: AuthFail}
	if p.Decide(t.Context(), missing, InternalTrustResult{}, "msg") != PolicyReject {
		t.Error("DMARC fail in strict should reject")
	}
}

func TestAuthPolicy_InternalTrustAdmits(t *testing.T) {
	t.Parallel()

	trust := InternalTrustResult{
		Trusted:  true,
		Reason:   "exchange_internal_authas+tenant_match",
		TenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	allNone := AuthVerdict{SPF: AuthNone, DKIM: AuthNone, DMARC: AuthNone}

	for _, mode := range []PolicyMode{ModeRequireDMARC, ModeStrict, ModeLogOnly} {
		t.Run(string(mode), func(t *testing.T) {
			p := NewAuthPolicy(mode, slog.Default())
			got := p.Decide(t.Context(), allNone, trust, "msg")
			if got != PolicyAccept {
				t.Errorf("internal-trusted under %q: got %q, want accept", mode, got)
			}
		})
	}
}

func TestAuthPolicy_InternalTrustEmptyDoesNotBypass(t *testing.T) {
	t.Parallel()

	p := NewAuthPolicy(ModeRequireDMARC, slog.Default())
	got := p.Decide(t.Context(), AuthVerdict{DMARC: AuthFail}, InternalTrustResult{}, "msg")
	if got != PolicyReject {
		t.Errorf("DMARC-fail with empty trust under require_dmarc: got %q, want reject", got)
	}
}

func TestAuthPolicy_InternalTrustEmitsDistinctDecision(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	p := NewAuthPolicy(ModeStrict, logger)

	trust := InternalTrustResult{
		Trusted:  true,
		Reason:   "exchange_internal_authas+tenant_match",
		TenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	_ = p.Decide(t.Context(), AuthVerdict{}, trust, "<msg>")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("log line is not valid JSON: %v\n%s", err, buf.String())
	}
	if got["eventType"] != "email.allow_internal_trust" {
		t.Errorf("eventType = %v, want email.allow_internal_trust", got["eventType"])
	}
	if got["decision"] != "allow_internal_trust" {
		t.Errorf("decision = %v, want allow_internal_trust", got["decision"])
	}
	if got["internalTrustReason"] != "exchange_internal_authas+tenant_match" {
		t.Errorf("internalTrustReason = %v, want exchange_internal_authas+tenant_match", got["internalTrustReason"])
	}
}
