// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type RequestRecorder struct {
	mu       sync.Mutex
	requests []RecordedRequest
}

type RecordedRequest struct {
	Messages []llm.ChatMessage
	Tools    []llm.ToolDefinition
	Model    string
}

func NewRequestRecorder() *RequestRecorder {
	return &RequestRecorder{}
}

func (r *RequestRecorder) Record(req *llm.ChatRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	msgs := make([]llm.ChatMessage, len(req.Messages))
	copy(msgs, req.Messages)

	tools := make([]llm.ToolDefinition, len(req.Tools))
	copy(tools, req.Tools)

	r.requests = append(r.requests, RecordedRequest{
		Messages: msgs,
		Tools:    tools,
		Model:    req.Model,
	})
}

func (r *RequestRecorder) Requests() []RecordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *RequestRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}
