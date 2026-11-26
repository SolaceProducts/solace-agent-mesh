// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
)

type backstopTool struct{ ran bool }

func (t *backstopTool) Name() string             { return "gated" }
func (t *backstopTool) Description() string      { return "" }
func (t *backstopTool) Instructions() string     { return "" }
func (t *backstopTool) IsLongRunning() bool      { return false }
func (t *backstopTool) RawStringArgs() []string  { return nil }
func (t *backstopTool) ContextParam() string     { return "" }
func (t *backstopTool) RequiredScopes() []string { return nil }
func (t *backstopTool) Parameters() *JSONSchema {
	return &JSONSchema{Type: "object", Properties: map[string]any{"x": map[string]any{"type": "string"}}}
}
func (t *backstopTool) ArtifactParams() map[string]ArtifactParamInfo { return nil }
func (t *backstopTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	t.ran = true
	return OK("ok"), nil
}

func TestDispatchBackstop_OnlyTheTwoIntendedReasonsDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ctx         *Context
		wantRun     bool
		wantRefusal string
	}{
		{"a human approved it", &Context{ApprovalReason: ApprovalHuman}, true, ""},
		{"enforcement is off", &Context{ApprovalReason: ApprovalEnforcementDisabled}, true, ""},

		{"no reason at all", &Context{}, false, "requires human approval"},
		{"nil context", nil, false, "requires human approval"},
		{"caller claims not gated", &Context{ApprovalReason: ApprovalReason("not_gated")}, false, "requires human approval"},
		{"an unrecognised value", &Context{ApprovalReason: ApprovalReason("banana")}, false, "requires human approval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			impl := &backstopTool{}
			ts := NewSet()
			if err := ts.Register(impl); err != nil {
				t.Fatalf("register: %v", err)
			}
			ts.SetHILConfigs(map[string]HILToolConfig{"gated": {RequireApproval: true}})

			res, err := ts.Dispatch(t.Context(), Call{ID: "c1", Name: "gated", Arguments: `{"x":"1"}`}, tc.ctx)
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if impl.ran != tc.wantRun {
				t.Fatalf("tool ran = %v, want %v", impl.ran, tc.wantRun)
			}
			if tc.wantRefusal != "" && (res == nil || !strings.Contains(res.Message, tc.wantRefusal)) {
				t.Errorf("want a refusal mentioning %q, got %+v", tc.wantRefusal, res)
			}
		})
	}
}

func TestDispatchBackstop_LeavesUngatedToolsAlone(t *testing.T) {
	impl := &backstopTool{}
	ts := NewSet()
	if err := ts.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := ts.Dispatch(t.Context(), Call{ID: "c1", Name: "gated", Arguments: `{"x":"1"}`}, &Context{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !impl.ran {
		t.Error("an ungated tool was refused")
	}
}

type countingTool struct {
	backstopTool
	runs atomic.Int64
}

func (t *countingTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	t.runs.Add(1)
	return OK("ok"), nil
}

func TestDispatchParallel_ApprovalDoesNotSpreadAcrossSiblings(t *testing.T) {
	impl := &countingTool{}
	ts := NewSet()
	if err := ts.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts.SetHILConfigs(map[string]HILToolConfig{"gated": {RequireApproval: true}})

	calls := []Call{
		{ID: "a", Name: "gated", Arguments: `{"x":"one"}`},
		{ID: "b", Name: "gated", Arguments: `{"x":"two"}`},
	}
	results := ts.DispatchParallel(t.Context(), calls, &Context{ApprovalReason: ApprovalHuman})

	if n := impl.runs.Load(); n != 0 {
		t.Errorf("a gated tool ran %d times on an approval granted to neither call", n)
	}
	for i, r := range results {
		if r == nil || !strings.Contains(r.Message, "requires human approval") {
			t.Errorf("call %d was not refused: %+v", i, r)
		}
	}
}

func TestDispatchBackstop_NamesItsRefusalOnTheResult(t *testing.T) {
	impl := &backstopTool{}
	ts := NewSet()
	if err := ts.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts.SetHILConfigs(map[string]HILToolConfig{"gated": {RequireApproval: true}})

	tCtx := &Context{}
	res, err := ts.Dispatch(t.Context(), Call{ID: "a", Name: "gated", Arguments: `{"x":"one"}`}, tCtx)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res == nil || !strings.Contains(res.Message, "requires human approval") {
		t.Fatalf("precondition: the backstop should have refused: %+v", res)
	}
	if impl.ran {
		t.Fatal("the gated tool ran")
	}
	if res.ApprovalRefusal != RefusalBackstop {
		t.Errorf("approvalRefusal = %q, want backstop — the record cannot say a gate refused this", res.ApprovalRefusal)
	}
}

func TestDispatchBackstop_OrdinaryFailureCarriesNoRefusal(t *testing.T) {
	impl := &failingBackstopTool{}
	ts := NewSet()
	if err := ts.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	ts.SetHILConfigs(map[string]HILToolConfig{"gated": {RequireApproval: true}})

	res, err := ts.Dispatch(t.Context(), Call{ID: "a", Name: "gated", Arguments: `{"x":"one"}`},
		&Context{ApprovalReason: ApprovalHuman})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !impl.ran {
		t.Fatal("precondition: the call never reached the tool, so it never reached the gate or the scrub either")
	}
	if res == nil || res.Status != StatusError {
		t.Fatalf("precondition: want a failed tool result, got %+v", res)
	}
	if res.ApprovalRefusal != "" {
		t.Errorf("approvalRefusal = %q on an ordinary failure, want empty", res.ApprovalRefusal)
	}
}

type failingBackstopTool struct {
	backstopTool
	ran bool
}

func (t *failingBackstopTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	t.ran = true
	return Error("the backend said no"), nil
}

func TestDispatch_ToolCannotForgeItsOwnRefusalLabel(t *testing.T) {
	ts := NewSet()
	if err := ts.Register(&forgingTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}

	var logged strings.Builder
	ts.SetLogger(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelError})))

	res, err := ts.Dispatch(t.Context(), Call{ID: "a", Name: "forger", Arguments: `{"x":"one"}`}, &Context{})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.ApprovalRefusal != "" {
		t.Errorf("approvalRefusal = %q — a tool labelled its own call as gate-refused", res.ApprovalRefusal)
	}
	got := logged.String()
	if !strings.Contains(got, "returned an approval-refusal label") {
		t.Errorf("the forgery attempt was neutralised without a trace; logs were:\n%s", got)
	}
	if !strings.Contains(got, "no_approval_route") {
		t.Errorf("the report does not say what label was discarded; logs were:\n%s", got)
	}
}

type forgingTool struct{ backstopTool }

func (t *forgingTool) Name() string { return "forger" }

func (t *forgingTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	r := OK("done")
	r.ApprovalRefusal = RefusalNoApprovalRoute
	return r, nil
}
