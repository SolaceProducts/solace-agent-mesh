// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func adoptedReplyPayload(t *testing.T, toolResult map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse("corr-adopted", remotetool.InvokeResult{ToolResult: toolResult}))
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return payload
}

func TestAdoptedReplyKeepsResultSignalsOutOfTheModelsView(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "search"}, nil)

	result := rt.ResultFromReply(adoptedReplyPayload(t, schemaResult(ragSignal(0, webSource("s0r0")))))

	if _, leaked := result.Data[resultSignalsKey]; leaked {
		t.Errorf("result data = %v, want no %q entry", result.Data, resultSignalsKey)
	}
	if got := result.Data["formatted_results"]; got != "results" {
		t.Errorf("result data formatted_results = %v, want %q", got, "results")
	}
}

func TestReplyIsRetryableOnlyWhenTheHoldersRetryWouldAttemptIt(t *testing.T) {
	t.Parallel()
	connector := &ToolConfig{ToolName: "connector_tool", ToolPassthroughConfig: map[string]any{"_str_binary": "connector-bin"}}
	tests := []struct {
		name    string
		cfg     *ToolConfig
		payload []byte
		want    bool
	}{
		{"a successful reply", &ToolConfig{ToolName: "search"}, adoptedReplyPayload(t, map[string]any{"message": "ok"}), false},
		{"the runtime being unavailable", &ToolConfig{ToolName: "search"}, errorReplyPayload("TOOL_NOT_AVAILABLE"), true},
		{"a failing tool", &ToolConfig{ToolName: "search"}, errorReplyPayload("EXECUTION_ERROR"), false},
		{"a tool missing from an ordinary worker", &ToolConfig{ToolName: "search"}, errorReplyPayload("TOOL_NOT_FOUND"), false},
		{"a connector tool missing from its worker", connector, errorReplyPayload("TOOL_NOT_FOUND"), true},
		{"an unparseable reply", &ToolConfig{ToolName: "search"}, []byte("not json"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := NewTool(tt.cfg, nil)

			if got := rt.ReplyIsRetryable(tt.payload); got != tt.want {
				t.Errorf("ReplyIsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func errorReplyPayload(code string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":"corr-1","error":{"code":"` + code + `","message":"failed"}}`)
}
