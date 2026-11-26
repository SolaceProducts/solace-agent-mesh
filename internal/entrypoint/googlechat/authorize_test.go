// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type stubAuthorizer struct {
	res   auth.SenderAuthorization
	err   error
	calls int
	req   auth.SenderAuthorizationRequest
}

func (s *stubAuthorizer) AuthorizeSender(_ context.Context, req auth.SenderAuthorizationRequest) (auth.SenderAuthorization, error) {
	s.calls++
	s.req = req
	return s.res, s.err
}

func adapterWithAuthorizer(t *testing.T, authorizer auth.SenderAuthorizer) *Adapter {
	t.Helper()
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.core = entrypoint.NewGatewayCore(a, nil)
	if authorizer != nil {
		a.core.SetIdentityAuthorizer(authorizer)
	}
	return a
}

func senderEvent(email string) *inboundEvent {
	return &inboundEvent{Kind: kindMessage, SenderEmail: email, SenderName: "users/42", SenderDisplayName: "Ada"}
}

func TestAuthorizeSender_ResolutionTable(t *testing.T) {
	for name, tc := range map[string]struct {
		res           auth.SenderAuthorization
		err           error
		wantAdmit     bool
		wantRetryable bool
		wantRefusal   entrypoint.SenderUnresolvedReason
	}{
		"matched":        {res: auth.SenderAuthorization{Resolution: identity.LookupMatched, Scopes: []string{"agent:*:invoke"}}, wantAdmit: true},
		"degraded":       {res: auth.SenderAuthorization{Resolution: identity.LookupDegraded}, wantRetryable: true},
		"lookup error":   {err: errors.New("broker down"), wantRetryable: true},
		"unrecognized":   {res: auth.SenderAuthorization{Resolution: "something_new"}, wantRetryable: true},
		"no match":       {res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}, wantRefusal: entrypoint.SenderNotLinked},
		"ambiguous":      {res: auth.SenderAuthorization{Resolution: identity.LookupAmbiguous}, wantRefusal: entrypoint.SenderNotUsable},
		"refused":        {res: auth.SenderAuthorization{Resolution: identity.LookupRefused}, wantRefusal: entrypoint.SenderNotUsable},
		"not configured": {res: auth.SenderAuthorization{Resolution: identity.LookupNotConfigured}, wantRefusal: entrypoint.SenderNotUsable},
		"empty (fails closed)": {
			res: auth.SenderAuthorization{Resolution: identity.LookupUnknown}, wantRefusal: entrypoint.SenderNotUsable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			stub := &stubAuthorizer{res: tc.res, err: tc.err}
			a := adapterWithAuthorizer(t, stub)

			verdict := a.authorizeSender(t.Context(), senderEvent("ada@example.com"))

			if stub.req.ClaimKey != emailClaimKey || stub.req.ClaimValue != "ada@example.com" {
				t.Errorf("lookup request = %+v, want the email claim matched against the sender email", stub.req)
			}
			if got := verdict.Identity != nil; got != tc.wantAdmit {
				t.Fatalf("admitted = %v, want %v (verdict %+v)", got, tc.wantAdmit, verdict)
			}
			if verdict.Retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", verdict.Retryable, tc.wantRetryable)
			}
			if !tc.wantAdmit && !tc.wantRetryable && verdict.Refusal != tc.wantRefusal {
				t.Errorf("refusal = %v, want %v", verdict.Refusal, tc.wantRefusal)
			}
			if tc.wantAdmit {
				id := verdict.Identity
				if id.UserID() != "ada@example.com" || !id.ScopesResolved() || len(id.Scopes()) != 1 || id.AuthMethod() != auth.AuthMethodGoogleChat {
					t.Errorf("identity = %+v, want the email principal with the resolved scopes", id)
				}
			}
		})
	}
}

func TestAuthorizeSender_FailsClosedWithoutAuthorizer(t *testing.T) {
	a := adapterWithAuthorizer(t, nil)

	verdict := a.authorizeSender(t.Context(), senderEvent("ada@example.com"))

	if verdict.Identity != nil || verdict.Retryable || verdict.Cause != causeNoAuthorizer {
		t.Fatalf("verdict = %+v, want a refusal because no authorizer is wired", verdict)
	}
}

func TestAuthorizeSender_MissingEmailRefusedWithoutLookup(t *testing.T) {
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := adapterWithAuthorizer(t, stub)

	verdict := a.authorizeSender(t.Context(), senderEvent(""))

	if verdict.Identity != nil || verdict.Cause != causeNoEmail || stub.calls != 0 {
		t.Fatalf("verdict = %+v, lookups = %d; want refusal with no lookup and never a synthetic identity", verdict, stub.calls)
	}
}

func TestAuthorizeSender_DomainAllowlistCheckedBeforeLookup(t *testing.T) {
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}}
	a := adapterWithAuthorizer(t, stub)
	a.cfg.AllowedEmailDomains = []string{"example.com"}

	if verdict := a.authorizeSender(t.Context(), senderEvent("mallory@evil.test")); verdict.Identity != nil || verdict.Cause != causeDomainNotAllowed {
		t.Fatalf("outside-domain verdict = %+v, want refused", verdict)
	}
	if stub.calls != 0 {
		t.Errorf("lookups = %d, want the allowlist to refuse before any lookup", stub.calls)
	}
	if verdict := a.authorizeSender(t.Context(), senderEvent("ada@EXAMPLE.com")); verdict.Identity == nil {
		t.Errorf("in-domain verdict = %+v, want admitted (domains compare case-insensitively)", verdict)
	}
}

func TestAuthorizeSender_AMatchedSenderCarriesTheirPlatformUserRow(t *testing.T) {
	const adaRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	stub := &stubAuthorizer{res: auth.SenderAuthorization{
		Resolution: identity.LookupMatched, ResolvedUserID: adaRow, Scopes: []string{"agent:*:invoke"},
	}}
	a := adapterWithAuthorizer(t, stub)

	verdict := a.authorizeSender(t.Context(), senderEvent("ada@example.com"))

	if verdict.Identity == nil || verdict.Identity.PlatformUserID() != adaRow {
		t.Errorf("identity = %+v, want the matched sender's users.id %s so their Google Chat spend books with their Web UI spend", verdict.Identity, adaRow)
	}
}

func TestAMatchedSendersMessageIsStampedWithTheirPlatformUserRow(t *testing.T) {
	const adaRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	brk, requests := attachCore(t, a)
	a.cfg.DefaultAgentName = "agent"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent"})
	authorizer := matchedAuthorizer()
	authorizer.res.ResolvedUserID = adaRow
	a.core.SetIdentityAuthorizer(authorizer)
	ev := dmJob(1, "spaces/DM1/messages/row", "hello").ev

	if res := a.process(t.Context(), &ev); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, requests)
	if err != nil {
		t.Fatalf("no task reached the agent: %v", err)
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserUUID; got != adaRow {
		t.Errorf("published userUuid = %q, want the matched sender's users.id %s", got, adaRow)
	}
}
