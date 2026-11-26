// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitContent(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		n        int
		expected []string
	}{
		{
			name:     "empty string",
			text:     "",
			n:        3,
			expected: []string{""},
		},
		{
			name:     "single char into 3",
			text:     "a",
			n:        3,
			expected: []string{"a"},
		},
		{
			name:     "short text fewer chars than n",
			text:     "ab",
			n:        3,
			expected: []string{"ab"},
		},
		{
			name:     "exact split",
			text:     "abcdef",
			n:        3,
			expected: []string{"ab", "cd", "ef"},
		},
		{
			name:     "uneven split",
			text:     "abcdefg",
			n:        3,
			expected: []string{"abc", "def", "g"},
		},
		{
			name:     "n is zero defaults to 1",
			text:     "hello",
			n:        0,
			expected: []string{"hello"},
		},
		{
			name:     "n is negative defaults to 1",
			text:     "hello",
			n:        -5,
			expected: []string{"hello"},
		},
		{
			name:     "n is 1",
			text:     "hello",
			n:        1,
			expected: []string{"hello"},
		},
		{
			name:     "split into more chunks than chars",
			text:     "ab",
			n:        10,
			expected: []string{"ab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitContent(tt.text, tt.n)
			if len(result) != len(tt.expected) {
				t.Fatalf("got %d chunks, want %d: %v", len(result), len(tt.expected), result)
			}
			for i, chunk := range result {
				if chunk != tt.expected[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, chunk, tt.expected[i])
				}
			}
		})
	}
}

func TestSplitContentBySize(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		size     int
		expected []string
	}{
		{name: "empty string", text: "", size: 4, expected: []string{""}},
		{name: "shorter than size", text: "abc", size: 4, expected: []string{"abc"}},
		{name: "exact size", text: "abcd", size: 4, expected: []string{"abcd"}},
		{name: "even multiple", text: "abcdefgh", size: 4, expected: []string{"abcd", "efgh"}},
		{name: "remainder", text: "abcdefghi", size: 4, expected: []string{"abcd", "efgh", "i"}},
		{name: "size zero returns whole", text: "hello", size: 0, expected: []string{"hello"}},
		{name: "size negative returns whole", text: "hello", size: -3, expected: []string{"hello"}},
		{
			name:     "multi-byte runes never split mid-byte, or json.Marshal emits U+FFFD",
			text:     "«—»",
			size:     1,
			expected: []string{"«", "—", "»"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitContentBySize(tt.text, tt.size)
			if len(result) != len(tt.expected) {
				t.Fatalf("got %d chunks, want %d: %q", len(result), len(tt.expected), result)
			}
			var reassembled string
			for i, chunk := range result {
				if chunk != tt.expected[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, chunk, tt.expected[i])
				}
				reassembled += chunk
			}
			if reassembled != tt.text {
				t.Errorf("reassembled %q, want %q", reassembled, tt.text)
			}
		})
	}
}

func TestExtractScenarioFromMessages(t *testing.T) {
	tests := []struct {
		name            string
		messages        []RequestMessage
		wantScenarioID  string
		wantInlineCount int
		wantTurnIndex   int
	}{
		{
			name:     "empty messages",
			messages: nil,
		},
		{
			name: "test_case_id in system message",
			messages: []RequestMessage{
				{Role: "system", Content: "You are a helper [test_case_id=test.123]"},
				{Role: "user", Content: "Hello"},
			},
			wantScenarioID: "test.123",
			wantTurnIndex:  0,
		},
		{
			name: "scenario tag in user message",
			messages: []RequestMessage{
				{Role: "system", Content: "You are a helper"},
				{Role: "user", Content: "[scenario:my-scenario] Hello"},
			},
			wantScenarioID: "my-scenario",
			wantTurnIndex:  0,
		},
		{
			name: "scenario tag overrides test_case_id when later",
			messages: []RequestMessage{
				{Role: "system", Content: "[test_case_id=old]"},
				{Role: "user", Content: "[scenario:new]"},
			},
			wantScenarioID: "new",
			wantTurnIndex:  0,
		},
		{
			name: "turn index counts assistant messages",
			messages: []RequestMessage{
				{Role: "system", Content: "[test_case_id=test1]"},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
				{Role: "user", Content: "More"},
				{Role: "assistant", Content: "Sure"},
				{Role: "user", Content: "Again"},
			},
			wantScenarioID: "test1",
			wantTurnIndex:  2,
		},
		{
			name: "inline responses via responses_json",
			messages: []RequestMessage{
				{Role: "system", Content: func() string {
					responses := []json.RawMessage{
						json.RawMessage(`{"choices":[{"message":{"content":"resp1"}}]}`),
					}
					data, _ := json.Marshal(responses)
					encoded := base64.StdEncoding.EncodeToString(data)
					return "[responses_json=" + encoded + "]"
				}()},
				{Role: "user", Content: "Hello"},
			},
			wantInlineCount: 1,
			wantTurnIndex:   0,
		},
		{
			name: "no markers found",
			messages: []RequestMessage{
				{Role: "system", Content: "You are a helpful assistant"},
				{Role: "user", Content: "Hello world"},
			},
			wantScenarioID:  "",
			wantInlineCount: 0,
			wantTurnIndex:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenarioID, inlineResponses, turnIndex := ExtractScenarioFromMessages(tt.messages)

			if scenarioID != tt.wantScenarioID {
				t.Errorf("scenarioID = %q, want %q", scenarioID, tt.wantScenarioID)
			}
			if len(inlineResponses) != tt.wantInlineCount {
				t.Errorf("inlineResponses count = %d, want %d", len(inlineResponses), tt.wantInlineCount)
			}
			if turnIndex != tt.wantTurnIndex {
				t.Errorf("turnIndex = %d, want %d", turnIndex, tt.wantTurnIndex)
			}
		})
	}
}

func TestBuildStreamFrames(t *testing.T) {
	tests := []struct {
		name           string
		resp           ChatCompletionResponse
		wantMinFrames  int
		wantDoneSuffix bool
	}{
		{
			name: "empty choices produces role + finish + done",
			resp: ChatCompletionResponse{
				ID:    "test-1",
				Model: "test-model",
			},
			wantMinFrames:  3,
			wantDoneSuffix: true,
		},
		{
			name: "text content produces role + content chunks + finish + done",
			resp: ChatCompletionResponse{
				ID:    "test-2",
				Model: "test-model",
				Choices: []Choice{
					{
						Message: ResponseMessage{
							Role:    "assistant",
							Content: strPtr("Hello, world!"),
						},
						FinishReason: "stop",
					},
				},
			},
			wantMinFrames:  4,
			wantDoneSuffix: true,
		},
		{
			name: "tool calls produce 2 frames per tool call",
			resp: ChatCompletionResponse{
				ID:    "test-3",
				Model: "test-model",
				Choices: []Choice{
					{
						Message: ResponseMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: FunctionCall{
										Name:      "get_weather",
										Arguments: `{"city": "NYC"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			},
			wantMinFrames:  4,
			wantDoneSuffix: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := BuildStreamFrames(tt.resp)
			if len(frames) < tt.wantMinFrames {
				t.Errorf("got %d frames, want at least %d", len(frames), tt.wantMinFrames)
			}
			if tt.wantDoneSuffix {
				lastFrame := string(frames[len(frames)-1])
				if lastFrame != "data: [DONE]\n\n" {
					t.Errorf("last frame = %q, want %q", lastFrame, "data: [DONE]\n\n")
				}
			}
			for i, frame := range frames {
				s := string(frame)
				if s == "data: [DONE]\n\n" {
					continue
				}
				if len(s) < 6 || s[:6] != "data: " {
					t.Errorf("frame[%d] doesn't start with 'data: ': %q", i, s[:min(30, len(s))])
				}
			}
		})
	}
}

func TestStripScenarioTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[scenario:test1] Hello", "Hello"},
		{"Hello [scenario:test1] world", "Hello  world"},
		{"No tag here", "No tag here"},
		{"", ""},
	}

	for _, tt := range tests {
		got := StripScenarioTag(tt.input)
		if got != tt.want {
			t.Errorf("StripScenarioTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseScenario(t *testing.T) {
	t.Run("valid scenario", func(t *testing.T) {
		data := []byte(`{
			"id": "test-scenario",
			"description": "A test",
			"responses": [
				{"choices": [{"message": {"role": "assistant", "content": "Hello"}, "finish_reason": "stop"}]}
			]
		}`)
		sc, err := ParseScenario(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sc.ID != "test-scenario" {
			t.Errorf("ID = %q, want %q", sc.ID, "test-scenario")
		}
		if len(sc.Precomputed) != 1 {
			t.Errorf("Precomputed count = %d, want 1", len(sc.Precomputed))
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ParseScenario([]byte(`not json`))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestSSEFrame(t *testing.T) {
	chunk := ChatCompletionChunk{
		ID:     "test-id",
		Object: "chat.completion.chunk",
		Model:  "test-model",
	}
	frame := SSEFrame(chunk)
	s := string(frame)
	if len(s) < 6 || s[:6] != "data: " {
		t.Errorf("frame should start with 'data: ', got: %q", s[:min(20, len(s))])
	}
	if s[len(s)-2:] != "\n\n" {
		t.Error("frame should end with double newline")
	}
	jsonPart := s[6 : len(s)-2]
	var parsed ChatCompletionChunk
	if err := json.Unmarshal([]byte(jsonPart), &parsed); err != nil {
		t.Errorf("frame JSON is invalid: %v", err)
	}
	if parsed.ID != "test-id" {
		t.Errorf("parsed ID = %q, want %q", parsed.ID, "test-id")
	}
}

func strPtr(s string) *string { return &s }

func TestPrecomputeResponseFinishReason(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain text without finish_reason defaults to stop",
			raw:  `{"choices":[{"message":{"content":"Hello"}}]}`,
			want: "stop",
		},
		{
			name: "tool calls without finish_reason default to tool_calls",
			raw:  `{"choices":[{"message":{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}}]}`,
			want: "tool_calls",
		},
		{
			name: "explicit finish_reason is preserved",
			raw:  `{"choices":[{"message":{"content":"Hel"},"finish_reason":"length"}]}`,
			want: "length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc, err := PrecomputeResponse(json.RawMessage(tt.raw), 0)
			if err != nil {
				t.Fatalf("PrecomputeResponse: %v", err)
			}

			var full ChatCompletionResponse
			if err := json.Unmarshal(pc.FullJSON, &full); err != nil {
				t.Fatalf("unmarshal FullJSON: %v", err)
			}
			if got := full.Choices[0].FinishReason; got != tt.want {
				t.Errorf("FullJSON finish_reason = %q, want %q", got, tt.want)
			}

			if got := streamedFinishReason(t, pc.StreamFrames); got != tt.want {
				t.Errorf("stream finish_reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func streamedFinishReason(t *testing.T, frames [][]byte) string {
	t.Helper()
	var reasons []string
	for _, frame := range frames {
		payload := strings.TrimSpace(strings.TrimPrefix(string(frame), "data: "))
		if payload == "[DONE]" {
			continue
		}
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("unmarshal frame %q: %v", frame, err)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				reasons = append(reasons, *choice.FinishReason)
			}
		}
	}
	if len(reasons) != 1 {
		t.Fatalf("stream carried %d finish reasons %q, want exactly 1", len(reasons), reasons)
	}
	return reasons[0]
}
