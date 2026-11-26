// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAnswerNamingAPeerFileReturnsItOnlyWhereTheGatewayFindsIt(t *testing.T) {
	tests := map[string]struct {
		scope      string
		wantReturn bool
	}{
		"a namespace-scoped store, where the peer and the caller share a location": {artifact.ScopeNamespace, true},
		"a per-agent store, where the peer saved under its own name":               {"agent", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			agent, _, _ := newTestAgent(
				&llm.ChatResponse{
					FinishReason: "tool_calls",
					ToolCalls:    []llm.ToolCall{{ID: "call-peer", Name: peerToolPrefix + "analyst", Arguments: `{}`}},
				},
				&llm.ChatResponse{Content: "The analyst built the deck, see deck.pptx.", FinishReason: "stop"},
			)
			agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memoryArtifactStore(), Scope: tc.scope, Namespace: "test-ns"}
			registerPeerReturning(t, agent, func() *tool.Result { return deckPeerResult(t) })

			resp, err := agent.HandleTask(t.Context(), "task-peer-scope", newUserMessage("Build the deck"),
				a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})
			if err != nil {
				t.Fatalf("HandleTask under artifact scope %q: %v", tc.scope, err)
			}

			body, _ := json.Marshal(resp)
			if returned := strings.Contains(string(body), peerDeckEmbed); returned != tc.wantReturn {
				t.Errorf("answer returns deck.pptx = %v, want %v under artifact scope %q: %s", returned, tc.wantReturn, tc.scope, body)
			}
		})
	}
}
