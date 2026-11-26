// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestFIFOClientStreamsAPacedResponseInChunks(t *testing.T) {
	raw := json.RawMessage(`{"latency": {"chunkChars": 4, "streamDelayMs": 10}, "choices": [{"message": {"role": "assistant", "content": "abcdefghijkl"}}]}`)
	pacing, err := StreamPacingOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ConvertOpenAIResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := NewFIFOClientWithEntries([]FIFOEntry{{Response: resp, Pacing: pacing}})
	start := time.Now()
	ch, err := c.ChatCompletionStream(t.Context(), &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	finished := false
	for chunk := range ch {
		if chunk.ContentDelta != "" {
			deltas = append(deltas, chunk.ContentDelta)
		}
		finished = finished || chunk.FinishReason != ""
	}
	if len(deltas) != 3 || strings.Join(deltas, "") != "abcdefghijkl" || !finished {
		t.Errorf("deltas = %q (finished %v), want the content in 3 chunks and a finish", deltas, finished)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("stream took %v, want the chunks spread %v apart", elapsed, pacing.ChunkDelay)
	}
}

func TestFIFOClientStreamsAnUnpacedResponseAsOneDelta(t *testing.T) {
	c := NewFIFOClientWithEntries([]FIFOEntry{{Response: &llm.ChatResponse{Content: "abcdefghijkl"}}})
	ch, err := c.ChatCompletionStream(t.Context(), &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	for chunk := range ch {
		if chunk.ContentDelta != "" {
			deltas = append(deltas, chunk.ContentDelta)
		}
	}
	if len(deltas) != 1 {
		t.Errorf("deltas = %q, want one", deltas)
	}
}

func TestStreamPacingOfRefusesWhatItCannotReproduce(t *testing.T) {
	for name, block := range map[string]string{
		"time to first token":      `{"ttftMs": 100, "chunkChars": 4}`,
		"delay without a chunk":    `{"streamDelayMs": 100}`,
		"time to first token only": `{"ttftMs": 100}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := StreamPacingOf(json.RawMessage(`{"latency": ` + block + `}`)); !errors.Is(err, errUnsupportedLatency) {
				t.Errorf("StreamPacingOf = %v, want it refused", err)
			}
		})
	}
}

func TestFIFOClientPacedStreamEndsWithItsContext(t *testing.T) {
	const chunks = 200
	c := NewFIFOClientWithEntries([]FIFOEntry{{
		Response: &llm.ChatResponse{Content: strings.Repeat("x", chunks)},
		Pacing:   StreamPacing{ChunkChars: 1},
	}})
	ctx, cancel := context.WithCancel(t.Context())
	ch, err := c.ChatCompletionStream(ctx, &llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	time.Sleep(50 * time.Millisecond)

	received := 1
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				if received >= chunks {
					t.Errorf("received %d chunks after the context ended, want the producer stopped", received)
				}
				return
			}
			received++
		case <-deadline:
			t.Fatal("the stream never closed after its context ended")
		}
	}
}
