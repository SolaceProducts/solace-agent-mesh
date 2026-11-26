// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type sessionCompactionLocks struct {
	m sync.Map
}

func (l *sessionCompactionLocks) tryLock(sessionID string) (unlock func(), ok bool) {
	mu, _ := l.m.LoadOrStore(sessionID, new(sync.Mutex))
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}

func (a *Agent) compactionLocksMap() *sessionCompactionLocks {
	if a.compactionLocks == nil {
		a.compactionLocks = &sessionCompactionLocks{}
	}
	return a.compactionLocks
}

func (a *Agent) CompactSession(ctx context.Context, info extensions.RequestInfo, params sessioncontrol.CompactParams) (*sessioncontrol.Result, error) {
	sessionID, targetPercent := params.SessionID, params.TargetPercent
	if sessionID == "" {
		return nil, &extensions.Error{Code: -32602, Message: "sessionId is required"}
	}
	if a.svc == nil || a.svc.Sessions() == nil {
		return nil, &extensions.Error{
			Code:    extensions.CodeInternal,
			Message: "session store is not configured on this agent",
		}
	}

	unlock, gotLock := a.compactionLocksMap().tryLock(sessionID)
	if !gotLock {
		return nil, &extensions.Error{
			Code:    extensions.CodeConflict,
			Message: "compaction already in progress for this session",
			Data:    map[string]any{"sessionId": sessionID},
		}
	}
	defer unlock()

	userID := sessionUserID(info.UserID)

	sess, err := a.svc.Sessions().Get(ctx, a.cfg.Name, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if sess == nil {
		return nil, &extensions.Error{
			Code:    extensions.CodeNotFound,
			Message: "session not found",
			Data:    map[string]any{"sessionId": sessionID},
		}
	}

	cfg := CompactionConfig{Enabled: true, CompactionPercentage: 0.25, SummaryMaxTokens: DefaultSummaryMaxTokens}
	if a.cfg.AutoSummarization != nil {
		cfg.CompactionPercentage = a.cfg.AutoSummarization.CompactionPercentage
		if cfg.CompactionPercentage <= 0 {
			cfg.CompactionPercentage = 0.25
		}
		if a.cfg.AutoSummarization.SummaryMaxTokens > 0 {
			cfg.SummaryMaxTokens = a.cfg.AutoSummarization.SummaryMaxTokens
		}
	}
	if targetPercent > 0 && targetPercent < 100 {
		cfg.CompactionPercentage = 1.0 - float64(targetPercent)/100.0
	}

	res, err := a.performCompaction(ctx, sess, cfg, a.manualCompactionSpend(ctx, userID, info, params))
	if err != nil {
		return nil, &extensions.Error{
			Code:    extensions.CodeInternal,
			Message: "compaction failed: " + llm.SanitizeErrorText(err),
		}
	}

	if saveErr := a.svc.Sessions().Save(ctx, sess); saveErr != nil {
		return nil, fmt.Errorf("save session after compaction: %w", saveErr)
	}

	postRemaining := estimatePostCompactionTokens(sess.Messages, res.Metadata)

	summary := ""
	remainingEvents := len(sess.Messages)
	if res.Metadata != nil {
		summary = res.Metadata.Summary
		if res.Metadata.CoveredThrough <= len(sess.Messages) {
			remainingEvents = 1 + (len(sess.Messages) - res.Metadata.CoveredThrough)
		}
	}
	out := &sessioncontrol.Result{
		SummarizedMessagesCount:       res.SummarizedMessagesCount,
		TokensFreed:                   res.TokensFreed,
		PostCompactionRemainingTokens: postRemaining,
		Summary:                       summary,
		RemainingEvents:               remainingEvents,
	}
	if u := res.SummarizerUsage; u != nil {
		out.SummarizerInputTokens = u.PromptTokens
		out.SummarizerOutputTokens = u.CompletionTokens
	}
	return out, nil
}

func estimatePostCompactionTokens(messages []session.Message, cm *CompactionMetadata) int {
	if cm == nil {
		return 0
	}
	tokens := len(cm.Summary) / 4
	if tokens == 0 && cm.Summary != "" {
		tokens = 1
	}
	for i := cm.CoveredThrough; i < len(messages); i++ {
		tokens += estimateTokens(messages[i])
	}
	return tokens
}

var _ sessioncontrol.Service = (*Agent)(nil)
