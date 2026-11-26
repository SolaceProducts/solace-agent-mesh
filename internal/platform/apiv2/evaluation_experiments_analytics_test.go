// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package apiv2

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type analyticsSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *analyticsSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *analyticsSink) all() []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productanalytics.Event(nil), s.events...)
}

func TestCreatingAnExperimentReportsItsTarget(t *testing.T) {
	sink := &analyticsSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-under-test", BindingKey: []byte("binding-key")}
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	testUsageReporter = analytics
	t.Cleanup(func() { testUsageReporter = nil })
	f := newWorkflowEvalsFixture(t, true)

	expectStatus(t, f.api.call(http.MethodPost, experimentsPath, f.body("agent-experiment", "Orchestrator")), http.StatusCreated)
	expectStatus(t, f.api.call(http.MethodPost, experimentsPath, f.workflowBody("workflow-experiment", "order_flow")), http.StatusCreated)

	f.api.core.ResourceAnalytics().AwaitReports(t.Context())
	if err := analytics.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	var targets []string
	for _, e := range sink.all() {
		if e.Name == productanalytics.EventEvalCreated {
			target, _ := e.Properties[productanalytics.PropTargetType].(string)
			targets = append(targets, target)
		}
	}
	slices.Sort(targets)
	if !slices.Equal(targets, []string{"agent", "workflow"}) {
		t.Errorf("eval:created target types = %v, want one agent and one workflow", targets)
	}
}
