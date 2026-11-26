// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type streamOutcome struct {
	content      string
	finishReason string
	err          error
}

func streamScenarioThroughBifrost(t *testing.T, scenarioJSON string) streamOutcome {
	t.Helper()

	sc, err := ParseScenario([]byte(scenarioJSON))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	srv := NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(sc)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("start server: %v", startErr)
	}
	t.Cleanup(srv.Stop)

	t.Setenv("OPENAI_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := llm.NewBifrostClient(ctx, llm.BifrostConfig{
		Provider: "openai",
		Model:    "perf-test-model",
		BaseURL:  "http://" + srv.Addr(),
	})
	if err != nil {
		t.Fatalf("new bifrost client: %v", err)
	}
	t.Cleanup(client.Shutdown)

	stream, err := client.ChatCompletionStream(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("start stream: %v", err)
	}

	var out streamOutcome
	var content strings.Builder
	for chunk := range stream {
		content.WriteString(chunk.ContentDelta)
		if chunk.FinishReason != "" {
			out.finishReason = chunk.FinishReason
		}
		if chunk.Err != nil {
			out.err = chunk.Err
		}
	}
	out.content = content.String()
	return out
}

func TestStreamFaultOmitFinishReasonFailsAsTruncated(t *testing.T) {
	for name, choice := range map[string]string{
		"no reason authored":       `{"message": {"content": "partial answer"}}`,
		"explicit reason stripped": `{"message": {"content": "partial answer"}, "finish_reason": "stop"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := streamScenarioThroughBifrost(t, `{
				"id": "truncated",
				"responses": [
					{"choices": [`+choice+`], "streamFault": {"omitFinishReason": true}}
				]
			}`)

			if got.content != "partial answer" {
				t.Errorf("content = %q, want %q", got.content, "partial answer")
			}
			llmErr, ok := errors.AsType[*llm.Error](got.err)
			if !ok {
				t.Fatalf("stream error = %v, want an *llm.Error", got.err)
			}
			if !llmErr.IsStreamTruncated() {
				t.Errorf("error = %v, want a stream truncation", llmErr)
			}
		})
	}
}

func TestStreamFaultOmitFinishReasonRejectsEmptyChoices(t *testing.T) {
	_, err := PrecomputeResponse([]byte(`{"choices": [], "streamFault": {"omitFinishReason": true}}`), 0)
	if err == nil {
		t.Fatal("precompute accepted omitFinishReason on a response with no choices, whose stream would still finish with stop")
	}
}

func TestStreamWithoutFaultDefaultsToStop(t *testing.T) {
	got := streamScenarioThroughBifrost(t, `{
		"id": "complete",
		"responses": [
			{"choices": [{"message": {"content": "partial answer"}}]}
		]
	}`)

	if got.err != nil {
		t.Fatalf("stream error = %v, want none", got.err)
	}
	if got.content != "partial answer" {
		t.Errorf("content = %q, want %q", got.content, "partial answer")
	}
	if got.finishReason != "stop" {
		t.Errorf("finish reason = %q, want %q", got.finishReason, "stop")
	}
}

func TestStreamWithEmptyChoicesFinishesWithStop(t *testing.T) {
	got := streamScenarioThroughBifrost(t, `{"id": "empty", "responses": [{"choices": []}]}`)

	if got.err != nil {
		t.Fatalf("stream error = %v, want none", got.err)
	}
	if got.finishReason != "stop" {
		t.Errorf("finish reason = %q, want %q", got.finishReason, "stop")
	}
}

func TestStreamFaultShapesTheFinishFrameNotTheFullJSON(t *testing.T) {
	pc, err := PrecomputeResponse([]byte(
		`{"choices": [{"message": {"content": "partial answer"}}], "streamFault": {"omitFinishReason": true}}`), 0)
	if err != nil {
		t.Fatalf("precompute: %v", err)
	}

	finishFrame := pc.StreamFrames[len(pc.StreamFrames)-2]
	if !bytes.Contains(finishFrame, []byte(`"finish_reason":null`)) {
		t.Errorf("finish frame = %s, want it to carry \"finish_reason\":null", finishFrame)
	}
	if bytes.Contains(pc.FullJSON, []byte("streamFault")) {
		t.Errorf("FullJSON = %s, want no streamFault block on the wire", pc.FullJSON)
	}
	if !bytes.Contains(pc.FullJSON, []byte(`"finish_reason":"stop"`)) {
		t.Errorf("FullJSON = %s, want the non-streaming body to keep its defaulted finish_reason", pc.FullJSON)
	}
}
