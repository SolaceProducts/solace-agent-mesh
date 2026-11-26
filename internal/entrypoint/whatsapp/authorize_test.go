// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type stubAuthorizer struct {
	res  auth.SenderAuthorization
	err  error
	last auth.SenderAuthorizationRequest
	//nolint:unused // read by tests asserting the lookup did not run
	calls int
}

func (s *stubAuthorizer) AuthorizeSender(_ context.Context, req auth.SenderAuthorizationRequest) (auth.SenderAuthorization, error) {
	s.calls++
	s.last = req
	return s.res, s.err
}

func authorizingRig(t *testing.T, mock *waMock, stub *stubAuthorizer, tune func(*WhatsAppConfig)) *Adapter {
	t.Helper()
	a := newAdapterRig(t, mock, "TestAgent")
	a.cfg.IdentityLookupKey = IdentityLookupPhone
	a.cfg.IdentityClaimKey = defaultPhoneClaimKey
	if tune != nil {
		tune(a.cfg)
	}
	a.core.SetIdentityAuthorizer(stub)
	return a
}

func TestMatchedSenderCarriesTheResolvedIdentity(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, nil)

	authorize := func(wamid string) senderAuthzOutcome {
		value, msg := bsuidTextMessage(wamid, "15551234567", "US.ALICE", "hello")
		env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
		return a.authorizeSender(a.ctx, env, "Alice")
	}

	t.Run("the request-path value and claim key reach the platform", func(t *testing.T) {
		stub.res = auth.SenderAuthorization{Resolution: identity.LookupMatched, Roles: []string{"r"}, Scopes: []string{"agent:*:invoke"}}
		out := authorize("wamid.A1")

		if out.Identity == nil {
			t.Fatalf("matched sender got no identity (resolution=%q)", out.Resolution)
		}
		if stub.last.ClaimKey != defaultPhoneClaimKey {
			t.Errorf("claim key = %q, want %q", stub.last.ClaimKey, defaultPhoneClaimKey)
		}
		if stub.last.ClaimValue != "15551234567" {
			t.Errorf("claim value = %q, want the request-path phone", stub.last.ClaimValue)
		}
		if stub.last.ContinuityToken != "US.ALICE" {
			t.Errorf("continuity token = %q, want the business-scoped id Meta supplied", stub.last.ContinuityToken)
		}
		if !out.Identity.ScopesResolved() {
			t.Error("ScopesResolved is false after a successful lookup; the lazy resolver would run again and fall back to defaults")
		}
	})

	t.Run("zero scopes still counts as resolved", func(t *testing.T) {
		stub.res = auth.SenderAuthorization{Resolution: identity.LookupMatched}
		out := authorize("wamid.A2")

		if out.Identity == nil || !out.Identity.ScopesResolved() {
			t.Fatal("an authorized sender holding zero scopes is not marked resolved")
		}
		if len(out.Identity.Scopes()) != 0 {
			t.Errorf("scopes = %v, want none", out.Identity.Scopes())
		}
	})
}

func TestAuthorizeSenderOutcomes(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, nil)

	for _, tc := range []struct {
		name          string
		res           auth.SenderAuthorization
		err           error
		wantSubmit    bool
		wantRetryable bool
	}{
		{"matched submits", auth.SenderAuthorization{Resolution: identity.LookupMatched}, nil, true, false},
		{"no_match refuses", auth.SenderAuthorization{Resolution: identity.LookupNoMatch}, nil, false, false},
		{"ambiguous refuses", auth.SenderAuthorization{Resolution: identity.LookupAmbiguous}, nil, false, false},
		{"refused refuses", auth.SenderAuthorization{Resolution: identity.LookupRefused}, nil, false, false},
		{"not_configured refuses", auth.SenderAuthorization{Resolution: identity.LookupNotConfigured}, nil, false, false},
		{"degraded holds rather than denying", auth.SenderAuthorization{Resolution: identity.LookupDegraded}, nil, false, true},
		{"transport failure holds", auth.SenderAuthorization{}, errors.New("broker timeout"), false, true},
		{"unknown keeps previous behaviour", auth.SenderAuthorization{Resolution: identity.LookupUnknown}, nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub.res, stub.err = tc.res, tc.err

			value, msg := textMessage("wamid.B1", "15551234567", "hello")
			env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
			out := a.authorizeSender(a.ctx, env, "Alice")

			if got := out.Identity != nil; got != tc.wantSubmit {
				t.Errorf("submits = %v, want %v (resolution=%q)", got, tc.wantSubmit, out.Resolution)
			}
			if out.Retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", out.Retryable, tc.wantRetryable)
			}
		})
	}
}

func TestRunAsSkipsTheSenderLookupEntirely(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) { c.RunAs = "ops" })

	value, msg := textMessage("wamid.C1", "15551234567", "hello")
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
	out := a.authorizeSender(a.ctx, env, "Alice")

	if stub.calls != 0 {
		t.Errorf("platform lookup ran %d times for a run_as entrypoint, want 0", stub.calls)
	}
	if out.Identity == nil || !strings.HasPrefix(out.Identity.UserID(), "system:") {
		t.Fatalf("run_as identity = %+v, want the system principal", out.Identity)
	}
}

func TestUnresolvedSenderGetsNoTaskAndIsSettled(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.D1", "15551234567", "What is the capital of France?")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack — a confirmed no_match is terminal, not retryable", got)
	}
	sends := mock.calls()
	if len(sends) != 1 {
		t.Fatalf("replies = %d (%v), want exactly the refusal notice", len(sends), sends)
	}
	if !strings.Contains(sends[0].Text.Body, "does not match a Solace Agent Mesh user") {
		t.Errorf("notice = %q, want the unresolved-sender copy", sends[0].Text.Body)
	}
}

func TestAmbiguousSenderIsNotOfferedASignIn(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupAmbiguous}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.E1", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	sends := mock.calls()
	if len(sends) != 1 {
		t.Fatalf("replies = %d, want exactly one notice", len(sends))
	}
	if strings.Contains(strings.ToLower(sends[0].Text.Body), "sign in") {
		t.Errorf("ambiguous sender was told to sign in: %q", sends[0].Text.Body)
	}
}

func TestDegradedLookupHoldsRatherThanRefusing(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{err: errors.New("broker timeout")}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.F1", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("settlement = %v, want a nack so the message is redelivered", got)
	}
	if sends := mock.calls(); len(sends) != 0 {
		t.Errorf("a held message told the sender something: %v", sends)
	}
}

func TestUnidentifiedSenderGetsATappableSignInLink(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.G1", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound Graph calls = %d, want exactly the sign-in prompt", len(raw))
	}
	body := string(raw[0])
	if !strings.Contains(body, "cta_url") {
		t.Errorf("prompt is not an interactive cta_url call: %s", body)
	}
	if !strings.Contains(body, `"url":"https://sam.example.com/?signInFrom=whatsapp"`) {
		t.Errorf("prompt does not link to the sign-in address with its WhatsApp source flag: %s", body)
	}
}

func TestEveryMessageBeforeSignInIsAnswered(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.H1", "15551234567", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	if n := len(mock.rawSends()); n != 1 {
		t.Fatalf("sends after the first message = %d, want the sign-in prompt", n)
	}

	second, secondMsg := textMessage("wamid.H2", "15551234567", "still there?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}

	raw := mock.rawSends()
	if len(raw) != 2 {
		t.Fatalf("sends after the follow-up = %d, want the prompt plus one reminder — silence reads as a broken service", len(raw))
	}
	assertSignInReminderButton(t, string(raw[1]))

	third, thirdMsg := textMessage("wamid.H3", "15551234567", "hello?")
	if got := a.deliver(third, thirdMsg); got != settleAck {
		t.Fatalf("third settlement = %v, want an ack", got)
	}
	raw = mock.rawSends()
	if len(raw) != 3 {
		t.Fatalf("sends after a third message = %d, want every message answered", len(raw))
	}
	assertSignInReminderButton(t, string(raw[2]))
}

func assertSignInReminderButton(t *testing.T, reminder string) {
	t.Helper()
	if !strings.Contains(reminder, "cta_url") || !strings.Contains(reminder, "https://sam.example.com") {
		t.Errorf("reminder = %s, want the sign-in button again", reminder)
	}
	for _, firstContact := range []string{"not recognized as a Solace Agent Mesh user", "does not match a Solace Agent Mesh user"} {
		if strings.Contains(reminder, firstContact) {
			t.Errorf("reminder = %s, want the follow-up wording, not a first-contact notice again", reminder)
		}
	}
	if !strings.Contains(reminder, "Tap Sign In") {
		t.Errorf("reminder = %s, want it to say they still need to sign in", reminder)
	}
	if !strings.Contains(reminder, "may not match the one the administrator registered") {
		t.Errorf("reminder = %s, want it to name the wrong-account case", reminder)
	}
}

func TestNoWebUIAddressMeansNoSignInOffer(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.I1", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	sends := mock.calls()
	if len(sends) != 1 {
		t.Fatalf("replies = %d, want one notice", len(sends))
	}
	if strings.Contains(strings.ToLower(sends[0].Text.Body), "sign in at") {
		t.Errorf("offered a sign-in with no address configured: %q", sends[0].Text.Body)
	}
}

func TestSystemRotationClassification(t *testing.T) {
	sentFrom := func() *inboundMessage {
		return &inboundMessage{From: "15551234567", FromUserID: "US.OLD"}
	}
	for _, tc := range []struct {
		name           string
		notification   *systemNotification
		wantKind       rotationKind
		wantRecognized bool
	}{
		{"nil is not a rotation", nil, rotationNone, true},
		{"number change discards", &systemNotification{Type: "user_changed_number", WaID: "15559998888"}, rotationValue, true},
		{"number change, newer spelling", &systemNotification{Type: "customer_changed_number", WaID: "15559998888"}, rotationValue, true},
		{"a business-scoped id change over a new number discards, as a number change does", &systemNotification{Type: "user_changed_user_id", WaID: "15559998888", UserID: "US.NEW"}, rotationValue, true},
		{"a business-scoped id change over the same number rebinds", &systemNotification{Type: "user_changed_user_id", WaID: "15551234567", UserID: "US.NEW"}, rotationIdentifier, true},
		{"a withheld new number discards", &systemNotification{Type: "user_changed_user_id", UserID: "US.NEW"}, rotationValue, true},
		{"an identity-hash change asks for nothing", &systemNotification{Type: "customer_identity_changed", Identity: "hash"}, rotationNone, true},
		{"identity-hash change, older spelling", &systemNotification{Type: "user_identity_changed", Identity: "hash"}, rotationNone, true},
		{"an unknown type is reported, not assumed benign", &systemNotification{Type: "something_new"}, rotationNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, recognized := systemRotation(tc.notification, sentFrom())
			if kind != tc.wantKind || recognized != tc.wantRecognized {
				t.Errorf("systemRotation = (%v, %v), want (%v, %v)", kind, recognized, tc.wantKind, tc.wantRecognized)
			}
		})
	}
}

func TestSystemNotificationNewSenderID(t *testing.T) {
	if got := (&systemNotification{WaID: "15559998888"}).newSenderID(); got != "15559998888" {
		t.Errorf("wa_id = %q, want the new id", got)
	}
	if got := (&systemNotification{NewWaID: "15559998888"}).newSenderID(); got != "15559998888" {
		t.Errorf("new_wa_id = %q, want the new id", got)
	}
	if got := (*systemNotification)(nil).newSenderID(); got != "" {
		t.Errorf("nil notification = %q, want empty", got)
	}
}

func TestRotationAsksForTheRightRemedy(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched, RotationApplied: true}}
	a := authorizingRig(t, mock, stub, nil)

	rotate := func(wamid string, n *systemNotification) {
		stub.calls = 0
		value, msg := systemMessage(wamid, "15551234567", n)
		msg.FromUserID = "US.OLD"
		value.Messages[0] = *msg
		env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
		a.handleSystemNotification(a.ctx, env)
	}

	t.Run("an identity rotation asks for a rebind", func(t *testing.T) {
		rotate("wamid.S1", &systemNotification{
			Type: "user_changed_user_id", WaID: "15551234567",
			UserID: "US.NEW", ParentUserID: "US.ENT.PORTFOLIO",
		})

		if stub.calls != 1 {
			t.Fatalf("platform calls = %d, want 1", stub.calls)
		}
		if stub.last.ClaimValue != "15551234567" {
			t.Errorf("claim value = %q, want the number the binding is keyed on", stub.last.ClaimValue)
		}
		if stub.last.PredecessorToken != "US.OLD" {
			t.Errorf("predecessor = %q, want the business-scoped id the customer used before", stub.last.PredecessorToken)
		}
		if stub.last.ContinuityToken != "US.NEW" {
			t.Errorf("continuity token = %q, want the new business-scoped id", stub.last.ContinuityToken)
		}
		if stub.last.DiscardBinding {
			t.Error("an identity rotation asked for a discard; the customer changed nothing and would be re-prompted")
		}
	})

	t.Run("a number change asks for a discard of the OLD value", func(t *testing.T) {
		rotate("wamid.S2", &systemNotification{Type: "user_changed_number", WaID: "15559998888"})

		if stub.calls != 1 {
			t.Fatalf("platform calls = %d, want 1", stub.calls)
		}
		if stub.last.ClaimValue != "15551234567" {
			t.Errorf("claim value = %q, want the number BEFORE the change — that is the value the binding is keyed on", stub.last.ClaimValue)
		}
		if !stub.last.DiscardBinding {
			t.Error("a number change did not ask for a discard; the stale binding would keep matching")
		}
	})

	t.Run("a rebind with no business-scoped id is not attempted", func(t *testing.T) {
		rotate("wamid.S3", &systemNotification{Type: "user_changed_user_id", WaID: "15551234567"})

		if stub.calls != 0 {
			t.Errorf("platform calls = %d, want 0 — there was no token of the right kind to swap to", stub.calls)
		}
	})

	t.Run("an unapplied rotation leaves the suppression record alone", func(t *testing.T) {
		seedPendingAuth(t, a, "15551234567")
		stub.res = auth.SenderAuthorization{Resolution: identity.LookupNoMatch}
		defer func() { stub.res = auth.SenderAuthorization{Resolution: identity.LookupMatched, RotationApplied: true} }()
		rotate("wamid.S4", &systemNotification{Type: "user_changed_number", WaID: "15559998888"})

		if stub.calls != 1 {
			t.Fatalf("platform calls = %d, want 1", stub.calls)
		}
		if !pendingAuthLive(t, a, "15551234567") {
			t.Error("the suppression record was retired on a rotation the platform did not apply")
		}
	})

	t.Run("an applied rotation retires the suppression record", func(t *testing.T) {
		seedPendingAuth(t, a, "15551234567")
		stub.res = auth.SenderAuthorization{Resolution: identity.LookupRefused, RotationApplied: true}
		defer func() { stub.res = auth.SenderAuthorization{Resolution: identity.LookupMatched, RotationApplied: true} }()
		rotate("wamid.S5", &systemNotification{Type: "user_changed_number", WaID: "15559998888"})

		if pendingAuthLive(t, a, "15551234567") {
			t.Error("the suppression record survived an applied rotation; the next sign-in prompt would be swallowed")
		}
	})
}

func seedPendingAuth(t *testing.T, a *Adapter, senderID string) {
	t.Helper()
	if a.pendingAuth == nil {
		t.Skip("this rig has no pending-auth store")
	}
	now := a.nowMs()
	if err := a.pendingAuth.Upsert(t.Context(), nil, &store.WhatsAppPendingAuth{
		GatewayID: a.gatewayID(),
		SenderID:  senderID,
		Kind:      store.PendingAuthKindLogin,
		LookupKey: a.cfg.IdentityLookupKey,
		ExpiresAt: now + 3_600_000,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed pending auth: %v", err)
	}
}

func pendingAuthLive(t *testing.T, a *Adapter, senderID string) bool {
	t.Helper()
	row, err := a.pendingAuth.Live(t.Context(), a.gatewayID(), senderID, a.nowMs())
	if err != nil {
		t.Fatalf("read pending auth: %v", err)
	}
	return row != nil
}

func TestSystemNotificationCreatesNoTask(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.S3", "15551234567", "")
	msg.Type = "system"
	msg.System = &systemNotification{Type: "user_changed_user_id", UserID: "US.77777"}

	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	if sends := mock.calls(); len(sends) != 0 {
		t.Errorf("a system notification produced outbound messages: %v", sends)
	}
}

func TestSweepReapsExpiredSignInRecords(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, nil)

	dead := &store.WhatsAppPendingAuth{
		GatewayID: a.gatewayID(), SenderID: "15551110000", Kind: store.PendingAuthKindLogin,
		ExpiresAt: a.nowMs() - 1, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := a.pendingAuth.Upsert(a.ctx, nil, dead); err != nil {
		t.Fatalf("seed an expired record: %v", err)
	}

	a.sweepExpiredPendingAuth()

	row, err := a.pendingAuth.Live(a.ctx, a.gatewayID(), "15551110000", 0)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if row != nil {
		t.Error("the expired sign-in record survived the sweep")
	}
}

func TestUsernameLookupMatchesTheHandle(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.IdentityLookupKey = IdentityLookupWAUsername
		c.IdentityClaimKey = "extension_deadbeef_waUsername"
	})

	value, msg := textMessage("wamid.U1", "15551234567", "hello")
	value.Contacts[0].Profile.Username = "realsheenanelson"
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}

	if out := a.authorizeSender(a.ctx, env, "Sheena"); out.Identity == nil {
		t.Fatalf("matched sender got no identity (resolution=%q)", out.Resolution)
	}
	if stub.last.ClaimValue != "realsheenanelson" {
		t.Errorf("claim value = %q, want the WhatsApp handle", stub.last.ClaimValue)
	}
	if stub.last.ClaimKey != "extension_deadbeef_waUsername" {
		t.Errorf("claim key = %q, want the configured claim", stub.last.ClaimKey)
	}
}

func TestUsernameLookupSenderWithoutAHandleIsNotOfferedThePhoneRemedy(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.IdentityLookupKey = IdentityLookupWAUsername
		c.IdentityClaimKey = "extension_deadbeef_waUsername"
		c.ContactRequestTemplate = "request_contact_info_1"
	})

	value, msg := textMessage("wamid.U3", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	if stub.calls != 0 {
		t.Errorf("platform lookups = %d, want 0: an absent handle is answerable without the round trip", stub.calls)
	}

	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound sends = %d, want exactly one notice", len(raw))
	}
	body := string(raw[0])
	for _, forbidden := range []string{"phone number", `"template"`, "cta_url"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("notice carries %q, offering a remedy this lookup cannot use: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "does not have a WhatsApp username") {
		t.Errorf("notice = %s, want it to name the missing handle", body)
	}
}

func TestPhoneWithheldSenderStillRoutesAndAuthorizes(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.IdentityLookupKey = IdentityLookupWAUsername
		c.IdentityClaimKey = "extension_deadbeef_waUsername"
	})

	msg := &inboundMessage{FromUserID: "US.13491208655302741918", ID: "wamid.U2", Type: "text"}
	msg.Text.Body = "hello"
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	value.Contacts[0].Profile.Name = "Sheena"
	value.Contacts[0].Profile.Username = "realsheenanelson"

	if got := msg.senderID(); got != "US.13491208655302741918" {
		t.Fatalf("senderID = %q, want the BSUID — an empty key would merge every phone-hidden sender", got)
	}
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
	out := a.authorizeSender(a.ctx, env, "Sheena")

	if out.Identity == nil {
		t.Fatalf("a phone-hidden sender was not authorized (resolution=%q)", out.Resolution)
	}
	if stub.last.ClaimValue != "realsheenanelson" {
		t.Errorf("claim value = %q, want the handle", stub.last.ClaimValue)
	}
	if stub.last.ContinuityToken != "US.13491208655302741918" {
		t.Errorf("continuity token = %q, want the BSUID", stub.last.ContinuityToken)
	}
}

func TestPhoneLookupYieldsNothingWhenTheNumberIsWithheld(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	msg := &inboundMessage{FromUserID: "US.111", ID: "wamid.U3", Type: "text"}
	msg.Text.Body = "hello"
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.111"}},
		Messages: []inboundMessage{*msg},
	}

	id := a.resolveIngressIdentity(value, msg)
	if id.LookupValue != "" {
		t.Errorf("lookup value = %q, want empty — there is no phone number to match", id.LookupValue)
	}
	if id.ContinuityToken != "US.111" {
		t.Errorf("continuity token = %q, want the BSUID", id.ContinuityToken)
	}
}

func captureLogs(t *testing.T, a *Adapter, fn func()) []map[string]any {
	t.Helper()
	h, ok := a.logger.Handler().(*tapHandler)
	if !ok {
		t.Fatalf("captureLogs needs a rig logger (%T has no tap) — build the adapter without a logger override", a.logger.Handler())
	}
	h.tap.arm()
	fn()
	captured := h.tap.disarm()

	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(captured), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func findLog(recs []map[string]any, substr string) map[string]any {
	for _, r := range recs {
		if msg, _ := r["msg"].(string); strings.Contains(msg, substr) {
			return r
		}
	}
	return nil
}

func TestRefusalLogNamesTheOutcomeAndTheAction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolution identity.LookupResolution
		wantInMsg  string
	}{
		{"no_match points at signing in", identity.LookupNoMatch, "asked to sign in"},
		{"ambiguous names the lockout", identity.LookupAmbiguous, "more than one SAM user"},
		{"refused points at the platform log", identity.LookupRefused, "platform declined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWAMock(t)
			stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: tc.resolution}}
			a := authorizingRig(t, mock, stub, nil)

			value, msg := textMessage("wamid.L1", "15551234567", "hello")
			recs := captureLogs(t, a, func() {
				a.deliver(value, msg)
			})

			rec := findLog(recs, "whatsapp inbound message refused")
			if rec == nil {
				t.Fatalf("no refusal log line; got %d lines", len(recs))
			}
			if got, _ := rec["msg"].(string); !strings.Contains(got, tc.wantInMsg) {
				t.Errorf("msg = %q, want it to contain %q", got, tc.wantInMsg)
			}
			if got, _ := rec["resolution"].(string); got != string(tc.resolution) {
				t.Errorf("resolution = %q, want %q", got, tc.resolution)
			}
			if got, _ := rec["identityClaimKey"].(string); got != defaultPhoneClaimKey {
				t.Errorf("identityClaimKey = %q, want %q", got, defaultPhoneClaimKey)
			}

			for _, r := range recs {
				if raw, err := json.Marshal(r); err == nil && strings.Contains(string(raw), "15551234567") {
					t.Errorf("a refusal log line carries the unauthenticated sender's number: %s", raw)
				}
			}
		})
	}
}

func TestSubmitLogCarriesTheResolvedUserID(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{
		Resolution:     identity.LookupMatched,
		ResolvedUserID: "01a08d54-8d82-70cf-9ec2-a0d14f242f7b",
		Scopes:         []string{"agent:*:invoke"},
	}}
	a := authorizingRig(t, mock, stub, nil)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	value, msg := textMessage("wamid.L2", "15551234567", "What is the capital of France?")
	recs := captureLogs(t, a, func() {
		a.deliver(value, msg)
	})

	rec := findLog(recs, "task submitted")
	if rec == nil {
		t.Fatalf("no task-submitted line; got %d lines", len(recs))
	}
	if got, _ := rec["resolvedUserId"].(string); got != "01a08d54-8d82-70cf-9ec2-a0d14f242f7b" {
		t.Errorf("resolvedUserId = %q, want the platform user id", got)
	}
	if got, _ := rec["user"].(string); !strings.HasPrefix(got, "whatsapp:***") {
		t.Errorf("user = %q, want it masked", got)
	}
}

func TestMatchedSenderSubmitsAsTheirWebUIPrincipal(t *testing.T) {
	const (
		senderPhone = "15551234567"
		email       = "alice@example.com"
	)
	for _, tt := range []struct {
		name       string
		principal  string
		want       string
		wantSource string
		wantWarn   bool
	}{
		{name: "the platform's principal replaces the sender's", principal: email, want: email, wantSource: principalSourceMatchedEmail},
		{name: "a platform that returns none keeps the sender's", want: "whatsapp:" + senderPhone, wantSource: principalSourceSender},
		{name: "a system principal is never adopted", principal: rbac.SystemUserPrincipal("default"), want: "whatsapp:" + senderPhone,
			wantSource: principalSourceSender, wantWarn: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newWAMock(t)
			stub := &stubAuthorizer{res: auth.SenderAuthorization{
				Resolution:        identity.LookupMatched,
				ResolvedUserID:    "01a08d54-8d82-70cf-9ec2-a0d14f242f7b",
				ResolvedPrincipal: tt.principal,
				Scopes:            []string{"agent:*:invoke"},
			}}
			a := authorizingRig(t, mock, stub, nil)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

			var published *broker.Message
			recs := captureLogs(t, a, func() {
				published = publishedMessage(t, a, "TestAgent", senderPhone)
			})
			if published == nil {
				t.Fatal("expected the task to reach the agent")
			}
			if got := published.UserProperties[a2a.PropUserID]; got != tt.want {
				t.Errorf("task principal = %q, want %q", got, tt.want)
			}
			if got := published.UserProperties[a2a.PropUserUUID]; got != "01a08d54-8d82-70cf-9ec2-a0d14f242f7b" {
				t.Errorf("task user uuid = %q, want the matched sender's users.id whichever principal it submits as", got)
			}
			meta := submittedMessageMetadata(t, published.Payload)
			for _, key := range []string{"requesting_user", "run_as"} {
				if _, present := meta[key]; present {
					t.Errorf("metadata carries %q for a claim-matched sender: %v", key, meta)
				}
			}
			for _, r := range recs {
				if raw, err := json.Marshal(r); err == nil && strings.Contains(string(raw), email) {
					t.Errorf("a log line carries the sender's email: %s", raw)
				}
			}
			assertInboundAuditIdentity(t, recs, tt.want, senderPhone)
			if rec := findLog(recs, "task submitted"); rec == nil || rec["principalSource"] != tt.wantSource {
				t.Errorf("task submitted principalSource = %v, want %q", rec["principalSource"], tt.wantSource)
			}
			if warned := findLog(recs, "reserved system principal") != nil; warned != tt.wantWarn {
				t.Errorf("system-principal WARN logged = %v, want %v", warned, tt.wantWarn)
			}
		})
	}
}

func TestMatchedSenderMediaAuditOmitsTheirEmail(t *testing.T) {
	const (
		senderPhone = "15551230001"
		email       = "alice@example.com"
	)
	a, _, _ := mediaAdapter(t,
		`{"url":"${SRV}/dl/MEDIA1","mime_type":"image/png","file_size":12,"id":"MEDIA1"}`,
		"/dl/MEDIA1", []byte("BYTES"))
	a.cfg.IdentityLookupKey = IdentityLookupPhone
	a.cfg.IdentityClaimKey = defaultPhoneClaimKey
	a.cfg.DefaultAgentName = "TestAgent"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
	a.core.SetIdentityAuthorizer(&stubAuthorizer{res: auth.SenderAuthorization{
		Resolution:        identity.LookupMatched,
		ResolvedPrincipal: email,
		Scopes:            []string{"agent:*:invoke"},
	}})

	msg := &inboundMessage{From: senderPhone, ID: "wamid.media", Type: "image",
		Image: &mediaObject{ID: "MEDIA1", MimeType: "image/png", Caption: "what is this?"}}
	value := &webhookChangeValue{Contacts: []webhookContact{{WaID: senderPhone}}, Messages: []inboundMessage{*msg}}
	var outcome inboundOutcome
	recs := captureLogs(t, a, func() {
		outcome = a.processInboundMessage(testEnvelope(a, value, msg), testSessionEpoch, true)
	})
	if outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", outcome)
	}

	rec := findAuditRecord(recs, "inbound")
	if rec == nil {
		t.Fatalf("no inbound audit record; got %d lines", len(recs))
	}
	if ref, _ := rec["artifactRef"].(string); ref == "" {
		t.Fatal("the audit record names no artifact, so this test is not exercising the line it guards")
	}
	for _, r := range recs {
		if raw, err := json.Marshal(r); err == nil && strings.Contains(string(raw), email) {
			t.Errorf("a log line carries the sender's email: %s", raw)
		}
	}
}

func TestHILAnswerAuditNamesTheMatchedUser(t *testing.T) {
	const (
		senderPhone = "15551230000"
		email       = "alice@example.com"
	)
	mock := newWAMock(t)
	a := authorizingRig(t, mock, &stubAuthorizer{}, nil)
	p := &pendingHIL{taskID: "task-1", agentName: "TestAgent",
		authorized: auth.NewTestSubject(auth.SubjectFields{UserID: email}), authorizedSource: principalSourceMatchedEmail}
	msg := &inboundMessage{From: senderPhone, ID: "wamid.tap", Type: "interactive"}

	recs := captureLogs(t, a, func() {
		a.auditHILAnswer(p, msg)
	})
	assertInboundAuditIdentity(t, recs, email, senderPhone)
}

func assertInboundAuditIdentity(t *testing.T, recs []map[string]any, wantPrincipal, senderPhone string) {
	t.Helper()
	rec := findAuditRecord(recs, "inbound")
	if rec == nil {
		t.Fatalf("no inbound audit record; got %d lines", len(recs))
	}
	if got, want := rec["userFP"], strutil.PIIFingerprint(wantPrincipal); got != want {
		t.Errorf("userFP = %v, want the fingerprint of %q (%s)", got, wantPrincipal, want)
	}
	sender := "whatsapp:" + senderPhone
	got, present := rec["senderFP"]
	switch {
	case wantPrincipal == sender && present:
		t.Errorf("senderFP = %v on a record whose principal is the sender", got)
	case wantPrincipal != sender && got != strutil.PIIFingerprint(sender):
		t.Errorf("senderFP = %v, want the sender's fingerprint %s", got, strutil.PIIFingerprint(sender))
	}
}

func findAuditRecord(recs []map[string]any, direction string) map[string]any {
	for _, r := range recs {
		if r["msg"] == auditMsg && r["direction"] == direction {
			return r
		}
	}
	return nil
}

func TestDegradedHoldLogNamesTheClaim(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{err: errors.New("broker timeout")}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.L3", "15551234567", "hello")
	recs := captureLogs(t, a, func() {
		a.deliver(value, msg)
	})

	rec := findLog(recs, "could not be decided")
	if rec == nil {
		t.Fatalf("no hold line; got %d lines", len(recs))
	}
	if got, _ := rec["identityClaimKey"].(string); got != defaultPhoneClaimKey {
		t.Errorf("identityClaimKey = %q, want %q", got, defaultPhoneClaimKey)
	}
	if got, _ := rec["level"].(string); got != "WARN" {
		t.Errorf("level = %q, want WARN", got)
	}
}

func TestWithheldNumberIsNotOfferedASignIn(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	msg := &inboundMessage{FromUserID: "US.13491208655302741918", ID: "wamid.W1", Type: "text"}
	msg.Text.Body = "hello"
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.13491208655302741918"}},
		Messages: []inboundMessage{*msg},
	}
	value.Contacts[0].Profile.Username = "realsheenanelson"

	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}

	if stub.calls != 0 {
		t.Errorf("platform lookups = %d, want 0 — an empty value is answerable without the round trip", stub.calls)
	}

	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound sends = %d, want exactly one notice", len(raw))
	}
	body := string(raw[0])
	if strings.Contains(body, "cta_url") || strings.Contains(strings.ToLower(body), "sign in") {
		t.Errorf("a sender with no number was offered a sign-in they cannot act on: %s", body)
	}
	if !strings.Contains(body, "does not share a phone number") {
		t.Errorf("notice = %s, want it to name what is actually missing", body)
	}
}

func TestSignInPromptBodyAgreesWithItsButton(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.W2", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("outbound sends = %d, want the sign-in prompt", len(raw))
	}
	body := string(raw[0])
	if !strings.Contains(body, "cta_url") {
		t.Fatalf("expected a tappable prompt: %s", body)
	}
	if strings.Contains(body, "Contact your administrator") {
		t.Errorf("the prompt body tells the reader to contact an administrator under a Sign in button: %s", body)
	}
	if strings.Count(body, "https://sam.example.com") != 1 {
		t.Errorf("the sign-in address appears %d times, want once (on the button): %s",
			strings.Count(body, "https://sam.example.com"), body)
	}
}

func TestRefusedSignInButtonFallsBackToTextWithTheAddress(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	mock.rejectInteractive = true
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	for i := range 2 {
		value, msg := textMessage("wamid.F"+strconv.Itoa(i), "15551234567", "hello")
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("message %d settled %v, want an ack", i+1, got)
		}
	}

	var fallbacks []string
	for _, raw := range mock.attempts() {
		var sent outboundTextMessage
		if err := json.Unmarshal(raw, &sent); err == nil && sent.Type == "text" {
			fallbacks = append(fallbacks, sent.Text.Body)
		}
	}
	if len(fallbacks) != 2 {
		t.Fatalf("text fallbacks = %d (%q), want one per refused button", len(fallbacks), fallbacks)
	}
	for _, body := range fallbacks {
		if !strings.Contains(body, "Sign in at https://sam.example.com") {
			t.Errorf("fallback = %q, want it to carry the sign-in address", body)
		}
	}
	if !strings.Contains(fallbacks[0], "not recognized as a Solace Agent Mesh user") {
		t.Errorf("first fallback = %q, want the first-contact wording", fallbacks[0])
	}
	if !strings.Contains(fallbacks[1], "may not match the one the administrator registered") {
		t.Errorf("follow-up fallback = %q, want the wrong-account wording", fallbacks[1])
	}
}

func TestDurableRecordMakesTheNextMessageAFollowUp(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.D1", "15551234567", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	if n := len(mock.rawSends()); n != 1 {
		t.Fatalf("sends after the first message = %d, want the sign-in card", n)
	}
	a.loginPrompts.forget("15551234567")

	second, secondMsg := textMessage("wamid.D2", "15551234567", "still there?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 2 {
		t.Fatalf("sends after the follow-up = %d, want two", len(raw))
	}
	assertSignInReminderButton(t, string(raw[1]))
}

func TestRecordForAnotherValueDoesNotMakeAFollowUp(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)
	seedPendingAuth(t, a, "15551234567")

	value, msg := textMessage("wamid.V1", "15551234567", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}
	raw := mock.rawSends()
	if len(raw) != 1 {
		t.Fatalf("sends = %d, want the sign-in card", len(raw))
	}
	if !strings.Contains(string(raw[0]), "not recognized as a Solace Agent Mesh user") {
		t.Errorf("card = %s, want the first-contact wording", raw[0])
	}
}

func TestUndeliveredSignInCardIsResentAsText(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.U1", "15551234567", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	mock.rawSends()
	record, err := a.pendingAuth.Live(t.Context(), a.gatewayID(), "15551234567", a.nowMs())
	if err != nil || record == nil || record.PromptWamid == "" {
		t.Fatalf("pending record = %+v, %v; want one carrying the card's wamid", record, err)
	}

	a.handleStatus(nil, &messageStatus{
		ID: record.PromptWamid, RecipientID: "15551234567", Status: "failed", Timestamp: "1720000000",
		Errors: []statusError{{Code: 131026}},
	})
	raw := mock.rawSends()
	if len(raw) != 2 {
		t.Fatalf("sends after the failed receipt = %d, want the card plus its text fallback", len(raw))
	}
	assertTextCarriesSignInAddress(t, string(raw[1]))

	second, secondMsg := textMessage("wamid.U2", "15551234567", "hello?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}
	attempts := mock.attempts()
	last := string(attempts[len(attempts)-1])
	if strings.Contains(last, `"type":"interactive"`) {
		t.Fatalf("follow-up attempted another card after one was undeliverable: %s", last)
	}
	assertTextCarriesSignInAddress(t, last)
	if !strings.Contains(last, "may not match the one the administrator registered") {
		t.Errorf("follow-up = %s, want the reminder wording", last)
	}
}

func TestRefusedSignInCardIsNotRetriedOnFollowUps(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	mock.rejectInteractive = true
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.R1", "15551234567", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	before := len(mock.attempts())

	second, secondMsg := textMessage("wamid.R2", "15551234567", "hello?")
	if got := a.deliver(second, secondMsg); got != settleAck {
		t.Fatalf("second settlement = %v, want an ack", got)
	}
	attempts := mock.attempts()
	if got := len(attempts) - before; got != 1 {
		t.Fatalf("attempts for the follow-up = %d, want one text send", got)
	}
	followUp := string(attempts[len(attempts)-1])
	assertTextCarriesSignInAddress(t, followUp)
	if !strings.Contains(followUp, "may not match the one the administrator registered") {
		t.Errorf("follow-up = %s, want the reminder wording", followUp)
	}
}

func TestNonHTTPSAddressSendsTheLinkAsText(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "http://localhost:8000")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	for i := range 2 {
		value, msg := textMessage("wamid.H"+strconv.Itoa(i), "15551234567", "hello")
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("message %d settled %v, want an ack", i+1, got)
		}
	}
	sends := mock.calls()
	if len(sends) != 2 {
		t.Fatalf("sends = %d, want one answer per message", len(sends))
	}
	for _, sent := range sends {
		if sent.Type != "text" || !strings.Contains(sent.Text.Body, "not recognized as a Solace Agent Mesh user") ||
			!strings.Contains(sent.Text.Body, "Sign in at http://localhost:8000/?signInFrom=whatsapp") {
			t.Errorf("send = type %q body %q, want first-contact text carrying the flagged address", sent.Type, sent.Text.Body)
		}
	}
}

func TestFailedSignInCardForAnotherReasonIsNotResent(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.G1", "15551234567", "hello")
	a.deliver(first, firstMsg)
	mock.rawSends()
	record, err := a.pendingAuth.Live(t.Context(), a.gatewayID(), "15551234567", a.nowMs())
	if err != nil || record == nil {
		t.Fatalf("pending record = %+v, %v", record, err)
	}
	a.handleStatus(nil, &messageStatus{
		ID: record.PromptWamid, RecipientID: "15551234567", Status: "failed", Timestamp: "1720000000",
		Errors: []statusError{{Code: 131000}},
	})
	if n := len(mock.rawSends()); n != 1 {
		t.Fatalf("sends after a generic failure = %d, want no resend", n)
	}

	second, secondMsg := textMessage("wamid.G2", "15551234567", "hello?")
	a.deliver(second, secondMsg)
	raw := mock.rawSends()
	assertSignInReminderButton(t, string(raw[len(raw)-1]))
}

func TestFailedFallbackTextIsNotResent(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	mock.rejectInteractive = true
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	first, firstMsg := textMessage("wamid.T1", "15551234567", "hello")
	a.deliver(first, firstMsg)
	before := len(mock.attempts())
	record, err := a.pendingAuth.Live(t.Context(), a.gatewayID(), "15551234567", a.nowMs())
	if err != nil || record == nil {
		t.Fatalf("pending record = %+v, %v", record, err)
	}
	a.handleStatus(nil, &messageStatus{
		ID: record.PromptWamid, RecipientID: "15551234567", Status: "failed", Timestamp: "1720000000",
		Errors: []statusError{{Code: 131026}},
	})
	if got := len(mock.attempts()) - before; got != 0 {
		t.Fatalf("attempts after the fallback text failed = %d, want none", got)
	}
}

func TestNeverAcceptedSignInCardIsNotResent(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	mock.rejectAll = true
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.N1", "15551234567", "hello")
	a.deliver(value, msg)
	texts := 0
	for _, raw := range mock.attempts() {
		if strings.Contains(string(raw), `"type":"text"`) {
			texts++
		}
	}
	if texts != 1 {
		t.Fatalf("text attempts = %d, want the one fallback", texts)
	}
}

func TestTextOnlySignInMemoryEnds(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}

	t.Run("cleared when the sender's situation changes", func(t *testing.T) {
		mock := newWAMock(t)
		a := authorizingRig(t, mock, stub, nil)
		a.loginButtonRefused.shouldWarn("15551234567", store.DefaultPendingAuthTTL)
		a.clearPendingLoginPrompt(t.Context(), "15551234567")

		value, msg := textMessage("wamid.M1", "15551234567", "hello")
		a.deliver(value, msg)
		if raw := mock.rawSends(); len(raw) != 1 || !strings.Contains(string(raw[0]), "cta_url") {
			t.Fatalf("sends = %q, want the sign-in card again", raw)
		}
	})

	t.Run("lapses after a day", func(t *testing.T) {
		mock := newWAMock(t)
		a := authorizingRig(t, mock, stub, nil)
		clock := time.Now()
		a.loginButtonRefused.now = func() time.Time { return clock }
		a.loginButtonRefused.shouldWarn("15551234567", store.DefaultPendingAuthTTL)
		clock = clock.Add(store.DefaultPendingAuthTTL + time.Minute)

		value, msg := textMessage("wamid.M2", "15551234567", "hello")
		a.deliver(value, msg)
		if raw := mock.rawSends(); len(raw) != 1 || !strings.Contains(string(raw[0]), "cta_url") {
			t.Fatalf("sends = %q, want the sign-in card again", raw)
		}
	})
}

func TestSignInWarningsAreWindowedByCondition(t *testing.T) {
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	warnings := func(logs *recordCollector, msg string) int {
		n := 0
		for _, r := range logs.all() {
			if r.Level == slog.LevelWarn && strings.HasPrefix(r.Message, msg) {
				n++
			}
		}
		return n
	}
	rig := func(t *testing.T) (*Adapter, *recordCollector) {
		logs := &recordCollector{}
		a := newAdapterRig(t, newWAMock(t), "TestAgent", slog.New(logs))
		a.cfg.IdentityLookupKey = IdentityLookupPhone
		a.cfg.IdentityClaimKey = defaultPhoneClaimKey
		a.core.SetIdentityAuthorizer(stub)
		return a, logs
	}

	t.Run("a web UI address that is not https", func(t *testing.T) {
		t.Setenv("WEBUI_FRONTEND_SERVER_URL", "http://localhost:8000")
		a, logs := rig(t)
		for i := range 3 {
			value, msg := textMessage("wamid.W"+strconv.Itoa(i), "1555123000"+strconv.Itoa(i), "hello")
			a.deliver(value, msg)
		}
		if n := warnings(logs, "sign-in button disabled"); n != 1 {
			t.Errorf("warnings = %d, want one for three senders", n)
		}
	})

	t.Run("an unreadable pending record", func(t *testing.T) {
		a, logs := rig(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for i := range 3 {
			value, msg := textMessage("wamid.P"+strconv.Itoa(i), "1555123000"+strconv.Itoa(i), "hello")
			env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
			if a.identityPromptSuppressed(ctx, env) {
				t.Fatal("an unreadable record must read as not yet asked")
			}
		}
		if n := warnings(logs, "could not read the pending sign-in record"); n != 1 {
			t.Errorf("warnings = %d, want one for three senders", n)
		}
	})
}

func assertTextCarriesSignInAddress(t *testing.T, raw string) {
	t.Helper()
	var sent outboundTextMessage
	if err := json.Unmarshal([]byte(raw), &sent); err != nil || sent.Type != "text" {
		t.Fatalf("send = %s, want a text message", raw)
	}
	if !strings.Contains(sent.Text.Body, "Sign in at https://sam.example.com/?signInFrom=whatsapp") {
		t.Errorf("text = %q, want it to carry the flagged sign-in address", sent.Text.Body)
	}
}

func TestSignInURLCarriesTheSourceFlag(t *testing.T) {
	a := authorizingRig(t, newWAMock(t), &stubAuthorizer{}, nil)
	for _, tc := range []struct{ base, want string }{
		{"https://sam.example.com", "https://sam.example.com/?signInFrom=whatsapp"},
		{"https://sam.example.com/sam", "https://sam.example.com/sam?signInFrom=whatsapp"},
		{"https://sam.example.com/?tenant=acme", "https://sam.example.com/?tenant=acme&signInFrom=whatsapp"},
		{"", ""},
	} {
		t.Run(tc.base, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.base)
			if got := a.signInURL(); got != tc.want {
				t.Errorf("signInURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSigningInBetweenMessagesEndsTheReminderLoop(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	first, firstMsg := textMessage("wamid.J1", "15551234567", "hello")
	if got := a.deliver(first, firstMsg); got != settleAck {
		t.Fatalf("first settlement = %v, want an ack", got)
	}
	if n := len(mock.rawSends()); n != 1 {
		t.Fatalf("sends after the refusal = %d, want the sign-in prompt", n)
	}

	stub.res = auth.SenderAuthorization{
		Resolution:     identity.LookupMatched,
		ResolvedUserID: "01a08d54-8d82-70cf-9ec2-a0d14f242f7b",
		Scopes:         []string{"agent:*:invoke"},
	}

	second, secondMsg := textMessage("wamid.J2", "15551234567", "What is the capital of France?")
	recs := captureLogs(t, a, func() {
		a.deliver(second, secondMsg)
	})

	if findLog(recs, "task submitted") == nil {
		t.Fatal("signing in did not get the next message through; the sender would be stuck reminding forever")
	}
	if rec := findLog(recs, "refused"); rec != nil {
		t.Errorf("a signed-in sender was still refused: %v", rec["msg"])
	}
}

func TestHILAnswerCarriesTheClaimMatchedIdentity(t *testing.T) {
	const phone = "15551230000"
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{
		Resolution: identity.LookupMatched,
		Roles:      []string{"support"},
		Scopes:     []string{"agent:TestAgent:invoke"},
	}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := bsuidTextMessage("wamid.answer", phone, "US."+phone, "yes")
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}
	env.authorized = a.authorizeSender(a.ctx, env, "Alice").Identity
	if env.authorized == nil {
		t.Fatal("the rig's own authorize step resolved nobody")
	}
	p := &pendingHIL{authorized: env.authorized}

	got, swapped := auth.ForWhatsAppSender(p.authorized, a.cfg.RunAs)
	if swapped {
		t.Fatal("a non-run_as entrypoint swapped the identity")
	}
	if !got.ScopesResolved() {
		t.Error("the answer leg's identity is not marked resolved, so the lazy resolver runs again and applies the defaults")
	}
	if len(got.Scopes()) != 1 || got.Scopes()[0] != "agent:TestAgent:invoke" {
		t.Errorf("scopes = %v, want the ones the claim match resolved", got.Scopes())
	}
	if len(got.Roles()) != 1 || got.Roles()[0] != "support" {
		t.Errorf("roles = %v, want the ones the claim match resolved", got.Roles())
	}
}

func TestARetiredPromptUsesTheEntrypointPrincipal(t *testing.T) {
	mock := newWAMock(t)
	a := authorizingRig(t, mock, &stubAuthorizer{}, nil)

	got := a.retireIdentity()
	if !rbac.IsSystemPrincipal(got.UserID()) {
		t.Errorf("retire identity = %q, want a system principal", got.UserID())
	}

	a.cfg.RunAs = "ops"
	if got := a.retireIdentity(); got.UserID() != rbac.SystemUserPrincipal("ops") {
		t.Errorf("retire identity under run_as = %q, want %q", got.UserID(), rbac.SystemUserPrincipal("ops"))
	}
}

func TestSpooledPreUpgradeEnvelopeRecoversItsIdentity(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.spooled", "15551234567", "hello")
	env := &ingressEnvelope{Context: *value, Message: *msg}
	env.Context.Messages = nil

	out := a.authorizeSender(a.ctx, env, "Alice")

	if out.NoLookupValue {
		t.Fatal("a spooled message was read as the provider withholding the number; it earns a terminal refusal and a billable contact-share template")
	}
	if stub.calls != 1 {
		t.Fatalf("platform calls = %d, want 1 — the value was recoverable from contacts[]", stub.calls)
	}
	if stub.last.ClaimValue != "15551234567" {
		t.Errorf("claim value = %q, want the value recomputed from the surviving contacts entry", stub.last.ClaimValue)
	}
	if env.Identity.LookupValueExact {
		t.Error("a recomputed pairing was reported as exact; the single-contact fallback cannot be re-derived downstream")
	}
}

func TestAGenuinelyWithheldNumberStillOffersContactShare(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{}
	a := authorizingRig(t, mock, stub, nil)

	msg := &inboundMessage{ID: "wamid.withheld", Type: "text", FromUserID: "US.NONUMBER"}
	msg.Text.Body = "hello"
	value := &webhookChangeValue{
		Contacts: []webhookContact{{UserID: "US.NONUMBER"}},
		Messages: []inboundMessage{*msg},
	}
	env := &ingressEnvelope{Context: *value, Message: *msg, Identity: a.resolveIngressIdentity(value, msg)}

	out := a.authorizeSender(a.ctx, env, "")
	if !out.NoLookupValue {
		t.Errorf("a sender with no disclosed number was not routed to the contact-share offer (resolution=%q)", out.Resolution)
	}
	if stub.calls != 0 {
		t.Errorf("platform calls = %d, want 0 — there is nothing to look up", stub.calls)
	}
}

func TestDegradedLookupOutlivesTheRefusalBudgetAndApologizes(t *testing.T) {
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupDegraded}}
	a := authorizingRig(t, mock, stub, nil)

	value, msg := textMessage("wamid.degraded", "15551230000", "hello")
	for i := range maxAuthzDenialAttempts + 1 {
		if got := a.deliver(value, msg); got != settleNack {
			t.Fatalf("delivery %d of a degraded lookup settled %v, want settleNack — "+
				"an outage must not spend the refusal budget", i+1, got)
		}
	}
	if sentText(mock.calls(), msgNotAuthorized) {
		t.Fatal("a sender was told they are not authorized because the platform could not answer; nobody asked")
	}

	budget, decline, kind := inboundRetryPolicy(outcomeAuthzUnavailable)
	if budget != maxTransientAttempts {
		t.Errorf("budget = %d, want the infrastructure budget %d", budget, maxTransientAttempts)
	}
	if decline != msgTemporaryFailure {
		t.Errorf("spent-budget copy = %q, want the apology", decline)
	}
	if kind == "unauthorized" {
		t.Error("the failure kind is shared with a real refusal, so one streak spends the other's budget")
	}
}

func TestRapidSendersGetOneFirstContactCard(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, nil)

	for i := range 3 {
		value, msg := textMessage("wamid.rapid"+strconv.Itoa(i), "15551230000", "hello")
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("message %d settled %v, want an ack", i+1, got)
		}
	}

	var firstContact, reminders int
	for _, raw := range mock.rawSends() {
		body := string(raw)
		if !strings.Contains(body, `"type":"interactive"`) {
			t.Errorf("send = %s, want every answer to carry the sign-in button", body)
			continue
		}
		switch {
		case strings.Contains(body, "not recognized as a Solace Agent Mesh user"):
			firstContact++
		case strings.Contains(body, "Tap Sign In"):
			reminders++
		}
	}
	if firstContact != 1 {
		t.Errorf("first-contact cards = %d, want 1 — three messages in the pre-commit window queued one each", firstContact)
	}
	if reminders != 2 {
		t.Errorf("reminder cards = %d, want 2 — a follow-up answered with silence reads as a broken service", reminders)
	}
}

func TestUnrecognizedResolutionHoldsRatherThanAdmits(t *testing.T) {
	for _, tt := range []struct {
		name       string
		resolution identity.LookupResolution
		wantHeld   bool
	}{
		{"absent stays permissive", identity.LookupUnknown, false},
		{"a value from a newer platform holds", identity.LookupResolution("quarantined"), true},
		{"a typo holds", identity.LookupResolution("mached"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newWAMock(t)
			stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: tt.resolution}}
			a := authorizingRig(t, mock, stub, nil)

			value, msg := textMessage("wamid.skew", "15551230000", "hello")
			got := a.deliver(value, msg)
			if tt.wantHeld && got != settleNack {
				t.Fatalf("settlement = %v, want settleNack — an answer this build cannot read must not admit the sender", got)
			}
			if !tt.wantHeld && got == settleNack {
				t.Fatalf("settlement = %v, want the message submitted — an absent resolution is today's behaviour, not a denial", got)
			}
		})
	}
}

func TestRecognizedCoversExactlyTheShippedVocabulary(t *testing.T) {
	for _, known := range []identity.LookupResolution{
		identity.LookupUnknown, identity.LookupMatched, identity.LookupNoMatch,
		identity.LookupAmbiguous, identity.LookupRefused, identity.LookupNotConfigured,
		identity.LookupDegraded,
	} {
		if !known.Recognized() {
			t.Errorf("%q is a shipped value and must be recognized", known)
		}
	}
	for _, unknown := range []identity.LookupResolution{"quarantined", "MATCHED", "matched ", "no-match"} {
		if unknown.Recognized() {
			t.Errorf("%q is not a shipped value; recognizing it would admit on a resolution nothing can interpret", unknown)
		}
	}
}
