// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package runtime

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type recordingSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (r *recordingSink) Send(_ context.Context, events []productanalytics.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, events...)
	return nil
}

func (r *recordingSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

type shutdownWindowComponent struct {
	svc   *productanalytics.Service
	order *[]string
}

func (c *shutdownWindowComponent) Init(context.Context, config.Config) error { return nil }
func (c *shutdownWindowComponent) Start(context.Context) error               { return nil }
func (c *shutdownWindowComponent) Health() error                             { return nil }

func (c *shutdownWindowComponent) Stop(ctx context.Context) error {
	*c.order = append(*c.order, "app")
	c.svc.Enqueue(ctx, productanalytics.SurfaceUI, []productanalytics.RawEvent{{Name: "app:launched"}})
	return nil
}

func TestWithUsageReporter_RuntimeStopsServiceLastAndClosesStore(t *testing.T) {
	var order []string
	sink := &recordingSink{}

	svc := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-1", BindingKey: []byte("test-binding-key")}
		}),
		Logger: slog.Default(),
		OnStop: func() { order = append(order, "store_close") },
	})

	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithUsageReporter(svc))
	rt.Register("app", &shutdownWindowComponent{svc: svc, order: &order})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("runtime stop: %v", err)
	}

	if len(order) != 2 || order[0] != "app" || order[1] != "store_close" {
		t.Fatalf("stop order = %v, want [app store_close] — the service must stop after the app components and close its store last", order)
	}
	if sink.count() != 1 {
		t.Errorf("sink received %d events, want 1 — the event raised during the shutdown window was not drained", sink.count())
	}
}

func TestWithUsageReporter_ClosesStoreWithoutAnyGatewayAdapter(t *testing.T) {
	closed := false
	svc := productanalytics.NewService(productanalytics.Options{
		Logger: slog.Default(),
		OnStop: func() { closed = true },
	})

	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithUsageReporter(svc))

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("runtime stop: %v", err)
	}

	if !closed {
		t.Error("store was never closed — no adapter drives the service, so only the runtime can")
	}
}

func TestWithUsageReporter_NilServiceRegistersNothing(t *testing.T) {
	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithUsageReporter(nil))

	if rt.Services().UsageReporter() != nil {
		t.Error("nil service must leave the accessor nil")
	}
	if len(rt.components) != 0 {
		t.Errorf("registered %d components for a nil service, want 0", len(rt.components))
	}
}

func TestWithUsageReporter_BoxedNilPointerRegistersNothing(t *testing.T) {
	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	rt := NewRuntime(b, nil, WithUsageReporter((*productanalytics.Service)(nil)))

	if rt.Services().UsageReporter() != nil {
		t.Error("a boxed nil pointer must leave the accessor nil")
	}
	if len(rt.components) != 0 {
		t.Errorf("registered %d components for a boxed nil pointer, want 0", len(rt.components))
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Errorf("stop: %v", err)
	}
}
