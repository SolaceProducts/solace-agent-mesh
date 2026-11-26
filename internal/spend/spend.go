// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

type Surface string

const (
	SurfaceAgent                  Surface = "agent"
	SurfaceCompaction             Surface = "compaction"
	SurfaceDecision               Surface = "decision"
	SurfaceTool                   Surface = "tool"
	SurfaceSessionTitle           Surface = "session_title"
	SurfacePromptBuilder          Surface = "prompt_builder"
	SurfaceTaskBuilder            Surface = "task_builder"
	SurfaceAgentGenerator         Surface = "agent_generator"
	SurfaceEvalJudge              Surface = "eval_judge"
	SurfaceModelTest              Surface = "model_test"
	SurfaceEvidenceJudge          Surface = "evidence_judge"
	SurfaceEvidenceDrafter        Surface = "evidence_drafter"
	SurfaceEvidenceScreen         Surface = "evidence_screen"
	SurfaceEvidenceExplainer      Surface = "evidence_explainer"
	SurfaceEvidenceEvalSetDrafter Surface = "evidence_eval_set_drafter"
	SurfaceEvidenceCoder          Surface = "evidence_coder"
)

type TriggerKind string

const (
	TriggerChat     TriggerKind = "chat"
	TriggerSchedule TriggerKind = "schedule"
	TriggerEvent    TriggerKind = "event"
	TriggerWorkflow TriggerKind = "workflow"
	TriggerAgent    TriggerKind = "agent"
	TriggerEval     TriggerKind = "eval"
	TriggerRequest  TriggerKind = "request"
	TriggerSystem   TriggerKind = "system"
)

type Channel string

type RootKind string

const (
	RootAgent    RootKind = a2a.RootKindAgent
	RootWorkflow RootKind = a2a.RootKindWorkflow
)

type Tokens struct {
	UncachedInput int64 `json:"uncached_input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheWrite    int64 `json:"cache_write"`
}

func TokensFromUsage(u llm.Usage) Tokens {
	prompt := max(u.PromptTokens, 0)
	cached := min(max(u.CachedTokens, 0), prompt)
	cacheWrite := min(max(u.CacheWriteTokens, 0), cached)
	return Tokens{
		UncachedInput: int64(prompt - cached),
		Output:        nonNegative(u.CompletionTokens),
		CacheRead:     int64(cached - cacheWrite),
		CacheWrite:    int64(cacheWrite),
	}
}

func nonNegative(n int) int64 {
	return int64(max(n, 0))
}

func (t Tokens) total() int64 {
	return t.UncachedInput + t.Output + t.CacheRead + t.CacheWrite
}

type Cost struct {
	UncachedInput int64
	Output        int64
	CacheRead     int64
	CacheWrite    int64
}

func CostOf(t Tokens, p pricing.ModelPrice) Cost {
	return Cost{
		UncachedInput: p.Cost(pricing.BucketInput, t.UncachedInput),
		Output:        p.Cost(pricing.BucketOutput, t.Output),
		CacheRead:     p.Cost(pricing.BucketCacheRead, t.CacheRead),
		CacheWrite:    p.Cost(pricing.BucketCacheWrite, t.CacheWrite),
	}
}

const EventSchemaVersion = 1

type Event struct {
	SchemaVersion       int         `json:"schema_version,omitempty"`
	ID                  string      `json:"id"`
	CompletedTime       int64       `json:"completed_time"`
	UserID              string      `json:"user_id,omitempty"`
	UserSub             string      `json:"user_sub,omitempty"`
	AgentID             string      `json:"agent_id,omitempty"`
	AgentName           string      `json:"agent_name,omitempty"`
	WorkflowID          string      `json:"workflow_id,omitempty"`
	WorkflowName        string      `json:"workflow_name,omitempty"`
	WorkflowNodeID      string      `json:"workflow_node_id,omitempty"`
	RootKind            RootKind    `json:"root_kind,omitempty"`
	RootID              string      `json:"root_id,omitempty"`
	RootName            string      `json:"root_name,omitempty"`
	RootNodeID          string      `json:"root_node_id,omitempty"`
	EntrypointID        string      `json:"entrypoint_id,omitempty"`
	EntrypointName      string      `json:"entrypoint_name,omitempty"`
	Channel             Channel     `json:"channel,omitempty"`
	TriggerKind         TriggerKind `json:"trigger_kind,omitempty"`
	TriggerID           string      `json:"trigger_id,omitempty"`
	Surface             Surface     `json:"surface"`
	ToolName            string      `json:"tool_name,omitempty"`
	ModelKey            string      `json:"model_key,omitempty"`
	ModelConfigID       string      `json:"model_config_id,omitempty"`
	ModelName           string      `json:"model_name,omitempty"`
	TaskID              string      `json:"task_id,omitempty"`
	ParentTaskID        string      `json:"parent_task_id,omitempty"`
	TraceID             string      `json:"trace_id,omitempty"`
	SessionID           string      `json:"session_id,omitempty"`
	AttemptCount        int         `json:"attempt_count,omitempty"`
	CacheWritesReported *bool       `json:"cache_writes_reported,omitempty"`
	Tokens              Tokens      `json:"tokens"`

	AvoidedInputBytes *int64 `json:"avoided_input_bytes,omitempty"`
}

const BytesPerEstimatedToken = 4

func EstimatedTokens(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return n / BytesPerEstimatedToken
}

func AvoidedInputCost(avoidedTokens int64, t Tokens, c Cost) (cost int64, ok bool) {
	inputTokens := t.UncachedInput + t.CacheRead + t.CacheWrite
	if avoidedTokens <= 0 || inputTokens <= 0 {
		return 0, inputTokens > 0
	}
	inputCost := new(big.Int).SetInt64(c.UncachedInput)
	inputCost.Add(inputCost, big.NewInt(c.CacheRead))
	inputCost.Add(inputCost, big.NewInt(c.CacheWrite))
	avoided := new(big.Int).Mul(inputCost, big.NewInt(avoidedTokens))
	avoided.Quo(avoided, big.NewInt(inputTokens))
	if !avoided.IsInt64() {
		return math.MaxInt64, true
	}
	return avoided.Int64(), true
}

func IsCanonicalID(s string) bool {
	return idgen.IsCanonical(s)
}

var ErrInvalidEvent = errors.New("invalid spend event")

const MaxFieldBytes = 256

const (
	RetentionWindow = 90 * 24 * time.Hour
	MaxClockSkew    = 5 * time.Minute
)

func (e *Event) Sanitize(now time.Time) (repairs []string, err error) {
	if !IsCanonicalID(e.ID) {
		return nil, invalid("id is not a canonical non-nil UUID")
	}
	if err := requireStorable("surface", string(e.Surface)); err != nil {
		return nil, err
	}

	for _, f := range []struct {
		name  string
		value *string
	}{
		{"user_sub", &e.UserSub}, {"agent_name", &e.AgentName}, {"workflow_name", &e.WorkflowName},
		{"workflow_node_id", &e.WorkflowNodeID}, {"root_name", &e.RootName}, {"root_node_id", &e.RootNodeID},
		{"entrypoint_name", &e.EntrypointName}, {"trigger_id", &e.TriggerID}, {"tool_name", &e.ToolName},
		{"model_key", &e.ModelKey}, {"model_name", &e.ModelName},
		{"task_id", &e.TaskID}, {"parent_task_id", &e.ParentTaskID},
		{"trace_id", &e.TraceID}, {"session_id", &e.SessionID},
	} {
		repairs = clearIfUnstorable(repairs, f.name, f.value)
	}
	repairs = clearIfUnstorable(repairs, "root_kind", &e.RootKind)
	repairs = clearIfUnstorable(repairs, "channel", &e.Channel)
	repairs = clearIfUnstorable(repairs, "trigger_kind", &e.TriggerKind)

	for _, p := range []struct {
		idField, nameField string
		id, name           *string
	}{
		{"user_id", "user_sub", &e.UserID, &e.UserSub},
		{"agent_id", "agent_name", &e.AgentID, &e.AgentName},
		{"workflow_id", "workflow_name", &e.WorkflowID, &e.WorkflowName},
		{"root_id", "root_name", &e.RootID, &e.RootName},
		{"entrypoint_id", "entrypoint_name", &e.EntrypointID, &e.EntrypointName},
	} {
		repairs = settlePair(repairs, p.idField, p.nameField, p.id, p.name)
	}

	if e.ModelConfigID != "" && !IsCanonicalID(e.ModelConfigID) {
		e.ModelConfigID = ""
		repairs = append(repairs, "model_config_id cleared: not a canonical non-nil UUID")
	}

	if e.AttemptCount < 0 || e.AttemptCount > math.MaxInt32 {
		e.AttemptCount = 0
		repairs = append(repairs, "attempt_count cleared: outside the range of a 32-bit integer")
	}
	if b := e.AvoidedInputBytes; b != nil && (*b < 0 || EstimatedTokens(*b) > pricing.MaxTokensBeforeCostOverflows) {
		e.AvoidedInputBytes = nil
		repairs = append(repairs, "avoided input cleared: negative or too large to price")
	}

	if e.UserID == "" && e.UserSub == "" {
		return repairs, invalid("neither user_id nor user_sub is set")
	}
	if e.ModelName == "" && e.ModelKey == "" {
		return repairs, invalid("neither model_name nor model_key is set")
	}
	hasRoot := e.RootID != "" || e.RootName != ""
	if (e.RootKind != "") != hasRoot {
		e.RootKind, e.RootID, e.RootName, e.RootNodeID = "", "", "", ""
		repairs = append(repairs, "root cleared: kind and id or name not set together")
	}
	if !hasRoot && e.RootNodeID != "" {
		e.RootNodeID = ""
		repairs = append(repairs, "root_node_id cleared: no root")
	}

	if e.CompletedTime < now.Add(-RetentionWindow).UnixMilli() {
		return repairs, invalid("completed_time is older than the retention window")
	}
	if e.CompletedTime > now.Add(MaxClockSkew).UnixMilli() {
		e.CompletedTime = now.UnixMilli()
		repairs = append(repairs, "completed_time clamped: in the future")
	}

	if err := validateTokens(e.Tokens); err != nil {
		return repairs, err
	}
	return repairs, nil
}

func settlePair(repairs []string, idField, nameField string, id, name *string) []string {
	if *id != "" && !IsCanonicalID(*id) {
		*id = ""
		repairs = append(repairs, idField+" cleared: not a canonical non-nil UUID")
	}
	if *id != "" && *name != "" {
		*name = ""
		repairs = append(repairs, nameField+" cleared: "+idField+" is set")
	}
	return repairs
}

func unstorableReason(value string) string {
	switch {
	case len(value) > MaxFieldBytes:
		return "too long"
	case !utf8.ValidString(value):
		return "not valid UTF-8"
	case strings.ContainsRune(value, 0):
		return "contains a NUL byte"
	}
	return ""
}

func requireStorable(name, value string) error {
	if value == "" {
		return invalid(name + " is empty")
	}
	if reason := unstorableReason(value); reason != "" {
		return invalid(name + " is " + reason)
	}
	return nil
}

func clearIfUnstorable[T ~string](repairs []string, name string, value *T) []string {
	reason := unstorableReason(string(*value))
	if reason == "" {
		return repairs
	}
	*value = ""
	return append(repairs, name+" cleared: "+reason)
}

func validateTokens(t Tokens) error {
	for _, n := range []int64{t.UncachedInput, t.Output, t.CacheRead, t.CacheWrite} {
		if n < 0 {
			return invalid("a token count is negative")
		}
		if n > pricing.MaxTokensBeforeCostOverflows {
			return invalid("token count is too large to price")
		}
	}
	if t.total() > pricing.MaxTokensBeforeCostOverflows {
		return invalid("token count is too large to price")
	}
	return nil
}

func invalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEvent, reason)
}
