// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type taskDelivery struct {
	redelivered bool
}

type continuation int

const (
	startAfresh continuation = iota
	dropRedelivery
	continueFromRows
	republishFinal
	rerunFailedTask
)

func (a *Agent) decideContinuation(ctx context.Context, delivery taskDelivery, taskID string, sess *session.Session, logger *slog.Logger) (continuation, []session.Message, error) {
	if !delivery.redelivered || a.checkpoints == nil {
		return startAfresh, nil, nil
	}
	cp, err := a.checkpoints.store.GetCheckpoint(ctx, taskID)
	if err != nil {
		return startAfresh, nil, err
	}
	if cp != nil {
		logger.Info("redelivered task has a checkpoint; leaving it to the checkpoint")
		return dropRedelivery, nil, nil
	}
	own := ownRows(sess.Messages, taskID)
	if len(own) == 0 {
		return startAfresh, nil, nil
	}
	last := own[len(own)-1]
	switch {
	case storedFinalAnswer(own) != nil:
		logger.Info("redelivered task already stored its final answer; publishing it")
		return republishFinal, own, nil
	case last.Role == "assistant" && last.Content == cancellationMarkerContent:
		logger.Info("redelivered task was cancelled; dropping it")
		return dropRedelivery, nil, nil
	case last.Role == "assistant" && last.Content == failedTaskMarkerContent:
		logger.Info("redelivered task had failed; running it again")
		return rerunFailedTask, nil, nil
	}
	logger.Info("continuing a redelivered task from its stored turns", "storedRows", len(own))
	return continueFromRows, own, nil
}

func ownRows(msgs []session.Message, taskID string) []session.Message {
	var own []session.Message
	for _, m := range msgs {
		if m.TaskID == taskID {
			own = append(own, m)
		}
	}
	return own
}

func (s *TaskExecutionState) seedFromOwnRows(own []session.Message) {
	s.restoreRunCounters(own)
	calls := min(assistantTurns(own), max(s.MaxCalls-1, 0))
	s.LoopIteration = calls
	s.LLMCallCount = calls
}

func (s *TaskExecutionState) restoreAdoptedRunCounters(own []session.Message) {
	if turnStored, _ := pausedTurnInRows(own, dispatchedPausedCallIDs(s)); !turnStored {
		return
	}
	s.runFacts.take()
	s.resetRunCounters()
	s.restoreRunCounters(own)
}

func assistantTurns(own []session.Message) int {
	n := 0
	for _, m := range own {
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

func (s *TaskExecutionState) restoreRunCounters(own []session.Message) {
	for _, m := range own {
		if m.Role == "tool" {
			s.recordReportedFiles(llm.ChatMessage{Role: m.Role, ToolName: m.ToolName, Content: m.Content})
		}
		if m.RunFacts != nil {
			s.seedRunFacts(m.RunFacts)
		}
	}
	if s.searchTurns != nil {
		for name, n := range searchCountsOf(own) {
			s.searchTurns.Seed(name, n)
		}
	}
}

func (s *TaskExecutionState) seedRunFacts(f *session.RunFacts) {
	for _, cu := range f.Usage {
		var u llm.Usage
		if json.Unmarshal(cu.Tokens, &u) != nil {
			continue
		}
		switch cu.Source {
		case usageSourceTool:
			s.addToolCallUsage(cu.Model, u)
		case usageSourceUnattributed:
			AddUsage(&s.TotalUsage, u)
		default:
			s.addCallUsage(cu.Model, u)
			s.LastInputTokens = u.PromptTokens
		}
	}
	for _, pf := range f.Files {
		rec := artifactCreationRecord{Filename: pf.Filename, Version: pf.Version, Status: pf.Status, ErrorMsg: pf.Error, Tags: pf.Tags}
		if pf.Source == producedBySourceBlock {
			s.CreatedArtifacts = append(s.CreatedArtifacts, rec)
		} else {
			s.addToolSave(rec)
		}
	}
	if f.Failure != nil && !s.HadToolFailure {
		s.HadToolFailure = true
		s.ToolFailureDetail = &tool.ToolFailedDetail{
			Code: f.Failure.Code, Message: f.Failure.Message,
			StderrTail: f.Failure.StderrTail, StackTrace: f.Failure.StackTrace,
		}
	}
}

func repairContinuedPrompt(messages []llm.ChatMessage, questionAt int, logger *slog.Logger) []llm.ChatMessage {
	messages, _ = repairOrphanUserMessages(messages, questionAt, logger)
	messages, _ = repairDanglingToolCalls(messages, len(messages), logger, nil)
	return messages
}

func (a *Agent) republishStoredFinal(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) *a2a.Response {
	content := *rc.storedFinalAnswer
	state.answered.Store(true)
	if state.IsRunBased {
		rc.sp.PublishFinal(ctx, content)
	}
	return a.buildFinalResponse(ctx, state, content)
}

func (a *Agent) rebuildAdoptedPrompt(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) {
	state.rebuildFromRows = false
	own := ownRows(rc.sess.Messages, state.TaskID)
	pausedIDs := dispatchedPausedCallIDs(state)
	pausedTurnStored, answersStored := pausedTurnInRows(own, pausedIDs)

	if answer := storedFinalAnswer(own); answer != nil {
		rc.storedFinalAnswer = answer
		state.endedQuietly = storedFinalQuiet(own)
		state.pauseReplayed.Store(true)
		rc.logger.Info("adopted task already stored its final answer; publishing it")
		return
	}
	if state.Question.Role == "" {
		if _, q, _ := splitOwnRows(rc.sess.Messages, state.TaskID); q != nil {
			state.Question = sessionMessagesToChat([]session.Message{*q})[0]
		}
	}

	claims := a.readSiblingClaims(ctx, rc.sess.AppName, rc.sess.UserID, rc.sess.SessionID, rc.logger)
	turns := a.historyTurnsFor(ctx, rc.sess, state.TaskID, a.taskStubBatchBytes(ctx, state), claims, rc.logger)
	turns.ownRows = ownRowsAfterQuestion
	if state.Question.Role != "" {
		turns.question = &state.Question
	}
	messages, questionAt := a.buildMessagesForTask(ctx, state.Identity, rc.sess, turns, "", rc.gc)
	var expected []llm.ChatMessage
	if !answersStored {
		for id := range pausedIDs {
			expected = append(expected, llm.ChatMessage{Role: "tool", ToolCallID: id})
		}
	}
	messages, _ = repairOrphanUserMessages(messages, questionAt, rc.logger)
	messages, _ = repairDanglingToolCalls(messages, len(messages), rc.logger, expected)
	state.HistoryOffset = len(messages)
	if !pausedTurnStored && len(pausedIDs) > 0 {
		messages = append(messages, withoutHostNotifyCalls(state.PausedAssistantMsg))
	}
	state.Messages = messages
	state.pauseReplayed.Store(answersStored)
	if answersStored {
		turns := assistantTurns(own)
		state.LoopIteration = max(state.LoopIteration, turns-1)
		state.LLMCallCount = max(state.LLMCallCount, turns)
	}

	searches := searchCountsOf(own)
	if !answersStored {
		for _, tc := range state.PausedAssistantMsg.ToolCalls {
			if pausedIDs[tc.ID] {
				searches[tool.SearchTurnKey(tc.Name)]++
			}
		}
	}
	for name, n := range searches {
		state.searchTurns.Seed(name, n)
	}
	rc.logger.Info("rebuilt an adopted task from its stored turns",
		"storedRows", len(own), "pausedTurnStored", pausedTurnStored, "answersStored", answersStored)
}

func dispatchedPausedCallIDs(state *TaskExecutionState) map[string]bool {
	synthetic := syntheticErrorCallIDs(state.SyntheticToolErrors)
	ids := make(map[string]bool, len(state.PausedAssistantMsg.ToolCalls))
	for _, tc := range state.PausedAssistantMsg.ToolCalls {
		if strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) || synthetic[tc.ID] {
			continue
		}
		ids[tc.ID] = true
	}
	return ids
}

func pausedTurnInRows(own []session.Message, pausedIDs map[string]bool) (turnStored, answersStored bool) {
	for _, m := range own {
		for _, tc := range m.ToolCalls {
			if pausedIDs[tc.ID] {
				turnStored = true
			}
		}
		if m.Role == "tool" && pausedIDs[m.ToolCallID] {
			answersStored = true
		}
	}
	return turnStored, answersStored
}

func storedFinalAnswer(own []session.Message) *string {
	if len(own) == 0 {
		return nil
	}
	last := own[len(own)-1]
	if last.RunFacts == nil || !last.RunFacts.Final {
		return nil
	}
	if last.RunFacts.Answer != "" {
		return &last.RunFacts.Answer
	}
	return &last.Content
}

func storedFinalQuiet(own []session.Message) bool {
	if len(own) == 0 {
		return false
	}
	last := own[len(own)-1]
	return last.RunFacts != nil && last.RunFacts.Final && last.RunFacts.Quiet
}

func withoutHostNotifyCalls(m llm.ChatMessage) llm.ChatMessage {
	kept := make([]llm.ToolCall, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		if !strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) {
			kept = append(kept, tc)
		}
	}
	m.ToolCalls = kept
	return m
}

func searchCountsOf(own []session.Message) map[string]int {
	counts := map[string]int{}
	for _, m := range own {
		if m.Role == "tool" {
			counts[tool.SearchTurnKey(m.ToolName)]++
		}
	}
	return counts
}

func (s *TaskExecutionState) resetRunCounters() {
	s.TotalUsage = llm.Usage{}
	s.UsageByModel = nil
	s.ToolUsageByModel = nil
	s.LastInputTokens = 0
	s.CreatedArtifacts = nil
	s.ReportedFiles = nil
	s.HadToolFailure = false
	s.ToolFailureDetail = nil
	s.toolSavesMu.Lock()
	s.toolSaves = nil
	s.toolSavesMu.Unlock()
}
