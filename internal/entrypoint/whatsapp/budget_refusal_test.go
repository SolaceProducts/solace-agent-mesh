// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

var waBudgetResetsAt = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

func blockMeshBudget(t *testing.T, core *entrypoint.GatewayCore) {
	t.Helper()
	budgettest.BlockMesh(t, core.BudgetGate(), waBudgetResetsAt)
}

func tapAgentRequests(t *testing.T, a *Adapter, agentName string) broker.Queue {
	t.Helper()
	return budgettest.TapAgentRequests(t, a.core.Services().Broker(), "test-ns", agentName)
}

func TestWhatsAppBudgetRefusal_SenderIsToldOnceAndTheMessageIsNotRetried(t *testing.T) {
	a, metrics, mock := newMetricsRig(t, agentUnderTest)
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentUnderTest)}})
	requests := tapAgentRequests(t, a, agentUnderTest)
	blockMeshBudget(t, a.core)

	value, msg := textMessage("wamid.BUDGET", "15551230000", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("first delivery settled %v, want settleAck: a spent budget refuses every retry until it resets", got)
	}
	if got := a.deliver(value, msg); got != settleAck {
		t.Errorf("redelivery settled %v, want settleAck for a message already answered", got)
	}

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("replies = %+v, want exactly one budget refusal", calls)
	}
	if want := entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(waBudgetResetsAt)); calls[0].Text.Body != want {
		t.Errorf("reply = %q, want %q", calls[0].Text.Body, want)
	}
	if calls[0].To != "15551230000" {
		t.Errorf("reply sent to %q, want the sender", calls[0].To)
	}

	if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a, "reason", taxonomy.RejectBudgetExhausted)); got != 1 {
		t.Errorf("submit failures{reason=budget_exhausted} = %d, want 1; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppSubmitFailures))
	}
	if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a, "reason", ackNoTaskDeclined)); got != 1 {
		t.Errorf("acked-without-task{reason=declined} = %d, want 1; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppAckedWithoutTask))
	}

	budgettest.AssertNotDelivered(t, a.core.Services().Broker(), requests)
}

func TestWhatsAppUnconfirmedBudgetState_TheMessageIsRetriedNotRefused(t *testing.T) {
	a, _, mock := newMetricsRig(t, agentUnderTest)
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentUnderTest)}})
	requests := tapAgentRequests(t, a, agentUnderTest)
	budgettest.Unconfirm(t, a.core.BudgetGate())

	value, msg := textMessage("wamid.UNCONFIRMED", "15551230000", "hello")
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("delivery settled %v, want settleNack so the broker redelivers once the state is confirmed", got)
	}
	if calls := mock.calls(); len(calls) != 0 {
		t.Errorf("replies = %+v, want none for a transient refusal", calls)
	}
	budgettest.AssertNotDelivered(t, a.core.Services().Broker(), requests)
}
