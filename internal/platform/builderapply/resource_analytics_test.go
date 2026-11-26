// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package builderapply

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type buildCaptureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (c *buildCaptureSink) Send(_ context.Context, events []productanalytics.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
	return nil
}

func (c *buildCaptureSink) all() []productanalytics.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]productanalytics.Event(nil), c.events...)
}

func withCapturingAnalytics(t *testing.T, svc *Service) (*productanalytics.Service, *buildCaptureSink, *platpkg.ResourceReporter) {
	t.Helper()
	sink := &buildCaptureSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-under-test", BindingKey: []byte("binding-key")}
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	reporter := platpkg.NewResourceReporter(analytics, noDeployHistory{}, slog.New(slog.DiscardHandler))
	svc.analytics = reporter
	return analytics, sink, reporter
}

func deliveredNames(t *testing.T, analytics *productanalytics.Service, sink *buildCaptureSink, reporter *platpkg.ResourceReporter) []string {
	t.Helper()
	reporter.AwaitReports(t.Context())
	if err := analytics.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	names := make([]string, 0)
	for _, e := range sink.all() {
		names = append(names, e.Name)
	}
	return names
}

func countOf(names []string, name string) int {
	n := 0
	for _, got := range names {
		if got == name {
			n++
		}
	}
	return n
}

func TestApplyReportsEachComponentItCreated(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if resp.Summary.Created != 2 {
		t.Fatalf("Created = %d, want 2: %+v", resp.Summary.Created, resp.Operations)
	}

	names := deliveredNames(t, analytics, sink, reporter)
	if countOf(names, productanalytics.EventAgentCreated) != 1 {
		t.Errorf("agent:created appeared %d times in %v, want 1", countOf(names, productanalytics.EventAgentCreated), names)
	}
	if countOf(names, productanalytics.EventConnectorCreated) != 1 {
		t.Errorf("connector:created appeared %d times in %v, want 1", countOf(names, productanalytics.EventConnectorCreated), names)
	}
}

func TestApplyReportsTheConnectorCategory(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db")},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	reporter.AwaitReports(t.Context())
	if err := analytics.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1: %+v", len(events), events)
	}
	if got := events[0].Properties[productanalytics.PropCategory]; got != "sql" {
		t.Errorf("category = %v, want the connector type the build saved", got)
	}
}

func TestADryRunApplyReportsNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		DryRun:     true,
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.DryRun {
		t.Fatal("the response should say it was a dry run")
	}

	if names := deliveredNames(t, analytics, sink, reporter); len(names) != 0 {
		t.Fatalf("a dry run reported %v, want nothing", names)
	}
}

func TestReapplyingAnExistingBuildReportsNoFurtherCreations(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	edited := connectorComponent("test-db")
	edited.Content = `{"name":"test-db","description":"A connector used in tests.","type":"sql","subtype":"postgres","values":{"hostname":"edited-host"}}`

	analytics, sink, reporter := withCapturingAnalytics(t, svc)
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{edited, agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "test-db"); op.Action != actionUpdate {
		t.Fatalf("the edited connector planned %q, want an update — the update path is what this test guards", op.Action)
	}

	if names := deliveredNames(t, analytics, sink, reporter); len(names) != 0 {
		t.Fatalf("re-applying reported %v, want nothing", names)
	}
}

func TestApplyReportsAWorkflowItCreatedAndNotAnUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Fulfilment")},
	}, testActor); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Shipping")},
	}, testActor); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	names := deliveredNames(t, analytics, sink, reporter)
	if countOf(names, productanalytics.EventWorkflowCreated) != 1 {
		t.Errorf("workflow:created appeared %d times in %v, want 1", countOf(names, productanalytics.EventWorkflowCreated), names)
	}
}

func TestApplyReportsTheClientThatAskedForTheBuild(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	plans, reg, err := svc.Plan(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	svc.ApplyPlanned(t.Context(), plans, reg, false, "tester", httputil.SurfaceWebUI)

	reporter.AwaitReports(t.Context())
	if err := analytics.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	for _, ev := range sink.all() {
		if ev.Name != productanalytics.EventAgentCreated {
			continue
		}
		if got := ev.Properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceWebUI {
			t.Errorf("submittedFrom = %#v, want %q", got, httputil.SurfaceWebUI)
		}
		return
	}
	t.Fatal("the build reported no agent:created")
}

func TestApplyReportsARemoteAgentAsRemote(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	analytics, sink, reporter := withCapturingAnalytics(t, svc)

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{a2aAgentComponent("remote_test_agent")},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	deliveredNames(t, analytics, sink, reporter)
	for _, ev := range sink.all() {
		if ev.Name != productanalytics.EventAgentCreated {
			continue
		}
		if got := ev.Properties[productanalytics.PropAgentType]; got != productanalytics.AgentTypeRemote {
			t.Errorf("agentType = %v, want %q", got, productanalytics.AgentTypeRemote)
		}
		return
	}
	t.Fatal("the build reported no agent:created")
}

type noDeployHistory struct{}

func (noDeployHistory) IsFirstAgentDeploy(context.Context, string) (bool, error) {
	return false, nil
}

func (noDeployHistory) IsFirstWorkflowDeploy(context.Context, string) (bool, error) {
	return false, nil
}

func a2aAgentComponent(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind:    kindA2AAgent,
		Name:    name,
		Content: `{"name":"` + name + `","agentUrl":"https://example.com/a2a","agentCardLocation":"well_known"}`,
	}
}
