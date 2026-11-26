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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func testSeedDescription() *string {
	d := "seeded for inventory tests"
	return &d
}

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (c *captureSink) Send(_ context.Context, events []productanalytics.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
	return nil
}

func (c *captureSink) all() []productanalytics.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]productanalytics.Event(nil), c.events...)
}

func newCapturingAnalytics() (*productanalytics.Service, *captureSink) {
	sink := &captureSink{}
	return productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: testAnalyticsIdentity,
		Logger:   slog.New(slog.DiscardHandler),
	}), sink
}

func reportNow(t *testing.T, r *inventoryReporter, svc *productanalytics.Service, sink *captureSink) []productanalytics.Event {
	t.Helper()
	r.reportOnce(t.Context())
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	return sink.all()
}

func onlyInventoryEvent(t *testing.T, events []productanalytics.Event) productanalytics.Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want exactly 1", len(events))
	}
	if events[0].Name != productanalytics.EventInstallInventory {
		t.Fatalf("event name = %q, want %q", events[0].Name, productanalytics.EventInstallInventory)
	}
	return events[0]
}

func TestInventoryReporterIsAbsentWithoutAWayToDeliver(t *testing.T) {
	db := store.TestDB(t)
	if r := newInventoryReporter(db, nil, slog.New(slog.DiscardHandler)); r != nil {
		t.Fatal("reporter should not be built when this process can neither emit nor publish")
	}
}

func TestInventoryReporterIsAbsentWithoutAStore(t *testing.T) {
	svc, _ := newCapturingAnalytics()
	if r := newInventoryReporter(nil, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler)); r != nil {
		t.Fatal("reporter should not be built when there is no platform store to read")
	}
}

func TestProductAnalyticsIsNilWithoutServices(t *testing.T) {
	if svc := (&PlatformCore{}).productAnalytics(); svc != nil {
		t.Fatal("a core with no runtime services must report no analytics service")
	}
}

func TestInventoryReportCarriesTheCounts(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()

	agents := store.NewAgentRepo(db)
	if err := agents.Create(ctx, &store.Agent{Name: "agent-one", CreatedBy: "a", UpdatedBy: "a"}, []string{"acme_payroll"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := agents.Create(ctx, &store.Agent{Name: "agent-two", CreatedBy: "a", UpdatedBy: "a"}, []string{"web"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := store.NewToolPackageRepo(db).Create(ctx, &store.ToolPackage{Name: "acme_payroll", DiscoveryStatus: "created", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	if err := store.NewWorkflowRepo(db).Create(ctx, &store.Workflow{Name: "workflow-one", AppConfig: "{}", CreatedBy: "a", UpdatedBy: "a"}, store.WorkflowBindings{Toolsets: []string{"acme_payroll"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if err := store.NewConnectorRepo(db).Create(ctx, &store.Connector{Name: "conn-one", Description: testSeedDescription(), Type: "sql", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := store.NewGatewayRepo(db).Create(ctx, &store.Gateway{Name: "gw-one", Description: testSeedDescription(), Type: "slack", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	svc, sink := newCapturingAnalytics()
	r := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
	if r == nil {
		t.Fatal("reporter should be built with both halves present")
	}
	ev := onlyInventoryEvent(t, reportNow(t, r, svc, sink))

	for key, want := range map[string]int64{
		productanalytics.PropAgentCount:                  2,
		productanalytics.PropWorkflowCount:               1,
		productanalytics.PropConnectorCount:              1,
		productanalytics.PropEntrypointCount:             1,
		productanalytics.PropToolsetCount:                1,
		productanalytics.PropAgentsWithCustomToolsets:    1,
		productanalytics.PropWorkflowsWithCustomToolsets: 1,
	} {
		if ev.Properties[key] != want {
			t.Errorf("%s = %v, want %d", key, ev.Properties[key], want)
		}
	}
	assertStringSet(t, ev.Properties, productanalytics.PropConnectorCategories, "sql")
	assertStringSet(t, ev.Properties, productanalytics.PropEntrypointTypes, "slack")
}

func TestInitBuildsTheReporterFromTheRuntimesAnalyticsService(t *testing.T) {
	appConfig := map[string]any{
		"namespace":    "inventory-wiring-test",
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}
	svc, _ := newCapturingAnalytics()
	core := NewPlatformCore(appConfig, ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(nil, nil,
		runtime.WithLogger(slog.New(slog.DiscardHandler)),
		runtime.WithUsageReporter(svc),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	t.Cleanup(func() {
		if err := svc.Stop(context.Background()); err != nil {
			t.Fatalf("stop analytics: %v", err)
		}
	})

	reporters := 0
	for _, background := range core.backgroundServices {
		if _, ok := background.(*inventoryReporter); ok {
			reporters++
		}
	}
	if reporters != 1 {
		t.Errorf("Init registered %d inventory reporters, want exactly 1", reporters)
	}
}

func TestInventoryReportSeparatesDeployedFromConfigured(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()

	if err := store.NewToolPackageRepo(db).Create(ctx, &store.ToolPackage{Name: "acme_payroll", DiscoveryStatus: "created", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	agents := store.NewAgentRepo(db)
	deployedAgent := &store.Agent{Name: "deployed-agent", CreatedBy: "a", UpdatedBy: "a"}
	if err := agents.Create(ctx, deployedAgent, []string{"acme_payroll"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	shelvedAgent := &store.Agent{Name: "shelved-agent", CreatedBy: "a", UpdatedBy: "a"}
	if err := agents.Create(ctx, shelvedAgent, []string{"acme_payroll"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	workflows := store.NewWorkflowRepo(db)
	deployedWorkflow := &store.Workflow{Name: "deployed-workflow", AppConfig: "{}", CreatedBy: "a", UpdatedBy: "a"}
	if err := workflows.Create(ctx, deployedWorkflow, store.WorkflowBindings{Toolsets: []string{"acme_payroll"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	shelvedWorkflow := &store.Workflow{Name: "shelved-workflow", AppConfig: "{}", CreatedBy: "a", UpdatedBy: "a"}
	if err := workflows.Create(ctx, shelvedWorkflow, store.WorkflowBindings{Toolsets: []string{"acme_payroll"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	agentDeployments := store.NewDeploymentRepo(db)
	if err := agentDeployments.Create(ctx, &store.AgentDeployment{
		AgentID: deployedAgent.ID, Action: "deploy", Status: "success", CreatedBy: "a",
	}); err != nil {
		t.Fatalf("create agent deployment: %v", err)
	}
	if err := agentDeployments.Create(ctx, &store.AgentDeployment{
		AgentID: shelvedAgent.ID, Action: "deploy", Status: "failed", CreatedBy: "a",
	}); err != nil {
		t.Fatalf("create failed agent deployment: %v", err)
	}
	workflowDeployments := store.NewWorkflowDeploymentRepo(db)
	if err := workflowDeployments.Create(ctx, &store.WorkflowDeployment{
		WorkflowID: deployedWorkflow.ID, Action: "deploy", Status: "success", CreatedBy: "a",
	}); err != nil {
		t.Fatalf("create workflow deployment: %v", err)
	}
	if err := workflowDeployments.Create(ctx, &store.WorkflowDeployment{
		WorkflowID: shelvedWorkflow.ID, Action: "undeploy", Status: "success", CreatedBy: "a",
	}); err != nil {
		t.Fatalf("create undeployed workflow deployment: %v", err)
	}

	inv, err := store.NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	built := inventoryProperties(inv)
	for key, want := range map[string]int{
		productanalytics.PropAgentCount:                          2,
		productanalytics.PropWorkflowCount:                       2,
		productanalytics.PropAgentsWithCustomToolsets:            2,
		productanalytics.PropWorkflowsWithCustomToolsets:         2,
		productanalytics.PropDeployedAgentCount:                  1,
		productanalytics.PropDeployedWorkflowCount:               1,
		productanalytics.PropDeployedAgentsWithCustomToolsets:    1,
		productanalytics.PropDeployedWorkflowsWithCustomToolsets: 1,
	} {
		if built[key] != want {
			t.Errorf("built %s = %v, want %d", key, built[key], want)
		}
	}

	svc, sink := newCapturingAnalytics()
	ev := onlyInventoryEvent(t, reportNow(t, newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler)), svc, sink))
	for key, want := range map[string]int64{
		productanalytics.PropDeployedAgentCount:                  1,
		productanalytics.PropDeployedWorkflowCount:               1,
		productanalytics.PropDeployedAgentsWithCustomToolsets:    1,
		productanalytics.PropDeployedWorkflowsWithCustomToolsets: 1,
	} {
		if ev.Properties[key] != want {
			t.Errorf("delivered %s = %v, want %d", key, ev.Properties[key], want)
		}
	}
}

func TestInventoryDeployedCountFollowsTheLatestDeployment(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()

	agent := &store.Agent{Name: "churned-agent", CreatedBy: "a", UpdatedBy: "a"}
	if err := store.NewAgentRepo(db).Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := store.NewDeploymentRepo(db)
	inventory := store.NewInventoryRepo(db)
	for _, step := range []struct {
		action, status string
		want           int
	}{
		{"deploy", "failed", 0},
		{"deploy", "success", 1},
		{"update", "success", 1},
		{"undeploy", "success", 0},
		{"deploy", "success", 1},
	} {
		if err := repo.Create(ctx, &store.AgentDeployment{
			AgentID: agent.ID, Action: step.action, Status: step.status, CreatedBy: "a",
		}); err != nil {
			t.Fatalf("create %s/%s deployment: %v", step.action, step.status, err)
		}
		got, err := inventory.CountDeployedAgents(ctx)
		if err != nil {
			t.Fatalf("count deployed agents: %v", err)
		}
		if got != step.want {
			t.Errorf("after %s/%s the deployed count is %d, want %d", step.action, step.status, got, step.want)
		}
	}
}

func TestInventorySeparatesSeededBuiltinsFromAuthoredAgents(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()

	if err := store.NewToolPackageRepo(db).Create(ctx, &store.ToolPackage{
		Name: "acme_payroll", DiscoveryStatus: "created", CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	agents := store.NewAgentRepo(db)
	authored := &store.Agent{Name: "authored-agent", CreatedBy: "operator@example.com", UpdatedBy: "operator@example.com"}
	if err := agents.Create(ctx, authored, []string{"acme_payroll"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create authored agent: %v", err)
	}
	seeded := make([]*store.Agent, 0, 2)
	for _, name := range []string{"Orchestrator", "Observability Agent"} {
		agent := &store.Agent{Name: name, CreatedBy: store.SeededAgentCreator, UpdatedBy: store.SeededAgentCreator}
		if err := agents.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		seeded = append(seeded, agent)
	}

	deployments := store.NewDeploymentRepo(db)
	for _, agent := range append([]*store.Agent{authored}, seeded...) {
		if err := deployments.Create(ctx, &store.AgentDeployment{
			AgentID: agent.ID, Action: "deploy", Status: "success", CreatedBy: "a",
		}); err != nil {
			t.Fatalf("deploy %s: %v", agent.Name, err)
		}
	}

	assertBuiltinSplit := func(t *testing.T, when string) {
		t.Helper()
		inv, err := store.NewInventoryRepo(db).Snapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot %s: %v", when, err)
		}
		built := inventoryProperties(inv)
		for key, want := range map[string]int{
			productanalytics.PropAgentCount:                       1,
			productanalytics.PropDeployedAgentCount:               1,
			productanalytics.PropAgentsWithCustomToolsets:         1,
			productanalytics.PropDeployedAgentsWithCustomToolsets: 1,
			productanalytics.PropBuiltinAgentCount:                2,
			productanalytics.PropDeployedBuiltinAgentCount:        2,
		} {
			if built[key] != want {
				t.Errorf("%s: %s = %v, want %d", when, key, built[key], want)
			}
		}
	}

	assertBuiltinSplit(t, "as seeded")

	rewritten := "An operator rewrote this prompt."
	edited := seeded[0]
	edited.Name = "Orchestrator, renamed"
	edited.SystemPrompt = &rewritten
	edited.UpdatedBy = "operator@example.com"
	if err := agents.Update(ctx, edited, []string{"acme_payroll"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("operator edit of a seeded built-in: %v", err)
	}
	assertBuiltinSplit(t, "after an operator edited a built-in")
}

func TestInventoryProducesEveryApprovedField(t *testing.T) {
	built := inventoryProperties(store.Inventory{})
	for _, field := range productanalytics.ApprovedFields(productanalytics.SurfaceServer, productanalytics.EventInstallInventory) {
		if _, ok := built[field]; !ok {
			t.Errorf("install:inventory approves %q but the snapshot never produces it", field)
		}
	}
}

func assertStringSet(t *testing.T, props map[string]any, key string, want ...string) {
	t.Helper()
	got, ok := props[key].([]string)
	if !ok {
		t.Fatalf("%s = %#v, want []string", key, props[key])
	}
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
}

func TestInventoryReportNamesNothing(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()

	names := []string{
		"zzagentnamezz", "zzworkflownamezz", "zzconnectornamezz",
		"zzgatewaynamezz", "zztoolsetnamezz",
	}
	if err := store.NewToolPackageRepo(db).Create(ctx, &store.ToolPackage{Name: "zztoolsetnamezz", DiscoveryStatus: "created", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	if err := store.NewAgentRepo(db).Create(ctx, &store.Agent{Name: "zzagentnamezz", CreatedBy: "a", UpdatedBy: "a"}, []string{"zztoolsetnamezz"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := store.NewWorkflowRepo(db).Create(ctx, &store.Workflow{Name: "zzworkflownamezz", AppConfig: "{}", CreatedBy: "a", UpdatedBy: "a"}, store.WorkflowBindings{Toolsets: []string{"zztoolsetnamezz"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if err := store.NewConnectorRepo(db).Create(ctx, &store.Connector{Name: "zzconnectornamezz", Description: testSeedDescription(), Type: "sql", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := store.NewGatewayRepo(db).Create(ctx, &store.Gateway{Name: "zzgatewaynamezz", Description: testSeedDescription(), Type: "slack", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	inv, err := store.NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	built, err := json.Marshal(inventoryProperties(inv))
	if err != nil {
		t.Fatalf("marshal built properties: %v", err)
	}
	for _, name := range names {
		if strings.Contains(string(built), name) {
			t.Errorf("the built properties carry the name %q: %s", name, built)
		}
	}

	svc, sink := newCapturingAnalytics()
	ev := onlyInventoryEvent(t, reportNow(t, newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler)), svc, sink))

	encoded, err := json.Marshal(ev.Properties)
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}
	for _, name := range names {
		if strings.Contains(string(encoded), name) {
			t.Errorf("emitted properties leaked the name %q: %s", name, encoded)
		}
	}
	if ev.Properties[productanalytics.PropAgentsWithCustomToolsets] != int64(1) {
		t.Errorf("agentsWithCustomToolsets = %v, want 1", ev.Properties[productanalytics.PropAgentsWithCustomToolsets])
	}
}

func TestSnapshotScheduleIsJittered(t *testing.T) {
	const nominal = 24 * time.Hour
	seen := make(map[time.Duration]bool)
	var total time.Duration
	const rounds = 500
	for range rounds {
		d := jittered(nominal)
		if d < nominal-nominal/jitterDivisor || d > nominal+nominal/jitterDivisor {
			t.Fatalf("jittered(%v) = %v, outside the nominal ±1/%d spread", nominal, d, jitterDivisor)
		}
		seen[d] = true
		total += d
	}
	if len(seen) < rounds/2 {
		t.Errorf("only %d distinct waits in %d rounds; the schedule is not being spread", len(seen), rounds)
	}
	mean := total / rounds
	if drift := max(mean-nominal, nominal-mean); drift > nominal/20 {
		t.Errorf("mean wait = %v, %v away from the nominal %v; the cadence must not drift", mean, drift, nominal)
	}
	startups := make(map[time.Duration]bool)
	for range 20 {
		startups[jittered(inventoryStartupDelay)] = true
	}
	if len(startups) < 2 {
		t.Error("the startup delay is not being spread")
	}
}

func TestJitterLeavesAnUnspreadableDurationAlone(t *testing.T) {
	for _, d := range []time.Duration{0, time.Nanosecond, 3 * time.Nanosecond} {
		if got := jittered(d); got != d {
			t.Errorf("jittered(%v) = %v, want it unchanged", d, got)
		}
	}
}

var reporterExclusive sync.Mutex

func assertNoReporterGoroutine(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	stacks := string(buf[:goruntime.Stack(buf, true)])
	if strings.Contains(stacks, "(*inventoryReporter).run") {
		t.Errorf("a snapshot goroutine was still running:\n%s", stacks)
	}
}

func TestInventoryReporterStopsCleanly(t *testing.T) {
	db := store.TestDB(t)
	reporterExclusive.Lock()
	defer reporterExclusive.Unlock()

	svc, _ := newCapturingAnalytics()
	r := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
	r.startupDelay = time.Hour
	r.interval = time.Hour

	if err := r.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return while the loop was parked in its startup delay")
	}
	assertNoReporterGoroutine(t)
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
}

func TestInventoryReporterSurvivesACancelledStartContext(t *testing.T) {
	db := store.TestDB(t)
	reporterExclusive.Lock()
	defer reporterExclusive.Unlock()

	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: testAnalyticsIdentity,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
	})
	if err := svc.Start(t.Context()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	r := newInventoryReporter(db, localEmitter{analytics: svc}, slog.New(slog.DiscardHandler))
	r.startupDelay = time.Millisecond
	r.interval = time.Hour

	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := r.Start(startCtx); err != nil {
		t.Fatalf("start: %v", err)
	}
	cancelStart()

	deadline := time.Now().Add(5 * time.Second)
	for len(sink.all()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no snapshot was emitted after the start context was cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("stop reporter: %v", err)
	}
	assertNoReporterGoroutine(t)
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
}

var testAnalyticsIdentity = productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
	return productanalytics.Identity{
		Marker:     "11111111-1111-4111-8111-111111111111",
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
	}
})

func TestInventoryCountsOnlyRealModelConfigurations(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	repo := store.NewModelConfigRepo(db)

	if err := repo.Create(ctx, &store.ModelConfiguration{Alias: "placeholder", Provider: store.ModelConfigPlaceholder, ModelName: store.ModelConfigPlaceholder, CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create placeholder: %v", err)
	}
	if n, err := store.NewInventoryRepo(db).CountConfiguredModels(ctx); err != nil || n != 0 {
		t.Fatalf("configured models = %d (err %v), want 0 while only a placeholder exists", n, err)
	}

	if err := repo.Create(ctx, &store.ModelConfiguration{Alias: "general", Provider: "anthropic", ModelName: "claude-sonnet-4-5", CreatedBy: "a", UpdatedBy: "a"}); err != nil {
		t.Fatalf("create real config: %v", err)
	}
	if n, err := store.NewInventoryRepo(db).CountConfiguredModels(ctx); err != nil || n != 1 {
		t.Fatalf("configured models = %d (err %v), want 1 once a real model is set", n, err)
	}
}
