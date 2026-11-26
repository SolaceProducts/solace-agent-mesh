// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type fakeDispatcher struct {
	calls      int64
	last       *NormalizedEmail
	lastCaller *gwauth.Subject
	lastTrust  InternalTrustResult
}

func (f *fakeDispatcher) Dispatch(_ context.Context, n *NormalizedEmail, caller *gwauth.Subject, trust InternalTrustResult) (bool, error) {
	atomic.AddInt64(&f.calls, 1)
	f.last = n
	f.lastCaller = caller
	f.lastTrust = trust
	return true, nil
}

func TestWorker_DispatchesMessage(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Provider:               NewFakeProvider(nil),
		Resolver:               NewEmailIdentityResolver(slog.Default(), true),
		Policy:                 &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs:     []string{"protection.outlook.com"},
		MaxMessageSizeBytes:    25 * 1024 * 1024,
		MaxAttachmentSizeBytes: 10 * 1024 * 1024,
		Dispatcher:             disp,
		Logger:                 slog.Default(),
	}
	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})
	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Errorf("dispatcher called %d times, want 1", got)
	}
	if disp.last == nil {
		t.Fatal("dispatcher did not capture the normalized email")
	}
	if disp.last.From.Address != "alice@vendor.com" {
		t.Errorf("normalized From = %q, want alice@vendor.com", disp.last.From.Address)
	}
	if disp.lastCaller == nil || disp.lastCaller.UserID() != "alice@vendor.com" || disp.lastCaller.AuthMethod() != gwauth.AuthMethodEmail {
		t.Errorf("dispatched caller = %+v, want the DMARC-verified sender as an email caller", disp.lastCaller)
	}
}

func TestWorker_RequireDMARC_DropsAuthFail(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(slog.Default(), true),
		Policy:             &AuthPolicy{Mode: ModeRequireDMARC, Logger: slog.Default()},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         disp,
		Logger:             slog.Default(),
	}
	raw := mustReadFixture(t, "synthetic/auth_fail_dmarc.eml")
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})
	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("require_dmarc should reject DMARC-fail message; calls=%d", got)
	}
}

func TestWorker_AllowlistAdmits(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(slog.Default(), false),
		Policy:             &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         disp,
		Logger:             slog.Default(),
		AllowedSenders:     []string{"alice@vendor.com"},
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Errorf("allowlisted sender should be dispatched; calls=%d", got)
	}
}

func TestWorker_AllowlistDropsUnknownSender(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(slog.Default(), false),
		Policy:             &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         disp,
		Logger:             slog.Default(),
		AllowedSenders:     []string{"someone-else@vendor.com"},
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("non-allowlisted sender should be dropped; calls=%d", got)
	}
}

func TestWorker_EmptyAllowlistMeansNoGate(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(slog.Default(), false),
		Policy:             &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         disp,
		Logger:             slog.Default(),
		AllowedSenders:     nil,
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Errorf("empty allowlist should not gate; calls=%d", got)
	}
}

func TestWorker_PassesTrustToDispatcher(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:           NewEmailIdentityResolver(slog.Default(), false),
		Policy:             &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs: []string{"protection.outlook.com"},
		Dispatcher:         disp,
		Logger:             slog.Default(),
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: mustReadFixture(t, "synthetic/basic_text.eml")})
	if disp.lastTrust.Trusted {
		t.Errorf("dispatcher received Trusted=true with feature disabled; got %+v", disp.lastTrust)
	}
}

func TestWorker_InternalTrustEndToEnd(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := []byte(
		"From: alice@example.org\r\n" +
			"To: sam-entrypoint@example.org\r\n" +
			"Subject: internal trust test\r\n" +
			"Message-ID: <internal-trust-test@example.org>\r\n" +
			"Authentication-Results: dkim=none (message not signed) header.d=none; dmarc=none\r\n" +
			"X-MS-Exchange-Organization-AuthAs: Internal\r\n" +
			"X-MS-Exchange-Organization-AuthMechanism: 04\r\n" +
			"X-MS-Exchange-Organization-AuthSource: MAILBOX01.prod.exchangelabs.com\r\n" +
			"X-MS-Exchange-CrossTenant-Id: " + tenant + "\r\n" +
			"\r\n" +
			"hello\r\n",
	)

	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:                NewEmailIdentityResolver(slog.Default(), true),
		Policy:                  &AuthPolicy{Mode: ModeStrict, Logger: slog.Default()},
		TrustedAuthservIDs:      []string{"protection.outlook.com"},
		Dispatcher:              disp,
		Logger:                  slog.Default(),
		TrustedInternalTenantID: tenant,
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})

	if got := atomic.LoadInt64(&disp.calls); got != 1 {
		t.Fatalf("internal-trusted strict-mode message: dispatcher calls=%d, want 1", got)
	}
	if !disp.lastTrust.Trusted {
		t.Errorf("dispatcher.lastTrust.Trusted = false; trust did not propagate end-to-end. got=%+v", disp.lastTrust)
	}
	if disp.lastTrust.TenantID != tenant {
		t.Errorf("dispatcher.lastTrust.TenantID = %q, want %q", disp.lastTrust.TenantID, tenant)
	}
}

func TestWorker_InternalTrustWrongTenantStillRejects(t *testing.T) {
	t.Parallel()

	const ourTenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const otherTenant = "99999999-9999-9999-9999-999999999999"
	raw := []byte(
		"From: attacker@elsewhere.com\r\n" +
			"To: sam-entrypoint@example.org\r\n" +
			"Subject: cross-tenant attempt\r\n" +
			"Message-ID: <attacker@elsewhere.com>\r\n" +
			"Authentication-Results: dkim=none header.d=none; dmarc=none\r\n" +
			"X-MS-Exchange-Organization-AuthAs: Internal\r\n" +
			"X-MS-Exchange-Organization-AuthSource: ATTACKER.prod.exchangelabs.com\r\n" +
			"X-MS-Exchange-CrossTenant-Id: " + otherTenant + "\r\n" +
			"\r\n" +
			"hello\r\n",
	)

	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:                NewEmailIdentityResolver(slog.Default(), true),
		Policy:                  &AuthPolicy{Mode: ModeRequireDMARC, Logger: slog.Default()},
		TrustedAuthservIDs:      []string{"protection.outlook.com"},
		Dispatcher:              disp,
		Logger:                  slog.Default(),
		TrustedInternalTenantID: ourTenant,
	}
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})

	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("cross-tenant message admitted; calls=%d. Tenant-mismatch must reject.", got)
	}
}

func TestWorker_DropsOversizedMessage(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := &Worker{
		Resolver:            NewEmailIdentityResolver(slog.Default(), true),
		Policy:              &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs:  []string{"protection.outlook.com"},
		MaxMessageSizeBytes: 100,
		Dispatcher:          disp,
		Logger:              slog.Default(),
	}
	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	w.Handle(t.Context(), RawMessage{UID: "u1", RFC822: raw})
	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("oversized message should be dropped; calls=%d", got)
	}
}

func TestWorker_DropsMalformedMIME(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	w := newTestWorker(disp)
	raw := mustReadFixture(t, "synthetic/malformed_mime_boundary.eml")
	result := w.Handle(t.Context(), RawMessage{UID: "u-malformed", RFC822: raw})
	if got := atomic.LoadInt64(&disp.calls); got != 0 {
		t.Errorf("malformed MIME should not reach dispatcher; calls=%d", got)
	}
	if result != HandleDropped {
		t.Errorf("HandleResult = %v, want HandleDropped (malformed inputs are deterministic drops, not retries)", result)
	}
}

func newTestWorker(disp Dispatcher) *Worker {
	return &Worker{
		Resolver:               NewEmailIdentityResolver(slog.Default(), true),
		Policy:                 &AuthPolicy{Mode: ModeLogOnly, Logger: slog.Default()},
		TrustedAuthservIDs:     []string{"protection.outlook.com"},
		MaxMessageSizeBytes:    25 * 1024 * 1024,
		MaxAttachmentSizeBytes: 10 * 1024 * 1024,
		Dispatcher:             disp,
		Logger:                 slog.Default(),
	}
}
