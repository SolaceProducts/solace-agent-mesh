// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPrecomputeResponseGoldenNoLatency(t *testing.T) {
	raw := json.RawMessage(`{"choices":[{"index":0,"message":{"role":"assistant","content":"Hello, world!","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":"tool_calls"}]}`)

	pc, err := PrecomputeResponse(raw, 0)
	if err != nil {
		t.Fatalf("PrecomputeResponse: %v", err)
	}

	wantFullJSON := `{"id":"chatcmpl-perf-0","object":"chat.completion","created":0,"model":"perf-test-model","choices":[{"index":0,"message":{"role":"assistant","content":"Hello, world!","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":"tool_calls"}]}`
	if string(pc.FullJSON) != wantFullJSON {
		t.Errorf("FullJSON mismatch:\n got: %s\nwant: %s", pc.FullJSON, wantFullJSON)
	}

	wantFrames := []string{
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\", wor\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ld!\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"type\":\"\",\"function\":{\"name\":\"\",\"arguments\":\"{\\\"city\\\":\\\"NYC\\\"}\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"chatcmpl-perf-0\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"perf-test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n",
		"data: [DONE]\n\n",
	}
	if len(pc.StreamFrames) != len(wantFrames) {
		t.Fatalf("frame count = %d, want %d", len(pc.StreamFrames), len(wantFrames))
	}
	for i, want := range wantFrames {
		if string(pc.StreamFrames[i]) != want {
			t.Errorf("frame[%d] mismatch:\n got: %q\nwant: %q", i, pc.StreamFrames[i], want)
		}
	}

	if pc.TTFT != nil {
		t.Errorf("TTFT = %v, want nil", *pc.TTFT)
	}
	if pc.StreamDelay != nil {
		t.Errorf("StreamDelay = %v, want nil", *pc.StreamDelay)
	}
}

func TestPrecomputeResponseLatencyDelays(t *testing.T) {
	t.Run("both delays set", func(t *testing.T) {
		raw := json.RawMessage(`{"latency":{"ttftMs":220,"streamDelayMs":18},"choices":[{"message":{"content":"hi"}}]}`)
		pc, err := PrecomputeResponse(raw, 0)
		if err != nil {
			t.Fatalf("PrecomputeResponse: %v", err)
		}
		if pc.TTFT == nil || *pc.TTFT != 220*time.Millisecond {
			t.Errorf("TTFT = %v, want 220ms", pc.TTFT)
		}
		if pc.StreamDelay == nil || *pc.StreamDelay != 18*time.Millisecond {
			t.Errorf("StreamDelay = %v, want 18ms", pc.StreamDelay)
		}
	})

	t.Run("only ttft set", func(t *testing.T) {
		raw := json.RawMessage(`{"latency":{"ttftMs":50},"choices":[{"message":{"content":"hi"}}]}`)
		pc, err := PrecomputeResponse(raw, 0)
		if err != nil {
			t.Fatalf("PrecomputeResponse: %v", err)
		}
		if pc.TTFT == nil || *pc.TTFT != 50*time.Millisecond {
			t.Errorf("TTFT = %v, want 50ms", pc.TTFT)
		}
		if pc.StreamDelay != nil {
			t.Errorf("StreamDelay = %v, want nil", *pc.StreamDelay)
		}
	})

	t.Run("empty latency object leaves both nil", func(t *testing.T) {
		raw := json.RawMessage(`{"latency":{},"choices":[{"message":{"content":"hi"}}]}`)
		pc, err := PrecomputeResponse(raw, 0)
		if err != nil {
			t.Fatalf("PrecomputeResponse: %v", err)
		}
		if pc.TTFT != nil {
			t.Errorf("TTFT = %v, want nil", *pc.TTFT)
		}
		if pc.StreamDelay != nil {
			t.Errorf("StreamDelay = %v, want nil", *pc.StreamDelay)
		}
	})
}

func TestPrecomputeResponseLatencyChunkChars(t *testing.T) {
	content := "abcdefghij"

	t.Run("per-response chunkChars", func(t *testing.T) {
		raw := json.RawMessage(`{"latency":{"chunkChars":2},"choices":[{"message":{"content":"` + content + `"}}]}`)
		pc, err := PrecomputeResponse(raw, 0)
		if err != nil {
			t.Fatalf("PrecomputeResponse: %v", err)
		}
		if want := 8; len(pc.StreamFrames) != want {
			t.Errorf("frame count = %d, want %d", len(pc.StreamFrames), want)
		}
	})

	t.Run("unset falls back to global", func(t *testing.T) {
		saved := StreamChunkChars
		StreamChunkChars = 5
		defer func() { StreamChunkChars = saved }()

		raw := json.RawMessage(`{"choices":[{"message":{"content":"` + content + `"}}]}`)
		pc, err := PrecomputeResponse(raw, 0)
		if err != nil {
			t.Fatalf("PrecomputeResponse: %v", err)
		}
		if want := 5; len(pc.StreamFrames) != want {
			t.Errorf("frame count = %d, want %d", len(pc.StreamFrames), want)
		}
	})
}

func TestBuildStreamFramesWrapperMatchesGlobal(t *testing.T) {
	saved := StreamChunkChars
	StreamChunkChars = 4
	defer func() { StreamChunkChars = saved }()

	resp := ChatCompletionResponse{
		ID:    "test",
		Model: "test-model",
		Choices: []Choice{{
			Message:      ResponseMessage{Role: "assistant", Content: strPtr("abcdefgh")},
			FinishReason: "stop",
		}},
	}

	wrapper := BuildStreamFrames(resp)
	explicit := BuildStreamFramesWithChunk(resp, StreamChunkChars)
	if len(wrapper) != len(explicit) {
		t.Fatalf("wrapper produced %d frames, explicit produced %d", len(wrapper), len(explicit))
	}
	for i := range wrapper {
		if !bytes.Equal(wrapper[i], explicit[i]) {
			t.Errorf("frame[%d] differs: wrapper=%q explicit=%q", i, wrapper[i], explicit[i])
		}
	}
}

func TestServerLatencyOverrides(t *testing.T) {
	t.Run("ttft override on non-streaming", func(t *testing.T) {
		sc := mustScenario(t, `{
			"id": "ttft-override",
			"responses": [
				{"latency": {"ttftMs": 150}, "choices": [{"message": {"content": "ok"}, "finish_reason": "stop"}]}
			]
		}`)
		srv := startServer(t, sc, 0, 0)
		elapsed := postChat(t, srv, `{"model":"m","messages":[{"role":"user","content":"[scenario:ttft-override] hi"}]}`, false)
		if elapsed < 150*time.Millisecond {
			t.Errorf("elapsed %v, want >= 150ms (per-response TTFT)", elapsed)
		}
	})

	t.Run("ttft global fallback when no latency", func(t *testing.T) {
		sc := mustScenario(t, `{
			"id": "ttft-global",
			"responses": [
				{"choices": [{"message": {"content": "ok"}, "finish_reason": "stop"}]}
			]
		}`)
		srv := startServer(t, sc, 120*time.Millisecond, 0)
		elapsed := postChat(t, srv, `{"model":"m","messages":[{"role":"user","content":"[scenario:ttft-global] hi"}]}`, false)
		if elapsed < 120*time.Millisecond {
			t.Errorf("elapsed %v, want >= 120ms (server global delay)", elapsed)
		}
	})

	t.Run("stream delay override on streaming", func(t *testing.T) {
		sc := mustScenario(t, `{
			"id": "stream-override",
			"responses": [
				{"latency": {"streamDelayMs": 40, "chunkChars": 2}, "choices": [{"message": {"content": "abcdef"}, "finish_reason": "stop"}]}
			]
		}`)
		srv := startServer(t, sc, 0, 0)
		elapsed := postChat(t, srv, `{"model":"m","messages":[{"role":"user","content":"[scenario:stream-override] hi"}],"stream":true}`, true)
		if want := 5 * 40 * time.Millisecond; elapsed < want {
			t.Errorf("elapsed %v, want >= %v (per-response stream delay)", elapsed, want)
		}
	})
}

func TestServerLatencyNotInWire(t *testing.T) {
	sc := mustScenario(t, `{
		"id": "wire-check",
		"responses": [
			{"latency": {"ttftMs": 5, "streamDelayMs": 1, "chunkChars": 3}, "choices": [{"message": {"content": "hello"}, "finish_reason": "stop"}]}
		]
	}`)
	srv := startServer(t, sc, 0, 0)

	full := postChatBody(t, srv, `{"model":"m","messages":[{"role":"user","content":"[scenario:wire-check] hi"}]}`)
	assertNoLatency(t, "non-streaming", full)

	stream := postChatBody(t, srv, `{"model":"m","messages":[{"role":"user","content":"[scenario:wire-check] hi"}],"stream":true}`)
	assertNoLatency(t, "streaming", stream)
}

func assertNoLatency(t *testing.T, label, body string) {
	t.Helper()
	for _, token := range []string{"latency", "ttftMs", "streamDelayMs", "chunkChars"} {
		if strings.Contains(body, token) {
			t.Errorf("%s response leaked %q into the wire body: %s", label, token, body)
		}
	}
}

func mustScenario(t *testing.T, s string) *Scenario {
	t.Helper()
	sc, err := ParseScenario([]byte(s))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	return sc
}

func startServer(t *testing.T, sc *Scenario, delay, streamDelay time.Duration) *MockLLMServer {
	t.Helper()
	srv := NewMockLLMServer("127.0.0.1:0", delay, streamDelay, slog.Default())
	srv.LoadScenarios(sc)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func postChat(t *testing.T, srv *MockLLMServer, body string, stream bool) time.Duration {
	t.Helper()
	start := time.Now()
	resp, err := http.Post("http://"+srv.Addr()+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return time.Since(start)
}

func postChatBody(t *testing.T, srv *MockLLMServer, body string) string {
	t.Helper()
	resp, err := http.Post("http://"+srv.Addr()+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(out)
}
