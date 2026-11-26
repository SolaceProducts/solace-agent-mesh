// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type recordingSigner struct {
	mu     sync.Mutex
	signed []*gwauth.Subject
}

func (s *recordingSigner) SignTask(identity *gwauth.Subject, taskID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signed = append(s.signed, identity)
	return "signed-" + taskID, nil
}

func (s *recordingSigner) calls() []*gwauth.Subject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*gwauth.Subject(nil), s.signed...)
}

func TestCallTool_CallerAndAttributionReachTheSubmit(t *testing.T) {
	cases := map[string]struct {
		staged          *gwauth.Subject
		wantAttributed  string
		wantUnsignedRun bool
	}{
		"identity in the context": {
			staged:         gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "alice@example.com", Authenticated: true, AuthMethod: "sam_access_token"}),
			wantAttributed: "alice@example.com",
		},
		"identity with no user id": {
			staged:         gwauth.NewTestSubject(gwauth.SubjectFields{Authenticated: true, AuthMethod: "sam_access_token"}),
			wantAttributed: mcpNoIdentityUserID,
		},
		"nothing in the context": {
			wantAttributed:  mcpNoIdentityUserID,
			wantUnsignedRun: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a, brk := newAuthzTestAdapter(t, mustParseConfig(t, nil), nil)
			a.toolMap = map[string]toolMapping{"target_greet": {agentName: "target-agent"}}
			a.agentTools = map[string][]string{}
			a.tasks = map[string]*taskState{}
			signer := &recordingSigner{}
			a.core.SetTaskSigner(signer)

			q, err := brk.CreateQueue("tool-call-caller", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.staged != nil {
				ctx = gwauth.ContextWithSubject(ctx, tc.staged)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _, _ = a.callTool(ctx, "target_greet", toolCall("target_greet", "hello"))
			}()

			recvCtx, recvCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer recvCancel()
			received, err := brk.Receive(recvCtx, q)
			cancel()
			<-done
			if err != nil || received == nil {
				t.Fatalf("no task published: %v", err)
			}

			props := a2a.DecodeUserProperties(received.UserProperties)
			if props.UserID != tc.wantAttributed {
				t.Errorf("wire UserID = %q, want %q", props.UserID, tc.wantAttributed)
			}
			signed := signer.calls()
			if tc.wantUnsignedRun {
				if len(signed) != 0 {
					t.Errorf("signed %d times, want none: nothing vouched for this caller", len(signed))
				}
				return
			}
			if len(signed) != 1 || !reflect.DeepEqual(*signed[0], *tc.staged) {
				t.Errorf("signed as %+v, want the identity from the context itself", signed)
			}
		})
	}
}

func TestCallTool_NoIdentityIsRefusedWhenEnforced(t *testing.T) {
	a, brk := newAuthzTestAdapter(t, mustParseConfig(t, nil), nil)
	a.toolMap = map[string]toolMapping{"target_greet": {agentName: "target-agent"}}
	a.agentTools = map[string][]string{}
	a.tasks = map[string]*taskState{}
	signer := &recordingSigner{}
	a.core.SetTaskSigner(signer)
	a.core.SetScopeResolver(authzStubResolver{scopes: []string{"agent:*:invoke"}})
	if !a.core.AuthzEnforced() {
		t.Fatal("fixture error: authz must be enforced")
	}

	q, err := brk.CreateQueue("tool-call-refused", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_, reason, callErr := a.callTool(t.Context(), "target_greet", toolCall("target_greet", "hello"))
	if callErr == nil || reason == "" {
		t.Fatalf("callTool = (reason %q, err %v), want a refusal", reason, callErr)
	}
	if n := len(signer.calls()); n != 0 {
		t.Errorf("signed %d times, want none", n)
	}
	recvCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(recvCtx, q); msg != nil {
		t.Error("a task was published for a refused call")
	}
}
