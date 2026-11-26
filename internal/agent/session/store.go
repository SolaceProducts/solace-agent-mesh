// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrOptimisticLock = errors.New("optimistic lock failure")

var ErrSessionExists = errors.New("session already exists")

type Store interface {
	Get(ctx context.Context, appName, userID, sessionID string) (*Session, error)

	GetByContextAndTask(ctx context.Context, appName, userID, contextID, taskID string) (*Session, error)

	Create(ctx context.Context, s *Session) error

	Save(ctx context.Context, s *Session) error

	AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []Message) error

	Delete(ctx context.Context, appName, userID, sessionID string) error

	ListSessions(ctx context.Context, appName, userID string) ([]*Session, error)

	SaveCheckpoint(ctx context.Context, cp *TaskCheckpoint) error

	UpdateCheckpoint(ctx context.Context, cp *TaskCheckpoint) error

	MarkAnswering(ctx context.Context, taskID, ownerID string, final *FinalMessage, until time.Time) (bool, error)

	RecordToolResult(ctx context.Context, taskID, corrID string, result *ToolCallResult) (pendingCount int, err error)

	AdoptCheckpoint(ctx context.Context, taskID, newOwnerID string, now time.Time, leaseFor, silentFor time.Duration, releasedOnly bool) (*TaskCheckpoint, error)

	PopCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error)

	GetCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error)

	ReclaimUnowned(ctx context.Context, taskID, ownerID string, until time.Time) (bool, error)

	CheckpointOwner(ctx context.Context, taskID string) (ownerID string, exists bool, err error)

	RenewLeases(ctx context.Context, ownerID string, taskIDs []string, until time.Time) (int, error)

	ReleaseOwnership(ctx context.Context, taskID, ownerID string) error

	RegisterRunningTask(ctx context.Context, task RunningTask, ownerID string, until time.Time) error

	RenewRunningTasks(ctx context.Context, ownerID string, tasks []RunningTask, until time.Time) (int, error)

	DeleteLapsedRunningTasks(ctx context.Context, appName string, lapsedBefore time.Time) (int, error)

	RemoveRunningTask(ctx context.Context, taskID, ownerID string) error

	CheckpointedTasks(ctx context.Context, sessionID string, taskIDs []string) ([]string, error)

	ListRunningTasks(ctx context.Context, appName, userID, sessionID string, liveAfter time.Time) ([]string, error)

	DeleteCheckpoint(ctx context.Context, taskID string) error

	RegisterPendingCalls(ctx context.Context, taskID, ownerID string, calls []PendingCall) error

	LookupPendingCall(ctx context.Context, agentName, corrID string) (*PendingCall, error)

	PendingCallTask(ctx context.Context, corrID string) (taskID string, held bool, err error)

	DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (deleted bool, err error)

	DeleteAbandonedCheckpoint(ctx context.Context, taskID string, lapsedBefore time.Time) (deleted bool, err error)
	PurgeAbandonedCheckpoints(ctx context.Context, agentName string, lapsedBefore time.Time, limit int) (int, error)

	DeletePendingCall(ctx context.Context, corrID string) error

	ListExpiredPendingCalls(ctx context.Context, agentName string, now time.Time, limit int) ([]PendingCall, error)

	ListResumableCheckpoints(ctx context.Context, agentName string, now time.Time, silentFor time.Duration, limit int, releasedOnly bool) ([]TaskCheckpoint, error)
}

const (
	PendingKindPeer         = "peer"
	PendingKindSubTask      = "subtask"
	PendingKindWorkflowPeer = "workflow_peer"
	PendingKindRemoteTool   = "str"
	PendingKindAuth         = "auth"
	PendingKindHILApproval  = "hil_approval"
	PendingKindToolApproval = "tool_approval"
)

type PendingCall struct {
	CorrID     string
	TaskID     string
	ToolCallID string
	AgentName  string
	ToolName   string
	TraceID    string
	Kind       string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
}

type Session struct {
	AppName   string
	UserID    string
	SessionID string
	ContextID string
	TaskID    *string
	Version   int
	Messages  []Message
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	Role      string
	Content   string
	ToolCalls []ToolCall
	ToolCallResult
	Timestamp     time.Time
	Reasoning     string    `json:",omitempty"`
	WithheldBytes int64     `json:",omitempty"`
	TaskID        string    `json:",omitempty"`
	RunFacts      *RunFacts `json:",omitempty"`
}

type RunFacts struct {
	Usage   []CallUsage    `json:",omitempty"`
	Files   []ProducedFile `json:",omitempty"`
	Failure *ToolFailure   `json:",omitempty"`
	Final   bool           `json:",omitempty"`
	Answer  string         `json:",omitempty"`
	Quiet   bool           `json:",omitempty"`
}

type CallUsage struct {
	Model  string          `json:",omitempty"`
	Source string          `json:",omitempty"`
	Tokens json.RawMessage `json:",omitempty"`
}

type ProducedFile struct {
	Filename string
	Version  int
	Source   string   `json:",omitempty"`
	Status   string   `json:",omitempty"`
	Error    string   `json:",omitempty"`
	Tags     []string `json:",omitempty"`
}

type ToolFailure struct {
	Code       string `json:",omitempty"`
	Message    string `json:",omitempty"`
	StderrTail string `json:",omitempty"`
	StackTrace string `json:",omitempty"`
}

type RunningTask struct {
	TaskID    string
	AppName   string
	UserID    string
	SessionID string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

var ErrCallNotOutstanding = errors.New("session: tool call is not outstanding")

var ErrNotOwner = errors.New("session: checkpoint is not held by this owner")

type CorrelationTakenError struct {
	CorrID string
}

func (e *CorrelationTakenError) Error() string {
	return "session: correlation id " + e.CorrID + " already belongs to another task"
}

type FinalMessage struct {
	Topic          string            `json:"topic"`
	Payload        []byte            `json:"payload"`
	UserProperties map[string]string `json:"user_properties,omitempty"`
}

type TaskCheckpoint struct {
	TaskID         string
	AppName        string
	UserID         string
	SessionID      string
	AgentName      string
	PendingCount   int
	State          []byte
	ToolResults    []ToolCallResult
	OwnerID        string
	LeaseExpiresAt *time.Time
	RenewedAt      *time.Time
	ReadAt         time.Time
	Final          *FinalMessage
	TraceID        string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	ReplacePendingCalls bool
	PendingCalls        []PendingCall
}

type ToolCallResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
	Payload    json.RawMessage `json:",omitempty"`

	Uncounted bool `json:"-"`
}
