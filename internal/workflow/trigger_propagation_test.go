// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func assertDispatchForwardsTrigger(t *testing.T, brk *capturingBroker, want string) {
	t.Helper()
	for _, m := range brk.snapshot() {
		if m.UserProperties[a2a.PropTrigger] == want {
			return
		}
	}
	t.Errorf("no dispatch carried trigger=%q", want)
}

func TestWorkflowDispatchForwardsTrigger(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerScheduled, a2a.TriggerInteractive} {
		t.Run("agent_node/"+trigger, func(t *testing.T) {
			node := &NodeConfig{ID: "n", Type: "agent", AgentName: "worker"}
			cfg := &WorkflowConfig{Nodes: []*NodeConfig{node}, FailFast: true}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "e1"))
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.Trigger = trigger
			e.dispatchAgentNode(context.Background(), node, "sub-1")
			assertDispatchForwardsTrigger(t, brk, trigger)
		})

		t.Run("map/"+trigger, func(t *testing.T) {
			template := &NodeConfig{ID: "double", Type: "agent", AgentName: "Doubler"}
			mapNode := &NodeConfig{ID: "fan", Type: "map", TemplateNode: "double"}
			cfg := &WorkflowConfig{Name: "MapWF", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("MapWF", "e1"))
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.Trigger = trigger
			e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0)
			assertDispatchForwardsTrigger(t, brk, trigger)
		})

		t.Run("loop/"+trigger, func(t *testing.T) {
			body := &NodeConfig{ID: "worker", Type: "agent", AgentName: "Worker"}
			loopNode := &NodeConfig{ID: "loop", Type: "loop", BodyNode: "worker", MaxIterations: 3}
			cfg := &WorkflowConfig{Name: "LoopWF", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("LoopWF", "e1"))
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.Trigger = trigger
			e.dispatchLoopIteration(context.Background(), loopNode, 0)
			assertDispatchForwardsTrigger(t, brk, trigger)
		})
	}
}

type dispatchSite struct {
	name       string
	topLevelID string
	dispatch   func(t *testing.T) (*DAGExecutor, *capturingBroker, func())
}

func dispatchSites() []dispatchSite {
	return []dispatchSite{
		{"agent_node", "n", func(t *testing.T) (*DAGExecutor, *capturingBroker, func()) {
			node := &NodeConfig{ID: "n", Type: "agent", AgentName: "worker"}
			cfg := &WorkflowConfig{Name: "workflow_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f61", Nodes: []*NodeConfig{node}, FailFast: true}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "e1"))
			return e, brk, func() { e.dispatchAgentNode(context.Background(), node, "sub-1") }
		}},
		{"map", "fan", func(t *testing.T) (*DAGExecutor, *capturingBroker, func()) {
			template := &NodeConfig{ID: "double", Type: "agent", AgentName: "Doubler"}
			mapNode := &NodeConfig{ID: "fan", Type: "map", TemplateNode: "double"}
			cfg := &WorkflowConfig{Name: "workflow_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f61", Namespace: "test-ns", Nodes: []*NodeConfig{mapNode, template}}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("MapWF", "e1"))
			return e, brk, func() { e.launchMapIteration(context.Background(), mapNode, map[string]any{"value": 1}, 0) }
		}},
		{"loop", "loop", func(t *testing.T) (*DAGExecutor, *capturingBroker, func()) {
			body := &NodeConfig{ID: "worker", Type: "agent", AgentName: "Worker"}
			loopNode := &NodeConfig{ID: "loop", Type: "loop", BodyNode: "worker", MaxIterations: 3}
			cfg := &WorkflowConfig{Name: "workflow_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f61", Namespace: "test-ns", Nodes: []*NodeConfig{loopNode, body}}
			dag, err := BuildDependencyGraph(cfg.Nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("LoopWF", "e1"))
			return e, brk, func() { e.dispatchLoopIteration(context.Background(), loopNode, 0) }
		}},
	}
}

func firstDispatchProps(t *testing.T, brk *capturingBroker) map[string]string {
	t.Helper()
	msgs := brk.snapshot()
	if len(msgs) == 0 {
		t.Fatal("expected a dispatch")
	}
	return msgs[0].UserProperties
}

func TestWorkflowDispatchBecomesTheRootWhenNoneArrived(t *testing.T) {
	for _, site := range dispatchSites() {
		t.Run(site.name, func(t *testing.T) {
			e, brk, dispatch := site.dispatch(t)
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.ClientID = "gw-1"
			e.execCtx.UserProps.Origin = a2a.Origin{Channel: httputil.SurfaceSlack, UserUUID: "user-uuid", EntrypointUUID: "gw-uuid"}

			dispatch()

			props := firstDispatchProps(t, brk)
			want := map[string]string{
				a2a.PropClientID:       "gw-1",
				a2a.PropEntrypointName: "gw-1",
				a2a.PropRootKind:       a2a.RootKindWorkflow,
				a2a.PropRootID:         "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f61",
				a2a.PropRootNodeID:     site.topLevelID,
				a2a.PropChannel:        httputil.SurfaceSlack,
				a2a.PropUserUUID:       "user-uuid",
				a2a.PropEntrypointUUID: "gw-uuid",
			}
			for key, value := range want {
				if props[key] != value {
					t.Errorf("%s = %q, want %q", key, props[key], value)
				}
			}
		})
	}
}

func TestWorkflowDispatchReplacesAPartialRootItReceived(t *testing.T) {
	partial := map[string]struct{ kind, id string }{
		"kind without an id": {kind: a2a.RootKindAgent},
		"id without a kind":  {id: "root-agent"},
	}
	for _, site := range dispatchSites() {
		for name, root := range partial {
			t.Run(site.name+"/"+name, func(t *testing.T) {
				e, brk, dispatch := site.dispatch(t)
				defer e.execCtx.Cancel()
				e.execCtx.UserProps.RootKind = root.kind
				e.execCtx.UserProps.RootID = root.id

				dispatch()

				props := firstDispatchProps(t, brk)
				if props[a2a.PropRootKind] != a2a.RootKindWorkflow || props[a2a.PropRootID] != "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f61" {
					t.Errorf("root = %q/%q, want the workflow as the root", props[a2a.PropRootKind], props[a2a.PropRootID])
				}
				if props[a2a.PropRootNodeID] != site.topLevelID {
					t.Errorf("%s = %q, want %q", a2a.PropRootNodeID, props[a2a.PropRootNodeID], site.topLevelID)
				}
			})
		}
	}
}

func TestWorkflowDispatchForwardsTheRootItReceivedVerbatim(t *testing.T) {
	for _, site := range dispatchSites() {
		t.Run(site.name, func(t *testing.T) {
			e, brk, dispatch := site.dispatch(t)
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.RootKind = a2a.RootKindAgent
			e.execCtx.UserProps.RootID = "root-agent"
			e.execCtx.UserProps.Origin = a2a.Origin{Channel: "webhook", UserUUID: "user-uuid", EntrypointUUID: "gw-uuid"}

			dispatch()

			props := firstDispatchProps(t, brk)
			if props[a2a.PropRootKind] != a2a.RootKindAgent || props[a2a.PropRootID] != "root-agent" {
				t.Errorf("root = %q/%q, want agent/root-agent", props[a2a.PropRootKind], props[a2a.PropRootID])
			}
			if _, ok := props[a2a.PropRootNodeID]; ok {
				t.Errorf("%s = %q stamped under an agent root", a2a.PropRootNodeID, props[a2a.PropRootNodeID])
			}
			if got := a2a.DecodeUserProperties(props).Origin; got != e.execCtx.UserProps.Origin {
				t.Errorf("origin = %+v, want %+v", got, e.execCtx.UserProps.Origin)
			}
		})
	}
}

func TestWorkflowDispatchForwardsNoOriginWhenNoneArrived(t *testing.T) {
	for _, site := range dispatchSites() {
		t.Run(site.name, func(t *testing.T) {
			e, brk, dispatch := site.dispatch(t)
			defer e.execCtx.Cancel()

			dispatch()

			props := firstDispatchProps(t, brk)
			for _, key := range []string{a2a.PropChannel, a2a.PropUserUUID, a2a.PropEntrypointUUID} {
				if got, ok := props[key]; ok {
					t.Errorf("%s = %q stamped though none arrived", key, got)
				}
			}
		})
	}
}

func TestDelegatedWorkflowDispatchForwardsTheEntrypointItReceived(t *testing.T) {
	for _, site := range dispatchSites() {
		t.Run(site.name, func(t *testing.T) {
			e, brk, dispatch := site.dispatch(t)
			defer e.execCtx.Cancel()
			e.execCtx.UserProps.ClientID = "delegator-client"
			e.execCtx.UserProps.DelegatingAgentName = "parent-agent"
			e.execCtx.UserProps.EntrypointName = "webui"

			dispatch()

			if got := firstDispatchProps(t, brk)[a2a.PropEntrypointName]; got != "webui" {
				t.Errorf("%s = %q, want webui", a2a.PropEntrypointName, got)
			}
		})
	}
}
