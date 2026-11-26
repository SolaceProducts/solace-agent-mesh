// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const answerBody = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o",
	"choices":[{"index":0,"message":{"role":"assistant","content":"It is 3pm."},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`

func answered(t *testing.T, request, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(request))
	CompletionHandler(body)(rec, req)
	return rec
}

func TestAnswerCompletion_StreamedRequestGetsSSEFramesCarryingTheAnswer(t *testing.T) {
	rec := answered(t, `{"stream":true}`, answerBody)

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	var content strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, isFrame := strings.CutPrefix(line, "data: ")
		var chunk ChatCompletionChunk
		if !isFrame || json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
		}
	}
	if content.String() != "It is 3pm." {
		t.Errorf("streamed content = %q, want %q", content.String(), "It is 3pm.")
	}
	for _, want := range []string{`"finish_reason":"stop"`, `"total_tokens":14`, "data: [DONE]"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("stream lacks %q: %s", want, rec.Body.String())
		}
	}
}

func TestAnswerCompletion_UnstreamedRequestGetsTheBodyAsItIs(t *testing.T) {
	rec := answered(t, `{"stream":false}`, answerBody)

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if rec.Body.String() != answerBody {
		t.Errorf("body = %s, want the answer unchanged", rec.Body.String())
	}
}

func TestAnswerCompletion_ABodyThatIsNotAChatCompletionIsNeverStreamed(t *testing.T) {
	const modelList = `{"data":[{"id":"gpt-4o"}]}`
	rec := answered(t, `{"stream":true}`, modelList)

	if rec.Body.String() != modelList || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("got %q (%s), want the model list unchanged", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}
