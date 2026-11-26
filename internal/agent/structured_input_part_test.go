// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/base64"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
)

func TestStructuredInputPart(t *testing.T) {
	input := a2a.NewFilePartWithBytes(base64.StdEncoding.EncodeToString([]byte(`{"order_id":"o1"}`)), "n1_input.json", "application/json")
	orderSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
	}
	textSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
	request := a2a.NewDataPart(map[string]any{"type": "structured_invocation_request", "node_id": "n1"})

	cases := []struct {
		name string
		msg  a2a.Message
		sc   *structured.Context
		want int
	}{
		{"not a structured invocation", a2a.Message{Parts: []a2a.Part{input}}, nil, -1},
		{"structured schema", a2a.Message{Parts: []a2a.Part{request, input}}, &structured.Context{InputSchema: orderSchema}, 1},
		{"single-text schema", a2a.Message{Parts: []a2a.Part{request, input}}, &structured.Context{InputSchema: textSchema}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := structuredInputPart(tc.msg, tc.sc); got != tc.want {
				t.Errorf("structuredInputPart = %d, want %d", got, tc.want)
			}
		})
	}
}
