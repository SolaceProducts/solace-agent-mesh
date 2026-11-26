// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func mcpResultWithModelView(text string) *tool.Result {
	return &tool.Result{
		Status: "processed",
		Data: map[string]any{
			"message_to_llm":  "Response from tool 'list_orders' processed.",
			"mcp_tool_output": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}},
		},
		LLMData: map[string]any{
			"mcp_tool_output": map[string]any{"content": []map[string]any{{"type": "text", "text": json.RawMessage(text)}}},
		},
	}
}

func TestTheModelSeesAnMCPDocumentAsJSON(t *testing.T) {
	agent, _, _ := newTestAgent()
	out, _ := agent.formatToolResult(context.Background(), mcpResultWithModelView(`{"id":9007199254740993,"total":12.50}`), nil, nil)

	if strings.Contains(out, `\"`) {
		t.Errorf("rendering still escapes the document: %s", out)
	}
	if !strings.Contains(out, `"text":{"id":9007199254740993,"total":12.50}`) {
		t.Errorf("rendering does not carry the document verbatim: %s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("rendering is not JSON: %v\n%s", err, out)
	}
}

func TestACheckpointedMCPResultRendersAsItDidBeforeThePause(t *testing.T) {
	agent, _, _ := newTestAgent()
	for _, text := range []string{`{"id":1234567890123456789,"z":1,"a":2.50}`, `{"a":1e999}`} {
		fresh := mcpResultWithModelView(text)
		want, _ := agent.formatToolResult(context.Background(), fresh, nil, nil)

		payload, err := json.Marshal(newPersistedToolResult(mcpResultWithModelView(text)))
		if err != nil {
			t.Fatalf("persist %s: %v", text, err)
		}
		restored := resultFromPayload(payload)
		if restored == nil {
			t.Fatalf("checkpoint holding %s did not restore", text)
		}
		got, _ := agent.formatToolResult(context.Background(), restored, nil, nil)
		if got != want {
			t.Errorf("resumed rendering differs from the fresh one for %s\nfresh:   %s\nresumed: %s", text, want, got)
		}
	}
}
