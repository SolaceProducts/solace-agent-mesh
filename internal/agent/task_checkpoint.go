// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type taskCheckpointer struct {
	store         session.Store
	agentName     string
	ownerID       string
	interval      time.Duration
	takeoverAfter time.Duration
	backstops     delegationBackstops
	logger        *slog.Logger
	sealer        auth.Sealer

	healthMu         sync.Mutex
	dbAnsweringSince time.Time

	discardMu       sync.Mutex
	pendingDiscards map[string]struct{}

	statusMu         sync.Mutex
	statusExtendedAt map[string]time.Time
}

var errTaskLost = errors.New("another process holds this task")

func (c *taskCheckpointer) takeoverWindow() time.Duration {
	if c.takeoverAfter > 0 {
		return c.takeoverAfter
	}
	return checkpointTakeoverAfter
}

func (c *taskCheckpointer) recordRenewalTick(ok bool, now time.Time) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	switch {
	case !ok:
		c.dbAnsweringSince = time.Time{}
	case c.dbAnsweringSince.IsZero():
		c.dbAnsweringSince = now
	}
}

func (c *taskCheckpointer) mayTakeOver(now time.Time) bool {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	return !c.dbAnsweringSince.IsZero() && now.Sub(c.dbAnsweringSince) >= c.takeoverWindow()
}

func (c *taskCheckpointer) ownerGone(cp *session.TaskCheckpoint, now time.Time) bool {
	switch {
	case cp.OwnerID == "":
		return true
	case cp.RenewedAt != nil:
		return cp.ReadAt.Sub(*cp.RenewedAt) >= c.takeoverWindow()
	default:
		return cp.LeaseExpiresAt == nil || !cp.LeaseExpiresAt.After(adoptionCutoff(now))
	}
}

type delegationBackstops struct {
	peer     time.Duration
	workflow time.Duration
}

const checkpointExpiryGrace = time.Minute

const registerPendingCallsFormat = "register pending calls: %w"

func (c *taskCheckpointer) durableExpiry(pc *PendingToolCall) time.Duration {
	if pc.Timeout > 0 {
		return pc.Timeout + checkpointExpiryGrace
	}
	switch pc.Kind {
	case PendingCallWorkflow:
		if c.backstops.workflow > 0 {
			return c.backstops.workflow + checkpointExpiryGrace
		}
	case PendingCallPeer:
		if c.backstops.peer > 0 {
			return c.backstops.peer + checkpointExpiryGrace
		}
	case PendingCallHILApproval, PendingCallHILAsk, PendingCallAuth, PendingCallRemoteTool, PendingCallToolApproval:
	}
	return 0
}

func newTaskCheckpointer(svc runtime.Services, agentName string, backstops delegationBackstops, logger *slog.Logger) *taskCheckpointer {
	if svc == nil || svc.Sessions() == nil {
		return nil
	}
	reg := svc.FeatureRegistry()
	if reg == nil || !reg.IsEnabled(features.KeyDurableTaskResume) {
		return nil
	}
	if !session.OutlivesProcess(svc.Sessions()) {
		logger.Warn("durable task resume is enabled, but the session store is local to this process and lost when it stops; "+
			"tasks wait in process as with the flag off, and a restart ends them. Configure a session_service to resume them",
			"agentName", agentName, "sessionStore", "memory")
		return nil
	}
	warnIfStoreIsPerPod(svc.Sessions(), agentName, logger)
	warnIfStoreCannotExtendPendingCalls(svc.Sessions(), agentName, logger)
	ownerID := idgen.New()
	sealer, _ := svc.CredentialService().(auth.Sealer)
	return &taskCheckpointer{
		store:     svc.Sessions(),
		agentName: agentName,
		ownerID:   ownerID,
		backstops: backstops,
		sealer:    sealer,
		logger:    logger.With("logger_name", "task-checkpoint", "checkpointOwnerId", ownerID),

		statusExtendedAt: make(map[string]time.Time),
	}
}

func warnIfStoreIsPerPod(store session.Store, agentName string, logger *slog.Logger) {
	if s, ok := store.(interface{ Dialect() string }); ok && s.Dialect() == "sqlite" {
		logger.Warn("durable task resume is enabled over a session store that is local to this process; "+
			"checkpoints will be written and no other replica can ever read them",
			"agentName", agentName, "sessionStore", "sqlite")
	}
}

func warnIfStoreCannotExtendPendingCalls(store session.Store, agentName string, logger *slog.Logger) {
	if _, ok := store.(pendingCallExtender); !ok {
		logger.Warn("the session store cannot extend a pending call's expiry; a peer call that outlives its owner "+
			"is timed out at its original expiry even while the peer reports status",
			"agentName", agentName)
	}
}

const durableWriteTimeout = 10 * time.Second

const (
	checkpointVersionV2    = 2
	checkpointVersionV3    = 3
	checkpointStateVersion = checkpointVersionV3
)

var pendingCallKindMinimumVersion = map[PendingCallKind]int{
	PendingCallPeer:         checkpointVersionV2,
	PendingCallWorkflow:     checkpointVersionV2,
	PendingCallHILApproval:  checkpointVersionV2,
	PendingCallAuth:         checkpointVersionV2,
	PendingCallHILAsk:       checkpointVersionV3,
	PendingCallRemoteTool:   checkpointVersionV3,
	PendingCallToolApproval: checkpointVersionV3,
}

func minimumCheckpointVersionOf(kind PendingCallKind) int {
	if version, declared := pendingCallKindMinimumVersion[kind]; declared {
		return version
	}
	return checkpointStateVersion
}

func checkpointVersionFor(state *TaskExecutionState) int {
	if len(state.PausedApprovals) > 0 {
		return checkpointVersionV3
	}
	version := checkpointVersionV2
	for _, pc := range state.PendingCalls {
		version = max(version, minimumCheckpointVersionOf(pc.Kind))
	}
	return version
}

var errCheckpointFromNewerBuild = errors.New("checkpoint written by a newer build")

const maxCheckpointBlobBytes = 8 << 20

const maxToolResultPayloadBytes = 1 << 20

const maxToolResultContentBytes = 64 << 10

func boundedResultContent(msg string) string {
	if len(msg) <= maxToolResultContentBytes {
		return msg
	}
	cut := maxToolResultContentBytes
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + "\n\n[truncated: too large to checkpoint]"
}

func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), durableWriteTimeout)
}

func pendingCallCorrelation(taskID string, pc *PendingToolCall) (corrID, kind string, ok bool) {
	switch pc.Kind {
	case PendingCallPeer:
		return pc.SubTaskID, session.PendingKindPeer, pc.SubTaskID != ""
	case PendingCallWorkflow:
		return pc.SubTaskID, session.PendingKindWorkflowPeer, pc.SubTaskID != ""
	case PendingCallHILApproval, PendingCallHILAsk:
		return pc.HILRequestID, session.PendingKindHILApproval, pc.HILRequestID != ""
	case PendingCallRemoteTool:
		return pc.SubTaskID, session.PendingKindRemoteTool, pc.SubTaskID != ""
	case PendingCallToolApproval:
		return pc.HILRequestID, session.PendingKindToolApproval, pc.HILRequestID != ""
	case PendingCallAuth:
		if pc.IsAuthFollower || taskID == "" || pc.ToolCallID == "" {
			return "", "", false
		}
		return authCorrelationID(taskID, pc.ToolCallID), session.PendingKindAuth, true
	default:
		return "", "", false
	}
}

func authCorrelationID(taskID, functionCallID string) string {
	return "auth:" + taskID + ":" + functionCallID
}

func (c *taskCheckpointer) sealAuthToken(state *TaskExecutionState) string {
	token := state.UserProps.AuthToken
	if token == "" && state.Identity != nil {
		token = state.Identity.AuthToken
	}
	if token == "" || c.sealer == nil {
		return ""
	}
	sealed, err := c.sealer.Seal(token)
	if err != nil {
		c.logger.Warn("could not encrypt the caller's token for the checkpoint; a process that takes this task over resumes it with no token",
			"taskID", state.TaskID, "error", err)
		return ""
	}
	return sealed
}

func (c *taskCheckpointer) restoreAuthToken(state *TaskExecutionState, logger *slog.Logger) {
	if state.sealedAuthToken == "" {
		return
	}
	if c.sealer == nil {
		logger.Warn("the checkpoint carries the caller's token and this process has no identity key; resuming with no token")
		return
	}
	token, err := c.sealer.Open(state.sealedAuthToken)
	if err != nil {
		logger.Warn("could not decrypt the caller's token from the checkpoint; resuming with no token", "error", err)
		return
	}
	state.UserProps.AuthToken = token
	if state.Identity != nil {
		state.Identity.AuthToken = token
	}
}

func (c *taskCheckpointer) save(ctx context.Context, state *TaskExecutionState) error {
	if c == nil {
		return nil
	}
	ctx, cancel := detach(ctx)
	defer cancel()
	if err := c.refuseHeldCorrelations(ctx, state); err != nil {
		return fmt.Errorf(registerPendingCallsFormat, err)
	}
	state.sealedAuthToken = c.sealAuthToken(state)
	blob, err := marshalTaskState(state)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if len(blob) > maxCheckpointBlobBytes {
		c.logger.Error("task state is too large to checkpoint; this task will not survive a restart of this process",
			"taskID", state.TaskID, "traceID", state.UserProps.TraceID,
			"stateBytes", len(blob), "limitBytes", maxCheckpointBlobBytes)
		if c.discardIfOwned(ctx, state.TaskID) && state.checkpointed.Load() {
			c.logger.Warn("oversized task's checkpoint went elsewhere; leaving it to its new owner",
				"taskID", state.TaskID)
			return fmt.Errorf("oversized checkpoint: %w", errTaskLost)
		}
		state.checkpointed.Store(false)
		return fmt.Errorf("checkpoint state is %d bytes, over the %d-byte limit", len(blob), maxCheckpointBlobBytes)
	}
	calls, uncorrelated := c.correlationsFor(state)
	if uncorrelated > 0 {
		c.logger.Warn("some of this task's pending calls have no durable correlation; a restart of this process will not resume them",
			"taskID", state.TaskID, "traceID", state.UserProps.TraceID,
			"uncorrelatedCalls", uncorrelated, "durableCalls", len(calls))
	}

	write := c.store.SaveCheckpoint
	if state.checkpointed.Load() {
		write = c.store.UpdateCheckpoint
	}
	lease := time.Now().Add(checkpointLease)
	if err := write(ctx, &session.TaskCheckpoint{
		TaskID:              state.TaskID,
		AppName:             c.agentName,
		UserID:              state.UserProps.UserID,
		SessionID:           state.EffectiveSessionID,
		AgentName:           c.agentName,
		PendingCount:        len(calls),
		State:               blob,
		OwnerID:             c.ownerID,
		LeaseExpiresAt:      &lease,
		TraceID:             state.UserProps.TraceID,
		ReplacePendingCalls: true,
		PendingCalls:        calls,
	}); err != nil {
		return c.failedSave(ctx, state, err)
	}
	state.checkpointed.Store(true)
	return nil
}

func (c *taskCheckpointer) failedSave(ctx context.Context, state *TaskExecutionState, err error) error {
	if errors.Is(err, session.ErrNotOwner) {
		return fmt.Errorf("save checkpoint: %w", errTaskLost)
	}
	if _, delErr := c.store.DeleteCheckpointIfOwned(ctx, state.TaskID, c.ownerID); delErr != nil {
		c.logger.Error("a checkpoint write failed and the earlier checkpoint of this task could not be removed; the task cannot be resumed from it reliably",
			"taskID", state.TaskID, "traceID", state.UserProps.TraceID, "error", delErr)
	}
	state.checkpointed.Store(false)
	return fmt.Errorf(registerPendingCallsFormat, err)
}

func (c *taskCheckpointer) refuseHeldCorrelations(ctx context.Context, state *TaskExecutionState) error {
	calls, _ := c.correlationsFor(state)
	for _, call := range calls {
		if call.Kind != session.PendingKindHILApproval {
			continue
		}
		holder, held, err := c.store.PendingCallTask(ctx, call.CorrID)
		if err != nil {
			c.logger.Warn("could not check whether another task holds a request id; registering it still refuses to take another task's row",
				"taskID", state.TaskID, "error", err)
			return nil
		}
		if held && holder != state.TaskID {
			return &session.CorrelationTakenError{CorrID: call.CorrID}
		}
	}
	return nil
}

func (c *taskCheckpointer) correlationsFor(state *TaskExecutionState) ([]session.PendingCall, int) {
	calls := make([]session.PendingCall, 0, len(state.PendingCalls))
	uncorrelated := 0
	for _, pc := range state.PendingCalls {
		if pc.Result != nil {
			continue
		}
		entry, ok := c.correlationEntry(state, pc)
		if !ok {
			uncorrelated++
			continue
		}
		calls = append(calls, entry)
	}
	return calls, uncorrelated
}

func (c *taskCheckpointer) correlationEntry(state *TaskExecutionState, pc *PendingToolCall) (session.PendingCall, bool) {
	corrID, kind, ok := pendingCallCorrelation(state.TaskID, pc)
	if !ok {
		return session.PendingCall{}, false
	}
	entry := session.PendingCall{
		CorrID: corrID, TaskID: state.TaskID, ToolCallID: pc.ToolCallID,
		AgentName: c.agentName, ToolName: pc.ToolName,
		TraceID: state.UserProps.TraceID, Kind: kind,
	}
	if expiry := c.durableExpiry(pc); expiry > 0 {
		expires := time.Now().Add(expiry)
		entry.ExpiresAt = &expires
	}
	return entry, true
}

func (c *taskCheckpointer) delegationExpiry(kind string) time.Duration {
	switch kind {
	case session.PendingKindPeer, session.PendingKindSubTask:
		return c.backstops.peer + checkpointExpiryGrace
	case session.PendingKindWorkflowPeer:
		return c.backstops.workflow + checkpointExpiryGrace
	default:
		return 0
	}
}

func (c *taskCheckpointer) claimStatusExtension(corrID string, now time.Time) bool {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	if last, ok := c.statusExtendedAt[corrID]; ok && now.Sub(last) < peerStatusExtendInterval {
		return false
	}
	for id, at := range c.statusExtendedAt {
		if now.Sub(at) >= peerStatusExtendInterval {
			delete(c.statusExtendedAt, id)
		}
	}
	c.statusExtendedAt[corrID] = now
	return true
}

func (c *taskCheckpointer) releaseStatusExtension(corrID string) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	delete(c.statusExtendedAt, corrID)
}

func (c *taskCheckpointer) releaseOwnership(ctx context.Context, taskID string) {
	if c == nil {
		return
	}
	if err := c.store.ReleaseOwnership(ctx, taskID, c.ownerID); err != nil {
		c.logger.Warn("failed to release checkpoint ownership", "taskID", taskID, "error", err)
	}
}

func (c *taskCheckpointer) recordResult(ctx context.Context, taskID string, pc *PendingToolCall, result *tool.Result) (resolvedElsewhere bool) {
	if c == nil {
		return false
	}
	ctx, cancel := detach(ctx)
	defer cancel()

	content := ""
	isError := false
	if result != nil {
		content = boundedResultContent(result.Message)
		isError = result.Status == tool.StatusError || result.Status == tool.StatusFailed
	}
	corrID, _, counted := pendingCallCorrelation(taskID, pc)
	_, err := c.store.RecordToolResult(ctx, taskID, corrID, &session.ToolCallResult{
		ToolCallID: pc.ToolCallID,
		ToolName:   pc.ToolName,
		Content:    content,
		IsError:    isError,
		Payload:    c.marshalResult(taskID, result),
		Uncounted:  !counted,
	})
	if errors.Is(err, session.ErrCallNotOutstanding) {
		c.logger.Debug("tool call was already resolved elsewhere; not recording it again",
			"taskID", taskID, "toolCallID", pc.ToolCallID)
		return true
	}
	if err != nil {
		c.logger.Warn("failed to record tool result on checkpoint; keeping the correlation so the reaper can still resolve it",
			"taskID", taskID, "toolCallID", pc.ToolCallID, "error", err)
	}
	return false
}

func (c *taskCheckpointer) discardIfOwned(ctx context.Context, taskID string) (takenElsewhere bool) {
	if c == nil {
		return false
	}
	dctx, cancel := detach(ctx)
	defer cancel()

	deleted, err := c.store.DeleteCheckpointIfOwned(dctx, taskID, c.ownerID)
	if err != nil {
		c.logger.Warn("could not delete this task's checkpoint; queueing a retry",
			"taskID", taskID, "error", err)
		c.rememberFailedDiscard(taskID)
		return false
	}
	if deleted {
		return false
	}
	c.logger.Debug("this task's checkpoint was not ours to delete", "taskID", taskID)
	return true
}

const maxPendingDiscards = 1024

const maxDiscardRetriesPerTick = 32

func (c *taskCheckpointer) rememberFailedDiscard(taskID string) {
	c.discardMu.Lock()
	defer c.discardMu.Unlock()
	if c.pendingDiscards == nil {
		c.pendingDiscards = make(map[string]struct{})
	}
	if len(c.pendingDiscards) >= maxPendingDiscards {
		c.logger.Error("too many undeleted checkpoints to track; this one may be resumed after finishing",
			"taskID", taskID, "trackedCount", len(c.pendingDiscards))
		return
	}
	c.pendingDiscards[taskID] = struct{}{}
}

func (c *taskCheckpointer) retryFailedDiscards(ctx context.Context) {
	if c == nil {
		return
	}
	c.discardMu.Lock()
	pending := make([]string, 0, min(len(c.pendingDiscards), maxDiscardRetriesPerTick))
	for taskID := range c.pendingDiscards {
		if len(pending) == maxDiscardRetriesPerTick {
			break
		}
		pending = append(pending, taskID)
	}
	c.discardMu.Unlock()

	for i, taskID := range pending {
		if ctx.Err() != nil {
			c.logger.Warn("stopped retrying failed checkpoint deletes; the tick ran out of budget",
				"retried", i, "remaining", len(pending)-i)
			return
		}
		deleted, err := c.store.DeleteCheckpointIfOwned(ctx, taskID, c.ownerID)
		if err != nil {
			c.logger.Warn("retrying a failed checkpoint delete did not succeed",
				"taskID", taskID, "error", err)
			continue
		}
		if !deleted {
			c.logger.Info("a checkpoint queued for retry is no longer ours to delete", "taskID", taskID)
		} else {
			c.logger.Info("removed a checkpoint whose earlier delete failed", "taskID", taskID)
		}
		c.discardMu.Lock()
		delete(c.pendingDiscards, taskID)
		c.discardMu.Unlock()
	}
}

func (c *taskCheckpointer) markAnswering(ctx context.Context, state *TaskExecutionState, final *broker.Message) bool {
	if c == nil || !state.checkpointed.Load() {
		return true
	}
	if final == nil {
		return !c.discardIfOwned(ctx, state.TaskID)
	}
	dctx, cancel := detach(ctx)
	defer cancel()
	marked, err := c.store.MarkAnswering(dctx, state.TaskID, c.ownerID, &session.FinalMessage{
		Topic:          state.UserProps.ReplyTo,
		Payload:        final.Payload,
		UserProperties: final.UserProperties,
	}, time.Now().Add(checkpointLease))
	if err != nil {
		c.logger.Warn("could not mark this task answering; answering it anyway", "taskID", state.TaskID, "error", err)
		return true
	}
	return marked
}

func (c *taskCheckpointer) removeCancelledRow(ctx context.Context, state *TaskExecutionState) bool {
	if c == nil {
		return true
	}
	return !c.discardIfOwned(ctx, state.TaskID) || !state.checkpointed.Load()
}

func (c *taskCheckpointer) lookup(ctx context.Context, corrID string) (*session.PendingCall, error) {
	if c == nil {
		return nil, nil
	}
	return c.store.LookupPendingCall(ctx, c.agentName, corrID)
}

type persistedTaskState struct {
	TaskID                  string             `json:"task_id"`
	ContextID               string             `json:"context_id"`
	EffectiveSessionID      string             `json:"effective_session_id"`
	OriginalSessionID       string             `json:"original_session_id"`
	ReplyTo                 string             `json:"reply_to"`
	UserProps               a2a.UserProperties `json:"user_props"`
	Identity                *persistedIdentity `json:"identity,omitempty"`
	SealedAuthToken         string             `json:"sealed_auth_token,omitempty"`
	IsRunBased              bool               `json:"is_run_based"`
	CallerDisplaysTemplates bool               `json:"caller_displays_templates"`
	DisplaysTemplateSignals bool               `json:"displays_template_signals"`
	QuietEndAllowed         bool               `json:"quiet_end_allowed,omitempty"`
	SpendOrigin             *SpendOrigin       `json:"spend_origin,omitempty"`
	GatewayMetadata         map[string]string  `json:"gateway_metadata,omitempty"`
	Version                 int                `json:"version"`
	Kind                    string             `json:"kind"`

	Messages          []llm.ChatMessage           `json:"messages,omitempty"`
	HistoryOffset     int                         `json:"history_offset,omitempty"`
	Question          *llm.ChatMessage            `json:"question,omitempty"`
	UnconfirmedAppend *persistedUnconfirmedAppend `json:"unconfirmed_append,omitempty"`
	PendingRunFacts   *session.RunFacts           `json:"pending_run_facts,omitempty"`

	LoopIteration    int                  `json:"loop_iteration"`
	MaxCalls         int                  `json:"max_calls"`
	LLMCallCount     int                  `json:"llm_call_count"`
	TotalUsage       llm.Usage            `json:"total_usage"`
	LastInputTokens  int                  `json:"last_input_tokens"`
	UsageByModel     map[string]llm.Usage `json:"usage_by_model,omitempty"`
	ToolUsageByModel map[string]llm.Usage `json:"tool_usage_by_model,omitempty"`

	CreatedArtifacts []artifactCreationRecord `json:"created_artifacts,omitempty"`
	ToolSaves        []artifactCreationRecord `json:"tool_saves,omitempty"`
	ReportedFiles    []string                 `json:"reported_files,omitempty"`

	HadVisibleText        bool     `json:"had_visible_text"`
	IsContinuation        bool     `json:"is_continuation"`
	ContinuationParts     []string `json:"continuation_parts,omitempty"`
	ImplicitContinuations int      `json:"implicit_continuations"`
	LastFinishReason      string   `json:"last_finish_reason,omitempty"`
	LastContent           string   `json:"last_content,omitempty"`

	BlockValidationRetries int    `json:"block_validation_retries"`
	CompactionRetries      int    `json:"compaction_retries"`
	CompactionSummary      string `json:"compaction_summary,omitempty"`

	StructuredCtx *structured.Context `json:"structured_ctx,omitempty"`

	HadToolFailure    bool                   `json:"had_tool_failure"`
	ToolFailureDetail *tool.ToolFailedDetail `json:"tool_failure_detail,omitempty"`

	UsageFacts                    json.RawMessage `json:"usage_facts,omitempty"`
	ModelOverrideAlias            string          `json:"model_override_alias,omitempty"`
	OverrideCacheStrategy         string          `json:"override_cache_strategy,omitempty"`
	OverrideReasoningBudgetTokens int             `json:"override_reasoning_budget_tokens,omitempty"`
	OverrideReasoningEffort       string          `json:"override_reasoning_effort,omitempty"`

	PausedAssistantMsg   llm.ChatMessage   `json:"paused_assistant_msg"`
	PauseReplayed        bool              `json:"pause_replayed,omitempty"`
	FinishAfterArtifacts bool              `json:"finish_after_artifacts,omitempty"`
	SyntheticToolErrors  []llm.ChatMessage `json:"synthetic_tool_errors,omitempty"`
	ParallelGroupID      string            `json:"parallel_group_id,omitempty"`

	PendingCalls map[string]persistedPendingCall `json:"pending_calls,omitempty"`
	TotalPending int32                           `json:"total_pending"`
	SyncResults  map[int]persistedToolResult     `json:"sync_results,omitempty"`

	PausedApprovals map[string]persistedApprovalPause `json:"paused_approvals,omitempty"`

	PendingAuthOutcome *persistedAuthOutcome `json:"pending_auth_outcome,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	PausedAt  time.Time `json:"paused_at"`
}

type persistedUnconfirmedAppend struct {
	StoredBefore int               `json:"stored_before"`
	Messages     []session.Message `json:"messages"`
}

type persistedPendingCall struct {
	ToolCallID     string               `json:"tool_call_id"`
	ToolName       string               `json:"tool_name"`
	Kind           string               `json:"kind"`
	Index          int                  `json:"index"`
	SubTaskID      string               `json:"sub_task_id,omitempty"`
	HILRequestID   string               `json:"hil_request_id,omitempty"`
	Timeout        time.Duration        `json:"timeout"`
	Result         *persistedToolResult `json:"result,omitempty"`
	OriginalArgs   map[string]any       `json:"original_args,omitempty"`
	AuthRetries    int                  `json:"auth_retries,omitempty"`
	CredentialKey  string               `json:"credential_key,omitempty"`
	IsAuthFollower bool                 `json:"is_auth_follower,omitempty"`
}

type persistedApprovalPause struct {
	HILRequestID string         `json:"hil_request_id"`
	Args         map[string]any `json:"args,omitempty"`
	Timeout      time.Duration  `json:"timeout"`
}

type persistedToolResult struct {
	Result                 *tool.Result           `json:"result"`
	FailedDetail           *tool.ToolFailedDetail `json:"failed_detail,omitempty"`
	ImageBlocks            []tool.ImageBlock      `json:"image_blocks,omitempty"`
	DataContent            map[int][]byte         `json:"data_content,omitempty"`
	LoadedSkillContentHash string                 `json:"loaded_skill_content_hash,omitempty"`
	LLMData                persistedLLMData       `json:"llm_data,omitempty"`
	ContentWindow          *tool.ContentWindow    `json:"content_window,omitempty"`
	WithheldBytes          int64                  `json:"withheld_bytes,omitempty"`
	DelegatedTask          bool                   `json:"delegated_task,omitempty"`
}

type persistedIdentity struct {
	UserID         string   `json:"user_id,omitempty"`
	Roles          []string `json:"roles,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	AuthMethod     string   `json:"auth_method,omitempty"`
	CredentialID   string   `json:"credential_id,omitempty"`
	PlatformUserID string   `json:"platform_user_id,omitempty"`
}

func newPersistedIdentity(id *auth.Identity) *persistedIdentity {
	if id == nil {
		return nil
	}
	return &persistedIdentity{
		UserID:         id.UserID,
		Roles:          id.Roles,
		Scopes:         id.Scopes,
		AuthMethod:     id.AuthMethod,
		CredentialID:   id.CredentialID,
		PlatformUserID: id.PlatformUserID,
	}
}

func (p *persistedIdentity) toIdentity() *auth.Identity {
	if p == nil {
		return nil
	}
	return &auth.Identity{
		UserID:         p.UserID,
		Roles:          p.Roles,
		Scopes:         p.Scopes,
		AuthMethod:     p.AuthMethod,
		CredentialID:   p.CredentialID,
		PlatformUserID: p.PlatformUserID,
	}
}

type persistedAuthOutcome struct {
	Success         bool   `json:"success"`
	CredentialKey   string `json:"credential_key,omitempty"`
	FunctionCallID  string `json:"function_call_id,omitempty"`
	ToolDisplayName string `json:"tool_display_name,omitempty"`
}

func newPersistedToolResult(r *tool.Result) *persistedToolResult {
	if r == nil {
		return nil
	}
	p := &persistedToolResult{
		Result:                 r,
		FailedDetail:           r.FailedDetail,
		ImageBlocks:            r.ImageBlocks,
		LoadedSkillContentHash: r.LoadedSkillContentHash,
		LLMData:                r.LLMData,
		ContentWindow:          r.ContentWindow,
		WithheldBytes:          r.WithheldBytes,
		DelegatedTask:          r.DelegatedTask,
	}
	for i := range r.DataObjects {
		if len(r.DataObjects[i].Content) == 0 {
			continue
		}
		if p.DataContent == nil {
			p.DataContent = make(map[int][]byte)
		}
		p.DataContent[i] = r.DataObjects[i].Content
	}
	return p
}

type persistedLLMData map[string]any

func (d *persistedLLMData) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(persistedLLMData, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	*d = out
	return nil
}

func (c *taskCheckpointer) marshalResult(taskID string, r *tool.Result) json.RawMessage {
	if r == nil {
		return nil
	}
	blob, err := json.Marshal(newPersistedToolResult(r))
	if err == nil && len(blob) <= maxToolResultPayloadBytes {
		return blob
	}
	if isPersonRequest(r) {
		if essentials, essentialsErr := json.Marshal(newPersistedToolResult(personRequestEssentials(r))); essentialsErr == nil {
			return essentials
		}
	}
	if r.DelegatedTask {
		if essentials, essentialsErr := json.Marshal(newPersistedToolResult(delegatedResultEssentials(r))); essentialsErr == nil && len(essentials) <= maxToolResultPayloadBytes {
			c.logger.Warn("a delegated result is too large to record in full; recording its state and the names of the files it produced without its other data",
				"taskID", taskID, "payloadBytes", len(blob), "limitBytes", maxToolResultPayloadBytes)
			return essentials
		}
	}
	if err != nil {
		c.logger.Warn("failed to serialize a tool result for its checkpoint; recording the message only",
			"taskID", taskID, "error", err)
		return nil
	}
	c.logger.Warn("tool result is too large to record in full; recording the message only, so a resumed turn sees its text without its structured data",
		"taskID", taskID, "payloadBytes", len(blob), "limitBytes", maxToolResultPayloadBytes)
	return nil
}

func delegatedResultEssentials(r *tool.Result) *tool.Result {
	data := map[string]any{}
	if status, _ := r.Data["status"].(map[string]any); status != nil {
		if state, ok := status["state"].(string); ok {
			data["status"] = map[string]any{"state": state}
		}
	}
	if metadata, _ := r.Data["metadata"].(map[string]any); metadata != nil {
		if produced, ok := metadata["produced_artifacts"]; ok {
			data["metadata"] = map[string]any{"produced_artifacts": produced}
		}
	}
	return &tool.Result{
		Status:        r.Status,
		Message:       boundedResultContent(r.Message),
		Data:          data,
		ErrorCode:     r.ErrorCode,
		FailedDetail:  r.FailedDetail,
		DelegatedTask: true,
	}
}

func resultFromPayload(payload json.RawMessage) *tool.Result {
	result, _ := decodeResultPayload(payload)
	return result
}

func decodeResultPayload(payload json.RawMessage) (*tool.Result, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var p persistedToolResult
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	return p.toResult(), nil
}

func (p *persistedToolResult) toResult() *tool.Result {
	if p == nil || p.Result == nil {
		return nil
	}
	r := p.Result
	r.FailedDetail = p.FailedDetail
	r.ImageBlocks = p.ImageBlocks
	r.LoadedSkillContentHash = p.LoadedSkillContentHash
	r.LLMData = p.LLMData
	r.ContentWindow = p.ContentWindow
	r.WithheldBytes = p.WithheldBytes
	r.DelegatedTask = p.DelegatedTask
	for i, content := range p.DataContent {
		if i >= 0 && i < len(r.DataObjects) {
			r.DataObjects[i].Content = content
		}
	}
	return r
}

func marshalTaskState(state *TaskExecutionState) ([]byte, error) {
	facts, err := json.Marshal(&state.usageFacts)
	if err != nil {
		return nil, fmt.Errorf("marshal usage facts: %w", err)
	}
	redactedProps := state.UserProps
	redactedProps.AuthToken = ""

	p := persistedTaskState{
		UsageFacts:                    facts,
		Version:                       checkpointVersionFor(state),
		TaskID:                        state.TaskID,
		ContextID:                     state.ContextID,
		EffectiveSessionID:            state.EffectiveSessionID,
		OriginalSessionID:             state.OriginalSessionID,
		ReplyTo:                       state.ReplyTo,
		UserProps:                     redactedProps,
		SpendOrigin:                   PersistedSpendOrigin(state.SpendOrigin),
		Identity:                      newPersistedIdentity(state.Identity),
		SealedAuthToken:               state.sealedAuthToken,
		IsRunBased:                    state.IsRunBased,
		CallerDisplaysTemplates:       state.CallerDisplaysTemplates,
		DisplaysTemplateSignals:       state.DisplaysTemplateSignals,
		QuietEndAllowed:               state.QuietEndAllowed,
		GatewayMetadata:               state.GatewayMetadata,
		Kind:                          state.kind,
		Question:                      questionToPersist(state),
		PendingRunFacts:               state.runFacts.snapshot(),
		LoopIteration:                 state.LoopIteration,
		MaxCalls:                      state.MaxCalls,
		LLMCallCount:                  state.LLMCallCount,
		TotalUsage:                    state.TotalUsage,
		LastInputTokens:               state.LastInputTokens,
		UsageByModel:                  state.UsageByModel,
		ToolUsageByModel:              state.ToolUsageByModel,
		CreatedArtifacts:              state.CreatedArtifacts,
		ToolSaves:                     state.toolSavesSnapshot(),
		ReportedFiles:                 state.ReportedFiles,
		HadVisibleText:                state.HadVisibleText,
		IsContinuation:                state.IsContinuation,
		ContinuationParts:             state.ContinuationParts,
		ImplicitContinuations:         state.ImplicitContinuations,
		LastFinishReason:              state.LastFinishReason,
		LastContent:                   state.LastContent,
		BlockValidationRetries:        state.BlockValidationRetries,
		CompactionRetries:             state.CompactionRetries,
		CompactionSummary:             state.CompactionSummary,
		StructuredCtx:                 state.StructuredCtx,
		HadToolFailure:                state.HadToolFailure,
		ToolFailureDetail:             state.ToolFailureDetail,
		ModelOverrideAlias:            state.ModelOverrideAlias,
		OverrideCacheStrategy:         state.OverrideCacheStrategy,
		OverrideReasoningBudgetTokens: state.OverrideReasoningBudgetTokens,
		OverrideReasoningEffort:       state.OverrideReasoningEffort,
		PausedAssistantMsg:            state.PausedAssistantMsg,
		PauseReplayed:                 state.pauseReplayed.Load(),
		FinishAfterArtifacts:          state.finishAfterArtifacts,
		SyntheticToolErrors:           state.SyntheticToolErrors,
		ParallelGroupID:               state.ParallelGroupID,
		TotalPending:                  atomic.LoadInt32(&state.TotalPending),
		CreatedAt:                     state.CreatedAt,
		PausedAt:                      state.PausedAt,
	}

	if len(state.PendingCalls) > 0 {
		p.PendingCalls = make(map[string]persistedPendingCall, len(state.PendingCalls))
		for id, pc := range state.PendingCalls {
			p.PendingCalls[id] = persistedPendingCall{
				ToolCallID:     pc.ToolCallID,
				ToolName:       pc.ToolName,
				Kind:           pc.Kind.String(),
				Index:          pc.Index,
				SubTaskID:      pc.SubTaskID,
				HILRequestID:   pc.HILRequestID,
				Timeout:        pc.Timeout,
				Result:         newPersistedToolResult(pc.Result),
				OriginalArgs:   pc.OriginalArgs,
				AuthRetries:    pc.AuthRetries,
				CredentialKey:  pc.CredentialKey,
				IsAuthFollower: pc.IsAuthFollower,
			}
		}
	}

	if len(state.SyncResults) > 0 {
		p.SyncResults = make(map[int]persistedToolResult, len(state.SyncResults))
		for idx, res := range state.SyncResults {
			if converted := newPersistedToolResult(res); converted != nil {
				p.SyncResults[idx] = *converted
			}
		}
	}

	if len(state.PausedApprovals) > 0 {
		p.PausedApprovals = make(map[string]persistedApprovalPause, len(state.PausedApprovals))
		for id, call := range state.PausedApprovals {
			p.PausedApprovals[id] = persistedApprovalPause{HILRequestID: call.requestID, Args: call.args, Timeout: call.timeout}
		}
	}

	if o := state.pendingAuthOutcome; o != nil {
		p.PendingAuthOutcome = &persistedAuthOutcome{
			Success:         o.success,
			CredentialKey:   o.credentialKey,
			FunctionCallID:  o.functionCallID,
			ToolDisplayName: o.toolDisplayName,
		}
	}

	return json.Marshal(p)
}

func unmarshalTaskState(data []byte) (*TaskExecutionState, error) {
	return unmarshalTaskStateUpTo(data, checkpointStateVersion)
}

func unmarshalTaskStateUpTo(data []byte, newestVersion int) (*TaskExecutionState, error) {
	var p persistedTaskState
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal task checkpoint: %w", err)
	}
	if p.Version > newestVersion {
		return nil, fmt.Errorf("task checkpoint version %d is newer than this build understands (%d): %w",
			p.Version, newestVersion, errCheckpointFromNewerBuild)
	}

	state := &TaskExecutionState{
		TaskID:                        p.TaskID,
		ContextID:                     p.ContextID,
		EffectiveSessionID:            p.EffectiveSessionID,
		OriginalSessionID:             p.OriginalSessionID,
		ReplyTo:                       p.ReplyTo,
		UserProps:                     p.UserProps,
		SpendOrigin:                   RestoredSpendOrigin(p.SpendOrigin),
		Identity:                      p.Identity.toIdentity(),
		sealedAuthToken:               p.SealedAuthToken,
		IsRunBased:                    p.IsRunBased,
		CallerDisplaysTemplates:       p.CallerDisplaysTemplates,
		DisplaysTemplateSignals:       p.DisplaysTemplateSignals,
		QuietEndAllowed:               p.QuietEndAllowed,
		GatewayMetadata:               p.GatewayMetadata,
		kind:                          p.Kind,
		Messages:                      p.Messages,
		HistoryOffset:                 p.HistoryOffset,
		LoopIteration:                 p.LoopIteration,
		MaxCalls:                      p.MaxCalls,
		LLMCallCount:                  p.LLMCallCount,
		TotalUsage:                    p.TotalUsage,
		LastInputTokens:               p.LastInputTokens,
		UsageByModel:                  p.UsageByModel,
		ToolUsageByModel:              p.ToolUsageByModel,
		CreatedArtifacts:              p.CreatedArtifacts,
		toolSaves:                     p.ToolSaves,
		ReportedFiles:                 p.ReportedFiles,
		HadVisibleText:                p.HadVisibleText,
		IsContinuation:                p.IsContinuation,
		ContinuationParts:             p.ContinuationParts,
		ImplicitContinuations:         p.ImplicitContinuations,
		LastFinishReason:              p.LastFinishReason,
		LastContent:                   p.LastContent,
		BlockValidationRetries:        p.BlockValidationRetries,
		CompactionRetries:             p.CompactionRetries,
		CompactionSummary:             p.CompactionSummary,
		StructuredCtx:                 p.StructuredCtx,
		finishAfterArtifacts:          p.FinishAfterArtifacts,
		HadToolFailure:                p.HadToolFailure,
		ToolFailureDetail:             p.ToolFailureDetail,
		ModelOverrideAlias:            p.ModelOverrideAlias,
		OverrideCacheStrategy:         p.OverrideCacheStrategy,
		OverrideReasoningBudgetTokens: p.OverrideReasoningBudgetTokens,
		OverrideReasoningEffort:       p.OverrideReasoningEffort,
		PausedAssistantMsg:            p.PausedAssistantMsg,
		SyntheticToolErrors:           p.SyntheticToolErrors,
		ParallelGroupID:               p.ParallelGroupID,
		TotalPending:                  p.TotalPending,
		CreatedAt:                     p.CreatedAt,
		PausedAt:                      p.PausedAt,
		PendingCalls:                  make(map[string]*PendingToolCall, len(p.PendingCalls)),
		SyncResults:                   make(map[int]*tool.Result, len(p.SyncResults)),
		subTaskCount:                  new(atomic.Int64),
		searchTurns:                   &tool.SearchTurns{},
		DoneCh:                        make(chan *a2a.Response, 1),
	}

	state.pauseReplayed.Store(p.PauseReplayed)
	if p.Question != nil {
		state.Question = *p.Question
	}
	state.rebuildFromRows = p.Version >= 2
	state.runFacts.restore(p.PendingRunFacts)

	if u := p.UnconfirmedAppend; u != nil {
		state.unconfirmedAppend = &unconfirmedSessionAppend{storedBefore: u.StoredBefore, msgs: u.Messages}
	}

	if len(p.UsageFacts) > 0 {
		if factsErr := state.usageFacts.UnmarshalJSON(p.UsageFacts); factsErr != nil {
			return nil, fmt.Errorf("restore usage facts: %w", factsErr)
		}
	}

	for id, pc := range p.PendingCalls {
		kind, known := parsePendingCallKind(pc.Kind)
		if !known {
			return nil, fmt.Errorf("unknown pending call kind %q for tool call %q: %w", pc.Kind, pc.ToolCallID, errCheckpointFromNewerBuild)
		}
		state.PendingCalls[id] = &PendingToolCall{
			ToolCallID:     pc.ToolCallID,
			ToolName:       pc.ToolName,
			Kind:           kind,
			Index:          pc.Index,
			SubTaskID:      pc.SubTaskID,
			HILRequestID:   pc.HILRequestID,
			Timeout:        pc.Timeout,
			Result:         pc.Result.toResult(),
			OriginalArgs:   pc.OriginalArgs,
			AuthRetries:    pc.AuthRetries,
			CredentialKey:  pc.CredentialKey,
			IsAuthFollower: pc.IsAuthFollower,
		}
	}

	for idx, res := range p.SyncResults {
		state.SyncResults[idx] = res.toResult()
	}

	if len(p.PausedApprovals) > 0 {
		state.PausedApprovals = make(map[string]pausedApprovalCall, len(p.PausedApprovals))
		for id, paused := range p.PausedApprovals {
			state.PausedApprovals[id] = pausedApprovalCall{requestID: paused.HILRequestID, args: paused.Args, timeout: paused.Timeout}
		}
	}

	if o := p.PendingAuthOutcome; o != nil {
		state.pendingAuthOutcome = &authOutcome{
			success:         o.Success,
			credentialKey:   o.CredentialKey,
			functionCallID:  o.FunctionCallID,
			toolDisplayName: o.ToolDisplayName,
		}
	}

	return state, nil
}

func questionToPersist(state *TaskExecutionState) *llm.ChatMessage {
	if state.Question.Role == "" {
		return nil
	}
	q := state.Question
	return &q
}
