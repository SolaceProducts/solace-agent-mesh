// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type valueComponent struct{}

func (valueComponent) Init(context.Context, config.Config) error { return nil }
func (valueComponent) Start(context.Context) error               { return nil }
func (valueComponent) Stop(context.Context) error                { return nil }
func (valueComponent) Health() error                             { return nil }

func newUsageReporterRuntime(t *testing.T, svc Component) *Runtime {
	t.Helper()
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	return NewRuntime(b, nil, WithUsageReporter(svc))
}

func TestWithUsageReporter_RegistersAndStopsLast(t *testing.T) {
	var order []string
	reporter := &mockComponent{name: "reporter", order: &order}
	rt := newUsageReporterRuntime(t, reporter)
	rt.Register("app", &mockComponent{name: "app", order: &order})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got := rt.Services().UsageReporter(); got != Component(reporter) {
		t.Errorf("UsageReporter() = %v, want the registered reporter", got)
	}
	if !reporter.stopCalled {
		t.Error("reporter was never stopped, so the runtime does not own its lifecycle")
	}
	want := []string{"stop:app", "stop:reporter"}
	got := order[len(order)-2:]
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("stop order tail = %v, want %v — the reporter must stop after the components that report to it", got, want)
	}
}

func TestWithUsageReporter_NilRegistersNothing(t *testing.T) {
	for name, svc := range map[string]Component{
		"untyped nil":       nil,
		"boxed nil pointer": (*mockComponent)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			rt := newUsageReporterRuntime(t, svc)

			if got := rt.Services().UsageReporter(); got != nil {
				t.Errorf("UsageReporter() = %v, want nil", got)
			}
			if len(rt.components) != 0 {
				t.Errorf("registered %d components, want 0", len(rt.components))
			}
			if err := rt.Stop(context.Background()); err != nil {
				t.Errorf("stop: %v", err)
			}
		})
	}
}

func TestWithUsageReporter_NonPointerComponentIsRegistered(t *testing.T) {
	rt := newUsageReporterRuntime(t, valueComponent{})

	if rt.Services().UsageReporter() == nil {
		t.Error("a non-pointer component must register")
	}
	if len(rt.components) != 1 {
		t.Errorf("registered %d components, want 1", len(rt.components))
	}
}
