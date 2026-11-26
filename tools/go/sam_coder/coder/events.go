// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	EventModel = "model"
	EventTool  = "tool"
)

type Event struct {
	Kind      string             `json:"kind"`
	Step      int                `json:"step"`
	At        time.Time          `json:"at"`
	Duration  time.Duration      `json:"duration_ns"`
	Content   string             `json:"content,omitempty"`
	ToolCalls []sdk.ChatToolCall `json:"tool_calls,omitempty"`
	Usage     *sdk.ChatUsage     `json:"usage,omitempty"`
	Tool      string             `json:"tool,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Result    string             `json:"result,omitempty"`
}
