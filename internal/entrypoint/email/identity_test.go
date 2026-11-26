// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"log/slog"
	"testing"
)

func TestEmailIdentityResolver_IssuesIdentityFromSenderEmail(t *testing.T) {
	t.Parallel()
	r := NewEmailIdentityResolver(slog.Default(), false)
	id, err := r.Resolve(t.Context(), EmailAddress{Address: "alice@vendor.com"}, AuthVerdict{}, InternalTrustResult{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == nil {
		t.Fatal("nil identity")
	}
	if id.UserID() != "alice@vendor.com" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "alice@vendor.com")
	}
	if id.Email() != "alice@vendor.com" {
		t.Errorf("Email = %q, want %q", id.Email(), "alice@vendor.com")
	}
	if id.AuthMethod() != "email" {
		t.Errorf("AuthMethod = %q, want %q", id.AuthMethod(), "email")
	}
	if !id.Authenticated() {
		t.Error("Authenticated = false, want true")
	}
	if got, _ := id.Metadata()["email_sender"].(string); got != "alice@vendor.com" {
		t.Errorf("Metadata[email_sender] = %q, want %q", got, "alice@vendor.com")
	}
}

func TestEmailIdentityResolver_CanonicalizesAndPreservesDisplayName(t *testing.T) {
	t.Parallel()
	r := NewEmailIdentityResolver(slog.Default(), false)
	id, err := r.Resolve(
		t.Context(),
		EmailAddress{Address: "ALICE@VENDOR.COM", DisplayName: "Alice Smith"},
		AuthVerdict{},
		InternalTrustResult{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.UserID() != "alice@vendor.com" {
		t.Errorf("UserID = %q, want canonical lowercase", id.UserID())
	}
	if id.Email() != "alice@vendor.com" {
		t.Errorf("Email = %q, want canonical lowercase", id.Email())
	}
	if id.Name() != "Alice Smith" {
		t.Errorf("Name = %q, want %q", id.Name(), "Alice Smith")
	}
}

func TestEmailIdentityResolver_RejectsEmptySenderAddress(t *testing.T) {
	t.Parallel()
	r := NewEmailIdentityResolver(slog.Default(), false)
	_, err := r.Resolve(t.Context(), EmailAddress{Address: ""}, AuthVerdict{}, InternalTrustResult{})
	if err == nil {
		t.Fatal("expected error for empty sender, got nil")
	}
}

func TestEmailIdentityResolver_RefusesIdentityOnDMARCFailureWhenGated(t *testing.T) {
	t.Parallel()
	cases := []AuthOutcome{AuthFail, AuthSoftfail, AuthNone, AuthNeutral, AuthTempError, AuthPermError, ""}
	for _, dmarc := range cases {
		t.Run(string(dmarc), func(t *testing.T) {
			t.Parallel()
			r := NewEmailIdentityResolver(slog.Default(), true)
			_, err := r.Resolve(
				t.Context(),
				EmailAddress{Address: "alice@vendor.com"},
				AuthVerdict{DMARC: dmarc},
				InternalTrustResult{},
			)
			if err == nil {
				t.Fatalf("DMARC=%q gated, expected error, got nil", dmarc)
			}
		})
	}
}

func TestEmailIdentityResolver_AllowsIdentityOnDMARCPassWhenGated(t *testing.T) {
	t.Parallel()
	r := NewEmailIdentityResolver(slog.Default(), true)
	id, err := r.Resolve(
		t.Context(),
		EmailAddress{Address: "alice@vendor.com"},
		AuthVerdict{DMARC: AuthPass},
		InternalTrustResult{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == nil || id.UserID() != "alice@vendor.com" {
		t.Fatalf("identity = %+v, want UserID=alice@vendor.com", id)
	}
}

func TestEmailIdentityResolver_InternalTrustBypassesDMARCGate(t *testing.T) {
	t.Parallel()

	r := NewEmailIdentityResolver(slog.Default(), true)

	trust := InternalTrustResult{
		Trusted:  true,
		Reason:   "exchange_internal_authas+tenant_match",
		TenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	id, err := r.Resolve(
		t.Context(),
		EmailAddress{Address: "alice@example.org"},
		AuthVerdict{DMARC: AuthNone},
		trust,
	)
	if err != nil {
		t.Fatalf("internal-trusted Resolve error: %v", err)
	}
	if id == nil || id.UserID() != "alice@example.org" {
		t.Fatalf("identity = %+v, want UserID=alice@example.org", id)
	}
}

func TestEmailIdentityResolver_AllowsIdentityOnDMARCFailureWhenUnGated(t *testing.T) {
	t.Parallel()
	r := NewEmailIdentityResolver(slog.Default(), false)
	id, err := r.Resolve(
		t.Context(),
		EmailAddress{Address: "alice@vendor.com"},
		AuthVerdict{DMARC: AuthFail},
		InternalTrustResult{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == nil || id.UserID() != "alice@vendor.com" {
		t.Fatalf("identity = %+v, want UserID=alice@vendor.com", id)
	}
}
