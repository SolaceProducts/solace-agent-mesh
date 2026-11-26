// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type Event interface {
	isWorkflowEvent()
}

type EventRestore struct{}

func (*EventRestore) isWorkflowEvent() {
}

type EventStart struct {
	TaskID                 string
	ContextID              string
	Input                  map[string]any
	UserProps              a2a.UserProperties
	OriginalMsg            *broker.Message
	IsStructuredInvocation bool
}

func (*EventStart) isWorkflowEvent() {
}

type EventNodeComplete struct {
	NodeID     string
	SubTaskID  string
	Output     map[string]any
	MapNodeID  string
	LoopNodeID string
	ItemIndex  int
}

func (*EventNodeComplete) isWorkflowEvent() {
}

type EventNodeFailed struct {
	NodeID     string
	SubTaskID  string
	Err        error
	ErrKind    ErrorKind
	MapNodeID  string
	LoopNodeID string
	ItemIndex  int
}

func (*EventNodeFailed) isWorkflowEvent() {
}

type EventNodePaused struct {
	NodeID     string
	SubTaskID  string
	RequestID  string
	Surface    json.RawMessage
	ExpiresAt  string
	MapNodeID  string
	LoopNodeID string
	ItemIndex  int

	Kind             string
	ToolNodeID       string
	ToolName         string
	PendingArgs      map[string]any
	retry            retryIdentity
	startUnpublished bool

	parked   bool
	unparked tool.ApprovalRefusal
	closed   bool
}

func (*EventNodePaused) isWorkflowEvent() {
}

type EventNodeApproved struct {
	NodeID     string
	ToolNodeID string
	SubTaskID  string
	ToolName   string
	Args       map[string]any
	MapNodeID  string
	LoopNodeID string
	ItemIndex  int
	retry      retryIdentity
}

func (*EventNodeApproved) isWorkflowEvent() {
}

type EventRetryDue struct {
	NodeID string
}

func (*EventRetryDue) isWorkflowEvent() {
}

type EventNodeTimeout struct {
	NodeID     string
	SubTaskID  string
	TimeoutMsg string
	Duration   time.Duration
	MapNodeID  string
	LoopNodeID string
	ItemIndex  int
}

func (*EventNodeTimeout) isWorkflowEvent() {
}

type EventWorkflowTimeout struct{}

func (*EventWorkflowTimeout) isWorkflowEvent() {
}

type EventCancel struct {
	Reason string
}

func (*EventCancel) isWorkflowEvent() {
}

type EventInstanceStopping struct{}

func (*EventInstanceStopping) isWorkflowEvent() {
}
