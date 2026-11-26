// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const maxAutoContinuations = 10

const continueGenerationResponse = `{"status":"continue","message":"You were interrupted due to a token limit. Please review your last output and continue *exactly where you left off with no other commentary*. \nTake care with newlines. If the last output did not finish with a newline, should the next character be a newline? This is especially important for files like csv or yaml files, where a missing newline will break the parser. \nFrom the user's point of view, this continuation needs to be seamless. This function (_continue_generation()) is an internal function that you must not call - it is just here to facilitate providing this message to you.\nIn the unlikely case you have nothing additional to output, then just return nothing."}`

const implicitContinueCorrection = `_continue_generation is an internal function the system uses to resume output after a token-limit interruption - you must never invoke it yourself. Your other work this turn was processed normally. Continue the task using those results; if you have more to say, just say it directly without calling _continue_generation.`

type implicitOutcome int

const (
	implicitNone implicitOutcome = iota
	implicitHonored
	implicitCapCut
)

func isImplicitContinueName(name string) bool {
	return name == "_continue" || name == "_continue_generation"
}

func onlyImplicitContinueCalls(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, tc := range toolCalls {
		if !isImplicitContinueName(tc.Name) {
			return false
		}
	}
	return true
}

func answerImplicitContinueCalls(toolCalls []llm.ToolCall, turnHasOtherWork bool, logger *slog.Logger) (filtered []llm.ToolCall, results []llm.ChatMessage) {
	hasContinue := false
	hasOther := turnHasOtherWork
	for _, tc := range toolCalls {
		if isImplicitContinueName(tc.Name) {
			hasContinue = true
		} else {
			hasOther = true
		}
	}
	if !hasContinue || !hasOther {
		return toolCalls, nil
	}
	filtered = make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if !isImplicitContinueName(tc.Name) {
			filtered = append(filtered, tc)
			continue
		}
		logger.Warn("stripping LLM-emitted continuation call", "id", tc.ID, "name", tc.Name)
		results = append(results, llm.ChatMessage{
			Role:       "tool",
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Content:    implicitContinueCorrection,
		})
	}
	return filtered, results
}

func autoContinueStream(
	ctx context.Context,
	stream <-chan llm.StreamChunk,
	req *llm.ChatRequest,
	invoke func(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error),
	logger *slog.Logger,
	maxCont int,
) (outCh <-chan llm.StreamChunk, continuationCount *int32) {
	out := make(chan llm.StreamChunk, 8)
	var count int32
	go func() {
		defer close(out)

		limit := maxAutoContinuations
		if maxCont >= 0 && maxCont < limit {
			limit = maxCont
		}

		var textBuf strings.Builder
		var hasToolCallDeltas bool
		var continuationCount int
		var totalUsage llm.Usage
		var usageSeen bool

		currentStream := stream
		messages := make([]llm.ChatMessage, len(req.Messages))
		copy(messages, req.Messages)
		defer func() { drainStream(ctx, currentStream) }()

		for {
			chunk, ok := readChunk(ctx, currentStream)
			if !ok {
				return
			}

			if chunk.ContentDelta != "" {
				textBuf.WriteString(chunk.ContentDelta)
			}
			if len(chunk.ToolCallDeltas) > 0 {
				hasToolCallDeltas = true
			}

			if chunk.Usage != nil {
				addStreamUsage(&totalUsage, chunk.Usage, usageSeen)
				usageSeen = true
			}

			if chunk.Err != nil {
				emit(ctx, out, chunk)
				return
			}

			if chunk.FinishReason == "" {
				if !emit(ctx, out, chunk) {
					return
				}
				continue
			}

			shouldContinue := !hasToolCallDeltas &&
				continuationCount < limit &&
				(chunk.FinishReason == "max_tokens" || chunk.FinishReason == "length")

			if !shouldContinue {
				finalChunk := chunk
				if finalChunk.Usage == nil {
					finalChunk.Usage = &totalUsage
				} else {
					finalChunk.Usage = &llm.Usage{
						PromptTokens:        totalUsage.PromptTokens,
						CompletionTokens:    totalUsage.CompletionTokens,
						TotalTokens:         totalUsage.TotalTokens,
						CachedTokens:        totalUsage.CachedTokens,
						CacheWriteTokens:    totalUsage.CacheWriteTokens,
						CacheWritesReported: totalUsage.CacheWritesReported,
						ReasoningTokens:     totalUsage.ReasoningTokens,
					}
				}
				emit(ctx, out, finalChunk)
				return
			}

			if chunk.ContentDelta != "" {
				if !emit(ctx, out, llm.StreamChunk{ContentDelta: chunk.ContentDelta}) {
					return
				}
			}

			continuationCount++
			atomic.StoreInt32(&count, int32(continuationCount))
			content := textBuf.String()
			if content == "" {
				content = " "
			}
			continueCallID := fmt.Sprintf("host-continue-%s", idgen.New())

			messages = append(messages,
				llm.ChatMessage{
					Role:    "assistant",
					Content: content,
					ToolCalls: []llm.ToolCall{{
						ID:        continueCallID,
						Name:      "_continue_generation",
						Arguments: "{}",
					}},
				},
				llm.ChatMessage{
					Role:       "tool",
					ToolCallID: continueCallID,
					ToolName:   "_continue_generation",
					Content:    continueGenerationResponse,
				},
			)

			logger.Debug("auto-continue: re-invoking LLM",
				"continuation", continuationCount,
				"text_bytes", textBuf.Len(),
			)

			newReq := *req
			newReq.Messages = messages

			newStream, err := invoke(ctx, &newReq)
			if err != nil {
				emit(ctx, out, llm.StreamChunk{Err: err})
				return
			}

			drainStream(ctx, currentStream)

			textBuf.Reset()
			hasToolCallDeltas = false
			currentStream = newStream
		}
	}()
	return out, &count
}

func readChunk(ctx context.Context, stream <-chan llm.StreamChunk) (llm.StreamChunk, bool) {
	select {
	case <-ctx.Done():
		return llm.StreamChunk{}, false
	case chunk, ok := <-stream:
		return chunk, ok
	}
}

func drainStream(ctx context.Context, stream <-chan llm.StreamChunk) {
	for {
		if _, ok := readChunk(ctx, stream); !ok {
			return
		}
	}
}

func emit(ctx context.Context, out chan<- llm.StreamChunk, chunk llm.StreamChunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

func addStreamUsage(total, part *llm.Usage, seenBefore bool) {
	total.CacheWritesReported = part.CacheWritesReported && (!seenBefore || total.CacheWritesReported)
	total.PromptTokens += part.PromptTokens
	total.CompletionTokens += part.CompletionTokens
	total.TotalTokens += part.TotalTokens
	total.CachedTokens += part.CachedTokens
	total.CacheWriteTokens += part.CacheWriteTokens
	total.ReasoningTokens += part.ReasoningTokens
}
