// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type unusedLLM struct{}

func (unusedLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("unused")
}

func (unusedLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("unused")
}

func TestTheDefaultLLMCarriesTheNameOfItsModel(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"anthropic/claude-sonnet-5-5", "claude-sonnet-5-5"},
		{"anthropic/claude-sonnet-4-6", "claude-sonnet-4-6"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			opts, err := BuildLLMOptions(t.Context(), map[string]any{
				"model": tc.model, "api_base": "http://127.0.0.1:1", "api_key": "test-key",
			}, slog.Default())
			if err != nil {
				t.Fatal(err)
			}
			rt := runtime.NewRuntime(nil, config.FromMap(nil), opts...)
			client, ok := rt.Services().LLM("").(*llm.ReconfigurableClient)
			if !ok {
				t.Fatalf("default LLM is %T, want the reconfigurable client the platform swaps", rt.Services().LLM(""))
			}
			t.Cleanup(func() {
				if built, ok := client.Reconfigure(unusedLLM{}).(interface{ Shutdown() }); ok {
					built.Shutdown()
				}
			})
			if got := client.Model(); got != tc.want {
				t.Errorf("default client's model = %q, want %q", got, tc.want)
			}
			if tc.want == "claude-sonnet-5-5" && !llm.ModelRefusesForcedToolChoice(client.Model()) {
				t.Errorf("the name %q is not one the tool-choice check knows to refuse a forced choice", client.Model())
			}
		})
	}
}
