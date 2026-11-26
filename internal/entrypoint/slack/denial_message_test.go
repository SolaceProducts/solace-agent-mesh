// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const deniedWireName = "agent_019fe224_dd07_764b_ae4c_125a04fb3ab2"

func denyingSlackAdapter(t *testing.T, scopes []string) (*SlackGatewayAdapter, *slackAPICalls, *broker.DevBroker) {
	t.Helper()
	a, calls, brk := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = deniedWireName
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: deniedWireName})
	a.core.SetScopeResolver(stubScopeResolver{scopes: scopes})
	return a, calls, brk
}

func agentRequests(t *testing.T, brk *broker.DevBroker, queueName string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", deniedWireName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func received(t *testing.T, brk *broker.DevBroker, q broker.Queue, timeout time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	msg, _ := brk.Receive(ctx, q)
	return msg
}

func lastPostedText(t *testing.T, calls *slackAPICalls) string {
	t.Helper()
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if calls.postCalls == 0 {
		t.Fatal("a turned-away user must be told something, not left in silence")
	}
	return calls.lastPostText
}

func TestSlackDenial_ShowsPermissionCopyNotAGenericFailure(t *testing.T) {
	a, calls, brk := denyingSlackAdapter(t, nil)
	deniedQueue := agentRequests(t, brk, "slack-denial-q")

	if got, _ := a.trySubmit(slackMessage()); got != taxonomy.RejectForbidden {
		t.Errorf("a denial must meter as %q, got %q", taxonomy.RejectForbidden, got)
	}
	if msg := received(t, brk, deniedQueue, 300*time.Millisecond); msg != nil {
		t.Errorf("a denied message reached the agent anyway: %s", string(msg.Payload))
	}

	reply := lastPostedText(t, calls)
	if strings.Contains(reply, deniedWireName) {
		t.Errorf("reply leaks the agent's internal wire name: %q", reply)
	}
	if strings.Contains(reply, "agent delegation denied") || strings.Contains(reply, "Failed to submit") {
		t.Errorf("reply exposes the raw Go error: %q", reply)
	}
	if reply == entrypoint.SubmitFailedText {
		t.Errorf("a denied user got the retryable message %q; they cannot retry their way out of a permission boundary", reply)
	}
	if !strings.Contains(reply, "don't have access") {
		t.Errorf("reply should name the permission boundary, got %q", reply)
	}

	granted, grantedCalls, grantedBrk := denyingSlackAdapter(t, []string{rbac.InvokeScopeForInstance(deniedWireName)})
	grantedQueue := agentRequests(t, grantedBrk, "slack-granted-q")
	if got, _ := granted.trySubmit(slackMessage()); got != "" {
		t.Fatalf("a properly scoped user must be submitted, not refused with %q", got)
	}
	if received(t, grantedBrk, grantedQueue, 2*time.Second) == nil {
		t.Fatal("a properly scoped user's message never reached the agent, so the assertions above prove nothing")
	}
	grantedCalls.mu.Lock()
	posted := grantedCalls.lastPostText
	grantedCalls.mu.Unlock()
	if strings.Contains(posted, "don't have access") {
		t.Errorf("a scoped user was shown the refusal copy: %q", posted)
	}
}

func TestSlackDenial_TargetedAgentIsIndistinguishableFromAnUnknownOne(t *testing.T) {
	probe := func(text string) string {
		t.Helper()
		a, calls, _ := denyingSlackAdapter(t, nil)
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Reporter"})
		in := slackMessage()
		in.text = text
		_, _ = a.trySubmit(in)
		return lastPostedText(t, calls)
	}

	denied := probe("@Reporter do a thing")
	unknown := probe("@NoSuchAgentAnywhere do a thing")

	if !strings.Contains(denied, "Reporter") || !strings.Contains(unknown, "NoSuchAgentAnywhere") {
		t.Fatalf("each refusal should echo the caller's own token; got %q and %q", denied, unknown)
	}
	if strings.Replace(denied, "Reporter", "TOKEN", 1) != strings.Replace(unknown, "NoSuchAgentAnywhere", "TOKEN", 1) {
		t.Errorf("denied and nonexistent targets must read identically in Slack:\n denied: %q\n unknown: %q", denied, unknown)
	}
}
