// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type countingScopeResolver struct {
	scopes []string
	calls  int
}

func (r *countingScopeResolver) ResolveScopes(string, []string) []string {
	r.calls++
	return r.scopes
}

func initedGateway(t *testing.T) *GatewayCore {
	t.Helper()
	core, _, _ := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return core
}

func TestDelegationGate_CallerWithoutIdentityTakesTheMissingIdentityBranch(t *testing.T) {
	t.Run("enforced: refused as no_identity, resolver never asked", func(t *testing.T) {
		core := initedGateway(t)
		resolver := &countingScopeResolver{scopes: []string{"agent:*:invoke"}}
		core.SetScopeResolver(resolver)
		if !core.AuthzEnforced() {
			t.Fatal("fixture error: authz must be enforced")
		}

		reason := core.delegationDenialReason(t.Context(), auth.ForUserIDOnly("alice@corp.com"), "target-agent")

		if reason != denyReasonNoIdentity {
			t.Errorf("reason = %q, want %q", reason, denyReasonNoIdentity)
		}
		if resolver.calls != 0 {
			t.Errorf("resolver called %d times, want 0", resolver.calls)
		}
	})

	t.Run("not enforced: allowed", func(t *testing.T) {
		core := initedGateway(t)
		if core.AuthzEnforced() {
			t.Fatal("fixture error: authz must not be enforced")
		}
		if reason := core.delegationDenialReason(t.Context(), auth.ForUserIDOnly("alice@corp.com"), "target-agent"); reason != "" {
			t.Errorf("reason = %q, want allowed", reason)
		}
	})
}

func TestSubmitTask_SignsTheFullIdentityNotTheAuthenticatedOne(t *testing.T) {
	msg := a2a.Message{Kind: "message", MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}

	t.Run("full identity, Authenticated false: signed", func(t *testing.T) {
		core := initedGateway(t)
		core.SetScopeResolver(&countingScopeResolver{scopes: []string{"agent:target-agent:invoke"}})
		signer := &scopeCapturingSigner{}
		core.SetTaskSigner(signer)

		caller := auth.ForSlackUser("alice@corp.com", "alice@corp.com", "U1", "T1")
		if caller.Authenticated() {
			t.Fatal("fixture error: ForSlackUser must leave Authenticated false for this case")
		}
		if err := core.SubmitTask(t.Context(), "target-agent", "task-full-identity", msg, caller); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		if signer.calls != 1 {
			t.Errorf("signer calls = %d, want 1", signer.calls)
		}
	})

	t.Run("no full identity: not signed", func(t *testing.T) {
		core := initedGateway(t)
		signer := &scopeCapturingSigner{}
		core.SetTaskSigner(signer)

		if err := core.SubmitTask(t.Context(), "target-agent", "task-without-identity", msg, auth.ForUserIDOnly("owner")); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		if signer.calls != 0 {
			t.Errorf("signer calls = %d, want 0", signer.calls)
		}
	})
}

func TestSubmitUserInputResponse_ReplyWithoutIdentityIsRefusedWhenEnforced(t *testing.T) {
	core := initedGateway(t)
	resolver := &countingScopeResolver{scopes: []string{"agent:*:invoke"}}
	core.SetScopeResolver(resolver)
	if !core.AuthzEnforced() {
		t.Fatal("fixture error: authz must be enforced")
	}

	err := core.SubmitUserInputResponse(t.Context(), "target-agent", "task-1",
		a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r1", Status: "approved"},
		auth.ForUserIDOnly("alice@corp.com"))

	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("err = %v, want ErrAgentDelegationDenied", err)
	}
	if resolver.calls != 0 {
		t.Errorf("resolver called %d times, want 0", resolver.calls)
	}
}

func TestDelegationGate_DoesNotReResolveAResolvedEmptyCaller(t *testing.T) {
	core := initedGateway(t)
	resolver := &countingScopeResolver{scopes: []string{"agent:target-agent:invoke"}}
	core.SetScopeResolver(resolver)
	caller := auth.ForAuthorizedWhatsAppSender(auth.ForWhatsAppSenderIdentity("15550005555", "Alice"), "whatsapp:15550005555", auth.SenderAuthorization{})

	core.delegationDenialReason(context.Background(), caller, "target-agent")

	if resolver.calls != 0 {
		t.Errorf("resolver called %d times for a caller resolved to no scopes, want 0", resolver.calls)
	}
}

func TestSubmit_NilOrZeroCallerIsRefused(t *testing.T) {
	core := initedGateway(t)
	msg := a2a.Message{Kind: "message", MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r1", Status: "approved"}

	for callerName, caller := range map[string]*auth.Subject{"nil": nil, "zero": {}, "empty user id": auth.ForUserIDOnly("")} {
		for name, submit := range map[string]func() error{
			"SubmitTask": func() error {
				return core.SubmitTask(t.Context(), "target-agent", "t1", msg, caller)
			},
			"SubmitTaskAttributedTo": func() error {
				return core.SubmitTaskAttributedTo(t.Context(), "target-agent", "t2", msg, caller, "alice")
			},
			"SubmitUserInputResponse": func() error {
				return core.SubmitUserInputResponse(t.Context(), "target-agent", "t3", resp, caller)
			},
			"SubmitUserInputResponseAttributedTo": func() error {
				return core.SubmitUserInputResponseAttributedTo(t.Context(), "target-agent", "t4", resp, caller, "alice")
			},
		} {
			t.Run(callerName+"/"+name, func(t *testing.T) {
				if err := submit(); !errors.Is(err, ErrNoCaller) {
					t.Fatalf("err = %v, want ErrNoCaller", err)
				}
			})
		}
	}
}

func TestSubmitUserInputResponse_SignsACallerWithoutAFullIdentity(t *testing.T) {
	core := initedGateway(t)
	if core.AuthzEnforced() {
		t.Fatal("fixture error: authz must not be enforced")
	}
	signer := &scopeCapturingSigner{}
	core.SetTaskSigner(signer)

	err := core.SubmitUserInputResponse(t.Context(), "target-agent", "task-1",
		a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r1", Status: "approved"},
		auth.ForUserIDOnly("alice@corp.com"))
	if err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}
	if signer.calls != 1 || signer.signedUserID != "alice@corp.com" {
		t.Errorf("signer calls = %d as %q, want 1 as alice@corp.com", signer.calls, signer.signedUserID)
	}
}

func TestSubmitUserInputResponseAttributedTo_RecordsTheAttributedUser(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	q, err := brk.CreateQueue("hil-attribution", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatal(err)
	}
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "r1", Status: "answered"}
	caller := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true})

	for _, tc := range []struct{ attributed, want string }{{"sam_dev_user", "sam_dev_user"}, {"", "alice"}} {
		if err := core.SubmitUserInputResponseAttributedTo(t.Context(), "target-agent", "task-1", resp, caller, tc.attributed); err != nil {
			t.Fatalf("submit: %v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("no broker message: %v", err)
		}
		if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != tc.want {
			t.Errorf("attributed %q: published user id = %q, want %q", tc.attributed, got, tc.want)
		}
	}
	if id := signer.firstIdentity(); id == nil || !reflect.DeepEqual(*id, *caller) {
		t.Errorf("signed as %+v, want the caller", id)
	}
}
