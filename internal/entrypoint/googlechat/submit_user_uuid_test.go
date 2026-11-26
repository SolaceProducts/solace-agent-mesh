// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestProcess_ASharedSpacePublishesTheRunAsRowNotTheSenders(t *testing.T) {
	const adaRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.cfg.DefaultAgentName = "agent"
	a.cfg.RunAs = "default"
	brk, requests := attachCore(t, a)
	a.core.SetIdentityAuthorizer(&stubAuthorizer{res: auth.SenderAuthorization{
		Resolution: identity.LookupMatched, ResolvedUserID: adaRow, Scopes: []string{"agent:*:invoke"},
	}})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent"})

	ev := senderEvent("ada@example.com")
	ev.IsDM, ev.Text, ev.SpaceName = false, "hello", "spaces/s1"
	if got := a.process(t.Context(), ev); got.outcome != outcomeSubmitted {
		t.Fatalf("process outcome = %v, want submitted", got.outcome)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, requests)
	if err != nil {
		t.Fatalf("no task reached the agent: %v", err)
	}
	if got := msg.UserProperties[a2a.PropUserUUID]; got != rbac.DefaultSystemUserID {
		t.Errorf("published userUuid = %q, want the run_as system user's row %s, not the sender's", got, rbac.DefaultSystemUserID)
	}
}
