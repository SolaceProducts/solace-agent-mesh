// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func gatedToolNode() *NodeConfig {
	return &NodeConfig{
		ID:       "call_gated",
		Type:     "tool",
		ToolName: "web_request",
		Input:    map[string]any{"url": "https://example.test"},
	}
}

func newApprovalExecutor(t *testing.T, hil map[string]tool.HILToolConfig) (*DAGExecutor, *recordingSchemaTool) {
	t.Helper()
	node := gatedToolNode()
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	t.Cleanup(e.execCtx.Cancel)

	rec := newRecordingSchemaTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.instance.hilToolConfigs = hil
	ts.SetHILConfigs(hil)
	e.state.PendingNodes[node.ID] = true
	return e, rec
}

func requireApproval() map[string]tool.HILToolConfig {
	return map[string]tool.HILToolConfig{
		"web_request": {RequireApproval: true, ShowArgs: true},
	}
}

func TestGatedToolNode_NotDispatchedWithoutApproval(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed.Load() {
		t.Fatal("a tool requiring human approval was dispatched from a workflow tool node without one")
	}
}

func TestGatedToolNode_FailsClosedWhenApprovalUnreachable(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed.Load() {
		t.Fatal("tool ran despite approval being unreachable")
	}
	failure := e.state.ErrorState
	if failure == nil {
		t.Fatal("node was neither dispatched nor failed — the workflow would hang")
	}
	if !strings.Contains(failure.ErrorMessage, "requires human approval") {
		t.Errorf("failure does not explain the refusal: %v", failure.ErrorMessage)
	}
}

func TestUngatedToolNode_StillDispatches(t *testing.T) {
	e, rec := newApprovalExecutor(t, nil)

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Fatal("an ungated tool was blocked")
	}
}

func TestApprovalWhen_GatesOnlyOnMatchingArgs(t *testing.T) {
	hil := map[string]tool.HILToolConfig{
		"web_request": {ApprovalWhen: []tool.ApprovalRule{{Arg: "method", Op: tool.OpEq, Value: "POST"}}},
	}

	t.Run("non-matching arg dispatches", func(t *testing.T) {
		e, rec := newApprovalExecutor(t, hil)
		e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test", "method": "GET"},
			NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
		e.instance.wg.Wait()
		if !rec.executed.Load() {
			t.Error("a call the predicate does not match was gated")
		}
	})

	t.Run("matching arg gates", func(t *testing.T) {
		e, rec := newApprovalExecutor(t, hil)
		e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test", "method": "POST"},
			NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
		e.instance.wg.Wait()
		if rec.executed.Load() {
			t.Error("a call the predicate matches was dispatched unapproved")
		}
	})
}

func TestGatedToolNode_EnforcementDisabledDispatches(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: false})
	e.instance.svc = runtime.NewRuntime(nil, nil,
		runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Fatal("disabling enforcement must restore the prior unprompted dispatch")
	}
}

func TestToolApprovalEnforced_DefaultsToEnforcingWithoutRegistry(t *testing.T) {
	w := &WorkflowInstance{}
	if !w.toolApprovalEnforced() {
		t.Error("approval enforcement must default on when no feature registry is wired")
	}
}

func TestGatedToolNode_IterationPathsAreGated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		routing NodeCompletion
	}{
		{"map template", NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 2}},
		{"loop body", NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1", LoopNodeID: "until_done", ItemIndex: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := newApprovalExecutor(t, requireApproval())
			e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"}, tc.routing)
			e.instance.wg.Wait()
			if rec.executed.Load() {
				t.Error("a gated tool ran unapproved on an iteration path")
			}
		})
	}
}

func TestApprovalGate_ThroughAdvance_DoesNotDeadlock(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())
	e.cfg.FailFast = true
	delete(e.state.PendingNodes, "call_gated")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = e.Advance(context.Background(), &EventStart{})
		e.instance.wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: the approval gate reported failure while e.mu was held")
	}
	if rec.executed.Load() {
		t.Error("a gated tool ran unapproved through the real dispatch path")
	}
	if e.state.ErrorState == nil {
		t.Fatal("the gate did not fail the node through the real path")
	}
	if len(e.state.PendingNodes) != 0 {
		t.Errorf("node left pending after the gate refused it: %v", e.state.PendingNodes)
	}
}

func TestApprovalPauseEvent_CarriesTheUnmadeCall(t *testing.T) {
	e, _ := newApprovalExecutor(t, requireApproval())
	args := map[string]any{"url": "https://example.test"}

	pause, err := e.approvalPauseEvent(gatedToolNode(), args,
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 3},
		tool.HILToolConfig{RequireApproval: true, ShowArgs: true})
	if err != nil {
		t.Fatalf("approvalPauseEvent: %v", err)
	}

	if pause.Kind != PauseKindApproval {
		t.Errorf("Kind = %q, want %q", pause.Kind, PauseKindApproval)
	}
	if pause.ToolName != "web_request" {
		t.Errorf("ToolName = %q, want web_request", pause.ToolName)
	}
	if pause.PendingArgs["url"] != "https://example.test" {
		t.Errorf("PendingArgs lost the call arguments: %v", pause.PendingArgs)
	}
	if !strings.HasPrefix(pause.RequestID, approvalRequestIDPrefix) {
		t.Errorf("RequestID = %q, want the shared hil-approval- prefix", pause.RequestID)
	}
	if pause.MapNodeID != "each" || pause.ItemIndex != 3 {
		t.Error("iteration routing must survive the pause so the resume routes as an iteration")
	}
	if len(pause.Surface) == 0 {
		t.Error("an approval pause with no surface would prompt the user with nothing")
	}
}

func TestApprovalPauseEvent_RedactsCredentials(t *testing.T) {
	e, _ := newApprovalExecutor(t, requireApproval())

	pause, err := e.approvalPauseEvent(gatedToolNode(),
		map[string]any{"url": "https://example.test", "api_key": "sk-live-abcdefghijklmnop"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"},
		tool.HILToolConfig{RequireApproval: true, ShowArgs: true})
	if err != nil {
		t.Fatalf("approvalPauseEvent: %v", err)
	}
	if strings.Contains(string(pause.Surface), "sk-live-abcdefghijklmnop") {
		t.Error("the approval surface leaked a credential from the tool arguments")
	}
}

func TestPersistedRouting_LegacyRowStillRoutes(t *testing.T) {
	var routing persistedRouting
	legacy := `{"node_id":"n","sub_task_id":"st","map_node_id":"each","item_index":2}`
	if err := json.Unmarshal([]byte(legacy), &routing); err != nil {
		t.Fatalf("a pre-existing row must still decode: %v", err)
	}
	if routing.NodeID != "n" || routing.SubTaskID != "st" || routing.MapNodeID != "each" || routing.ItemIndex != 2 {
		t.Errorf("legacy routing did not survive the added fields: %+v", routing)
	}
	if routing.Kind == PauseKindApproval {
		t.Error("a legacy row must not be read as an approval pause")
	}
}

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func recordingLog() (func() string, *slog.Logger) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf.String, logger
}

func TestParseHILConfigs_FailsClosedOnMisshapenBlock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hil     any
		wantErr string
	}{
		{"hil is not a map", true, "must be a map"},
		{"require_approval quoted", map[string]any{"require_approval": "true"}, "must be a bool"},
		{"show_args quoted", map[string]any{"show_args": "false"}, "must be a bool"},
		{"timeout not a string", map[string]any{"timeout": 30}, "must be a duration string"},
		{"timeout unparseable", map[string]any{"timeout": "half an hour"}, "not a valid duration"},
		{"tools written as a list", map[string]any{"tools": []any{map[string]any{"web_request": true}}}, "must be a map"},
		{"an override that is not a map", map[string]any{"tools": map[string]any{"web_request": true}}, "must be a map"},
		{"an override naming no registered tool", map[string]any{"tools": map[string]any{"web_requset": map[string]any{"require_approval": true}}}, "does not register"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{{
				ToolMap:   map[string]any{"tool_type": "builtin", "hil": tc.hil},
				ToolNames: []string{"web_request"},
			}})
			if err == nil {
				t.Fatal("a misshapen gate was accepted; the tool would run ungated")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tc.wantErr)
			}
		})
	}
}

func TestParseHILConfigs_DegradedEntryDoesNotFailTheBoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry toolinit.EntryRecord
	}{
		{"the backend was unreachable", toolinit.EntryRecord{
			ToolNames: nil, Status: toolinit.EntryStatusFailed, Error: "connect failed",
		}},
		{"the entry registered nothing and said why", toolinit.EntryRecord{
			ToolNames: nil, Status: toolinit.EntryStatusReady, Error: "spec had no operations",
		}},
		{"the entry registered part of its surface", toolinit.EntryRecord{
			ToolNames: []string{"srv__list"}, Status: toolinit.EntryStatusReady, Error: "3 tools dropped",
		}},
		{"the entry registered nothing at all", toolinit.EntryRecord{
			ToolNames: nil, Status: toolinit.EntryStatusReady,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := tc.entry
			entry.ToolMap = map[string]any{
				"tool_type": "mcp",
				"tool_name": "search",
				"hil":       map[string]any{"tools": map[string]any{"srv__search": map[string]any{"require_approval": true}}},
			}
			if !entry.Degraded() {
				t.Fatalf("precondition: this record is not degraded, so it proves nothing")
			}
			got, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{entry})
			if err != nil {
				t.Fatalf("a degraded entry must not fail config load: %v", err)
			}
			if _, gated := got["srv__search"]; gated {
				t.Error("a tool this entry never registered cannot be gated; it does not exist")
			}
		})
	}
}

func TestParseHILConfigs_HealthyEntryStillRejectsUnknownOverride(t *testing.T) {
	_, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{{
		ToolMap: map[string]any{
			"tool_type": "mcp",
			"hil":       map[string]any{"tools": map[string]any{"srv__searhc": map[string]any{"require_approval": true}}},
		},
		ToolNames: []string{"srv__search"},
		Status:    toolinit.EntryStatusReady,
	}})
	if err == nil {
		t.Fatal("a typo on a healthy entry was accepted; the tool it meant to gate stays ungated")
	}
}

func TestParseHILConfigs_FilteredEntryWarnsInsteadOfFailing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		filter   map[string]any
		override string
	}{
		{"deny_list, name excluded by it", map[string]any{"deny_list": []any{"delete_thing"}}, "srv__delete_thing"},
		{"deny_list, name nowhere near it", map[string]any{"deny_list": []any{"delete_thing"}}, "srv__raed_thing"},
		{"allow_list", map[string]any{"allow_list": []any{"read_thing"}}, "srv__delete_thing"},
		{"tool_name", map[string]any{"tool_name": "read_thing"}, "srv__delete_thing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logged, logger := recordingLog()
			toolMap := map[string]any{"tool_type": "mcp"}
			for k, v := range tc.filter {
				toolMap[k] = v
			}
			toolMap["hil"] = map[string]any{"tools": map[string]any{
				tc.override: map[string]any{"require_approval": true},
			}}

			got, err := parseHILConfigs(logger, []toolinit.EntryRecord{{
				ToolMap:   toolMap,
				ToolNames: []string{"srv__read_thing"},
				Status:    toolinit.EntryStatusReady,
			}})
			if err != nil {
				t.Fatalf("a filtering entry must not fail config load: %v", err)
			}
			if _, gated := got[tc.override]; gated {
				t.Errorf("%q is not registered and cannot be gated; it does not exist", tc.override)
			}
			out := logged()
			if !strings.Contains(out, "NOT gated") || !strings.Contains(out, tc.override) {
				t.Errorf("the ambiguity was not reported to the operator; logs were:\n%s", out)
			}
		})
	}
}

func TestParseHILConfigs_BuiltinEntryStillRejectsUnknownOverride(t *testing.T) {
	for _, toolType := range []string{"builtin", "python", "sam_remote"} {
		t.Run(toolType, func(t *testing.T) {
			_, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{{
				ToolMap: map[string]any{
					"tool_type": toolType,
					"tool_name": "web_request",
					"hil": map[string]any{"tools": map[string]any{
						"web_requset": map[string]any{"require_approval": true},
					}},
				},
				ToolNames: []string{"web_request"},
				Status:    toolinit.EntryStatusReady,
			}})
			if err == nil {
				t.Fatal("a typo was accepted on an entry that derives no names; web_request stays ungated")
			}
		})
	}
}

func TestParseHILConfigs_DegradedEntryWarnsAboutAnUnknownOverride(t *testing.T) {
	logged, logger := recordingLog()
	_, err := parseHILConfigs(logger, []toolinit.EntryRecord{{
		ToolMap: map[string]any{
			"tool_type": "mcp",
			"hil":       map[string]any{"tools": map[string]any{"srv__search": map[string]any{"require_approval": true}}},
		},
		ToolNames: nil,
		Status:    toolinit.EntryStatusFailed,
		Error:     "connect failed",
	}})
	if err != nil {
		t.Fatalf("a degraded entry must not fail config load: %v", err)
	}
	if out := logged(); !strings.Contains(out, "NOT gated") || !strings.Contains(out, "srv__search") {
		t.Errorf("the author was told nothing about an ungated tool; logs were:\n%s", out)
	}
}

func TestParseHILConfigs_NullFilterDoesNotRelaxTheCheck(t *testing.T) {
	for _, key := range []string{"tool_name", "allow_list", "deny_list"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{{
				ToolMap: map[string]any{
					"tool_type": "mcp",
					key:         nil,
					"hil": map[string]any{"tools": map[string]any{
						"srv__searhc": map[string]any{"require_approval": true},
					}},
				},
				ToolNames: []string{"srv__search"},
				Status:    toolinit.EntryStatusReady,
			}})
			if err == nil {
				t.Fatalf("a null %s relaxed the typo check; the tool it meant to gate stays ungated", key)
			}
		})
	}
}

func TestParseHILConfigs_KeysOnRegisteredNames(t *testing.T) {
	got, err := parseHILConfigs(discardLog(), []toolinit.EntryRecord{{
		ToolMap: map[string]any{
			"tool_type": "mcp",
			"tool_name": "authored_filter",
			"hil":       map[string]any{"require_approval": true},
		},
		ToolNames: []string{"srv__alpha", "srv__beta"},
	}})
	if err != nil {
		t.Fatalf("parseHILConfigs: %v", err)
	}
	for _, name := range []string{"srv__alpha", "srv__beta"} {
		if _, ok := got[name]; !ok {
			t.Errorf("registered tool %q is not gated", name)
		}
	}
	if _, ok := got["authored_filter"]; ok {
		t.Error("gate keyed on the authored name, which no tool node addresses")
	}
}

func TestResumeApprovalPause(t *testing.T) {
	for _, tc := range []struct {
		name         string
		nodeStatus   string
		data         map[string]any
		wantDispatch bool
	}{
		{"approved dispatches", "answered", map[string]any{"approved": true}, true},
		{"denied fails the node", "answered", map[string]any{"approved": false}, false},
		{"timeout fails the node", "timed_out", nil, false},
		{"cancelled fails the node", "cancelled", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := newApprovalExecutor(t, requireApproval())
			w := e.instance
			w.logger = slog.Default()

			exec := &WorkflowExecution{Executor: e, Context: e.execCtx, State: e.state}
			routing := persistedRouting{
				NodeID:      "call_gated",
				SubTaskID:   "sub-1",
				Kind:        PauseKindApproval,
				ToolName:    "web_request",
				PendingArgs: map[string]any{"url": "https://example.test"},
			}

			w.resumeApprovalPause(t.Context(), "hil-approval-1", "exec-1", exec, routing, tc.nodeStatus, tc.data)
			w.wg.Wait()
			e.instance.wg.Wait()

			if got := rec.executed.Load(); got != tc.wantDispatch {
				t.Errorf("tool dispatched = %v, want %v", got, tc.wantDispatch)
			}
			if !tc.wantDispatch && e.state.ErrorState == nil {
				t.Error("a refused approval must fail the node, not leave it pending")
			}
		})
	}
}

func TestApprovalPause_IterationCarriesTheToolNodeID(t *testing.T) {
	e, _ := newApprovalExecutor(t, requireApproval())

	pause, err := e.approvalPauseEvent(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "each", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 1},
		tool.HILToolConfig{RequireApproval: true})
	if err != nil {
		t.Fatalf("approvalPauseEvent: %v", err)
	}

	if pause.NodeID != "each" {
		t.Errorf("NodeID = %q, want the parent map node so the completion routes correctly", pause.NodeID)
	}
	if pause.ToolNodeID != "call_gated" {
		t.Errorf("ToolNodeID = %q, want the tool node whose call was gated", pause.ToolNodeID)
	}
}

func TestApprovedDispatch_DoesNotRePause(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())

	e.invokeApprovedToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"}, retryIdentity{})
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Fatal("an approved call must dispatch; re-gating it would pause forever")
	}
}

func TestApprovedDispatch_IterationRunsTheToolNode(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())
	e.state.PendingNodes["each"] = true
	w := e.instance

	exec := &WorkflowExecution{Executor: e, Context: e.execCtx, State: e.state}
	w.resumeApprovalPause(t.Context(), "hil-approval-map", "exec-1", exec, persistedRouting{
		NodeID:      "each",
		ToolNodeID:  "call_gated",
		SubTaskID:   "sub-1",
		MapNodeID:   "each",
		ItemIndex:   1,
		Kind:        PauseKindApproval,
		ToolName:    "web_request",
		PendingArgs: map[string]any{"url": "https://example.test"},
	}, "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	if !rec.executed.Load() {
		t.Fatal("an approved map iteration did not dispatch the tool node")
	}
}

func TestApprovalPause_PersistsKindAndArgs(t *testing.T) {
	w, _ := newHILInstance(t)

	node := gatedToolNode()
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	state := NewExecutionState("wf", "exec-1")
	state.PendingNodes[node.ID] = true

	execCtx := NewExecutionContext(t.Context(), "task-1", "ctx-1", nil,
		a2a.UserProperties{UserID: "approver@test"}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: cfg, dag: dag, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}

	err = w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID:      node.ID,
		SubTaskID:   "sub-1",
		RequestID:   "hil-approval-persist",
		Surface:     json.RawMessage(`{}`),
		ExpiresAt:   time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
		Kind:        PauseKindApproval,
		ToolName:    "web_request",
		PendingArgs: map[string]any{"url": "https://example.test"},
	})
	if err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}

	row, err := w.pausedRepo.Get(t.Context(), "hil-approval-persist")
	if err != nil || row == nil {
		t.Fatalf("paused row not stored: %v", err)
	}
	var routing persistedRouting
	if err := json.Unmarshal([]byte(row.Routing), &routing); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if routing.Kind != PauseKindApproval || routing.ToolName != "web_request" {
		t.Errorf("row lost the approval marker: %+v", routing)
	}
	if routing.PendingArgs["url"] != "https://example.test" {
		t.Errorf("row lost the arguments the resume must dispatch with: %+v", routing.PendingArgs)
	}
}

func TestWarnOnUngatableTools(t *testing.T) {
	newInstance := func(hil map[string]tool.HILToolConfig) *WorkflowInstance {
		rt := runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.Default()))
		return &WorkflowInstance{
			cfg:            &WorkflowConfig{Name: "wf", Namespace: "test-ns"},
			svc:            rt.Services(),
			logger:         slog.Default(),
			hilToolConfigs: hil,
		}
	}
	capture := func(w *WorkflowInstance) string {
		var buf strings.Builder
		w.warnOnUngatableTools(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		return buf.String()
	}

	t.Run("warns and names the tools when approvals are unreachable", func(t *testing.T) {
		got := capture(newInstance(requireApproval()))
		if !strings.Contains(got, "web_request") {
			t.Errorf("the warning must name the tools that will fail; got %q", got)
		}
		if !strings.Contains(got, "will fail") {
			t.Errorf("the warning must say which way they fail; got %q", got)
		}
	})

	t.Run("says they dispatch when enforcement is disabled", func(t *testing.T) {
		w := newInstance(requireApproval())
		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: false})
		w.svc = runtime.NewRuntime(nil, nil,
			runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

		got := capture(w)
		if !strings.Contains(got, "web_request") {
			t.Errorf("the warning must name the affected tools; got %q", got)
		}
		if strings.Contains(got, "will fail") {
			t.Errorf("with enforcement off these tools dispatch; the warning must not say they fail: %q", got)
		}
		if !strings.Contains(got, "WITHOUT asking") {
			t.Errorf("the warning must say the tools run unprompted; got %q", got)
		}
	})

	t.Run("silent when approvals are reachable", func(t *testing.T) {
		w := newInstance(requireApproval())
		w.pausedRepo = &wfstore.PausedRepo{}
		if got := capture(w); got != "" {
			t.Errorf("warned despite approvals being reachable: %q", got)
		}
	})

	t.Run("silent when nothing is gated", func(t *testing.T) {
		if got := capture(newInstance(nil)); got != "" {
			t.Errorf("warned with no gated tools: %q", got)
		}
	})

	t.Run("nil logger does not panic", func(t *testing.T) {
		newInstance(requireApproval()).warnOnUngatableTools(nil)
	})
}

func TestNoApprovalRouteError_NamesTheActualCause(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured bool
		enabled    bool
		wantSubstr string
	}{
		{"configured and enabled", true, true, "is not available"},
		{"configured but disabled", true, false, "enable workflow_hil_pause_resume"},
		{"not configured", false, true, "enable workflow_hil_pause_resume"},
		{"neither", false, false, "enable workflow_hil_pause_resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newApprovalExecutor(t, requireApproval())
			if tc.configured {
				e.instance.cfg.HILDatabaseURL = "sqlite:///unused.db"
			}
			reg := features.NewRegistry()
			reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: tc.enabled})
			e.instance.svc = runtime.NewRuntime(nil, nil,
				runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

			got := e.noApprovalRouteError("web_request").Error()
			if !strings.Contains(got, tc.wantSubstr) {
				t.Errorf("message does not name the cause (want %q): %s", tc.wantSubstr, got)
			}
		})
	}
}

func TestArmPausedStore_FatalOnlyWhenAGateCouldFire(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gates    map[string]tool.HILToolConfig
		enforce  bool
		wantFail bool
	}{
		{"gated and enforcing", requireApproval(), true, true},
		{"gated but not enforcing", requireApproval(), false, false},
		{"nothing to gate still starts when the store cannot open", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newApprovalExecutor(t, tc.gates)
			w := e.instance
			w.hilToolConfigs = tc.gates
			w.cfg.HILDatabaseURL = "wat://not-a-supported-driver"
			reg := features.NewRegistry()
			reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
			reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: tc.enforce})
			w.svc = runtime.NewRuntime(nil, nil,
				runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

			err := w.armPausedStore(t.Context())
			if tc.wantFail && err == nil {
				t.Error("a workflow whose gates can fire started without any way to ask; every gated call now fails closed while it advertises itself as running")
			}
			if !tc.wantFail && err != nil {
				t.Errorf("a workflow with nothing to gate was stopped from restarting by a store blip: %v", err)
			}
		})
	}
}
