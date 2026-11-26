// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const (
	missingUsageMessage      = "LLM call reported no usage; its spend is not recorded"
	missingUsageWarnInterval = 10 * time.Minute
)

var unreportedUsage = newUsageGapLog(missingUsageWarnInterval, time.Now)

type recordingClient struct {
	inner        llm.Client
	boundModel   string
	publisher    *Publisher
	logger       *slog.Logger
	missingUsage *usageGapLog
}

func NewRecordingClient(inner llm.Client, boundModel string, publisher *Publisher) llm.Client {
	if publisher == nil {
		return inner
	}
	return &recordingClient{
		inner:        inner,
		boundModel:   boundModel,
		publisher:    publisher,
		logger:       publisher.logger,
		missingUsage: unreportedUsage,
	}
}

func (c *recordingClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	resp, err := c.inner.ChatCompletion(ctx, req)
	if req.Spend == nil {
		return resp, err
	}
	end := callEnd{finished: err == nil, failed: err != nil}
	if resp != nil {
		end.model = resp.Model
	}
	if resp != nil && resp.Usage != nil && hasTokens(*resp.Usage) {
		end.usage, end.attempts = resp.Usage, resp.Attempts
	} else {
		end.usage, end.attempts = billedUsage(err)
	}
	c.settle(req, end)
	return resp, err
}

func (c *recordingClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	stream, err := c.inner.ChatCompletionStream(ctx, req)
	if err != nil || req.Spend == nil {
		return stream, err
	}

	out := make(chan llm.StreamChunk, 16)
	go func() {
		defer close(out)
		var end callEnd
		if forwardStream(ctx, stream, out, &end) {
			end.cancelled = true
			drainUsage(stream, &end)
		}
		c.settle(req, end)
	}()
	return out, nil
}

type callEnd struct {
	usage     *llm.Usage
	attempts  int
	model     string
	finished  bool
	failed    bool
	cancelled bool
}

func (e *callEnd) observe(chunk llm.StreamChunk) {
	if usage := reportedUsage(chunk); usage != nil {
		e.usage = usage
	}
	e.attempts = max(e.attempts, chunk.Attempts)
	e.model = cmp.Or(chunk.Model, e.model)
	if chunk.FinishReason != "" {
		e.finished = true
	}
	if chunk.Err != nil {
		e.failed = true
	}
}

func (e *callEnd) completed() bool {
	return e.finished && !e.failed && !e.cancelled
}

func forwardStream(ctx context.Context, stream <-chan llm.StreamChunk, out chan<- llm.StreamChunk, end *callEnd) (cancelled bool) {
	for chunk := range stream {
		end.observe(chunk)
		select {
		case out <- chunk:
		case <-ctx.Done():
			return true
		}
	}
	return false
}

func drainUsage(stream <-chan llm.StreamChunk, end *callEnd) {
	for chunk := range stream {
		end.observe(chunk)
	}
}

func reportedUsage(chunk llm.StreamChunk) *llm.Usage {
	if chunk.Usage != nil && hasTokens(*chunk.Usage) {
		return chunk.Usage
	}
	usage, _ := billedUsage(chunk.Err)
	return usage
}

func billedUsage(err error) (*llm.Usage, int) {
	llmErr, ok := errors.AsType[*llm.Error](err)
	if !ok || llmErr.BilledUsage == nil || !hasTokens(*llmErr.BilledUsage) {
		return nil, 0
	}
	return llmErr.BilledUsage, llmErr.Attempts
}

func hasTokens(u llm.Usage) bool {
	return TokensFromUsage(u).total() > 0
}

func (c *recordingClient) settle(req *llm.ChatRequest, end callEnd) {
	model := cmp.Or(end.model, samapp.SentModelName(req.EffectiveModel(c.boundModel)))
	if end.usage == nil {
		c.missingUsage.note(c.logger, model, end.completed())
		return
	}
	event := NewEvent(req.Spend, model, TokensFromUsage(*end.usage), end.attempts)
	event.CacheWritesReported = new(end.usage.CacheWritesReported)
	c.publisher.Record(event)
}

func NewEvent(attribution *llm.SpendAttribution, modelName string, tokens Tokens, attempts int) Event {
	e := Event{
		ID:             idgen.New(),
		CompletedTime:  time.Now().UnixMilli(),
		UserID:         attribution.UserID,
		UserSub:        attribution.UserSub,
		AgentID:        attribution.AgentID,
		AgentName:      attribution.AgentName,
		WorkflowID:     attribution.WorkflowID,
		WorkflowName:   attribution.WorkflowName,
		WorkflowNodeID: attribution.WorkflowNodeID,
		RootKind:       RootKind(attribution.RootKind),
		RootID:         attribution.RootID,
		RootName:       attribution.RootName,
		RootNodeID:     attribution.RootNodeID,
		EntrypointID:   attribution.EntrypointID,
		EntrypointName: attribution.EntrypointName,
		Channel:        Channel(attribution.Channel),
		TriggerKind:    TriggerKind(attribution.TriggerKind),
		TriggerID:      attribution.TriggerID,
		Surface:        Surface(attribution.Surface),
		ToolName:       attribution.ToolName,
		ModelKey:       attribution.ModelKey,
		ModelConfigID:  attribution.ModelConfigID,
		ModelName:      modelName,
		TaskID:         attribution.TaskID,
		ParentTaskID:   attribution.ParentTaskID,
		TraceID:        attribution.TraceID,
		SessionID:      attribution.SessionID,
		AttemptCount:   attempts,
		Tokens:         tokens,
	}
	if attribution.AvoidedInputBytes != nil {
		e.AvoidedInputBytes = new(*attribution.AvoidedInputBytes)
	}
	return e
}

type usageGapLog struct {
	models *WarnLimiter
}

func newUsageGapLog(interval time.Duration, now func() time.Time) *usageGapLog {
	return &usageGapLog{models: NewWarnLimiter(interval, now)}
}

func (l *usageGapLog) note(logger *slog.Logger, model string, completed bool) {
	if !completed {
		logger.Debug(missingUsageMessage, "modelName", model, "completed", false)
		return
	}
	if suppressed, warn := l.models.Admit(model); warn {
		logger.Warn(missingUsageMessage, "modelName", model, "completed", true, "suppressed", suppressed)
	}
}
