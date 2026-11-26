// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

func (h *TestHarness) AssertBasicMetrics(t *testing.T) {
	t.Helper()

	recorder, ok := h.Metrics.(*observability.RecordingMetrics)
	if !ok {
		return
	}

	if h.scenario.BuilderChatFlow != nil && h.scenario.GatewayInput == nil && len(h.scenario.GatewayInputs) == 0 {
		return
	}

	if len(h.scenario.LLMInteractions) == 0 {
		return
	}

	if !recorder.HasHistogram("operation.duration", "type", "agent") {
		t.Error("metrics: expected operation.duration with type=agent (agent execution)")
	}

	if !recorder.HasHistogram("gen_ai.client.operation.duration") {
		t.Error("metrics: expected gen_ai.client.operation.duration (LLM call)")
	}

	for _, m := range recorder.Histograms("operation.duration") {
		if _, ok := m.Labels["error.type"]; !ok {
			t.Errorf("metrics: operation.duration missing error_type label: %v", m.Labels)
		}
		if _, ok := m.Labels["component.name"]; !ok {
			t.Errorf("metrics: operation.duration missing component_name label: %v", m.Labels)
		}
	}

	for _, m := range recorder.Histograms("gen_ai.client.operation.duration") {
		if _, ok := m.Labels["gen_ai.request.model"]; !ok {
			t.Errorf("metrics: gen_ai.client.operation.duration missing gen_ai_request_model label: %v", m.Labels)
		}
	}
}
