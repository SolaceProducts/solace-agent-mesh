// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"math"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm/modelinfo"
)

type ContextUsageResponse struct {
	SessionID            string  `json:"sessionId"`
	CurrentContextTokens int     `json:"currentContextTokens"`
	PromptTokens         int     `json:"promptTokens"`
	CompletionTokens     int     `json:"completionTokens"`
	CachedTokens         int     `json:"cachedTokens"`
	MaxInputTokens       *int    `json:"maxInputTokens"`
	UsagePercentage      float64 `json:"usagePercentage"`
	Model                string  `json:"model"`
	TotalEvents          int     `json:"totalEvents"`
	TotalMessages        int     `json:"totalMessages"`
	TotalTasks           int     `json:"totalTasks"`
	HasCompaction        bool    `json:"hasCompaction"`
}

func (h *sessionHandler) handleContextUsage(w http.ResponseWriter, r *http.Request) {
	usage, fail := h.contextUsage(r.Context(), resolveUserID(r, h.core), r.PathValue("id"), r.URL.Query().Get("model"))
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (h *sessionHandler) contextUsage(ctx context.Context, userID, sessionID, queryModel string) (ContextUsageResponse, *sessionFailure) {
	if ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID) == nil {
		return ContextUsageResponse{}, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	tasks, err := h.taskRepo.ListBySessionForUserWithTokens(ctx, sessionID, userID)
	if err != nil {
		h.logger.Error("list session tasks", "sessionID", sessionID, "error", err)
		return ContextUsageResponse{}, failSession(http.StatusInternalServerError, "failed to get context usage")
	}
	chatCount, err := h.chatRepo.CountBySessionForUser(ctx, sessionID, userID)
	if err != nil {
		h.logger.Error("count chat tasks", "sessionID", sessionID, "error", err)
		return ContextUsageResponse{}, failSession(http.StatusInternalServerError, "failed to get context usage")
	}
	return buildContextUsage(ctx, sessionID, tasks, chatCount, queryModel, defaultModelFromConfig(h.core.Config())), nil
}

func defaultModelFromConfig(cfg *entrypoint.GatewayConfig) string {
	if cfg == nil {
		return ""
	}
	switch m := cfg.ModelRaw.(type) {
	case string:
		return m
	case map[string]any:
		if s, ok := m["model"].(string); ok {
			return s
		}
	}
	return ""
}

func buildContextUsage(
	_ context.Context,
	sessionID string,
	tasks []*store.Task,
	chatCount int,
	queryModel string,
	defaultModel string,
) ContextUsageResponse {
	resp := ContextUsageResponse{
		SessionID:     sessionID,
		Model:         firstNonEmpty(queryModel, defaultModel),
		TotalMessages: chatCount * 2,
		TotalTasks:    chatCount,
	}
	if len(tasks) == 0 {
		return resp
	}

	var (
		promptSum     int
		completionSum int
	)
	for _, t := range tasks {
		if t.TotalInputTokens != nil {
			promptSum += *t.TotalInputTokens
		}
		if t.TotalOutputTokens != nil {
			completionSum += *t.TotalOutputTokens
		}
	}
	resp.PromptTokens = promptSum
	resp.CompletionTokens = completionSum

	latest := tasks[0]
	realLatest := latest
	for _, t := range tasks {
		if t.Kind != store.TaskKindCompactionCost {
			realLatest = t
			break
		}
	}

	if realLatest.TotalCachedInputTokens != nil {
		resp.CachedTokens = *realLatest.TotalCachedInputTokens
	}

	if contextTask := newestTaskWithContext(tasks); contextTask == nil {
		resp.CurrentContextTokens = 0
	} else if contextTask.Kind == store.TaskKindCompactionCost {
		resp.CurrentContextTokens = readPostCompactionRemaining(contextTask)
	} else if v, ok := readLastInputTokens(contextTask); ok {
		resp.CurrentContextTokens = v
	} else if contextTask.TotalInputTokens != nil {
		resp.CurrentContextTokens = *contextTask.TotalInputTokens
	}

	modelTask := newestTaskWithModel(tasks)
	if queryModel == "" {
		if dominant, ok := dominantModelFromUsage(modelTask); ok {
			resp.Model = dominant
		}
	}
	if resp.Model == "" {
		resp.Model = defaultModel
	}
	stampedMax := stampedMaxInputForModel(modelTask, resp.Model)

	resp.MaxInputTokens = resolveMaxInputForResponse(resp.Model, stampedMax)
	resp.UsagePercentage = computeUsagePercentage(resp.CurrentContextTokens, resp.MaxInputTokens)
	return resp
}

func newestTaskWithContext(tasks []*store.Task) *store.Task {
	for _, t := range tasks {
		if !hasOnlyDecisionUsage(t) {
			return t
		}
	}
	return nil
}

func newestTaskWithModel(tasks []*store.Task) *store.Task {
	for _, t := range tasks {
		if t.Kind == store.TaskKindCompactionCost {
			continue
		}
		if _, ok := dominantModelFromUsage(t); ok {
			return t
		}
	}
	return nil
}

func resolveMaxInputForResponse(modelID string, stamped int) *int {
	if stamped > 0 {
		v := stamped
		return &v
	}
	if v, ok := modelinfo.MaxInputTokens(modelID); ok {
		return &v
	}
	return nil
}

func computeUsagePercentage(current int, max *int) float64 {
	if max == nil || *max <= 0 || current <= 0 {
		return 0
	}
	pct := float64(current) / float64(*max) * 100
	pct = math.Round(pct*10) / 10
	if pct > 100 {
		return 100
	}
	return pct
}

func dominantModelFromUsage(t *store.Task) (string, bool) {
	if t == nil || t.TokenUsageDetails == nil || *t.TokenUsageDetails == "" {
		return "", false
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(*t.TokenUsageDetails), &details); err != nil {
		return "", false
	}
	by, _ := details["by_model"].(map[string]any)
	bestModel := ""
	bestTokens := -1
	for model, raw := range by {
		entry, ok := raw.(map[string]any)
		if !ok || isDecisionUsage(entry) || isToolUsage(entry) {
			continue
		}
		toks, _ := readNumber(entry["input_tokens"])
		if toks > bestTokens {
			bestTokens = toks
			bestModel = model
		}
	}
	if bestModel == "" {
		return "", false
	}
	return bestModel, true
}

func isDecisionUsage(entry map[string]any) bool {
	return entry["kind"] == "decision"
}

func isToolUsage(entry map[string]any) bool {
	return entry["kind"] == "tool"
}

func hasOnlyDecisionUsage(t *store.Task) bool {
	if t == nil || t.TokenUsageDetails == nil || *t.TokenUsageDetails == "" {
		return false
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(*t.TokenUsageDetails), &details); err != nil {
		return false
	}
	by, _ := details["by_model"].(map[string]any)
	if len(by) == 0 {
		return false
	}
	for _, raw := range by {
		if entry, ok := raw.(map[string]any); !ok || !isDecisionUsage(entry) {
			return false
		}
	}
	return true
}

func stampedMaxInputForModel(t *store.Task, modelID string) int {
	if t == nil || t.TokenUsageDetails == nil || *t.TokenUsageDetails == "" {
		return 0
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(*t.TokenUsageDetails), &details); err != nil {
		return 0
	}
	by, _ := details["by_model"].(map[string]any)
	if entry, ok := by[modelID].(map[string]any); ok {
		if v, ok := readNumber(entry["max_input_tokens"]); ok && v > 0 {
			return v
		}
	}
	for _, raw := range by {
		entry, ok := raw.(map[string]any)
		if !ok || isToolUsage(entry) {
			continue
		}
		if v, ok := readNumber(entry["max_input_tokens"]); ok && v > 0 {
			return v
		}
	}
	return 0
}

func readLastInputTokens(t *store.Task) (int, bool) {
	if t == nil || t.TokenUsageDetails == nil || *t.TokenUsageDetails == "" {
		return 0, false
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(*t.TokenUsageDetails), &details); err != nil {
		return 0, false
	}
	v, ok := readNumber(details["last_input_tokens"])
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}

func readPostCompactionRemaining(t *store.Task) int {
	if t == nil || t.TokenUsageDetails == nil || *t.TokenUsageDetails == "" {
		return 0
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(*t.TokenUsageDetails), &details); err != nil {
		return 0
	}
	v, _ := readNumber(details["post_compaction_remaining_tokens"])
	return v
}

func readNumber(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
