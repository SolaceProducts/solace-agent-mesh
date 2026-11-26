// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"slices"
	"strings"
)

var ErrNoModel = errors.New("no model configured for this request")

type Client interface {
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
}

type ChatRequest struct {
	Model string

	Messages []ChatMessage

	Tools []ToolDefinition

	ToolChoice string

	MaxTokens int

	Temperature *float64

	StopSequences []string

	ParallelToolCalls *bool

	Reasoning *ReasoningConfig

	ExtraParams map[string]any

	WithStoredParams bool

	ModelOverride *BifrostConfig

	Observability ObservabilityContext

	Spend *SpendAttribution
}

func (r *ChatRequest) EffectiveModel(boundModel string) string {
	if r.ModelOverride != nil && r.ModelOverride.Model != "" {
		return r.ModelOverride.Model
	}
	if r.Model != "" {
		return r.Model
	}
	return boundModel
}

type SpendAttribution struct {
	UserID         string
	UserSub        string
	AgentID        string
	AgentName      string
	WorkflowID     string
	WorkflowName   string
	WorkflowNodeID string
	RootKind       string
	RootID         string
	RootName       string
	RootNodeID     string
	EntrypointID   string
	EntrypointName string
	Channel        string
	TriggerKind    string
	TriggerID      string
	Surface        string
	ToolName       string
	ModelKey       string
	ModelConfigID  string
	TaskID         string
	ParentTaskID   string
	TraceID        string
	SessionID      string

	AvoidedInputBytes *int64
}

type ObservabilityContext struct {
	ComponentName string
	OwnerID       string
}

type ReasoningConfig struct {
	BudgetTokens int
	Effort       string
}

var acceptedEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max", "none"}

func NormalizeReasoningEffort(effort string) string {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	if slices.Contains(acceptedEfforts, normalized) {
		return normalized
	}
	return ""
}

type ChatMessage struct {
	Role         string
	Content      string
	ToolCalls    []ToolCall
	ToolCallID   string
	ToolName     string
	CacheControl *CacheControl

	Reasoning string

	NoCacheBreakpoint bool

	OmitFromTrace bool

	ContentBlocks []ContentBlock

	WithheldBytes int64
}

type ContentBlock struct {
	Type        string
	Text        string
	ImageURL    string
	AudioData   string
	AudioFormat string
	FileData    string
	FileName    string
	FileType    string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolDefinition struct {
	Name         string
	Description  string
	Parameters   map[string]any
	CacheControl *CacheControl

	DeferLoading bool `json:",omitempty"`
}

type ChatResponse struct {
	Content string

	ReasoningContent string

	RawContent string

	ToolCalls []ToolCall

	FinishReason string

	Usage *Usage

	Attempts int

	Model string
}

type StreamChunk struct {
	ContentDelta string

	ReasoningDelta string

	ToolCallDeltas []ToolCallDelta

	FinishReason string

	Usage *Usage

	Err error

	Attempts int

	Model string
}

type ToolCallDelta struct {
	Index          int
	ID             string
	Name           string
	ArgumentsDelta string
}

type Usage struct {
	PromptTokens        int
	CompletionTokens    int
	TotalTokens         int
	CachedTokens        int
	CacheWriteTokens    int
	CacheWritesReported bool
	ReasoningTokens     int
}

type TokenBuckets struct {
	Input      int
	CacheRead  int
	CacheWrite int
	Output     int
}

func (u Usage) Buckets() TokenBuckets {
	prompt := max(u.PromptTokens, 0)
	cached := min(max(u.CachedTokens, 0), prompt)
	cacheWrite := min(max(u.CacheWriteTokens, 0), cached)
	return TokenBuckets{
		Input:      prompt - cached,
		CacheRead:  cached - cacheWrite,
		CacheWrite: cacheWrite,
		Output:     max(u.CompletionTokens, 0),
	}
}

type CacheControl struct {
	Type string
	TTL  string
}
