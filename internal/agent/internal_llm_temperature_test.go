// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const oSeriesRejection = "Unsupported parameter: 'temperature' is not supported with this model."

type tempRejectingLLM struct {
	mu      sync.Mutex
	sawTemp []bool
}

func (m *tempRejectingLLM) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	hadTemp := req.Temperature != nil
	m.sawTemp = append(m.sawTemp, hadTemp)
	m.mu.Unlock()
	if hadTemp {
		return nil, &llm.Error{StatusCode: 400, Message: oSeriesRejection}
	}
	return &llm.ChatResponse{Content: "recovered"}, nil
}

func (m *tempRejectingLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, nil
}

func (m *tempRejectingLLM) temps() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.sawTemp...)
}

func TestInternalLLMCall_RecoversFromTemperatureRejection(t *testing.T) {
	client := &tempRejectingLLM{}
	h := newInternalLLMHarness(t, client, "task-temp-1")

	got, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.3)
	if err != nil {
		t.Fatalf("internalLLMCall should have recovered via the fallback: %v", err)
	}
	if got != "recovered" {
		t.Errorf("content = %q, want %q", got, "recovered")
	}
	temps := client.temps()
	if len(temps) != 2 {
		t.Fatalf("calls = %v, want 2 (probe then retry)", temps)
	}
	if !temps[0] || temps[1] {
		t.Errorf("calls = %v, want [true false] — probe with the temperature, retry without", temps)
	}
}

func TestInternalLLMCall_MemoSkipsProbeAfterRejection(t *testing.T) {
	client := &tempRejectingLLM{}
	h := newInternalLLMHarness(t, client, "task-temp-2")

	for range 3 {
		if _, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.3); err != nil {
			t.Fatalf("internalLLMCall: %v", err)
		}
	}

	temps := client.temps()
	if len(temps) != 4 {
		t.Fatalf("calls = %v, want 4 (probe, retry, then one each)", temps)
	}
	for i, hadTemp := range temps[2:] {
		if hadTemp {
			t.Errorf("call %d re-probed with a temperature; the memo did not apply", i+3)
		}
	}
}

func TestInternalLLMCall_StatusEventReportsEffectiveTemperature(t *testing.T) {
	client := &tempRejectingLLM{}
	h := newInternalLLMHarness(t, client, "task-temp-3")

	if _, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.3); err != nil {
		t.Fatalf("first internalLLMCall: %v", err)
	}
	first := drainStatusSignals(t, h.broker, h.queue, 2)
	if got, present := invocationTemperature(t, first[0]); !present || got != 0.3 {
		t.Fatalf("probe llm_invocation temperature = %v (present=%v), want 0.3", got, present)
	}

	if _, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.3); err != nil {
		t.Fatalf("second internalLLMCall: %v", err)
	}
	second := drainStatusSignals(t, h.broker, h.queue, 1)
	if got, present := invocationTemperature(t, second[0]); present {
		t.Errorf("memoized llm_invocation reported temperature %v, but none was sent — a trace-driven reproduction would send the wrong request", got)
	}
}

func invocationTemperature(t *testing.T, signal map[string]any) (float64, bool) {
	t.Helper()
	if signal["type"] != "llm_invocation" {
		t.Fatalf("signal type = %v, want llm_invocation", signal["type"])
	}
	request, ok := signal["request"].(map[string]any)
	if !ok {
		t.Fatalf("llm_invocation carried no request map: %#v", signal)
	}
	config, ok := request["config"].(map[string]any)
	if !ok {
		return 0, false
	}
	genConfig, ok := config["generation_config"].(map[string]any)
	if !ok {
		return 0, false
	}
	raw, present := genConfig["temperature"]
	if !present {
		return 0, false
	}
	temp, ok := raw.(float64)
	if !ok {
		t.Fatalf("generation_config.temperature = %#v, want a float64", raw)
	}
	return temp, true
}
