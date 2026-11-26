// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestStatusWireAgentNameBytes(t *testing.T) {
	const agentSlug = "agent_0199aaaa_bbbb"

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-wire-1"
	q, err := brk.CreateQueue("status-wire-collector", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	sp := NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      "task-wire-1",
		ContextID:   "ctx-wire-1",
		AgentName:   agentSlug,
		SeqGen:      NewEventSeqGen(),
	}, slog.Default())

	sp.PublishText(t.Context(), "hello")

	msgs := receiveAll(t, brk, q, 1, 3*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published status message, got %d", len(msgs))
	}
	payload := string(msgs[0].Payload)

	agentNameFragment := fmt.Sprintf(`%q:%q`, a2a.MetaAgentName, agentSlug)
	if agentNameFragment != `"agent_name":"agent_0199aaaa_bbbb"` {
		t.Fatalf("a2a.MetaAgentName drifted: fragment = %q", agentNameFragment)
	}

	frozen := []string{
		agentNameFragment,
		`"agent_name":"agent_0199aaaa_bbbb"`,
		`"jsonrpc":"2.0"`,
		`"kind":"status-update"`,
	}
	for _, want := range frozen {
		if !strings.Contains(payload, want) {
			t.Errorf("status wire payload missing frozen bytes %q\npayload: %s", want, payload)
		}
	}
}
