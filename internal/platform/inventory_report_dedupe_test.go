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
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	pastore "github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics/store"
)

func sharedConsentStore(t *testing.T) string {
	t.Helper()
	url := "sqlite:///" + filepath.Join(t.TempDir(), "analytics.db")
	db, err := pastore.Open(url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open shared consent store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate shared consent store: %v", err)
	}
	if err := productanalytics.NewPersistedState(pastore.NewProductAnalyticsRepo(db), slog.New(slog.DiscardHandler)).
		SetConsent(t.Context(), productanalytics.ConsentGranted); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	return url
}

func replicaAnalytics(t *testing.T, storeURL string, started bool) (*productanalytics.Service, *captureSink) {
	t.Helper()
	db, err := pastore.Open(storeURL, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open consent store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	state := productanalytics.NewPersistedState(pastore.NewProductAnalyticsRepo(db), slog.New(slog.DiscardHandler))
	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Config:   productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if started {
		if err := svc.Start(t.Context()); err != nil {
			t.Fatalf("start analytics: %v", err)
		}
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	return svc, sink
}

func inventoryEventCount(events []productanalytics.Event) int {
	n := 0
	for _, e := range events {
		if e.Name == productanalytics.EventInstallInventory {
			n++
		}
	}
	return n
}

func stopAndCount(t *testing.T, svc *productanalytics.Service, sink *captureSink) int {
	t.Helper()
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	return inventoryEventCount(sink.all())
}

func TestOnlyOneReplicaFilesTheDaysSnapshot(t *testing.T) {
	db := store.TestDB(t)
	storeURL := sharedConsentStore(t)

	filed := 0
	for range 3 {
		svc, sink := replicaAnalytics(t, storeURL, false)
		reporter := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
		if reporter == nil {
			t.Fatal("reporter should be built with both halves present")
		}
		reporter.reportOnce(t.Context())
		filed += stopAndCount(t, svc, sink)
	}

	if filed != 1 {
		t.Errorf("three replicas filed %d snapshots for one install-day, want exactly 1", filed)
	}
}

func TestFanOutIngestOfOnePublishFilesOneSnapshot(t *testing.T) {
	storeURL := sharedConsentStore(t)
	brk := testDevBroker(t)

	sinks := make([]*captureSink, 0, 3)
	for range 3 {
		svc, sink := replicaAnalytics(t, storeURL, true)
		ingest := productanalytics.NewBrokerIngest(brk, svc, testNamespace, slog.New(slog.DiscardHandler))
		if ingest == nil {
			t.Fatal("broker ingest should be built with both a broker and a service")
		}
		if err := ingest.Start(t.Context()); err != nil {
			t.Fatalf("start broker ingest: %v", err)
		}
		t.Cleanup(func() { _ = ingest.Stop(context.Background()) })
		sinks = append(sinks, sink)
	}

	published, err := json.Marshal(productanalytics.IngestEnvelope{
		Events: []productanalytics.IngestEvent{{
			Name:       productanalytics.EventInstallInventory,
			Properties: map[string]any{productanalytics.PropAgentCount: 4},
		}},
	})
	if err != nil {
		t.Fatalf("marshal published snapshot: %v", err)
	}
	if err := brk.Publish(t.Context(), a2a.ProductAnalyticsIngestTopic(testNamespace),
		&broker.Message{Payload: published}); err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}

	filedAcrossReplicas := func() int {
		total := 0
		for _, sink := range sinks {
			total += inventoryEventCount(sink.all())
		}
		return total
	}
	deadline := time.Now().Add(analyticsFlushGrace)
	for filedAcrossReplicas() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(quietGrace)

	if filed := filedAcrossReplicas(); filed != 1 {
		t.Errorf("three replicas received one publish and filed %d snapshots, want exactly 1", filed)
	}
}

func TestASecondSnapshotOnTheSameDayIsNotFiled(t *testing.T) {
	db := store.TestDB(t)
	svc, sink := replicaAnalytics(t, sharedConsentStore(t), false)

	reporter := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
	reporter.reportOnce(t.Context())
	reporter.reportOnce(t.Context())

	if filed := stopAndCount(t, svc, sink); filed != 1 {
		t.Errorf("two snapshots on one day filed %d, want 1", filed)
	}
}

func TestOnlyTheInventoryEventIsDeduplicated(t *testing.T) {
	svc, sink := replicaAnalytics(t, sharedConsentStore(t), false)

	for range 3 {
		svc.Enqueue(t.Context(), productanalytics.SurfaceServer, []productanalytics.RawEvent{
			{Name: productanalytics.EventAgentDeployed},
			{Name: productanalytics.EventInstallInventory, Properties: map[string]any{productanalytics.PropAgentCount: 1}},
		})
	}
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}

	events := sink.all()
	deploys := 0
	for _, e := range events {
		if e.Name == productanalytics.EventAgentDeployed {
			deploys++
		}
	}
	if deploys != 3 {
		t.Errorf("three deploys delivered %d events, want all 3 through", deploys)
	}
	if got := inventoryEventCount(events); got != 1 {
		t.Errorf("three snapshots delivered %d events, want 1", got)
	}
}

func TestASnapshotIsDroppedWhenTheClaimCannotBeTaken(t *testing.T) {
	db := store.TestDB(t)
	url := "sqlite:///" + filepath.Join(t.TempDir(), "unmigrated.db")
	paDB, err := pastore.Open(url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open unmigrated store: %v", err)
	}
	t.Cleanup(func() { _ = paDB.Close() })
	if err := paDB.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	state := productanalytics.NewPersistedState(pastore.NewProductAnalyticsRepo(paDB), slog.New(slog.DiscardHandler))
	if err := state.SetConsent(t.Context(), productanalytics.ConsentGranted); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	if _, err := paDB.ExecContext(t.Context(), `DROP TABLE product_analytics_inventory_claim`); err != nil {
		t.Fatalf("drop claim table: %v", err)
	}

	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Logger:   slog.New(slog.DiscardHandler),
	})
	reporter := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
	reporter.reportOnce(t.Context())

	if filed := stopAndCount(t, svc, sink); filed != 0 {
		t.Errorf("a snapshot was filed %d times against an unclaimable store, want it dropped", filed)
	}
}
