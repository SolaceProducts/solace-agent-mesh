// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"testing"
)

type gateCallTool struct {
	props map[string]any
}

func (t *gateCallTool) Name() string            { return "gated_call" }
func (t *gateCallTool) Description() string     { return "" }
func (t *gateCallTool) Instructions() string    { return "" }
func (t *gateCallTool) IsLongRunning() bool     { return false }
func (t *gateCallTool) RawStringArgs() []string { return nil }
func (t *gateCallTool) ContextParam() string    { return "" }
func (t *gateCallTool) RequiredScopes() []string {
	return nil
}
func (t *gateCallTool) Parameters() *JSONSchema {
	return &JSONSchema{Type: "object", Properties: t.props}
}
func (t *gateCallTool) ArtifactParams() map[string]ArtifactParamInfo { return nil }
func (t *gateCallTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	return OK("ok"), nil
}

func objectParamTool() *gateCallTool {
	return &gateCallTool{props: map[string]any{
		"payload": map[string]any{"type": "object"},
	}}
}

func TestShouldGateCall_MatchesThroughAJSONStringArgument(t *testing.T) {
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "payload.mode", Op: OpEq, Value: "delete"}}}
	args := map[string]any{"payload": `{"mode":"delete"}`}

	if cfg.ShouldGate(args) {
		t.Fatal("precondition: the raw map must not match, or this proves nothing")
	}
	if !ShouldGateCall(objectParamTool(), cfg, args) {
		t.Error("the caller did not gate a call the backstop will refuse: the user is never asked, then the call is blocked")
	}
}

func TestShouldGateCall_MatchesThroughNativeGoValues(t *testing.T) {
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "payload.mode", Op: OpEq, Value: "delete"}}}
	args := map[string]any{"payload": map[string]string{"mode": "delete"}}

	if cfg.ShouldGate(args) {
		t.Fatal("precondition: a map[string]string intermediate must not match raw")
	}
	if !ShouldGateCall(objectParamTool(), cfg, args) {
		t.Error("a nested value that only becomes walkable after the JSON round trip was not gated")
	}
}

func TestShouldGateCall_DoesNotMutateTheCallersArgs(t *testing.T) {
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "payload.mode", Op: OpEq, Value: "delete"}}}
	args := map[string]any{"payload": `{"mode":"delete"}`}

	ShouldGateCall(objectParamTool(), cfg, args)

	if _, stillAString := args["payload"].(string); !stillAString {
		t.Errorf("the caller's payload was rewritten to %T; the approved args must dispatch as authored", args["payload"])
	}
}

func TestShouldGateCall_DoesNotGateANonMatchingCall(t *testing.T) {
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "payload.mode", Op: OpEq, Value: "delete"}}}

	if ShouldGateCall(objectParamTool(), cfg, map[string]any{"payload": `{"mode":"read"}`}) {
		t.Error("gated a call the rule does not match")
	}
	if ShouldGateCall(objectParamTool(), HILToolConfig{}, map[string]any{"payload": `{"mode":"delete"}`}) {
		t.Error("gated a call with no gate configured")
	}
}

func TestShouldGateCall_GatesWhenTheArgsCannotBeRendered(t *testing.T) {
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "payload.mode", Op: OpEq, Value: "delete"}}}
	args := map[string]any{"payload": make(chan int)}

	if !ShouldGateCall(objectParamTool(), cfg, args) {
		t.Error("an unrenderable argument skipped the gate; the backstop would then refuse a call nobody was offered")
	}
}

func TestShouldGateCall_EdgeCases(t *testing.T) {
	if !ShouldGateCall(nil, HILToolConfig{RequireApproval: true}, nil) {
		t.Error("require_approval must gate regardless of tool or args")
	}
	cfg := HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "mode", Op: OpEq, Value: "delete"}}}
	if !ShouldGateCall(nil, cfg, map[string]any{"mode": "delete"}) {
		t.Error("a nil tool must still evaluate top-level rules")
	}
}
