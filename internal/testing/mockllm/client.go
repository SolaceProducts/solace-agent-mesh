// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type FIFOEntry struct {
	Response *llm.ChatResponse
	Err      error
	Latency  time.Duration
	Pacing   StreamPacing
}

type StreamPacing struct {
	ChunkChars int
	ChunkDelay time.Duration
}

var errUnsupportedLatency = errors.New("the in-process mock LLM honours latency.chunkChars, with an optional latency.streamDelayMs, and nothing else; set the interaction's latency_ms for a delay before the answer")

func StreamPacingOf(raw json.RawMessage) (StreamPacing, error) {
	var envelope struct {
		Latency *ResponseLatency `json:"latency"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return StreamPacing{}, fmt.Errorf("parse latency: %w", err)
	}
	lat := envelope.Latency
	if lat == nil {
		return StreamPacing{}, nil
	}
	if lat.TTFTMs != nil || lat.ChunkChars == nil {
		return StreamPacing{}, errUnsupportedLatency
	}
	pacing := StreamPacing{ChunkChars: *lat.ChunkChars}
	if lat.StreamDelayMs != nil {
		pacing.ChunkDelay = time.Duration(*lat.StreamDelayMs) * time.Millisecond
	}
	return pacing, nil
}

type FIFOClient struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
	entries   []FIFOEntry
	index     int
	recorder  *RequestRecorder

	gateIndex int
	gateReady chan struct{}
	gateOpen  chan struct{}
}

func NewFIFOClient(responses []*llm.ChatResponse) *FIFOClient {
	return &FIFOClient{
		responses: responses,
		recorder:  NewRequestRecorder(),
		gateIndex: -1,
	}
}

func NewFIFOClientWithEntries(entries []FIFOEntry) *FIFOClient {
	return &FIFOClient{
		entries:   entries,
		recorder:  NewRequestRecorder(),
		gateIndex: -1,
	}
}

func (c *FIFOClient) Recorder() *RequestRecorder {
	return c.recorder
}

func (c *FIFOClient) RewriteToolCallArgs(fn func(string) string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		if c.entries[i].Response != nil {
			for j := range c.entries[i].Response.ToolCalls {
				c.entries[i].Response.ToolCalls[j].Arguments = fn(c.entries[i].Response.ToolCalls[j].Arguments)
			}
		}
	}
	for i := range c.responses {
		if c.responses[i] != nil {
			for j := range c.responses[i].ToolCalls {
				c.responses[i].ToolCalls[j].Arguments = fn(c.responses[i].ToolCalls[j].Arguments)
			}
		}
	}
}

func (c *FIFOClient) Reset(responses []*llm.ChatResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = responses
	c.entries = nil
	c.index = 0
	c.gateIndex = -1
	c.recorder.Reset()
}

func (c *FIFOClient) ResetWithEntries(entries []FIFOEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = nil
	c.entries = entries
	c.index = 0
	c.gateIndex = -1
	c.recorder.Reset()
}

func (c *FIFOClient) SetGate(index int) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gateIndex = index
	c.gateReady = make(chan struct{})
	c.gateOpen = make(chan struct{})
	return c.gateReady
}

func (c *FIFOClient) OpenGate() {
	c.mu.Lock()
	ch := c.gateOpen
	c.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (c *FIFOClient) waitAtGateReleasingLock(ctx context.Context) error {
	if c.gateIndex < 0 || c.index != c.gateIndex {
		return nil
	}
	ready := c.gateReady
	open := c.gateOpen

	select {
	case <-ready:
	default:
		close(ready)
	}

	c.mu.Unlock()
	var err error
	select {
	case <-open:
	case <-ctx.Done():
		err = ctx.Err()
	}
	c.mu.Lock()
	return err
}

func (c *FIFOClient) nextEntryLocked() FIFOEntry {
	if c.entries != nil {
		if c.index >= len(c.entries) {
			return FIFOEntry{Response: &llm.ChatResponse{
				Content:      "default response (no more primed responses)",
				FinishReason: "stop",
			}}
		}
		entry := c.entries[c.index]
		c.index++
		return entry
	}

	if c.index >= len(c.responses) {
		return FIFOEntry{Response: &llm.ChatResponse{
			Content:      "default response (no more primed responses)",
			FinishReason: "stop",
		}}
	}
	resp := c.responses[c.index]
	c.index++
	return FIFOEntry{Response: resp}
}

func (c *FIFOClient) take(ctx context.Context, req *llm.ChatRequest) (FIFOEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recorder.Record(req)
	if err := c.waitAtGateReleasingLock(ctx); err != nil {
		return FIFOEntry{}, err
	}
	return c.nextEntryLocked(), nil
}

func answer(ctx context.Context, entry FIFOEntry) (*llm.ChatResponse, error) {
	if entry.Latency > 0 {
		select {
		case <-time.After(entry.Latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if entry.Err != nil {
		return nil, entry.Err
	}
	return entry.Response, nil
}

func (c *FIFOClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	entry, err := c.take(ctx, req)
	if err != nil {
		return nil, err
	}
	return answer(ctx, entry)
}

func (c *FIFOClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	entry, err := c.take(ctx, req)
	if err != nil {
		return nil, err
	}
	resp, err := answer(ctx, entry)
	if err != nil {
		return nil, err
	}

	ch := make(chan llm.StreamChunk, 10)
	go func() {
		defer close(ch)
		if resp.ReasoningContent != "" {
			ch <- llm.StreamChunk{ReasoningDelta: resp.ReasoningContent}
		}
		if resp.Content != "" && !streamContent(ctx, ch, resp.Content, entry.Pacing) {
			return
		}
		if len(resp.ToolCalls) > 0 {
			var deltas []llm.ToolCallDelta
			for i, tc := range resp.ToolCalls {
				deltas = append(deltas, llm.ToolCallDelta{
					Index:          i,
					ID:             tc.ID,
					Name:           tc.Name,
					ArgumentsDelta: tc.Arguments,
				})
			}
			ch <- llm.StreamChunk{ToolCallDeltas: deltas}
		}
		finishReason := resp.FinishReason
		if finishReason == "" {
			finishReason = "stop"
		}
		ch <- llm.StreamChunk{
			FinishReason: finishReason,
			Usage:        resp.Usage,
		}
	}()
	return ch, nil
}

func ConvertOpenAIResponse(raw json.RawMessage) (*llm.ChatResponse, error) {
	var oai ChatCompletionResponse
	if err := json.Unmarshal(raw, &oai); err != nil {
		return nil, fmt.Errorf("parse OpenAI response: %w", err)
	}

	if len(oai.Choices) == 0 {
		return &llm.ChatResponse{FinishReason: "stop"}, nil
	}

	choice := oai.Choices[0]
	resp := &llm.ChatResponse{
		FinishReason: choice.FinishReason,
	}

	if choice.Message.Content != nil {
		resp.Content = *choice.Message.Content
	}
	if choice.Message.ReasoningContent != nil {
		resp.ReasoningContent = *choice.Message.ReasoningContent
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	if oai.Usage != nil {
		resp.Usage = &llm.Usage{
			PromptTokens:     oai.Usage.PromptTokens,
			CompletionTokens: oai.Usage.CompletionTokens,
			TotalTokens:      oai.Usage.TotalTokens,
			CachedTokens:     oai.Usage.CachedTokens,
			CacheWriteTokens: oai.Usage.CacheWriteTokens,
			ReasoningTokens:  oai.Usage.ReasoningTokens,
		}
	}

	return resp, nil
}

func ConvertOpenAIResponses(raws []json.RawMessage) ([]*llm.ChatResponse, error) {
	responses := make([]*llm.ChatResponse, 0, len(raws))
	for i, raw := range raws {
		resp, err := ConvertOpenAIResponse(raw)
		if err != nil {
			return nil, fmt.Errorf("response %d: %w", i, err)
		}
		responses = append(responses, resp)
	}
	return responses, nil
}

func streamContent(ctx context.Context, ch chan<- llm.StreamChunk, content string, pacing StreamPacing) bool {
	if pacing.ChunkChars <= 0 {
		ch <- llm.StreamChunk{ContentDelta: content}
		return true
	}
	for i, chunk := range SplitContentBySize(content, pacing.ChunkChars) {
		if i > 0 && pacing.ChunkDelay > 0 {
			timer := time.NewTimer(pacing.ChunkDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return false
		case ch <- llm.StreamChunk{ContentDelta: chunk}:
		}
	}
	return true
}
