// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestSharedSpaceSubmitCarriesBothPrincipalsToTheAgent(t *testing.T) {
	rig := newClickRig(t)
	a := rig.replicaWithTaskLog(t, newFakeChat())
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: dmMeta().AgentName})
	a.cfg.DefaultAgentName = dmMeta().AgentName
	a.cfg.RunAs = "svc-bot"

	res := a.process(t.Context(), &inboundEvent{
		Kind: kindMessage, Addressed: true, Text: "hello",
		SpaceName: sharedMeta().SpaceName, ThreadName: sharedMeta().ThreadName, MessageName: sharedMeta().SpaceName + "/messages/M1",
		SenderName: requesterName, SenderEmail: requesterEmail, SenderDisplayName: "Ada",
	})
	if res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := rig.brk.Receive(ctx, rig.agent)
	if err != nil || msg == nil {
		t.Fatalf("the agent received nothing: %v", err)
	}
	msg.Ack()
	var req struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("agent request is not JSON-RPC: %v", err)
	}
	md := req.Params.Message.Metadata
	if md["requesting_user"] != requesterEmail || md["run_as"] != rbac.SystemUserPrincipal("svc-bot") {
		t.Errorf("requesting_user/run_as = %v/%v, want %q/%q",
			md["requesting_user"], md["run_as"], requesterEmail, rbac.SystemUserPrincipal("svc-bot"))
	}
}
