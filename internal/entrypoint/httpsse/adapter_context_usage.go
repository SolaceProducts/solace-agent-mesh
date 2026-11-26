// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func (a *HTTPSSEAdapter) augmentFinalResponseWithContextUsage(
	ctx context.Context,
	taskID string,
	task *a2a.Task,
	sessionID, userID string,
	raw []byte,
) []byte {
	if a.taskRepo == nil || a.chatRepo == nil {
		return nil
	}
	if sessionID == "" || userID == "" {
		return nil
	}
	if task == nil {
		return nil
	}
	synthetic := syntheticTaskFromFinalResponse(taskID, sessionID, userID, task)
	if synthetic == nil {
		return nil
	}
	if hasOnlyDecisionUsage(synthetic) {
		return nil
	}

	cu := a.computeFinalResponseContextUsage(ctx, taskID, synthetic, sessionID, userID)
	if cu == nil {
		return nil
	}
	return injectContextUsage(raw, cu)
}

func (a *HTTPSSEAdapter) computeFinalResponseContextUsage(
	ctx context.Context,
	taskID string,
	synthetic *store.Task,
	sessionID, userID string,
) *ContextUsageResponse {
	priorInput, priorOutput, err := a.taskRepo.SumTokensBySessionForUser(ctx, sessionID, userID, taskID)
	if err != nil {
		a.logger.Warn("context_usage: sum prior task tokens", "sessionID", sessionID, "error", err)
		return nil
	}

	aggregate := &store.Task{TotalInputTokens: &priorInput, TotalOutputTokens: &priorOutput}
	tasks := []*store.Task{synthetic, aggregate}

	chatCount, err := a.chatRepo.CountBySessionForUser(ctx, sessionID, userID)
	if err != nil {
		a.logger.Warn("context_usage: count chat tasks", "sessionID", sessionID, "error", err)
	}

	cfg := a.core.Config()
	resp := buildContextUsage(ctx, sessionID, tasks, chatCount, "", defaultModelFromConfig(cfg))
	return &resp
}

func syntheticTaskFromFinalResponse(taskID, sessionID, userID string, task *a2a.Task) *store.Task {
	if task == nil {
		return nil
	}
	usage, ok := task.Metadata["token_usage"].(map[string]any)
	if !ok || usage == nil {
		return nil
	}

	in, hasIn := readNumber(usage["total_input_tokens"])
	out, hasOut := readNumber(usage["total_output_tokens"])
	cached, hasCached := readNumber(usage["total_cached_input_tokens"])
	if !hasIn && !hasOut && !hasCached {
		return nil
	}

	t := &store.Task{
		ID:        taskID,
		UserID:    userID,
		SessionID: &sessionID,
	}
	if hasIn {
		v := in
		t.TotalInputTokens = &v
	}
	if hasOut {
		v := out
		t.TotalOutputTokens = &v
	}
	if hasCached {
		v := cached
		t.TotalCachedInputTokens = &v
	}
	if details, err := json.Marshal(usage); err == nil {
		s := string(details)
		t.TokenUsageDetails = &s
	}
	return t
}

func injectContextUsage(raw []byte, cu *ContextUsageResponse) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw
	}
	cuJSON, err := json.Marshal(cu)
	if err != nil {
		return raw
	}
	envelope["context_usage"] = cuJSON
	out, err := json.Marshal(envelope)
	if err != nil {
		return raw
	}
	return out
}
