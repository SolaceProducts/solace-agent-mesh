// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type agentAccessLog struct {
	auth.NoopAuditLogger
	mu     sync.Mutex
	events []auth.AgentAccessEvent
}

func (a *agentAccessLog) LogAgentAccess(_ context.Context, e auth.AgentAccessEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

func (a *agentAccessLog) decisions() []auth.AgentAccessEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]auth.AgentAccessEvent(nil), a.events...)
}

type scopedAuth struct {
	fakeAuth
	scopes []string
}

func (s scopedAuth) Authenticate(ctx context.Context, tok, taskID string) (*auth.Identity, error) {
	identity, err := s.fakeAuth.Authenticate(ctx, tok, taskID)
	if identity != nil {
		identity.Scopes = s.scopes
	}
	return identity, err
}

func TestAPeerCallWritesOneAccessDecisionToTheAuditLog(t *testing.T) {
	for name, tc := range map[string]struct {
		scopes  []string
		allowed bool
	}{
		"allowed": {[]string{"agent:*:invoke"}, true},
		"denied":  {[]string{"agent:claims:use"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			audit := &agentAccessLog{}
			r := newRigAuth(t, []any{claimsAgent(nil)}, scopedAuth{scopes: tc.scopes}, nil,
				runtime.WithAuditLogger(audit), runtime.WithAgentAuthorizer(rbac.NewAgentAuth(audit, slog.Default())))
			c := r.connect()
			r.consume(c)
			r.taskOn(c, "T1", nil)
			requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))

			c.send(peerCall("S1", peerName, "Is policy P-9 active?"))
			if tc.allowed {
				recv(t, requests)
			} else {
				c.refused(errCodeForbidden, "S1")
			}

			decisions := audit.decisions()
			if len(decisions) != 1 || decisions[0].AgentName != peerName || decisions[0].Allowed != tc.allowed || decisions[0].UserID != "alice" {
				t.Fatalf("audit log %+v", decisions)
			}
		})
	}
}

func TestAPeerCallFromATaskAtTheMaximumCallDepthIsRefusedAndOneBelowItGoesOutDeeper(t *testing.T) {
	r, c := connectedRig(t, nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))

	r.taskOn(c, "T-below", map[string]string{a2a.PropCallDepth: strconv.Itoa(maxCallDepth - 1)})
	below := peerCall("S-below", peerName, "Is policy P-9 active?")
	below["parentTaskId"] = "T-below"
	c.send(below)
	m := recv(t, requests)
	if got := m.UserProperties[a2a.PropCallDepth]; got != strconv.Itoa(maxCallDepth) {
		t.Fatalf("a call from depth %d went out at depth %q", maxCallDepth-1, got)
	}

	r.taskOn(c, "T-at", map[string]string{a2a.PropCallDepth: strconv.Itoa(maxCallDepth)})
	at := peerCall("S-at", peerName, "Is policy P-9 active?")
	at["parentTaskId"] = "T-at"
	c.send(at)
	if f := c.refused(errCodeForbidden, "S-at"); f.Message != "call depth limit reached" {
		t.Fatalf("refused with %q", f.Message)
	}
	none(t, requests)
	if hasSubtask(r, "S-at") {
		t.Fatal("a refused peer call stays registered")
	}
}
