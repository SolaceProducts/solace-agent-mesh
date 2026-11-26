// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var billedErr = &llm.Error{Message: "stream cancelled", BilledUsage: &llm.Usage{PromptTokens: 800, CompletionTokens: 20}}

func TestRecordingClient_UsageReportedBeforeAnErrorIsRecorded(t *testing.T) {
	streamed := &scriptedClient{chunks: []llm.StreamChunk{
		{ContentDelta: "x", Usage: cachedUsage},
		{Err: &llm.Error{Message: "upstream reset"}},
	}}
	unary := &scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}, err: &llm.Error{Message: "invalid tool call"}}
	for name, call := range map[string]func(llm.Client, *llm.ChatRequest){
		"stream": drainCall,
		"unary":  unaryCall,
	} {
		t.Run(name, func(t *testing.T) {
			inner := llm.Client(streamed)
			if name == "unary" {
				inner = unary
			}
			bus := newSpendBus(t)
			pub := bus.publisher(t)

			call(NewRecordingClient(inner, "bound", pub), &llm.ChatRequest{Model: "m", Spend: attribution()})

			events := bus.published(t, pub)
			if want := TokensFromUsage(*cachedUsage); len(events) != 1 || events[0].Tokens != want {
				t.Errorf("events = %+v, want one with tokens %+v", events, want)
			}
		})
	}
}

func TestRecordingClient_AFailedCallIsRecordedFromTheUsageTheProviderBilled(t *testing.T) {
	cases := map[string]struct {
		inner llm.Client
		call  func(llm.Client, *llm.ChatRequest)
	}{
		"stream": {&scriptedClient{chunks: []llm.StreamChunk{{ContentDelta: "x"}, {Err: billedErr}}}, drainCall},
		"unary":  {&scriptedClient{err: fmt.Errorf("model override: %w", billedErr)}, unaryCall},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)

			c.call(NewRecordingClient(c.inner, "bound", pub), &llm.ChatRequest{Model: "m", Spend: attribution()})

			events := bus.published(t, pub)
			if want := (Tokens{UncachedInput: 800, Output: 20}); len(events) != 1 || events[0].Tokens != want {
				t.Errorf("events = %+v, want one with tokens %+v", events, want)
			}
		})
	}
}

func TestRecordingClient_ACompletedCallWithNoUsageWarnsOncePerIntervalPerModel(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gaps := newUsageGapLog(time.Minute, func() time.Time { return now })
	logs := &syncBuffer{}
	bus := newSpendBus(t)
	client := &recordingClient{
		inner:        &scriptedClient{chunks: []llm.StreamChunk{{ContentDelta: "secret prompt echo"}, {FinishReason: "stop"}}},
		boundModel:   "bound",
		publisher:    bus.publisher(t),
		logger:       slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		missingUsage: gaps,
	}
	call := func(model string) {
		drainCall(client, &llm.ChatRequest{Model: model, Spend: attribution()})
	}

	call("model-a")
	call("model-a")
	call("model-a")
	call("model-b")
	now = now.Add(time.Minute)
	call("model-a")

	warnings := linesWith(logs.String(), "level=WARN")
	want := []string{"modelName=openai/model-a completed=true suppressed=0", "modelName=openai/model-b completed=true suppressed=0",
		"modelName=openai/model-a completed=true suppressed=2"}
	if len(warnings) != len(want) {
		t.Fatalf("warnings = %q, want %d", warnings, len(want))
	}
	for i, w := range want {
		if !strings.Contains(warnings[i], missingUsageMessage) || !strings.Contains(warnings[i], w) {
			t.Errorf("warning %d = %q, want it to contain %q", i, warnings[i], w)
		}
	}
	if strings.Contains(logs.String(), "secret") {
		t.Errorf("logs carry the response content: %q", logs.String())
	}
}

func TestRecordingClient_ACallThatEndsWithoutCompletingAndWithoutUsageDoesNotWarn(t *testing.T) {
	cases := map[string]struct {
		inner llm.Client
		call  func(llm.Client, *llm.ChatRequest)
	}{
		"stream error":          {&scriptedClient{chunks: []llm.StreamChunk{{Err: &llm.Error{StatusCode: 429}}}}, drainCall},
		"stream without finish": {&scriptedClient{chunks: []llm.StreamChunk{{ContentDelta: "x"}}}, drainCall},
		"unary error":           {&scriptedClient{err: &llm.Error{StatusCode: 401}}, unaryCall},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &syncBuffer{}
			bus := newSpendBus(t)
			client := &recordingClient{
				inner:        c.inner,
				boundModel:   "bound",
				publisher:    bus.publisher(t),
				logger:       slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				missingUsage: newUsageGapLog(time.Minute, time.Now),
			}

			c.call(client, &llm.ChatRequest{Model: "m", Spend: attribution()})

			if warnings := linesWith(logs.String(), "level=WARN"); len(warnings) != 0 {
				t.Errorf("warnings = %q, want none", warnings)
			}
			if debug := linesWith(logs.String(), "level=DEBUG"); len(debug) != 1 || !strings.Contains(debug[0], "completed=false") {
				t.Errorf("debug lines = %q, want one noting the incomplete call", debug)
			}
		})
	}
}

func drainCall(client llm.Client, req *llm.ChatRequest) {
	stream, err := client.ChatCompletionStream(context.Background(), req)
	if err != nil {
		return
	}
	for range stream {
	}
}

func unaryCall(client llm.Client, req *llm.ChatRequest) {
	_, _ = client.ChatCompletion(context.Background(), req)
}

func linesWith(logs, substr string) []string {
	var lines []string
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, substr) {
			lines = append(lines, line)
		}
	}
	return lines
}
