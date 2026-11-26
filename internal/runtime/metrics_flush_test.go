// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type shutdownMetrics struct {
	order    *[]string
	shutdown int
}

func (s *shutdownMetrics) Counter(string, int64, ...string)           {}
func (s *shutdownMetrics) Histogram(string, time.Duration, ...string) {}
func (s *shutdownMetrics) Gauge(string, float64, ...string)           {}
func (s *shutdownMetrics) Shutdown(context.Context) error {
	s.shutdown++
	if s.order != nil {
		*s.order = append(*s.order, "shutdown:metrics")
	}
	return nil
}

func TestWithMetrics_FlushesOnStopAfterComponents(t *testing.T) {
	var order []string
	rec := &shutdownMetrics{order: &order}

	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithMetrics(rec))
	rt.Register("app", &mockComponent{name: "app", order: &order})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if rec.shutdown != 1 {
		t.Fatalf("Shutdown called %d times, want exactly 1", rec.shutdown)
	}

	idx := func(want string) int {
		for i, s := range order {
			if s == want {
				return i
			}
		}
		return -1
	}
	stopApp, flush := idx("stop:app"), idx("shutdown:metrics")
	if stopApp < 0 || flush < 0 {
		t.Fatalf("missing stop events; order=%v", order)
	}
	if flush < stopApp {
		t.Errorf("metrics flush (%d) must occur AFTER component stop (%d); order=%v", flush, stopApp, order)
	}
}

func TestWithMetrics_NoShutdownInterface_NoFlushComponent(t *testing.T) {
	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithMetrics(NoopMetrics{}))

	for _, nc := range rt.components {
		if nc.name == "metrics_flush" {
			t.Fatalf("NoopMetrics must not register a metrics_flush component")
		}
	}
}
