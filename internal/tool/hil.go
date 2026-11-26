// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type HILToolConfig struct {
	RequireApproval bool
	ApprovalMessage string
	Timeout         time.Duration
	ShowArgs        bool
	ApprovalWhen    []ApprovalRule
	Approvers       HILApprovers
}

type HILApprovers struct {
	Users  []string
	Places []HILPlace
}

type HILPlace struct {
	ChannelType string
	Address     map[string]any
}

func (a HILApprovers) Configured() bool { return len(a.Users) > 0 || len(a.Places) > 0 }

type ApprovalReason string

const (
	ApprovalNone                ApprovalReason = ""
	ApprovalHuman               ApprovalReason = "human"
	ApprovalEnforcementDisabled ApprovalReason = "enforcement_disabled"
)

type ApprovalRefusal string

const (
	RefusalDenied             ApprovalRefusal = "denied"
	RefusalTimedOut           ApprovalRefusal = "timed_out"
	RefusalCancelled          ApprovalRefusal = "cancelled"
	RefusalUnreadable         ApprovalRefusal = "unreadable"
	RefusalUnexpected         ApprovalRefusal = "unanswerable"
	RefusalRunEnded           ApprovalRefusal = "run_ended"
	RefusalRunEndedUnanswered ApprovalRefusal = "run_ended_unanswered"
	RefusalNoApprovalRoute    ApprovalRefusal = "no_approval_route"
	RefusalExitHandler        ApprovalRefusal = "exit_handler"
	RefusalBackstop           ApprovalRefusal = "backstop"
	RefusalPromptFailed       ApprovalRefusal = "prompt_failed"
	RefusalNoHumanInEvalRun   ApprovalRefusal = "no_human_in_eval_run"
)

func HILRefusalErrorCode(refusal ApprovalRefusal) string {
	return taxonomy.HILErrorCodePrefix + strings.ToUpper(string(refusal))
}

func AllApprovalRefusals() []ApprovalRefusal {
	return []ApprovalRefusal{
		RefusalDenied,
		RefusalTimedOut,
		RefusalCancelled,
		RefusalUnreadable,
		RefusalUnexpected,
		RefusalRunEnded,
		RefusalRunEndedUnanswered,
		RefusalNoApprovalRoute,
		RefusalExitHandler,
		RefusalPromptFailed,
		RefusalBackstop,
		RefusalNoHumanInEvalRun,
	}
}

type ApprovalOp string

const (
	OpEq        ApprovalOp = "eq"
	OpIn        ApprovalOp = "in"
	OpNotIn     ApprovalOp = "not_in"
	OpExists    ApprovalOp = "exists"
	OpNotExists ApprovalOp = "not_exists"
	OpGt        ApprovalOp = "gt"
	OpLt        ApprovalOp = "lt"
	OpGte       ApprovalOp = "gte"
	OpLte       ApprovalOp = "lte"
)

type ApprovalRule struct {
	Arg   string
	Op    ApprovalOp
	Value any
}

func (r ApprovalRule) Evaluate(args map[string]any) bool {
	v, found := lookupArg(args, r.Arg)
	switch r.Op {
	case OpExists:
		return found
	case OpNotExists:
		return !found
	}
	if !found {
		return false
	}
	switch r.Op {
	case OpEq:
		return scalarEqual(v, r.Value)
	case OpIn:
		return anyEqual(v, r.Value)
	case OpNotIn:
		return !anyEqual(v, r.Value)
	case OpGt, OpLt, OpGte, OpLte:
		left, lok := coerceFloat(v)
		right, rok := coerceFloat(r.Value)
		if !lok || !rok {
			return false
		}
		switch r.Op {
		case OpGt:
			return left > right
		case OpLt:
			return left < right
		case OpGte:
			return left >= right
		case OpLte:
			return left <= right
		}
	}
	return false
}

func (c HILToolConfig) ShouldGate(args map[string]any) bool {
	if c.RequireApproval {
		return true
	}
	for _, rule := range c.ApprovalWhen {
		if rule.Evaluate(args) {
			return true
		}
	}
	return false
}

func ShouldGateCall(t Tool, cfg HILToolConfig, args map[string]any) bool {
	if cfg.RequireApproval {
		return true
	}
	if t == nil || len(args) == 0 {
		return cfg.ShouldGate(args)
	}
	view, faithful := dispatchView(t, args)
	if !faithful {
		return true
	}
	return cfg.ShouldGate(view)
}

func dispatchView(t Tool, args map[string]any) (map[string]any, bool) {
	decoded, ok := roundTripThroughTransportJSON(args)
	if !ok {
		return nil, false
	}
	coerceJSONStringArgs(t.Parameters(), decoded)
	return decoded, true
}

func roundTripThroughTransportJSON(args map[string]any) (map[string]any, bool) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func lookupArg(args map[string]any, path string) (any, bool) {
	if path == "" || args == nil {
		return nil, false
	}
	var cur any = args
	for seg := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, present := m[seg]
		if !present {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func scalarEqual(a, b any) bool {
	if af, aok := coerceFloat(a); aok {
		if bf, bok := coerceFloat(b); bok {
			return af == bf
		}
		return false
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	}
	return a == b
}

func anyEqual(v, list any) bool {
	items, ok := list.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if scalarEqual(v, item) {
			return true
		}
	}
	return false
}

func coerceFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}
