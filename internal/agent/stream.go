// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type OutputEventType int

const (
	EventText OutputEventType = iota

	EventToolStart

	EventToolEnd

	EventBlockStart

	EventBlockContent

	EventBlockEnd

	EventEmbed

	EventThinkingContent

	EventDone

	EventError
)

type OutputEvent struct {
	Type OutputEventType

	Text string

	ToolCall *llm.ToolCall

	ToolName   string
	ToolCallID string

	ToolResult string

	Block *FencedBlockEvent

	Embed *EmbedRef

	FinishReason string

	Usage *llm.Usage

	Err error
}

func chunkAccumulator(ctx context.Context, stream <-chan llm.StreamChunk) <-chan accumulatedChunk {
	out := make(chan accumulatedChunk, 8)
	go func() {
		defer close(out)

		toolCalls := make(map[int]*llm.ToolCall)
		argBuilders := make(map[int]*strings.Builder)
		var lastFinishReason string
		var lastUsage *llm.Usage

		emit := func(c accumulatedChunk) bool {
			select {
			case out <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}

		flushArgs := func() {
			for idx, b := range argBuilders {
				toolCalls[idx].Arguments = b.String()
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-stream:
				if !ok {
					for idx, b := range argBuilders {
						toolCalls[idx].Arguments = b.String()
					}
					flushArgs()
					if !emitToolCallsSorted(toolCalls, emit) {
						return
					}
					emit(accumulatedChunk{finishReason: lastFinishReason, usage: lastUsage})
					return
				}

				if chunk.Err != nil {
					emit(accumulatedChunk{err: chunk.Err})
					return
				}

				if chunk.ReasoningDelta != "" {
					if !emit(accumulatedChunk{reasoningDelta: chunk.ReasoningDelta}) {
						return
					}
				}

				if chunk.ContentDelta != "" {
					if !emit(accumulatedChunk{text: chunk.ContentDelta}) {
						return
					}
				}

				for _, delta := range chunk.ToolCallDeltas {
					tc, exists := toolCalls[delta.Index]
					if !exists {
						tc = &llm.ToolCall{
							ID:   delta.ID,
							Name: delta.Name,
						}
						toolCalls[delta.Index] = tc
						argBuilders[delta.Index] = &strings.Builder{}
					}
					if delta.ID != "" {
						tc.ID = delta.ID
					}
					if delta.Name != "" {
						tc.Name = delta.Name
					}
					argBuilders[delta.Index].WriteString(delta.ArgumentsDelta)
				}

				if chunk.FinishReason != "" {
					for idx, b := range argBuilders {
						toolCalls[idx].Arguments = b.String()
					}
					lastFinishReason = chunk.FinishReason
					flushArgs()
					if !emitToolCallsSorted(toolCalls, emit) {
						return
					}
					toolCalls = make(map[int]*llm.ToolCall)
					argBuilders = make(map[int]*strings.Builder)
				}
				if chunk.Usage != nil {
					lastUsage = chunk.Usage
				}
			}
		}
	}()
	return out
}

func emitToolCallsSorted(toolCalls map[int]*llm.ToolCall, emit func(accumulatedChunk) bool) bool {
	indices := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indices = append(indices, idx)
	}
	slices.Sort(indices)
	for _, idx := range indices {
		if !emit(accumulatedChunk{toolCall: toolCalls[idx]}) {
			return false
		}
	}
	return true
}

type accumulatedChunk struct {
	text           string
	reasoningDelta string
	toolCall       *llm.ToolCall
	err            error
	finishReason   string
	usage          *llm.Usage
}

type outputBatcher struct {
	minBatchSize  int
	flushInterval time.Duration
}

func newOutputBatcher(minBatchSize int, flushIntervalMs ...int) *outputBatcher {
	if minBatchSize <= 0 {
		minBatchSize = 1
	}
	var interval time.Duration
	if len(flushIntervalMs) > 0 && flushIntervalMs[0] > 0 {
		interval = time.Duration(flushIntervalMs[0]) * time.Millisecond
	}
	return &outputBatcher{minBatchSize: minBatchSize, flushInterval: interval}
}

func (b *outputBatcher) run(ctx context.Context, in <-chan fencedOutput) <-chan OutputEvent {
	out := make(chan OutputEvent, 8)
	go func() {
		defer close(out)

		var textBuf strings.Builder
		var blockContentBuf strings.Builder
		var blockContentBlock *FencedBlockEvent
		var lastFinishReason string
		var lastUsage *llm.Usage
		flushedFirstText := false

		flushText := func() {
			if textBuf.Len() > 0 {
				select {
				case out <- OutputEvent{Type: EventText, Text: textBuf.String()}:
				case <-ctx.Done():
					return
				}
				textBuf.Reset()
			}
		}

		flushBlockContent := func() {
			if blockContentBuf.Len() > 0 {
				select {
				case out <- OutputEvent{Type: EventBlockContent, Text: blockContentBuf.String(), Block: blockContentBlock}:
				case <-ctx.Done():
					return
				}
				blockContentBuf.Reset()
			}
		}

		emit := func(ev OutputEvent) {
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}

		var tick <-chan time.Time
		if b.flushInterval > 0 {
			ticker := time.NewTicker(b.flushInterval)
			defer ticker.Stop()
			tick = ticker.C
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-tick:
				flushText()
				flushBlockContent()
			case fo, ok := <-in:
				if !ok {
					flushText()
					flushBlockContent()
					emit(OutputEvent{Type: EventDone, FinishReason: lastFinishReason, Usage: lastUsage})
					return
				}

				if fo.finishReason != "" {
					lastFinishReason = fo.finishReason
				}
				if fo.usage != nil {
					lastUsage = fo.usage
				}

				switch fo.outputType {
				case fencedOutputError:
					flushText()
					flushBlockContent()
					emit(OutputEvent{Type: EventError, Err: fo.err})
					return

				case fencedOutputToolCall:
					flushText()
					if fo.toolCall != nil {
						emit(OutputEvent{
							Type:       EventToolStart,
							ToolCall:   fo.toolCall,
							ToolName:   fo.toolCall.Name,
							ToolCallID: fo.toolCall.ID,
						})
					}

				case fencedOutputBlockStart:
					flushText()
					emit(OutputEvent{Type: EventBlockStart, Block: fo.block})

				case fencedOutputBlockContent:
					blockContentBuf.WriteString(fo.text)
					blockContentBlock = fo.block
					if blockContentBuf.Len() >= b.minBatchSize {
						flushBlockContent()
					}

				case fencedOutputBlockEnd:
					flushBlockContent()
					emit(OutputEvent{Type: EventBlockEnd, Block: fo.block})

				case fencedOutputReasoning:
					flushText()
					emit(OutputEvent{Type: EventThinkingContent, Text: fo.reasoningDelta})

				case fencedOutputText:
					if fo.token != nil && fo.token.Type == EmbedToken {
						flushText()
						emit(OutputEvent{Type: EventEmbed, Embed: fo.token.Embed})
						continue
					}

					textBuf.WriteString(fo.text)
					if !flushedFirstText || textBuf.Len() >= b.minBatchSize {
						flushedFirstText = true
						flushText()
					}
				}
			}
		}
	}()
	return out
}

func runStreamingPipeline(ctx context.Context, stream <-chan llm.StreamChunk, batchSize int, flushIntervalMs ...int) <-chan OutputEvent {
	accumulated := chunkAccumulator(ctx, stream)
	tokenized := embedTokenizer(ctx, accumulated)
	parsed := fencedBlockParser(ctx, tokenized)
	batcher := newOutputBatcher(batchSize, flushIntervalMs...)
	return batcher.run(ctx, parsed)
}
