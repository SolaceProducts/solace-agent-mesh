// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

var chatBudgetResetsAt = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

func blockMeshBudget(t *testing.T, core *entrypoint.GatewayCore) {
	t.Helper()
	budgettest.BlockMesh(t, core.BudgetGate(), chatBudgetResetsAt)
}

func TestIngress_BudgetRefusalIsPostedOnceAndAckedWithoutRetry(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)
	a.cfg.DefaultAgentName = "agent"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent"})
	a.core.SetIdentityAuthorizer(matchedAuthorizer())
	blockMeshBudget(t, a.core)
	logs := recordLogs(a)
	const key = "spaces/DM1/messages/budget"

	if got := runIngressJob(t, a, dmJob(1, key, "hello")); !got.acked {
		t.Fatalf("budget refusal settled %+v, want ack: a spent budget refuses every retry until it resets", got)
	}
	if got := runIngressJob(t, a, dmJob(2, key, "hello")); !got.acked {
		t.Errorf("redelivery settled %+v, want ack for an event already answered", got)
	}

	want := entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(chatBudgetResetsAt))
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, want) })
	a.bgWG.Wait()
	writes, texts := chat.snapshot()
	if len(texts) != 1 || texts[0] != want {
		t.Fatalf("messages = %q, want exactly the budget refusal %q", texts, want)
	}
	if writes[0].space != "spaces/DM1" || writes[0].thread != "spaces/DM1/threads/T1" {
		t.Errorf("refusal posted to %+v, want the sender's space and thread", writes[0])
	}
	if n := logs.count(slog.LevelError, "google chat"); n != 0 {
		t.Errorf("error logs = %d, want none: a spent budget is policy, not a fault", n)
	}

	budgettest.AssertNotDelivered(t, brk, requests)
}

func TestIngress_AnUnconfirmedBudgetStateIsRetriedNotRefused(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)
	a.cfg.DefaultAgentName = "agent"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent"})
	a.core.SetIdentityAuthorizer(matchedAuthorizer())
	budgettest.Unconfirm(t, a.core.BudgetGate())

	if got := runIngressJob(t, a, dmJob(1, "spaces/DM1/messages/unconfirmed", "hello")); got.acked {
		t.Fatalf("event settled %+v, want it left for redelivery once the state is confirmed", got)
	}
	a.bgWG.Wait()
	if _, texts := chat.snapshot(); anyTextContains(texts, "usage budget") {
		t.Errorf("messages = %q, want no budget refusal for a transient state", texts)
	}
	budgettest.AssertNotDelivered(t, brk, requests)
}
