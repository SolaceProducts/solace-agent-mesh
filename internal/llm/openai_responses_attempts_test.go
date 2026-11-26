// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestACallServedByTheResponsesAPICountsTheChatAttemptOpenAITurnedAway(t *testing.T) {
	hi := func() *ChatRequest { return &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}} }
	t.Run("unary", func(t *testing.T) {
		client := newFakeOpenAIClient(t, &fakeOpenAI{responsesJSON: completedResponseJSON}, "gpt-5-pro")

		for i, want := range []int{2, 1} {
			resp, err := client.ChatCompletion(t.Context(), hi())
			if err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
			if resp.Attempts != want {
				t.Errorf("call %d Attempts = %d, want %d", i, resp.Attempts, want)
			}
		}
	})
	t.Run("stream", func(t *testing.T) {
		client := newFakeOpenAIClient(t, &fakeOpenAI{responsesSSE: []string{
			`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"It is noon."}`,
			streamCompleted,
		}}, "gpt-5-pro")

		for i, want := range []int{2, 1} {
			if res := collectStream(t, client, hi()); res.final.Attempts != want {
				t.Errorf("call %d final chunk Attempts = %d, want %d", i, res.final.Attempts, want)
			}
		}
	})
}
