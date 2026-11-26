// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const secret = "sk-live-abcdef123456"

var oversized = strings.Repeat("x", 20000)

func dataOf(t *testing.T, m *broker.Message) map[string]any {
	t.Helper()
	var data map[string]any
	signalOf(t, m, &data)
	return data
}

func capped(t *testing.T, s any) {
	t.Helper()
	str, _ := s.(string)
	if len(str) > maxStringBytes || !strings.Contains(str, "truncated") {
		t.Fatalf("a %d-byte string was not capped at %d", len(str), maxStringBytes)
	}
}

func TestAnAgentsSecretsAndOversizedValuesAreRedactedAndCappedAsANativeAgents(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	args := map[string]any{"api_key": secret, "note": oversized}

	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o",
		"toolCalls": []any{map[string]any{"id": "c1", "name": "lookup", "args": args}}}))
	c.send(event("T1", map[string]any{"kind": eventToolStarted, "callId": "c1", "name": "lookup", "args": args}))
	c.send(event("T1", map[string]any{"kind": eventToolFinished, "callId": "c1", "name": "lookup", "result": map[string]any{"session_token": secret, "rows": oversized}}))
	var got []*broker.Message
	for range 3 {
		m := recv(t, status)
		if strings.Contains(string(m.Payload), secret) || len(m.Payload) > 3*maxStringBytes {
			t.Fatalf("published %d bytes with the secret or uncapped", len(m.Payload))
		}
		got = append(got, m)
	}
	capped(t, dataOf(t, got[1])["tool_args"].(map[string]any)["note"])
	capped(t, dataOf(t, got[2])["result_data"].(map[string]any)["rows"])
	sameMessage(t, got[1], r.nativeStatus("T1", func(ctx context.Context, sp *agent.StatusPublisher) {
		sp.PublishToolInvocationStart(ctx, "lookup", args, "c1", "")
	}))
}
