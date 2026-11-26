// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type PendingCallKind int

const (
	PendingCallPeer PendingCallKind = iota
	PendingCallWorkflow
	PendingCallHILApproval
	PendingCallHILAsk
	PendingCallAuth
	PendingCallRemoteTool
	PendingCallToolApproval

	highestPendingCallKind = PendingCallToolApproval
)

var pendingCallKindNames = map[PendingCallKind]string{
	PendingCallPeer:         "peer",
	PendingCallWorkflow:     "workflow",
	PendingCallHILApproval:  "hil_approval",
	PendingCallHILAsk:       "hil_ask",
	PendingCallAuth:         "auth",
	PendingCallRemoteTool:   "remote_tool",
	PendingCallToolApproval: "tool_approval",
}

func (k PendingCallKind) String() string {
	if name, ok := pendingCallKindNames[k]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", int(k))
}

func (k PendingCallKind) waitsOnPerson() bool {
	return k == PendingCallHILApproval || k == PendingCallHILAsk || k == PendingCallToolApproval
}

func parsePendingCallKind(name string) (PendingCallKind, bool) {
	for kind, known := range pendingCallKindNames {
		if known == name {
			return kind, true
		}
	}
	return 0, false
}

type PendingToolCall struct {
	ToolCallID     string
	ToolName       string
	Kind           PendingCallKind
	Index          int
	SubTaskID      string
	HILRequestID   string
	Timeout        time.Duration
	TimeoutTimer   *time.Timer
	Result         *tool.Result
	OriginalArgs   map[string]any
	AuthRetries    int
	CredentialKey  string
	IsAuthFollower bool

	notYetDurable bool

	reportUndelivered func()
}

type TaskExecutionState struct {
	TaskID                  string
	ContextID               string
	EffectiveSessionID      string
	ReplyTo                 string
	UserProps               a2a.UserProperties
	SpendOrigin             SpendOrigin
	Identity                *auth.Identity
	OriginalSessionID       string
	IsRunBased              bool
	CallerDisplaysTemplates bool
	DisplaysTemplateSignals bool
	QuietEndAllowed         bool
	endedQuietly            bool
	GatewayMetadata         map[string]string
	kind                    string

	Messages          []llm.ChatMessage
	HistoryOffset     int
	unconfirmedAppend *unconfirmedSessionAppend

	LoopIteration          int
	MaxCalls               int
	LLMCallCount           int
	subTaskCount           *atomic.Int64
	TotalUsage             llm.Usage
	UsageByModel           map[string]llm.Usage
	ToolUsageByModel       map[string]llm.Usage
	LastInputTokens        int
	CreatedArtifacts       []artifactCreationRecord
	ReportedFiles          []string
	toolSaves              []artifactCreationRecord
	toolSavesMu            sync.Mutex
	Question               llm.ChatMessage
	searchTurns            *tool.SearchTurns
	rebuildFromRows        bool
	sealedAuthToken        string
	answered               atomic.Bool
	runFacts               pendingRunFacts
	HadVisibleText         bool
	IsContinuation         bool
	ContinuationParts      []string
	ImplicitContinuations  int
	LastFinishReason       string
	LastContent            string
	BlockValidationRetries int
	CompactionRetries      int
	CompactionSummary      string
	StructuredCtx          *structured.Context
	HadToolFailure         bool
	ToolFailureDetail      *tool.ToolFailedDetail
	usageFacts             taskUsageFacts
	toolLLMUsage           toolLLMUsage

	ModelOverride               *llm.BifrostConfig
	ModelOverrideAlias          string
	ModelOverrideConfigID       string
	ModelOverrideReportModel    string
	ModelOverrideMaxInputTokens int

	OverrideCacheStrategy         string
	OverrideReasoningBudgetTokens int
	OverrideReasoningEffort       string

	teardownStarted atomic.Bool

	resuming         atomic.Bool
	resumedFromPause atomic.Bool
	resumeRequested  atomic.Bool

	publishHandoff atomic.Int32

	pauseReplayed atomic.Bool

	finishAfterArtifacts bool

	repeatFailures repeatedFailures

	PausedAssistantMsg  llm.ChatMessage
	SyntheticToolErrors []llm.ChatMessage
	ParallelGroupID     string

	PendingCalls map[string]*PendingToolCall
	TotalPending int32
	Fenced       atomic.Bool

	durableWrites sync.WaitGroup

	checkpointed atomic.Bool

	Cancelled atomic.Bool

	SyncResults map[int]*tool.Result

	PausedApprovals map[string]pausedApprovalCall

	CreatedAt time.Time
	PausedAt  time.Time

	DoneCh chan *a2a.Response

	mu          sync.Mutex
	requestHold chan struct{}

	authMu sync.Mutex

	pendingAuthOutcome *authOutcome
}

func (s *TaskExecutionState) recordCallUsage(model string, u llm.Usage) {
	s.addCallUsage(model, u)
	s.runFacts.addUsage(model, usageSourceAgent, u)
}

func (s *TaskExecutionState) addCallUsage(model string, u llm.Usage) {
	AddUsage(&s.TotalUsage, u)
	if s.UsageByModel == nil {
		s.UsageByModel = make(map[string]llm.Usage)
	}
	m := s.UsageByModel[model]
	AddUsage(&m, u)
	s.UsageByModel[model] = m
}

func (s *TaskExecutionState) recordUnattributedUsage(u llm.Usage) {
	AddUsage(&s.TotalUsage, u)
	s.runFacts.addUsage("", usageSourceUnattributed, u)
}

func (s *TaskExecutionState) addToolCallUsage(model string, u llm.Usage) {
	s.addCallUsage(model, u)
	if s.ToolUsageByModel == nil {
		s.ToolUsageByModel = make(map[string]llm.Usage)
	}
	m := s.ToolUsageByModel[model]
	AddUsage(&m, u)
	s.ToolUsageByModel[model] = m
}

type toolLLMUsage struct {
	mu      sync.Mutex
	byModel map[string]llm.Usage
}

func (t *toolLLMUsage) record(model string, u llm.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byModel == nil {
		t.byModel = make(map[string]llm.Usage)
	}
	m := t.byModel[model]
	AddUsage(&m, u)
	t.byModel[model] = m
}

func (t *toolLLMUsage) drain() map[string]llm.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	drained := t.byModel
	t.byModel = nil
	return drained
}

func (s *TaskExecutionState) foldToolLLMUsage() bool {
	drained := s.toolLLMUsage.drain()
	for model, u := range drained {
		s.addToolCallUsage(model, u)
		s.runFacts.addUsage(model, usageSourceTool, u)
	}
	return len(drained) > 0
}

func (s *TaskExecutionState) usageSnapshot() taskUsageSnapshot {
	s.foldToolLLMUsage()
	return taskUsageSnapshot{usage: s.TotalUsage, lastInputTokens: s.LastInputTokens}
}

type authOutcome struct {
	success         bool
	credentialKey   string
	functionCallID  string
	toolDisplayName string
}

func (s *TaskExecutionState) setPendingAuthOutcome(o *authOutcome) {
	s.pendingAuthOutcome = o
}

func (s *TaskExecutionState) takePendingAuthOutcome() *authOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.pendingAuthOutcome
	s.pendingAuthOutcome = nil
	return o
}

func (s *TaskExecutionState) claimPendingCall(toolCallID string, idx int, result *tool.Result) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.PendingCalls[toolCallID]; !ok {
		return false
	}
	delete(s.PendingCalls, toolCallID)
	s.SyncResults[idx] = result
	return true
}

const publishHandoffParties = 2

func (s *TaskExecutionState) armPublishHandoff() {
	s.publishHandoff.Store(publishHandoffParties)
}

func (s *TaskExecutionState) passPublishHandoff() bool {
	return s.publishHandoff.Add(-1) == 0
}

func (s *TaskExecutionState) holdRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requestHold == nil {
		s.requestHold = make(chan struct{})
	}
}

func (s *TaskExecutionState) heldRequest() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestHold
}

func (s *TaskExecutionState) releaseRequestHold() {
	s.mu.Lock()
	held := s.requestHold
	s.requestHold = nil
	s.mu.Unlock()
	if held != nil {
		close(held)
	}
}

func (s *TaskExecutionState) DecrementPending() int32 {
	return atomic.AddInt32(&s.TotalPending, -1)
}

func (s *TaskExecutionState) setModelOverride(o *ResolvedModelOverride) {
	if o == nil {
		return
	}
	s.ModelOverride = o.Config
	s.ModelOverrideAlias = o.Alias
	s.ModelOverrideConfigID = o.ConfigID
	s.ModelOverrideReportModel = o.ReportModel
	s.ModelOverrideMaxInputTokens = o.MaxInputTokens
	s.OverrideCacheStrategy = o.CacheStrategy
	s.OverrideReasoningBudgetTokens = o.ReasoningBudgetTokens
	s.OverrideReasoningEffort = o.ReasoningEffort
}

type taskStateStore struct {
	mu     sync.RWMutex
	states map[string]*TaskExecutionState
}

func newTaskStateStore() *taskStateStore {
	return &taskStateStore{
		states: make(map[string]*TaskExecutionState),
	}
}

func (s *taskStateStore) Store(state *TaskExecutionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state.TaskID] = state
}

func (s *taskStateStore) Load(taskID string) *TaskExecutionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[taskID]
}

func (s *taskStateStore) Delete(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, taskID)
}

func (s *taskStateStore) Snapshot() []*TaskExecutionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*TaskExecutionState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, st)
	}
	return out
}

func (s *taskStateStore) FindByHILRequestID(requestID string) (*TaskExecutionState, *PendingToolCall) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, state := range s.states {
		state.mu.Lock()
		for _, pc := range state.PendingCalls {
			if pc.Kind.waitsOnPerson() && pc.HILRequestID == requestID && !pc.notYetDurable {
				state.mu.Unlock()
				return state, pc
			}
		}
		state.mu.Unlock()
	}
	return nil, nil
}

func (s *taskStateStore) FindHILWaitOfTask(taskID, requestID string) (*TaskExecutionState, *PendingToolCall) {
	s.mu.RLock()
	state := s.states[taskID]
	s.mu.RUnlock()
	if state == nil {
		return nil, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, pc := range state.PendingCalls {
		if pc.Kind.waitsOnPerson() && pc.HILRequestID == requestID && !pc.notYetDurable {
			return state, pc
		}
	}
	return nil, nil
}

func (s *taskStateStore) holdsHILRequestID(requestID string) bool {
	state, _ := s.FindByHILRequestID(requestID)
	return state != nil
}

type armedPendingCall struct {
	pc     *PendingToolCall
	result *tool.Result
}

func (s *TaskExecutionState) recordToolSave(info tool.ArtifactSavedInfo) {
	rec := artifactCreationRecord{
		Filename:    info.Filename,
		MIMEType:    info.MIMEType,
		Description: info.Description,
		Version:     info.Version,
		SizeBytes:   int(info.SizeBytes),
		Status:      "success",
		Tags:        info.Tags,
	}
	s.runFacts.addFile(producedFileOf(rec, producedBySourceTool))
	s.addToolSave(rec)
}

func (s *TaskExecutionState) addToolSave(rec artifactCreationRecord) {
	s.toolSavesMu.Lock()
	defer s.toolSavesMu.Unlock()
	for i, r := range s.toolSaves {
		if r.Filename == rec.Filename {
			s.toolSaves[i] = rec
			return
		}
	}
	s.toolSaves = append(s.toolSaves, rec)
}

func (s *TaskExecutionState) recordCreatedArtifacts(recs []artifactCreationRecord) {
	s.CreatedArtifacts = append(s.CreatedArtifacts, recs...)
	for _, rec := range recs {
		s.runFacts.addFile(producedFileOf(rec, producedBySourceBlock))
	}
}

func (s *TaskExecutionState) recordReportedFiles(m llm.ChatMessage) {
	if isPeerOrWorkflowTool(m.ToolName) {
		s.recordLegacyReportedFiles(m)
	}
	if isDelegationTool(m.ToolName) {
		s.recordDelegatedFiles(m)
	}
}

func (s *TaskExecutionState) recordLegacyReportedFiles(m llm.ChatMessage) {
	var result struct {
		Artifacts []string `json:"artifacts"`
	}
	if json.Unmarshal([]byte(m.Content), &result) != nil {
		return
	}
	for _, spec := range result.Artifacts {
		if name, _ := ParseArtifactSpec(spec); name != "" {
			s.addReportedFile(name)
		}
	}
}

func (s *TaskExecutionState) recordDelegatedFiles(m llm.ChatMessage) {
	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(m.Content), &result) != nil {
		return
	}
	var produced []any
	if json.Unmarshal(result["produced_artifacts"], &produced) != nil {
		return
	}
	for _, name := range reportedNames(produced, "filename") {
		s.addReportedFile(name)
	}
}

func (s *TaskExecutionState) addReportedFile(name string) {
	if isSavedArtifactName(name) && !slices.Contains(s.ReportedFiles, name) {
		s.ReportedFiles = append(s.ReportedFiles, name)
	}
}

func isSavedArtifactName(name string) bool {
	return artifact.IsFilenameSafe(name) &&
		!artifact.IsInternalArtifact(name) &&
		!a2a.IsWorkflowHandoffArtifactName(name) &&
		!strings.Contains(name, ":") &&
		!strings.Contains(name, EmbedOpen) &&
		!strings.Contains(name, EmbedClose) &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func isPeerOrWorkflowTool(toolName string) bool {
	return strings.HasPrefix(toolName, peerToolPrefix) || strings.HasPrefix(toolName, a2a.WorkflowToolPrefix)
}

func isDelegationTool(toolName string) bool {
	return toolName == subTaskToolName || isPeerOrWorkflowTool(toolName)
}

func reportedNames(entries []any, key string) []string {
	var names []string
	for _, entry := range entries {
		fields, _ := entry.(map[string]any)
		if name, _ := fields[key].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (s *TaskExecutionState) toolSavesSnapshot() []artifactCreationRecord {
	s.toolSavesMu.Lock()
	defer s.toolSavesMu.Unlock()
	return slices.Clone(s.toolSaves)
}

func artifactSavedHook(sp *StatusPublisher, state *TaskExecutionState) func(context.Context, tool.ArtifactSavedInfo) {
	return func(ctx context.Context, info tool.ArtifactSavedInfo) {
		publishArtifactSavedFromHook(ctx, sp, info)
		state.recordToolSave(info)
	}
}
