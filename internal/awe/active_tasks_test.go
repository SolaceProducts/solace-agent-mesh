// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeReporter struct {
	name   string
	kind   string
	counts map[a2a.ActiveTaskKey]int
}

func (f *fakeReporter) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake"
}
func (f *fakeReporter) DisplayName() string { return "" }
func (f *fakeReporter) Kind() string        { return f.kind }
func (f *fakeReporter) Init(context.Context, config.Config, runtime.Services) error {
	return nil
}
func (f *fakeReporter) Start(context.Context) error                 { return nil }
func (f *fakeReporter) Stop(context.Context) error                  { return nil }
func (f *fakeReporter) Health() error                               { return nil }
func (f *fakeReporter) ActiveTaskCounts() map[a2a.ActiveTaskKey]int { return f.counts }

type nonReporter struct{}

func (nonReporter) Name() string                                                { return "proxy" }
func (nonReporter) DisplayName() string                                         { return "" }
func (nonReporter) Kind() string                                                { return "proxy" }
func (nonReporter) Init(context.Context, config.Config, runtime.Services) error { return nil }
func (nonReporter) Start(context.Context) error                                 { return nil }
func (nonReporter) Stop(context.Context) error                                  { return nil }
func (nonReporter) Health() error                                               { return nil }

func key(kind, trigger string) a2a.ActiveTaskKey {
	return a2a.ActiveTaskKey{Kind: kind, Trigger: trigger}
}

func TestExecutorActiveTaskCountsMerges(t *testing.T) {
	e := &Executor{}
	e.AddInstance(nonReporter{})
	e.AddInstance(&fakeReporter{kind: a2a.TaskKindAgent, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindAgent, a2a.TriggerInteractive): 2,
		key(a2a.TaskKindAgent, a2a.TriggerScheduled):   1,
	}})
	e.AddInstance(&fakeReporter{kind: a2a.TaskKindAgent, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindAgent, a2a.TriggerInteractive):       3,
		key(a2a.TaskKindWorkflowAgent, a2a.TriggerScheduled): 2,
	}})
	e.AddInstance(&fakeReporter{kind: a2a.TaskKindWorkflow, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindWorkflow, a2a.TriggerScheduled): 1,
	}})

	got := e.ActiveTaskCounts()
	want := map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindAgent, a2a.TriggerInteractive):       5,
		key(a2a.TaskKindAgent, a2a.TriggerScheduled):         1,
		key(a2a.TaskKindWorkflowAgent, a2a.TriggerScheduled): 2,
		key(a2a.TaskKindWorkflow, a2a.TriggerScheduled):      1,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d series, want %d: %v", len(got), len(want), got)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("counts[%v] = %d, want %d", k, got[k], n)
		}
	}
}

func TestExecutorActiveTaskCountsByAgent(t *testing.T) {
	e := &Executor{}
	e.AddInstance(nonReporter{})
	e.AddInstance(&fakeReporter{name: "alpha", kind: a2a.TaskKindAgent, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindAgent, a2a.TriggerInteractive): 2,
		key(a2a.TaskKindAgent, a2a.TriggerScheduled):   1,
	}})
	e.AddInstance(&fakeReporter{name: "beta", kind: a2a.TaskKindAgent, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindAgent, a2a.TriggerInteractive): 4,
	}})
	e.AddInstance(&fakeReporter{name: "flow", kind: a2a.TaskKindWorkflow, counts: map[a2a.ActiveTaskKey]int{
		key(a2a.TaskKindWorkflow, a2a.TriggerScheduled): 5,
	}})
	e.AddInstance(&fakeReporter{name: "idle", kind: a2a.TaskKindAgent, counts: nil})

	got := e.ActiveTaskCountsByAgent()
	want := map[string]int{"alpha": 3, "beta": 4}
	if len(got) != len(want) {
		t.Fatalf("got %d agents, want %d: %v", len(got), len(want), got)
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("counts[%q] = %d, want %d", name, got[name], n)
		}
	}
}

func TestExecutorActiveTaskCountsByAgentEmpty(t *testing.T) {
	e := &Executor{}
	e.AddInstance(nonReporter{})
	if got := e.ActiveTaskCountsByAgent(); len(got) != 0 {
		t.Fatalf("got %d agents, want 0: %v", len(got), got)
	}
}
