// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const testNamespace = "inventory-ingest-test"

const analyticsFlushGrace = 5 * time.Second

const quietGrace = 300 * time.Millisecond

func testDevBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func newFlushingAnalytics(t *testing.T) (*productanalytics.Service, *captureSink) {
	t.Helper()
	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Config:   productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
		Sink:     sink,
		Consent:  productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: testAnalyticsIdentity,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err := svc.Start(t.Context()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	return svc, sink
}

func startIngest(t *testing.T, brk broker.Broker) *captureSink {
	t.Helper()
	svc, sink := newFlushingAnalytics(t)
	ingest := productanalytics.NewBrokerIngest(brk, svc, testNamespace, slog.New(slog.DiscardHandler))
	if ingest == nil {
		t.Fatal("broker ingest should be built with both a broker and a service")
	}
	if err := ingest.Start(t.Context()); err != nil {
		t.Fatalf("start broker ingest: %v", err)
	}
	t.Cleanup(func() { _ = ingest.Stop(context.Background()) })
	return sink
}

func awaitEvents(t *testing.T, sink *captureSink, want int) []productanalytics.Event {
	t.Helper()
	deadline := time.Now().Add(analyticsFlushGrace)
	for {
		if got := sink.all(); len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("sink received %d events, want %d", len(sink.all()), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertSinkStaysEmpty(t *testing.T, sink *captureSink, why string) {
	t.Helper()
	time.Sleep(quietGrace)
	if got := sink.all(); len(got) != 0 {
		t.Fatalf("%s: sink received %d events, want 0 (first %q)", why, len(got), got[0].Name)
	}
}

func seedInventory(t *testing.T, db *store.DB) store.Inventory {
	t.Helper()
	ctx := t.Context()
	agents := store.NewAgentRepo(db)
	for _, name := range []string{"agent-one", "agent-two", "agent-three"} {
		if err := agents.Create(ctx, &store.Agent{Name: name, CreatedBy: "a", UpdatedBy: "a"}, []string{"web"}, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %s: %v", name, err)
		}
	}
	if err := store.NewConnectorRepo(db).Create(ctx, &store.Connector{Name: "conn-one", Description: testSeedDescription(), Type: "sql", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := store.NewGatewayRepo(db).Create(ctx, &store.Gateway{Name: "gw-one", Description: testSeedDescription(), Type: "slack", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	inv, err := store.NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 3 || inv.Connectors != 1 || inv.Gateways != 1 {
		t.Fatalf("precondition: seeded snapshot is %+v, want 3 agents / 1 connector / 1 entrypoint", inv)
	}
	return inv
}

func assertCarriesSnapshot(t *testing.T, ev productanalytics.Event, inv store.Inventory) {
	t.Helper()
	props := inventoryProperties(inv)
	for key, want := range map[string]int{
		productanalytics.PropAgentCount:      inv.Agents,
		productanalytics.PropConnectorCount:  inv.Connectors,
		productanalytics.PropEntrypointCount: inv.Gateways,
	} {
		if props[key] != want {
			t.Errorf("inventoryProperties[%s] = %v, want %d", key, props[key], want)
		}
		if ev.Properties[key] != int64(want) {
			t.Errorf("delivered %s = %v, want %d", key, ev.Properties[key], want)
		}
	}
	assertStringSet(t, ev.Properties, productanalytics.PropConnectorCategories, "sql")
	assertStringSet(t, ev.Properties, productanalytics.PropEntrypointTypes, "slack")
}

func TestSplitPlatformWithNoSharedStoreReportsThroughTheBroker(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	db := store.TestDB(t)
	inv := seedInventory(t, db)
	brk := testDevBroker(t)
	received := startIngest(t, brk)

	emitter := inventoryEmitterFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	if emitter == nil {
		t.Fatal("a platform with a broker in this build must have somewhere to publish")
	}
	reporter := newInventoryReporter(db, emitter, slog.New(slog.DiscardHandler))
	if reporter == nil {
		t.Fatal("reporter should be built with a store and a publisher")
	}
	reporter.reportOnce(t.Context())

	ev := onlyInventoryEvent(t, awaitEvents(t, received, 1))
	assertCarriesSnapshot(t, ev, inv)

	if ev.DistinctID == "" {
		t.Error("delivered event carries no install marker")
	}
}

func TestALocalPipelineIsPreferredToPublishing(t *testing.T) {
	db := store.TestDB(t)
	inv := seedInventory(t, db)
	brk := testDevBroker(t)
	received := startIngest(t, brk)
	local, localSink := newFlushingAnalytics(t)

	emitter := inventoryEmitterFor(local, brk, testNamespace, slog.New(slog.DiscardHandler))
	reporter := newInventoryReporter(db, emitter, slog.New(slog.DiscardHandler))
	if reporter == nil {
		t.Fatal("reporter should be built with a store and a pipeline")
	}
	reporter.reportOnce(t.Context())

	assertCarriesSnapshot(t, onlyInventoryEvent(t, awaitEvents(t, localSink, 1)), inv)
	assertSinkStaysEmpty(t, received, "a process with its own pipeline must not also publish")
}

func TestNoPublishingWhenThisBuildCollectsNothing(t *testing.T) {
	if buildedition.ProductAnalytics {
		t.Skip("only meaningful without -tags product_analytics")
	}
	brk := testDevBroker(t)
	if emitter := inventoryEmitterFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler)); emitter != nil {
		t.Fatalf("emitter = %T, want none when this build carries no collection path", emitter)
	}
}

func TestNoPublishingWithoutABroker(t *testing.T) {
	if emitter := inventoryEmitterFor(nil, nil, testNamespace, slog.New(slog.DiscardHandler)); emitter != nil {
		t.Fatalf("emitter = %T, want none without a broker", emitter)
	}
}

func TestAFailedPublishCostsOneSnapshotAndNothingElse(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	db := store.TestDB(t)
	seedInventory(t, db)
	brk := broker.NewDevBroker()
	emitter := inventoryEmitterFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	reporter := newInventoryReporter(db, emitter, slog.New(slog.DiscardHandler))
	if reporter == nil {
		t.Fatal("reporter should be built with a store and a publisher")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		reporter.reportOnce(t.Context())
	}()
	select {
	case <-done:
	case <-time.After(analyticsFlushGrace):
		t.Fatal("reportOnce did not return with an unusable broker")
	}
}

func TestTheIngestTopicAcceptsOnlyTheInventoryEvent(t *testing.T) {
	brk := testDevBroker(t)
	received := startIngest(t, brk)

	publish(t, brk, productanalytics.IngestEnvelope{Events: []productanalytics.IngestEvent{{
		Name:       productanalytics.EventAgentRunCompleted,
		Properties: map[string]any{productanalytics.PropOutcome: "success"},
	}}})
	assertSinkStaysEmpty(t, received, "a run event must not be accepted off the ingest topic")

	publish(t, brk, productanalytics.IngestEnvelope{Events: []productanalytics.IngestEvent{{
		Name:       productanalytics.EventInstallInventory,
		Properties: map[string]any{productanalytics.PropAgentCount: 7},
	}}})
	ev := onlyInventoryEvent(t, awaitEvents(t, received, 1))
	if ev.Properties[productanalytics.PropAgentCount] != int64(7) {
		t.Errorf("delivered %s = %v, want 7", productanalytics.PropAgentCount, ev.Properties[productanalytics.PropAgentCount])
	}
}

func TestTheIngestTopicRefusesAnOversizedEnvelope(t *testing.T) {
	brk := testDevBroker(t)
	received := startIngest(t, brk)

	env := productanalytics.IngestEnvelope{}
	for range 200 {
		env.Events = append(env.Events, productanalytics.IngestEvent{
			Name:       productanalytics.EventInstallInventory,
			Properties: map[string]any{productanalytics.PropAgentCount: 1},
		})
	}
	publish(t, brk, env)
	assertSinkStaysEmpty(t, received, "an oversized envelope must be refused whole")
}

func TestTheIngestTopicSurvivesAnUndecodableMessage(t *testing.T) {
	brk := testDevBroker(t)
	received := startIngest(t, brk)

	topic := a2a.ProductAnalyticsIngestTopic(testNamespace)
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: []byte("{not json")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	publish(t, brk, productanalytics.IngestEnvelope{Events: []productanalytics.IngestEvent{{
		Name:       productanalytics.EventInstallInventory,
		Properties: map[string]any{productanalytics.PropAgentCount: 2},
	}}})
	ev := onlyInventoryEvent(t, awaitEvents(t, received, 1))
	if ev.Properties[productanalytics.PropAgentCount] != int64(2) {
		t.Errorf("delivered %s = %v, want 2", productanalytics.PropAgentCount, ev.Properties[productanalytics.PropAgentCount])
	}
}

func TestThePublishedPayloadNamesNothing(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	db := store.TestDB(t)
	ctx := t.Context()
	const agentName = "zzagentnamezz"
	const connectorName = "zzconnectornamezz"
	const gatewayName = "zzgatewaynamezz"
	if err := store.NewAgentRepo(db).Create(ctx, &store.Agent{Name: agentName, CreatedBy: "a", UpdatedBy: "a"}, []string{"web"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := store.NewConnectorRepo(db).Create(ctx, &store.Connector{Name: connectorName, Description: testSeedDescription(), Type: "sql", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := store.NewGatewayRepo(db).Create(ctx, &store.Gateway{Name: gatewayName, Description: testSeedDescription(), Type: "slack", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	brk := testDevBroker(t)
	payloads := capturePublishedPayloads(t, brk)

	emitter := inventoryEmitterFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	newInventoryReporter(db, emitter, slog.New(slog.DiscardHandler)).reportOnce(ctx)

	raw := string(payloads.next(t))
	for _, name := range []string{agentName, connectorName, gatewayName} {
		if strings.Contains(raw, name) {
			t.Errorf("published payload names %q: %s", name, raw)
		}
	}
	if !strings.Contains(raw, productanalytics.PropAgentCount) {
		t.Errorf("published payload carries no counts: %s", raw)
	}
	if !strings.Contains(raw, productanalytics.EventInstallInventory) {
		t.Errorf("published payload does not name the event: %s", raw)
	}
}

func TestTheReceiverStillDecidesConsent(t *testing.T) {
	brk := testDevBroker(t)
	sink := &captureSink{}
	denied := productanalytics.NewService(productanalytics.Options{
		Config:   productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
		Sink:     sink,
		Consent:  productanalytics.ConsentFunc(func(context.Context) bool { return false }),
		Identity: testAnalyticsIdentity,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err := denied.Start(t.Context()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() { _ = denied.Stop(context.Background()) })

	ingest := productanalytics.NewBrokerIngest(brk, denied, testNamespace, slog.New(slog.DiscardHandler))
	if err := ingest.Start(t.Context()); err != nil {
		t.Fatalf("start broker ingest: %v", err)
	}
	t.Cleanup(func() { _ = ingest.Stop(context.Background()) })

	publish(t, brk, productanalytics.IngestEnvelope{Events: []productanalytics.IngestEvent{{
		Name:       productanalytics.EventInstallInventory,
		Properties: map[string]any{productanalytics.PropAgentCount: 4},
	}}})
	assertSinkStaysEmpty(t, sink, "a published snapshot must not be emitted by an install that denied consent")
}

type publishedPayloads struct {
	brk   broker.Broker
	queue broker.Queue
}

func capturePublishedPayloads(t *testing.T, brk broker.Broker) publishedPayloads {
	t.Helper()
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.ProductAnalyticsIngestTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return publishedPayloads{brk: brk, queue: queue}
}

func (p publishedPayloads) next(t *testing.T) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), analyticsFlushGrace)
	defer cancel()
	for {
		msg, err := p.brk.Receive(ctx, p.queue)
		if err != nil {
			t.Fatalf("receive published payload: %v", err)
		}
		if msg != nil {
			return msg.Payload
		}
	}
}

func publish(t *testing.T, brk broker.Broker, env productanalytics.IngestEnvelope) {
	t.Helper()
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := brk.Publish(t.Context(), a2a.ProductAnalyticsIngestTopic(testNamespace), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}
