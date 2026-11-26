// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestARefusedWebhookRequestIsReportedOnceAsAWebhook(t *testing.T) {
	rig, sink := newWebhookAnalyticsRig(t)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("wrong-token"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	events := sink.rejections(t)
	if len(events) != 1 {
		t.Fatalf("task:submit_rejected delivered %d times for one refused request, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropRejectReason]; got != productanalytics.RejectForbidden {
		t.Errorf("rejectReason = %v, want %q", got, productanalytics.RejectForbidden)
	}
	if got := events[0].Properties[productanalytics.PropEntrypointType]; got != "webhook" {
		t.Errorf("entrypointType = %v, want %q", got, "webhook")
	}
	if got, present := events[0].Properties[productanalytics.PropSubmittedFrom]; present {
		t.Errorf("submittedFrom = %v, want absent: a webhook has no client to name", got)
	}
}

func newWebhookAnalyticsRig(t *testing.T) (*webhookRig, *captureSink) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	adapter, err := NewWebhookAdapter(map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	if err != nil {
		t.Fatalf("NewWebhookAdapter: %v", err)
	}
	var handler http.Handler
	adapter.SetOnHandlerReady(func(h http.Handler) { handler = h })

	core := entrypoint.NewGatewayCore(adapter, map[string]any{"namespace": testNS, "gateway_id": testGW})
	sink := &captureSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{
				Marker:     "11111111-1111-4111-8111-111111111111",
				BindingKey: []byte("0123456789abcdef0123456789abcdef"),
			}
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
	})
	if err := analytics.Start(t.Context()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = analytics.Stop(ctx)
	})
	artSvc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "local-dev"}
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithArtifacts(artSvc), runtime.WithUsageReporter(analytics)).Services())

	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	rig := &webhookRig{t: t, brk: brk, core: core, adapter: adapter, server: httptest.NewServer(handler)}
	t.Cleanup(rig.cleanup)
	return rig, sink
}

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *captureSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *captureSink) named(name productanalytics.EventName) []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []productanalytics.Event
	for _, ev := range s.events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

func (s *captureSink) rejections(t *testing.T) []productanalytics.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.named(productanalytics.EventTaskSubmitRejected)) > 0 {
			time.Sleep(200 * time.Millisecond)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s.named(productanalytics.EventTaskSubmitRejected)
}
