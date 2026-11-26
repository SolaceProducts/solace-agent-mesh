// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestResolveDelivery(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  map[string]any
		want string
	}{
		"nothing set":                         {raw: map[string]any{}, want: DeliveryHTTPS},
		"nil config":                          {raw: nil, want: DeliveryHTTPS},
		"explicit https":                      {raw: map[string]any{"delivery": "https"}, want: DeliveryHTTPS},
		"padded and capitalised":              {raw: map[string]any{"delivery": " HTTPS "}, want: DeliveryHTTPS},
		"explicit pubsub":                     {raw: map[string]any{"delivery": "PubSub"}, want: DeliveryPubSub},
		"stored before HTTPS existed":         {raw: map[string]any{"pubsub_subscription": testSubscription}, want: DeliveryPubSub},
		"blank delivery with a subscription":  {raw: map[string]any{"delivery": "  ", "pubsub_subscription": testSubscription}, want: DeliveryPubSub},
		"explicit https beats a subscription": {raw: map[string]any{"delivery": "https", "pubsub_subscription": testSubscription}, want: DeliveryHTTPS},
		"blank subscription":                  {raw: map[string]any{"pubsub_subscription": " "}, want: DeliveryHTTPS},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ResolveDelivery(tc.raw); got != tc.want {
				t.Errorf("ResolveDelivery(%v) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestMountIntentAgreesWithParseConfig(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"delivery absent":                 {"endpoint_url": addOnEndpointForTest},
		"delivery padded":                 {"delivery": " https", "endpoint_url": addOnEndpointForTest},
		"delivery capitalised":            {"delivery": "HTTPS", "endpoint_url": addOnEndpointForTest},
		"subscription only (legacy row)":  {"pubsub_subscription": testSubscription},
		"explicit pubsub":                 {"delivery": "pubsub", "pubsub_subscription": testSubscription},
		"explicit https with a stale sub": {"delivery": "https", "endpoint_url": addOnEndpointForTest, "pubsub_subscription": testSubscription},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig(raw)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			wantKind := entrypoint.MountNone
			if cfg.Delivery == DeliveryHTTPS {
				wantKind = entrypoint.MountNamed
			}
			if got := NewAdapter(raw).MountIntent().Kind; got != wantKind {
				t.Errorf("MountIntent = %v for delivery %q, want %v", got, cfg.Delivery, wantKind)
			}
		})
	}
}

func TestParseConfig_LegacySubscriptionOnlyRowStaysOnPubSub(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"pubsub_subscription": testSubscription})
	if err != nil {
		t.Fatalf("a row stored with only a subscription must still parse: %v", err)
	}
	if cfg.Delivery != DeliveryPubSub || cfg.PubSubSubscription != testSubscription {
		t.Errorf("config = %+v, want pubsub delivery on the stored subscription", cfg)
	}
}

func TestHTTPSAudienceProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  map[string]any
		want []FieldProblem
	}{
		"pubsub is not checked":   {raw: map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "endpoint_url": "http://x"}},
		"valid endpoint audience": {raw: map[string]any{"endpoint_url": addOnEndpointForTest}},
		"valid project audience":  {raw: map[string]any{"auth_audience_type": "project_number", "project_number": 42.0}},
		"plain http endpoint": {
			raw:  map[string]any{"endpoint_url": "http://sam.example.com/events"},
			want: []FieldProblem{{Field: "endpoint_url"}},
		},
		"endpoint is not a URL": {
			raw:  map[string]any{"endpoint_url": "sam.example.com"},
			want: []FieldProblem{{Field: "endpoint_url"}},
		},
		"project audience without a project number": {
			raw:  map[string]any{"auth_audience_type": "project_number"},
			want: []FieldProblem{{Field: "project_number"}},
		},
		"add-on signer under the project audience": {
			raw:  map[string]any{"auth_audience_type": "project_number", "project_number": "1", "addon_service_account_email": addOnSignerForTest},
			want: []FieldProblem{{Field: "addon_service_account_email"}},
		},
		"every problem at once": {
			raw: map[string]any{"auth_audience_type": "project_number", "addon_service_account_email": "someone@example.com"},
			want: []FieldProblem{
				{Field: "addon_service_account_email"}, {Field: "addon_service_account_email"}, {Field: "project_number"},
			},
		},
		"unknown audience type": {
			raw:  map[string]any{"auth_audience_type": "anyone", "endpoint_url": addOnEndpointForTest},
			want: []FieldProblem{{Field: "auth_audience_type"}},
		},
		"mistyped fields": {
			raw:  map[string]any{"endpoint_url": 7, "project_number": true},
			want: []FieldProblem{{Field: "endpoint_url"}, {Field: "project_number"}, {Field: "endpoint_url"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := HTTPSAudienceProblems(tc.raw)
			fields := func(ps []FieldProblem) []string {
				out := make([]string, len(ps))
				for i, p := range ps {
					out[i] = p.Field
				}
				return out
			}
			if !slices.Equal(fields(got), fields(tc.want)) {
				t.Errorf("problems = %+v, want fields %v", got, fields(tc.want))
			}
			_, parseErr := ParseConfig(tc.raw)
			if (len(got) == 0) != (parseErr == nil) {
				t.Errorf("HTTPSAudienceProblems found %d problems but ParseConfig error = %v; they must agree", len(got), parseErr)
			}
		})
	}
}

func newHTTPSCore(t *testing.T, a *Adapter) *entrypoint.GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flags := features.NewRegistry()
	flags.Register(features.Definition{Key: features.KeyGoogleChatGateway, Default: true})
	a.chatOverride = newFakeChat()
	core := entrypoint.NewGatewayCore(a, map[string]any{"namespace": "test-ns", "gateway_id": "gc-https"})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(flags)).Services())
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	return core
}

func TestInit_HTTPSDeliveryMountsTheEventsRoute(t *testing.T) {
	a := NewAdapter(map[string]any{"endpoint_url": addOnEndpointForTest})
	var mounted http.Handler
	a.SetOnHandlerReady(func(h http.Handler) { mounted = h })
	if err := newHTTPSCore(t, a).Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if mounted == nil {
		t.Fatal("HTTPS delivery initialised without handing over its events handler")
	}
}

func TestInit_HTTPSDeliveryWithoutAMountFailsLoudly(t *testing.T) {
	a := NewAdapter(map[string]any{"endpoint_url": addOnEndpointForTest})
	core := newHTTPSCore(t, a)
	err := core.Init(t.Context(), nil)
	if !errors.Is(err, errEventsRouteUnmounted) {
		t.Fatalf("Init = %v, want errEventsRouteUnmounted", err)
	}
	ingressQueue := a2a.GoogleChatIngressQueueName("test-ns", "gc-https")
	if core.Services().Broker().(*broker.DevBroker).HasQueue(ingressQueue) {
		t.Errorf("the failed Init left ingress queue %q provisioned", ingressQueue)
	}
}

func TestWarnIfSpoolNotDurable(t *testing.T) {
	a := NewAdapter(nil)
	logs := &logCapture{}
	a.logger = slog.New(logs)
	const msg = "google chat HTTPS delivery is spooling on the dev broker"

	a.warnIfSpoolNotDurable(ctxHonoringBroker{broker.NewDevBroker()})
	if n := logs.count(slog.LevelWarn, msg); n != 0 {
		t.Fatalf("a broker that is not a dev broker drew %d durability warnings", n)
	}
	a.warnIfSpoolNotDurable(broker.NewDevBroker())
	if n := logs.count(slog.LevelWarn, msg); n != 1 {
		t.Errorf("dev broker durability warnings = %d, want 1", n)
	}
}

type deprovisionRecorder struct {
	*broker.DevBroker
	names []string
}

func (b *deprovisionRecorder) DeprovisionQueue(name string) error {
	b.names = append(b.names, name)
	return b.DevBroker.DeprovisionQueue(name)
}

func TestDeprovisionAdapterQueues_RemovesTheIngressQueueWhateverTheDelivery(t *testing.T) {
	for _, delivery := range []string{DeliveryHTTPS, DeliveryPubSub} {
		t.Run(delivery, func(t *testing.T) {
			a := newTestAdapter(t, store.TestDB(t), newFakeChat())
			a.cfg.Delivery = delivery
			dev := broker.NewDevBroker()
			recorder := &deprovisionRecorder{DevBroker: dev}
			attachCoreOn(t, a, dev, recorder)
			if err := a.DeprovisionAdapterQueues(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := []string{a2a.GoogleChatIngressQueueName("test-ns", a.gatewayID)}
			if !slices.Equal(recorder.names, want) {
				t.Errorf("deprovisioned %v, want %v", recorder.names, want)
			}
		})
	}
}
