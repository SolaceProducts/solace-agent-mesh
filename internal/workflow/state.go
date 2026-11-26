// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

type WorkflowExecutionState struct {
	WorkflowName string    `json:"workflow_name"`
	ExecutionID  string    `json:"execution_id"`
	StartTime    time.Time `json:"start_time"`

	CompletedNodes map[string]string    `json:"completed_nodes"`
	PendingNodes   map[string]bool      `json:"pending_nodes"`
	SkippedNodes   map[string]string    `json:"skipped_nodes"`
	SkipCauses     map[string]SkipCause `json:"skip_causes,omitempty"`

	NodeOutputs map[string]map[string]any `json:"node_outputs"`

	ParallelBranchAssignments map[string]map[string]int `json:"parallel_branch_assignments"`

	ActiveBranches map[string][]BranchInfo `json:"active_branches"`

	LoopIterations map[string]int `json:"loop_iterations"`

	RetryCounts map[string]int    `json:"retry_counts"`
	RetryGroups map[string]string `json:"retry_groups,omitempty"`

	ErrorState  *ErrorInfo  `json:"error_state,omitempty"`
	FailedNodes []ErrorInfo `json:"failed_nodes,omitempty"`

	Metadata map[string]any `json:"metadata"`

	WorkflowDeadline  time.Time `json:"workflow_deadline,omitzero"`
	Cancelled         bool      `json:"cancelled,omitempty"`
	TerminationReason string    `json:"termination_reason,omitempty"`
	InExitHandler     bool      `json:"in_exit_handler,omitempty"`
	ExitHandlersRan   bool      `json:"exit_handlers_ran,omitempty"`
	Finalized         bool      `json:"finalized,omitempty"`

	HasPausedRows bool `json:"has_paused_rows,omitempty"`

	RestoredAttempts map[string]int `json:"restored_attempts,omitempty"`

	SubTasks map[string]string `json:"sub_tasks"`

	SubTaskTargets map[string]string `json:"sub_task_targets,omitempty"`

	Timers []Timer `json:"timers,omitempty"`

	PendingOutcome   string      `json:"pending_outcome,omitempty"`
	PendingErrorMsg  string      `json:"pending_error_msg,omitempty"`
	BudgetResetsAt   *time.Time  `json:"budget_resets_at,omitempty"`
	BudgetTier       budget.Tier `json:"budget_tier,omitempty"`
	BudgetSystemUser string      `json:"budget_system_user,omitempty"`
	ExitHandlerQueue []string    `json:"exit_handler_queue,omitempty"`

	UsageFacts usagefacts.Facts `json:"usage_facts"`

	DecisionUsage map[string]DecisionTokens `json:"decision_usage,omitempty"`

	SearchTurns tool.SearchTurns `json:"search_turns"`

	mu sync.Mutex
}

type TimerKind string

const (
	TimerKindRetry           TimerKind = "retry"
	TimerKindNodeTimeout     TimerKind = "node_timeout"
	TimerKindWorkflowTimeout TimerKind = "workflow_timeout"
	TimerKindWake            TimerKind = "wake"
)

type Timer struct {
	Kind       TimerKind     `json:"kind"`
	NodeID     string        `json:"node_id,omitempty"`
	SubTaskID  string        `json:"sub_task_id,omitempty"`
	FiresAt    time.Time     `json:"fires_at"`
	Duration   time.Duration `json:"duration,omitempty"`
	MapNodeID  string        `json:"map_node_id,omitempty"`
	LoopNodeID string        `json:"loop_node_id,omitempty"`
	ItemIndex  int           `json:"item_index,omitempty"`
}

type BranchInfo struct {
	Index     int           `json:"index,omitempty"`
	Iteration int           `json:"iteration,omitempty"`
	SubTaskID string        `json:"sub_task_id"`
	Type      string        `json:"type"`
	Result    *BranchResult `json:"result,omitempty"`
}

type BranchResult struct {
	ArtifactName    string `json:"artifact_name"`
	ArtifactVersion int    `json:"artifact_version"`
}

type ErrorInfo struct {
	FailedNodeID  string `json:"failed_node_id"`
	FailureReason string `json:"failure_reason"`
	ErrorMessage  string `json:"error_message"`
	Timestamp     string `json:"timestamp"`
}

func NewExecutionState(workflowName, executionID string) *WorkflowExecutionState {
	return &WorkflowExecutionState{
		WorkflowName:              workflowName,
		ExecutionID:               executionID,
		StartTime:                 time.Now(),
		CompletedNodes:            make(map[string]string),
		PendingNodes:              make(map[string]bool),
		SkippedNodes:              make(map[string]string),
		NodeOutputs:               map[string]map[string]any{"workflow": {"run_id": executionID}},
		ParallelBranchAssignments: make(map[string]map[string]int),
		ActiveBranches:            make(map[string][]BranchInfo),
		LoopIterations:            make(map[string]int),
		RetryCounts:               make(map[string]int),
		Metadata:                  make(map[string]any),
		SubTasks:                  make(map[string]string),
		SubTaskTargets:            make(map[string]string),
	}
}

func (s *WorkflowExecutionState) IsNodeCompleted(nodeID string) bool {
	_, ok := s.CompletedNodes[nodeID]
	return ok
}

func (s *WorkflowExecutionState) IsNodeSkipped(nodeID string) bool {
	_, ok := s.SkippedNodes[nodeID]
	return ok
}

type SkipCause string

const (
	SkipCauseUnknown        SkipCause = ""
	SkipCauseConditionFalse SkipCause = "condition_false"
	SkipCauseNotSelected    SkipCause = "not_selected"
	SkipCauseUnresolved     SkipCause = "unresolved_reference"
	SkipCauseFailed         SkipCause = "failed"
	SkipCauseUpstreamFailed SkipCause = "upstream_failed"
)

func (c SkipCause) Pruned() bool {
	return c == SkipCauseConditionFalse || c == SkipCauseNotSelected
}

const (
	severityPrune = iota
	severityUnresolved
	severityUnknown
	severityFailure
)

func (c SkipCause) severity() int {
	switch c {
	case SkipCauseConditionFalse, SkipCauseNotSelected:
		return severityPrune
	case SkipCauseUnresolved:
		return severityUnresolved
	case SkipCauseFailed, SkipCauseUpstreamFailed:
		return severityFailure
	default:
		return severityUnknown
	}
}

func (s *WorkflowExecutionState) markSkipped(nodeID, reason string, cause SkipCause) {
	s.SkippedNodes[nodeID] = reason
	if s.SkipCauses == nil {
		s.SkipCauses = make(map[string]SkipCause)
	}
	s.SkipCauses[nodeID] = cause
}

func (s *WorkflowExecutionState) SkipCause(nodeID string) SkipCause {
	return s.SkipCauses[nodeID]
}

func (s *WorkflowExecutionState) IsNodePending(nodeID string) bool {
	return s.PendingNodes[nodeID]
}

func (s *WorkflowExecutionState) AddPending(nodeID string) {
	s.PendingNodes[nodeID] = true
}

func (s *WorkflowExecutionState) RemovePending(nodeID string) {
	delete(s.PendingNodes, nodeID)
}

func (s *WorkflowExecutionState) TrackSubTask(subTaskID, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SubTasks[subTaskID] = nodeID
}

func (s *WorkflowExecutionState) NodeForSubTask(subTaskID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nodeID, ok := s.SubTasks[subTaskID]
	return nodeID, ok
}

func (s *WorkflowExecutionState) SetSubTaskTarget(subTaskID, brokerName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SubTaskTargets == nil {
		s.SubTaskTargets = make(map[string]string)
	}
	s.SubTaskTargets[subTaskID] = brokerName
}

func (s *WorkflowExecutionState) TargetForSubTask(subTaskID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.SubTaskTargets[subTaskID]
	return name, ok
}

func (s *WorkflowExecutionState) RemoveSubTaskByNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for subTaskID, n := range s.SubTasks {
		if n == nodeID {
			delete(s.SubTasks, subTaskID)
			delete(s.SubTaskTargets, subTaskID)
		}
	}
}

func (s *WorkflowExecutionState) RemoveSubTask(subTaskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.SubTasks, subTaskID)
	delete(s.SubTaskTargets, subTaskID)
}

func (s *WorkflowExecutionState) SnapshotSubTasks() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.SubTasks))
	maps.Copy(out, s.SubTasks)
	return out
}

func (s *WorkflowExecutionState) SubTaskIDs() []string {
	snap := s.SnapshotSubTasks()
	out := make([]string, 0, len(snap))
	for id := range snap {
		out = append(out, id)
	}
	return out
}

func (s *WorkflowExecutionState) RetryGroupFor(nodeID string) string {
	if group, ok := s.RetryGroups[nodeID]; ok {
		return group
	}
	if s.RetryGroups == nil {
		s.RetryGroups = make(map[string]string)
	}
	group := idgen.New()
	s.RetryGroups[nodeID] = group
	return group
}

func (s *WorkflowExecutionState) AddTimer(t Timer) {
	s.Timers = append(s.Timers, t)
}

func (s *WorkflowExecutionState) RemoveTimer(kind TimerKind, nodeID string) bool {
	for i, t := range s.Timers {
		if t.Kind == kind && t.NodeID == nodeID {
			s.Timers = append(s.Timers[:i], s.Timers[i+1:]...)
			return true
		}
	}
	return false
}

func (s *WorkflowExecutionState) RemoveTimerBySubTask(kind TimerKind, subTaskID string) bool {
	for i, t := range s.Timers {
		if t.Kind == kind && t.SubTaskID == subTaskID {
			s.Timers = append(s.Timers[:i], s.Timers[i+1:]...)
			return true
		}
	}
	return false
}

type WorkflowExecutionContext struct {
	Ctx    context.Context
	Cancel context.CancelFunc

	ToolCtx    context.Context
	ToolCancel context.CancelFunc

	requestMu    sync.Mutex
	request      *broker.Message
	requestAcked bool
	holder       wfstore.Holder

	UserProps a2a.UserProperties

	TaskID    string
	ContextID string

	IsStructuredInvocation bool

	SpendOrigin agent.SpendOrigin

	ModelOverrideAlias string

	Identity *auth.Identity

	questionsMu sync.Mutex
	questions   map[string]string
}

func (c *WorkflowExecutionContext) recordQuestion(requestID, subTaskID string) {
	c.questionsMu.Lock()
	defer c.questionsMu.Unlock()
	if c.questions == nil {
		c.questions = make(map[string]string)
	}
	c.questions[requestID] = subTaskID
}

func (c *WorkflowExecutionContext) askerOf(requestID string) (subTaskID string, ok bool) {
	c.questionsMu.Lock()
	defer c.questionsMu.Unlock()
	subTaskID, ok = c.questions[requestID]
	return subTaskID, ok
}

func (c *WorkflowExecutionContext) forgetQuestion(requestID string) {
	c.questionsMu.Lock()
	defer c.questionsMu.Unlock()
	delete(c.questions, requestID)
}

func NewExecutionContext(
	ctx context.Context,
	taskID, contextID string,
	msg *broker.Message,
	props a2a.UserProperties,
	isStructured bool,
) *WorkflowExecutionContext {
	execCtx, cancel := context.WithCancel(ctx)
	toolCtx, toolCancel := context.WithCancel(execCtx)
	return &WorkflowExecutionContext{
		Ctx:                    execCtx,
		Cancel:                 cancel,
		ToolCtx:                toolCtx,
		ToolCancel:             toolCancel,
		request:                msg,
		UserProps:              props,
		TaskID:                 taskID,
		ContextID:              contextID,
		IsStructuredInvocation: isStructured,
	}
}

func (c *WorkflowExecutionContext) ackRequest() {
	c.requestMu.Lock()
	msg := c.request
	acked := c.requestAcked
	c.requestAcked = true
	c.request = nil
	c.requestMu.Unlock()
	if msg != nil && !acked {
		msg.Ack()
	}
}

func (c *WorkflowExecutionContext) holdWith(msg *broker.Message, h wfstore.Holder) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.request, c.requestAcked, c.holder = msg, false, h
}

func (c *WorkflowExecutionContext) holderOf() wfstore.Holder {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	return c.holder
}

func (c *WorkflowExecutionContext) setHolder(h wfstore.Holder) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.holder = h
}

func (c *WorkflowExecutionContext) dropRequest() {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.request, c.requestAcked = nil, true
}

func (c *WorkflowExecutionContext) holderKey() string {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	return c.holder.Key
}

func (c *WorkflowExecutionContext) holdsRequest() bool {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	return c.request != nil && !c.requestAcked
}

func (c *WorkflowExecutionContext) adoptRedelivery(msg *broker.Message) {
	c.requestMu.Lock()
	acked := c.requestAcked
	if !acked {
		c.request = msg
	}
	c.requestMu.Unlock()
	if acked {
		msg.Ack()
	}
}

func (s *WorkflowExecutionState) SubTaskForNode(nodeID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for subTaskID, n := range s.SubTasks {
		if n == nodeID {
			return subTaskID, true
		}
	}
	return "", false
}

type ErrorKind int

const (
	ErrorKindUnknown ErrorKind = iota
	ErrorKindFailure
	ErrorKindError
)

type DecisionTokens struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type NodeCompletion struct {
	NodeID    string
	SubTaskID string
	Output    map[string]any
	Error     error
	ErrorKind ErrorKind

	MapNodeID string
	ItemIndex int

	LoopNodeID string

	Retry bool
}
