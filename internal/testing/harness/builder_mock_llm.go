// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func (h *TestHarness) startBuilderMockLLM(gw *HTTPGateway) error {
	responses := make([]json.RawMessage, 0, len(h.scenario.LLMInteractions))
	for i, interaction := range h.scenario.LLMInteractions {
		if interaction.IsError() {
			return fmt.Errorf("llm_interaction[%d]: error_response not supported by builder mock LLM", i)
		}
		raw, err := interaction.StaticResponseJSON()
		if err != nil {
			return fmt.Errorf("llm_interaction[%d]: %w", i, err)
		}
		responses = append(responses, raw)
	}

	scenario := &mockllm.Scenario{
		ID:        "builder_chat_flow",
		Responses: responses,
	}
	if err := scenario.Precompute(); err != nil {
		return fmt.Errorf("precompute mock scenario: %w", err)
	}

	server := mockllm.NewMockLLMServer("127.0.0.1:0", 0, 0, h.logger)
	server.LoadScenarios(scenario)
	if err := server.Start(); err != nil {
		return fmt.Errorf("start mock LLM server: %w", err)
	}

	h.builderMockLLM = server
	gw.SetLLMEndpoint(bifrostLLMEndpoint(server.Addr()))
	return nil
}

func bifrostLLMEndpoint(addr string) string {
	return "http://" + addr
}
