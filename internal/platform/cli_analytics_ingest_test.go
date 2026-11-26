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
	goruntime "runtime"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type reportedHost struct{ OS, Arch, AppVersion string }

var cliHost = reportedHost{OS: "windows", Arch: "386", AppVersion: "main-v9.9.9"}

func fileCLIReport(relay cliIngestRelay, host reportedHost) {
	relayIngester{relay: relay}.Ingest(context.Background(), CLIIngestBatch{
		OS: host.OS, Arch: host.Arch, AppVersion: host.AppVersion,
		Events: []CLIIngestBatchEvent{{
			SessionID:  "b3a3f6d2-0000-7000-8000-000000000001",
			OccurredAt: "2026-08-20T10:00:00.000Z",
			Event:      productanalytics.EventCLICommandRun,
			Properties: map[string]any{
				productanalytics.PropCommand: "sam config apply",
				productanalytics.PropOutcome: productanalytics.OutcomeSuccess,
			},
		}},
	})
}

func startCLIIngest(t *testing.T, brk broker.Broker) *captureSink {
	t.Helper()
	svc, sink := newFlushingAnalytics(t)
	ingest := productanalytics.NewCLIBrokerIngest(brk, svc, testNamespace, slog.New(slog.DiscardHandler))
	if ingest == nil {
		t.Fatal("cli broker ingest should be built with both a broker and a service")
	}
	if err := ingest.Start(t.Context()); err != nil {
		t.Fatalf("start cli broker ingest: %v", err)
	}
	t.Cleanup(func() { _ = ingest.Stop(context.Background()) })
	return sink
}

func assertReportedHost(t *testing.T, ev productanalytics.Event) {
	t.Helper()
	if cliHost.OS == goruntime.GOOS || cliHost.Arch == goruntime.GOARCH {
		t.Fatalf("precondition: cliHost %+v is indistinguishable from this process's %s/%s", cliHost, goruntime.GOOS, goruntime.GOARCH)
	}
	for key, want := range map[string]string{
		"os":         cliHost.OS,
		"arch":       cliHost.Arch,
		"appVersion": cliHost.AppVersion,
	} {
		if got := ev.Properties[key]; got != want {
			t.Errorf("%s = %v, want the reporting CLI host's %q", key, got, want)
		}
	}
	if got := ev.Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceCLI {
		t.Errorf("interface = %v, want %q", got, productanalytics.SurfaceCLI)
	}
}

func TestSplitPlatformRelaysACLIRunOverTheBroker(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	brk := testDevBroker(t)
	received := startCLIIngest(t, brk)

	relay := cliIngestRelayFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	if relay == nil {
		t.Fatal("a platform with a broker in this build must have somewhere to relay")
	}
	fileCLIReport(relay, cliHost)

	events := awaitEvents(t, received, 1)
	if len(events) != 1 {
		t.Fatalf("sink received %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Name != productanalytics.EventCLICommandRun {
		t.Fatalf("event = %q, want %q", ev.Name, productanalytics.EventCLICommandRun)
	}
	if got := ev.Properties[productanalytics.PropCommand]; got != "sam config apply" {
		t.Errorf("command = %v, want the reported one", got)
	}
	assertReportedHost(t, ev)
}

func TestSplitPlatformWithItsOwnPipelineFilesACLIRunDirectly(t *testing.T) {
	svc, sink := newCapturingAnalytics()
	relay := cliIngestRelayFor(svc, nil, testNamespace, slog.New(slog.DiscardHandler))
	if _, ok := relay.(localCLIRelay); !ok {
		t.Fatalf("relay = %T, want the local pipeline to win outright", relay)
	}

	fileCLIReport(relay, cliHost)
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("sink received %d events, want 1", len(events))
	}
	assertReportedHost(t, events[0])
}

func TestAProcessWithAPipelineRelaysNothingOverTheBroker(t *testing.T) {
	brk := testDevBroker(t)
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.ProductAnalyticsCLIIngestTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe cli topic: %v", err)
	}

	svc, sink := newCapturingAnalytics()
	relay := cliIngestRelayFor(svc, brk, testNamespace, slog.New(slog.DiscardHandler))
	fileCLIReport(relay, cliHost)
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}

	if events := sink.all(); len(events) != 1 {
		t.Fatalf("pipeline filed %d events, want 1", len(events))
	}
	ctx, cancel := context.WithTimeout(t.Context(), quietGrace)
	defer cancel()
	if msg, err := brk.Receive(ctx, queue); err == nil && msg != nil {
		t.Errorf("the CLI ingest topic carried %q as well, want the run filed once", string(msg.Payload))
	}
}

func TestAForgedCLIPlatformIsReducedBeforeItIsPublished(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	brk := testDevBroker(t)
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if subErr := brk.Subscribe(queue, a2a.ProductAnalyticsCLIIngestTopic(testNamespace)); subErr != nil {
		t.Fatalf("subscribe topic: %v", subErr)
	}

	relay := cliIngestRelayFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	forged := reportedHost{OS: "Ubuntu 22.04", Arch: "x86_64", AppVersion: "built by alice@example.com"}
	fileCLIReport(relay, forged)

	msg, err := brk.Receive(t.Context(), queue)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var env productanalytics.IngestEnvelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Platform == nil {
		t.Fatal("envelope carries no platform")
	}
	if *env.Platform != (productanalytics.IngestPlatform{}) {
		t.Errorf("published platform = %+v, want every unrecognized field emptied at the boundary", *env.Platform)
	}
}

func TestTheRelayPublishesOnTheCLITopicAlone(t *testing.T) {
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
	brk := testDevBroker(t)
	serverQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(serverQueue, a2a.ProductAnalyticsIngestTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe server topic: %v", err)
	}
	received := startCLIIngest(t, brk)

	relay := cliIngestRelayFor(nil, brk, testNamespace, slog.New(slog.DiscardHandler))
	fileCLIReport(relay, cliHost)
	awaitEvents(t, received, 1)

	ctx, cancel := context.WithTimeout(t.Context(), quietGrace)
	defer cancel()
	if msg, err := brk.Receive(ctx, serverQueue); err == nil && msg != nil {
		t.Errorf("the server ingest topic carried %q, want nothing", string(msg.Payload))
	}
}

func TestNoCLIRelayWithoutAWayToDeliver(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	if relay := cliIngestRelayFor(nil, nil, testNamespace, logger); relay != nil {
		t.Errorf("relay = %T with no pipeline and no broker, want none", relay)
	}
	if buildedition.ProductAnalytics {
		return
	}
	if relay := cliIngestRelayFor(nil, testDevBroker(t), testNamespace, logger); relay != nil {
		t.Errorf("relay = %T when this build carries no collection path, want none", relay)
	}
}

func initCore(t *testing.T, opts ...runtime.Option) *PlatformCore {
	t.Helper()
	appConfig := map[string]any{
		"namespace":    testNamespace,
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}
	core := NewPlatformCore(appConfig, ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(nil, nil,
		append([]runtime.Option{runtime.WithLogger(slog.New(slog.DiscardHandler))}, opts...)...).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	return core
}

type recordingRelay struct {
	host productanalytics.ClientPlatform
	raws []productanalytics.RawEvent
}

func (r *recordingRelay) relay(_ context.Context, host productanalytics.ClientPlatform, raws []productanalytics.RawEvent) {
	r.host = host
	r.raws = raws
}

func TestCLIIngest_HookIsTrueNilWhenNothingCanDeliver(t *testing.T) {
	svc, _ := newCapturingAnalytics()
	t.Cleanup(func() {
		if err := svc.Stop(context.Background()); err != nil {
			t.Errorf("stop analytics: %v", err)
		}
	})
	if got := initCore(t, runtime.WithUsageReporter(svc)).CLIIngest(); got == nil {
		t.Error("a process with a pipeline exposes no CLI ingest hook")
	}
	if got := initCore(t).CLIIngest(); got != nil {
		t.Errorf("hook = %#v in a process that can deliver nothing, want nil", got)
	}
}
