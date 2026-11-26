// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2arpc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type compactRequest struct {
	TargetPercent int `json:"targetPercent,omitempty"`
}

type compactResponse struct {
	EventsCompacted        int     `json:"eventsCompacted"`
	Summary                string  `json:"summary"`
	RemainingEvents        int     `json:"remainingEvents"`
	RemainingTokens        int     `json:"remainingTokens"`
	SessionID              string  `json:"sessionId"`
	SummarizerCostUsd      float64 `json:"summarizerCostUsd"`
	SummarizerInputTokens  int     `json:"summarizerInputTokens"`
	SummarizerOutputTokens int     `json:"summarizerOutputTokens"`
}

type compactBubble struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type gatewaySessionLocks struct {
	m sync.Map
}

func (l *gatewaySessionLocks) tryLock(sessionID string) (release func(), ok bool) {
	mu, _ := l.m.LoadOrStore(sessionID, new(sync.Mutex))
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}

var compactRPCTimeoutNS atomic.Int64

func init() {
	compactRPCTimeoutNS.Store(int64(60 * time.Second))
}

func compactRPCTimeout() time.Duration {
	return time.Duration(compactRPCTimeoutNS.Load())
}

func setCompactRPCTimeout(d time.Duration) (restore func()) {
	prev := compactRPCTimeoutNS.Swap(int64(d))
	return func() { compactRPCTimeoutNS.Store(prev) }
}

func (h *sessionHandler) handleCompact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if isUnnamedSession(sessionID) {
		writeError(w, http.StatusNotFound, sessionNotFoundMsg)
		return
	}
	userID := auth.UserIDFromRequest(r, h.core.Config().DefaultUserIdentity)

	var req compactRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			badRequestFromDecode(w, err)
			return
		}
	}
	if req.TargetPercent < 0 || req.TargetPercent > 100 {
		writeError(w, http.StatusBadRequest, "targetPercent must be between 0 and 100")
		return
	}
	result, fail := h.compactSession(r.Context(), userID, sessionID, req.TargetPercent,
		h.core.RequestOrigin(contextSubject(r), userID, httputil.ClientSurface(r)))
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *sessionHandler) compactSession(ctx context.Context, userID, sessionID string, targetPercent int, origin a2a.Origin) (compactResponse, *sessionFailure) {
	session := ownedSession(ctx, h.sessRepo, h.logger, sessionID, userID)
	if session == nil {
		return compactResponse{}, failSession(http.StatusNotFound, sessionNotFoundMsg)
	}
	release, gotLock := h.compactLocks.tryLock(sessionID)
	if !gotLock {
		return compactResponse{}, failSession(http.StatusConflict, "compaction already in progress for this session")
	}
	defer release()

	if session.AgentID == nil || *session.AgentID == "" {
		return compactResponse{}, failSession(http.StatusBadGateway, "session has no associated agent")
	}
	agentName := *session.AgentID
	if !h.core.AgentRegistry().SupportsExtension(agentName, sessioncontrol.URI) {
		return compactResponse{}, failSession(http.StatusBadGateway, fmt.Sprintf("agent %q does not support manual compaction", agentName))
	}
	rpc := h.core.RPCClient()
	if rpc == nil {
		return compactResponse{}, failSession(http.StatusInternalServerError, "extension RPC client is not configured")
	}

	rpcCtx, cancel := context.WithTimeout(ctx, compactRPCTimeout())
	defer cancel()

	costTaskID := idgen.New()
	params := sessioncontrol.CompactParams{
		SessionID:     sessionID,
		TargetPercent: targetPercent,
		TaskID:        costTaskID,
		TraceID:       idgen.New(),
		Origin:        origin,
	}
	h.logger.Debug("session compaction requested", "traceID", params.TraceID, "taskID", costTaskID, "agentName", agentName)
	rawResult, callErr := rpc.Call(rpcCtx, agentName, userID, sessioncontrol.MethodCompact, sessioncontrol.URI, params)
	if callErr != nil {
		status, msg := mapCompactError(callErr)
		return compactResponse{}, failSession(status, msg)
	}
	var agentResult sessioncontrol.Result
	if err := json.Unmarshal(rawResult, &agentResult); err != nil {
		return compactResponse{}, h.loggedFailure(http.StatusBadGateway, "agent returned an unparseable response", "decode agent compact result", err, "sessionID", sessionID)
	}
	if writeErr := h.persistCompactionArtefacts(ctx, sessionID, userID, costTaskID, &agentResult); writeErr != nil {
		h.logger.Error("persist compaction artefacts", "sessionID", sessionID, "error", writeErr)
	}
	return compactResponse{
		SessionID:              sessionID,
		EventsCompacted:        agentResult.SummarizedMessagesCount,
		Summary:                agentResult.Summary,
		RemainingEvents:        agentResult.RemainingEvents,
		RemainingTokens:        agentResult.PostCompactionRemainingTokens,
		SummarizerCostUsd:      agentResult.SummarizerCostUsd,
		SummarizerInputTokens:  agentResult.SummarizerInputTokens,
		SummarizerOutputTokens: agentResult.SummarizerOutputTokens,
	}, nil
}

func mapCompactError(err error) (int, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "agent did not respond in time"
	}
	if rpcErr, ok := errors.AsType[*a2arpc.RPCError](err); ok {
		switch rpcErr.Code {
		case -32001:
			return http.StatusNotFound, sessionNotFoundMsg
		case -32002:
			return http.StatusConflict, rpcErr.Message
		case -32003:
			return http.StatusNotFound, sessionNotFoundMsg
		}
		return http.StatusBadGateway, rpcErr.Message
	}
	return http.StatusBadGateway, "compaction failed: " + err.Error()
}

func (h *sessionHandler) persistCompactionArtefacts(
	ctx context.Context,
	sessionID, userID, costTaskID string,
	result *sessioncontrol.Result,
) error {
	if h.taskRepo == nil || h.chatRepo == nil {
		return errors.New("gateway has no DB repos wired")
	}

	now := time.Now().UnixMilli()
	costInput := result.SummarizerInputTokens
	costOutput := result.SummarizerOutputTokens
	post := result.PostCompactionRemainingTokens
	cached := 0

	usageDetails := buildCompactionUsageDetails(result)
	costRow := &store.Task{
		ID:                            costTaskID,
		UserID:                        userID,
		StartTime:                     now,
		EndTime:                       &now,
		LastActivityTime:              &now,
		ExecutionMode:                 "foreground",
		SessionID:                     &sessionID,
		TotalInputTokens:              &costInput,
		TotalOutputTokens:             &costOutput,
		TotalCachedInputTokens:        &cached,
		TokenUsageDetails:             &usageDetails,
		Kind:                          store.TaskKindCompactionCost,
		PostCompactionRemainingTokens: &post,
	}
	if err := h.taskRepo.Create(ctx, costRow); err != nil {
		return fmt.Errorf("write compaction-cost task: %w", err)
	}

	bubble := compactBubble{
		ID:        idgen.New(),
		Kind:      "compaction_notification",
		Type:      "system",
		Text:      compactSummaryText(result),
		CreatedAt: epochMsToISO8601(now),
	}
	bubbleJSON, err := json.Marshal([]compactBubble{bubble})
	if err != nil {
		return fmt.Errorf("marshal compaction bubble: %w", err)
	}
	bubbleStr := string(bubbleJSON)
	notification := &store.ChatTask{
		ID:             idgen.New(),
		SessionID:      sessionID,
		UserID:         userID,
		MessageBubbles: bubbleStr,
		CreatedTime:    now,
		Kind:           store.ChatTaskKindNotification,
	}
	if err := h.chatRepo.Create(ctx, notification); err != nil {
		return fmt.Errorf("write manual-compaction chat task: %w", err)
	}

	if h.sessRepo != nil {
		if err := h.sessRepo.Touch(ctx, sessionID); err != nil {
			return fmt.Errorf("touch session: %w", err)
		}
	}
	return nil
}

func buildCompactionUsageDetails(result *sessioncontrol.Result) string {
	details := map[string]any{
		"post_compaction_remaining_tokens": result.PostCompactionRemainingTokens,
		"summarizer_cost_usd":              result.SummarizerCostUsd,
	}
	out, _ := json.Marshal(details)
	return string(out)
}

func compactSummaryText(result *sessioncontrol.Result) string {
	return fmt.Sprintf(
		"Session compacted. Summarised %d messages, freeing approximately %d tokens.",
		result.SummarizedMessagesCount, result.TokensFreed,
	)
}
