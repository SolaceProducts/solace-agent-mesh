// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const hilPauseResumeFlag = "workflow_hil_pause_resume"

type parsedWait struct {
	Type         string
	WaitDuration time.Duration
	Signal       string
	Timeout      time.Duration
}

func workflowWith(nodes ...map[string]any) map[string]any {
	list := make([]any, len(nodes))
	for i, n := range nodes {
		list[i] = n
	}
	return map[string]any{"output_mapping": map[string]any{"done": "yes"}, "nodes": list}
}

func TestParseWorkflowConfig_DurableWaitNodes(t *testing.T) {
	signal64 := strings.Repeat("s", 64)
	for _, tc := range []struct {
		name    string
		raw     map[string]any
		want    parsedWait
		wantErr string
	}{
		{
			name: "a timer reads its duration in days and has no timeout",
			raw:  workflowWith(map[string]any{"id": "cool", "type": "timer", "duration": "3d"}),
			want: parsedWait{Type: "timer", WaitDuration: 72 * time.Hour},
		},
		{
			name:    "a timer without a duration is refused",
			raw:     workflowWith(map[string]any{"id": "cool", "type": "timer"}),
			wantErr: `timer node "cool" missing 'duration'`,
		},
		{
			name:    "a timer of zero length is refused",
			raw:     workflowWith(map[string]any{"id": "cool", "type": "timer", "duration": "0s"}),
			wantErr: `duration "0s" must be positive`,
		},
		{
			name:    "a timer with a timeout is refused",
			raw:     workflowWith(map[string]any{"id": "cool", "type": "timer", "duration": "1h", "timeout": "2h"}),
			wantErr: "'timeout' does not apply",
		},
		{
			name:    "a timer with a retry strategy is refused",
			raw:     workflowWith(map[string]any{"id": "cool", "type": "timer", "duration": "1h", "retry_strategy": map[string]any{"limit": 2}}),
			wantErr: "retry_strategy does not apply",
		},
		{
			name: "a wait without a timeout waits with no limit instead of the default node timeout",
			raw:  workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": "paid"}),
			want: parsedWait{Type: "wait", Signal: "paid"},
		},
		{
			name: "a wait reads its timeout",
			raw:  workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": "paid", "timeout": "7d"}),
			want: parsedWait{Type: "wait", Signal: "paid", Timeout: 168 * time.Hour},
		},
		{
			name: "a signal name of 64 characters is accepted",
			raw:  workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": signal64}),
			want: parsedWait{Type: "wait", Signal: signal64},
		},
		{
			name:    "a signal name of 65 characters is refused",
			raw:     workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": signal64 + "s"}),
			wantErr: "must be 1 to 64 letters",
		},
		{
			name:    "a signal name with a topic separator is refused",
			raw:     workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": "pay/ment"}),
			wantErr: "must be 1 to 64 letters",
		},
		{
			name:    "a wait without a signal is refused",
			raw:     workflowWith(map[string]any{"id": "pay", "type": "wait"}),
			wantErr: `wait node "pay" missing 'signal'`,
		},
		{
			name: "two waits on the same signal are refused",
			raw: workflowWith(
				map[string]any{"id": "first", "type": "wait", "signal": "paid"},
				map[string]any{"id": "second", "type": "wait", "signal": "paid", "depends_on": []any{"first"}},
			),
			wantErr: `wait nodes "first" and "second" both wait for signal "paid"`,
		},
		{
			name: "a wait as an on_exit handler is refused",
			raw: func() map[string]any {
				raw := workflowWith(map[string]any{"id": "pay", "type": "wait", "signal": "paid"})
				raw["on_exit"] = "pay"
				return raw
			}(),
			wantErr: "cannot be an on_exit handler",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseWorkflowConfig("wf", "ns", tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseWorkflowConfig error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWorkflowConfig: %v", err)
			}
			n := cfg.Nodes[0]
			got := parsedWait{Type: n.Type, WaitDuration: n.WaitDuration, Signal: n.Signal, Timeout: n.Timeout}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parsed node mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildDependencyGraph_RefusesAWaitingIterationBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent *NodeConfig
		body   *NodeConfig
	}{
		{
			name:   "a timer as a loop body",
			parent: &NodeConfig{ID: "again", Type: "loop", BodyNode: "cool", Condition: "true"},
			body:   &NodeConfig{ID: "cool", Type: NodeTypeTimer, WaitDuration: time.Hour},
		},
		{
			name:   "a wait as a map template",
			parent: &NodeConfig{ID: "each", Type: "map", TemplateNode: "pay", ItemsExpression: "[1]"},
			body:   &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: "paid"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildDependencyGraph([]*NodeConfig{tc.parent, tc.body})
			if err == nil || !strings.Contains(err.Error(), "cannot wait") {
				t.Errorf("BuildDependencyGraph(%s) error = %v, want a refusal that the body cannot wait", tc.name, err)
			}
		})
	}
}

type durableWaitInit struct {
	Err         bool
	DurableWait bool
}

func TestInit_DurableWaitNodesNeedTheirFlagAndTheWorkflowStore(t *testing.T) {
	timerCfg := func() []*NodeConfig { return []*NodeConfig{timerNode()} }
	for _, tc := range []struct {
		name        string
		nodes       []*NodeConfig
		flags       []string
		wantErrText string
		want        durableWaitInit
	}{
		{
			name:        "a timer with the durable waits flag off is refused",
			nodes:       timerCfg(),
			flags:       []string{hilPauseResumeFlag},
			wantErrText: features.KeyWorkflowDurableWaits,
			want:        durableWaitInit{Err: true},
		},
		{
			name:        "a timer with the workflow store not enabled is refused",
			nodes:       timerCfg(),
			flags:       []string{features.KeyWorkflowDurableWaits},
			wantErrText: hilPauseResumeFlag,
			want:        durableWaitInit{Err: true},
		},
		{
			name:        "a timer with both flags and a store but without workflow_checkpoint_resume is refused",
			nodes:       timerCfg(),
			flags:       []string{features.KeyWorkflowDurableWaits, hilPauseResumeFlag},
			wantErrText: "workflow_checkpoint_resume",
			want:        durableWaitInit{Err: true},
		},
		{
			name:  "a timer with the flag and both stores is accepted and parks runs",
			nodes: timerCfg(),
			flags: []string{features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag},
			want:  durableWaitInit{DurableWait: true},
		},
		{
			name:  "an agent-only workflow with all three flags and a store has durable waits on",
			nodes: []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
			flags: []string{features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag},
			want:  durableWaitInit{DurableWait: true},
		},
		{
			name:  "an agent-only workflow with the durable waits and HIL flags but without workflow_checkpoint_resume has durable waits off",
			nodes: []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
			flags: []string{features.KeyWorkflowDurableWaits, hilPauseResumeFlag},
			want:  durableWaitInit{},
		},
		{
			name:  "a workflow without timer or wait nodes needs neither",
			nodes: []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
			want:  durableWaitInit{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &WorkflowConfig{
				Name: "wf", Namespace: "test-ns", Nodes: tc.nodes, OutputMapping: map[string]any{"done": "yes"},
				HILDatabaseURL: "sqlite:///" + filepath.Join(t.TempDir(), "workflow.db"),
			}
			w := NewWorkflowInstance(cfg)
			t.Cleanup(w.closePausedStore)

			err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: flagsOn(tc.flags...)})

			got := durableWaitInit{Err: err != nil, DurableWait: w.durableWaits}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Init(flags %v) mismatch (-want +got):\n%s\nerror: %v", tc.flags, diff, err)
			}
			if tc.wantErrText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrText)) {
				t.Errorf("Init(flags %v) error = %v, want one naming %q", tc.flags, err, tc.wantErrText)
			}
		})
	}
}

func TestStart_RefusesADurableWaitWorkflowWhoseStoreIsUnavailable(t *testing.T) {
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{timerNode()}, OutputMapping: map[string]any{"done": "yes"},
		HILDatabaseURL: "sqlite:///" + filepath.Join(t.TempDir(), "workflow.db"),
	}
	w := NewWorkflowInstance(cfg)
	svc := &stubServices{brk: &capturingBroker{}, features: flagsOn(features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag)}
	if err := w.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.WithoutCancel(t.Context())) })
	w.closePausedStore()
	notADirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADirectory, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg.HILDatabaseURL = "sqlite:///" + filepath.Join(notADirectory, "workflow.db")

	err := w.Start(t.Context())

	if err == nil || !strings.Contains(err.Error(), "park runs in the workflow store: open workflow store") {
		t.Errorf("Start with the workflow store unavailable = %v, want a refusal that carries the store's open error", err)
	}
}
