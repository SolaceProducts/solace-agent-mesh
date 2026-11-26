// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"testing"
)

func TestACallReportsTheModelItWasSentTo(t *testing.T) {
	server := mockOpenAIServer(t, nonStreamHandler("fine", "stop"))
	defer server.Close()
	client := newRetryTestClient(t, server.URL, new(0))

	for name, tc := range map[string]struct{ model, want string }{
		"a request naming no model is sent to the configured one":    {"", "openai/test-model"},
		"a request naming a model is sent to it":                     {"other-model", "openai/other-model"},
		"a model already naming the provider is not qualified twice": {"openai/other-model", "openai/other-model"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
				Model: tc.model, Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Model != tc.want {
				t.Errorf("Model = %q, want %q", resp.Model, tc.want)
			}
		})
	}
}

func TestEveryStreamChunkReportsTheModelTheStreamWasSentTo(t *testing.T) {
	server := mockOpenAIServer(t, streamHandler([]string{"Hello", " world"}))
	defer server.Close()
	client := newRetryTestClient(t, server.URL, new(0))

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := 0
	for chunk := range stream {
		chunks++
		if chunk.Model != "openai/test-model" {
			t.Errorf("chunk %d Model = %q, want openai/test-model", chunks, chunk.Model)
		}
	}
	if chunks == 0 {
		t.Fatal("the stream delivered no chunks")
	}
}

func TestACallServedByTheResponsesAPIReportsTheModelItWasSentTo(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		client := newFakeOpenAIClient(t, &fakeOpenAI{responsesJSON: completedResponseJSON}, "gpt-5.6-terra")

		resp, err := client.ChatCompletion(t.Context(), toolsWithReasoningRequest())
		if err != nil {
			t.Fatalf("ChatCompletion: %v", err)
		}
		if resp.Model != "openai/gpt-5.6-terra" {
			t.Errorf("Model = %q, want openai/gpt-5.6-terra", resp.Model)
		}
	})
	t.Run("stream", func(t *testing.T) {
		fake := &fakeOpenAI{responsesSSE: []string{
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"It is noon."}`,
			streamCompleted,
		}}
		client := newFakeOpenAIClient(t, fake, "gpt-5.6-terra")

		res := collectStream(t, client, toolsWithReasoningRequest())

		if res.final.Model != "openai/gpt-5.6-terra" {
			t.Errorf("final chunk Model = %q, want openai/gpt-5.6-terra", res.final.Model)
		}
	})
}
