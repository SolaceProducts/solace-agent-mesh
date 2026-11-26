// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

const chatCompletionObject = "chat.completion"

func CompletionHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(request))
		AnswerCompletion(w, request, []byte(body))
	}
}

func AnswerCompletion(w http.ResponseWriter, request, body []byte) {
	var asked struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(request, &asked)

	var completion ChatCompletionResponse
	if !asked.Stream || json.Unmarshal(body, &completion) != nil || completion.Object != chatCompletionObject {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, frame := range BuildStreamFrames(completion) {
		_, _ = w.Write(frame)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
