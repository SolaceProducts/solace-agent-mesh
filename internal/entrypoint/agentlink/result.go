// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm/modelinfo"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	codeInternalError      = -32603
	msgInternalError       = "internal error"
	maxFailureMessageBytes = 2048
)

func (s *agentState) handleResult(ctx context.Context, c *conn, f inFrame) {
	if f.State != stateCompleted && f.State != stateFailed && f.State != stateCanceled {
		c.sendError(errCodeBadMessage, "state must be completed, failed or canceled", f.TaskID)
		return
	}
	parts, problem := resultParts(f)
	if problem != "" {
		c.sendError(errCodeBadMessage, problem, f.TaskID)
		return
	}
	s.mu.Lock()
	b, refusal := s.openTaskLocked(c, typeResult, f.TaskID)
	if b == nil {
		s.mu.Unlock()
		c.sendError(errCodeUnknownTask, refusal, f.TaskID)
		return
	}
	if b.replyTo == "" {
		s.mu.Unlock()
		c.sendError(errCodeBadMessage, "the task has no reply address", f.TaskID)
		return
	}
	run := s.runLocked(f.TaskID, b)
	resp := s.answerLocked(f, b, run, parts)
	b.final, b.closedAt = true, s.a.now()
	up := a2a.DecodeUserProperties(b.props)
	replyTo := b.replyTo
	finalStatus := b.request.runBased && resp.Error == nil && f.State != stateCanceled && !b.finalStatusSent
	b.finalStatusSent = b.finalStatusSent || finalStatus
	s.mu.Unlock()

	if finalStatus {
		s.boundedPublish(ctx, func(ctx context.Context) { run.sp.PublishFinal(ctx, f.Text) })
	}
	if err := s.publishAnswer(ctx, replyTo, resp, up, s.answerSeq(b, f.TaskID)); err != nil {
		s.a.logger.Warn("agent link result publish failed", "agentName", s.cfg.Name, "taskID", f.TaskID, "traceID", up.TraceID, "error", err)
		s.reopenUnanswered(b, failedByStandDown)
		c.sendError(errCodePublishFailed, "the result could not be delivered; send it again", f.TaskID)
		return
	}
	s.endTask(f.TaskID)
}

func resultParts(f inFrame) ([]a2a.Part, string) {
	var parts []a2a.Part
	for _, file := range f.Files {
		if _, err := base64.StdEncoding.DecodeString(file.Bytes); err != nil || file.Bytes == "" {
			return nil, "a file carries its content as base64 bytes"
		}
		parts = append(parts, a2a.NewFilePartWithBytes(file.Bytes, file.Name, file.MimeType))
	}
	if len(f.Data) > 0 && string(f.Data) != "null" {
		var data any
		if err := decodeNumbers(f.Data, &data); err != nil {
			return nil, "data must be JSON"
		}
		if carriesUIShape(data) {
			return nil, "data must not contain an object with the type of a SAM signal"
		}
		if key := taskRecordKeyIn(data); key != "" {
			return nil, "data must not contain a metadata object naming " + key + ", which SAM records for the task"
		}
		parts = append(parts, a2a.NewDataPart(data))
	}
	return append(parts, a2a.NewTextPart(f.Text)), ""
}

func (s *agentState) answerLocked(f inFrame, b *taskBinding, run *taskRun, parts []a2a.Part) *a2a.Response {
	contextID := b.request.message.ContextID
	switch {
	case f.State == stateCanceled:
		metadata := map[string]any{"agent_name": s.cfg.Name}
		if totals := run.totalsLocked(); totals != nil {
			metadata["token_usage"] = totals
		}
		run.facts.AddTo(metadata)
		return agent.CancelledResponse(f.TaskID, contextID, metadata)
	case f.State == stateFailed && (f.Error == nil || f.Error.Category == ""):
		var described string
		if f.Error != nil {
			described = f.Error.Message
		}
		message := cmp.Or(redactedText(described), msgInternalError)
		message, _ = tool.CapText(message, maxFailureMessageBytes)
		return run.errorResponseLocked(f.TaskID, message)
	case f.State == stateFailed && f.Error != nil:
		category := f.Error.Category
		if !modelFailureCategories[category] {
			category = llm.ErrorCategoryUnknown
		}
		resp := run.errorResponseLocked(f.TaskID, modelFailureMessage(category))
		agent.AttachFailureCategory(resp, category, f.Error.Provider)
		return resp
	default:
		metadata := map[string]any{"agent_name": s.cfg.Name}
		run.facts.AddTo(metadata)
		if totals := run.totalsLocked(); totals != nil {
			totals["by_model"] = run.byModelLocked()
			metadata["token_usage"] = totals
		}
		return agent.FinalResponse(f.TaskID, contextID, a2a.TaskState(f.State), parts, metadata)
	}
}

func (r *taskRun) errorResponseLocked(taskID, message string) *a2a.Response {
	resp := a2a.NewErrorResponse(taskID, codeInternalError, message)
	data := map[string]any{}
	if totals := r.totalsLocked(); totals != nil {
		data["token_usage"] = totals
	}
	r.facts.AddTo(data)
	if len(data) > 0 {
		resp.Error.Data = data
	}
	return resp
}

func modelFailureMessage(category string) string {
	if category == llm.ErrorCategoryContextOverflow {
		return llm.ContextLimitUserMessage
	}
	return llm.UserFacingErrorMessage(&llm.Error{Message: "the model call failed (" + category + ")"})
}

func (r *taskRun) totalsLocked() map[string]any {
	if !agent.UsageRecorded(r.total) {
		return nil
	}
	totals := agent.TokenUsageTotals(r.total)
	totals["last_input_tokens"] = r.lastInput
	return totals
}

func (r *taskRun) byModelLocked() map[string]any {
	out := make(map[string]any, len(r.byModel))
	for model, u := range r.byModel {
		entry := agent.ByModelUsageEntry(u)
		if maxInput, ok := modelinfo.MaxInputTokens(model); ok {
			entry["max_input_tokens"] = maxInput
		}
		out[model] = entry
	}
	return out
}

func (s *agentState) answerSeq(b *taskBinding, taskID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.answerSeq == 0 {
		b.answerSeq = s.seq.Next(taskID)
	}
	return b.answerSeq
}

func (s *agentState) publishAnswer(ctx context.Context, replyTo string, resp *a2a.Response, up a2a.UserProperties, seq uint64) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	props := agent.ResponseUserProperties(s.cfg.Name, up)
	a2a.AddEventSeq(props, s.cfg.Name, seq)
	return s.publish(ctx, replyTo, body, props, true)
}
