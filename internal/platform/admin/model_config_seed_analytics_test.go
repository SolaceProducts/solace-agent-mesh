// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type seedUnderTest struct {
	repo      *store.ModelConfigRepo
	reporter  *platpkg.ResourceReporter
	analytics *productanalytics.Service
	sink      *analyticsCaptureSink
}

func newSeedUnderTest(t *testing.T) *seedUnderTest {
	t.Helper()
	sink := &analyticsCaptureSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-under-test", BindingKey: []byte("binding-key")}
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	return &seedUnderTest{
		repo:      store.NewModelConfigRepo(testOpenDB(t)),
		reporter:  platpkg.NewResourceReporter(analytics, nil, slog.New(slog.DiscardHandler)),
		analytics: analytics,
		sink:      sink,
	}
}

func (s *seedUnderTest) seed(t *testing.T) {
	t.Helper()
	if err := SeedModelConfigs(t.Context(), s.repo, s.reporter, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func (s *seedUnderTest) delivered(t *testing.T) []productanalytics.Event {
	t.Helper()
	s.reporter.AwaitReports(t.Context())
	if err := s.analytics.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	return s.sink.all()
}

func modelConfiguredEvents(events []productanalytics.Event) []productanalytics.Event {
	var configured []productanalytics.Event
	for _, event := range events {
		if event.Name == productanalytics.EventModelConfigured {
			configured = append(configured, event)
		}
	}
	return configured
}

func TestSeedingFromTheEnvironmentReportsTheActivationMilestone(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "openai/gpt-4o")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test-key")

	s := newSeedUnderTest(t)
	s.seed(t)

	events := modelConfiguredEvents(s.delivered(t))
	if len(events) != 1 {
		t.Fatalf("seeding one alias reported %d model:configured events, want 1", len(events))
	}
	properties := events[0].Properties
	if got := properties[productanalytics.PropModel]; got != "gpt-4o" {
		t.Errorf("model = %v, want the family gpt-4o", got)
	}
	if got := properties[productanalytics.PropProvider]; got != "openai" {
		t.Errorf("provider = %v, want openai", got)
	}
	if got := properties[productanalytics.PropModelType]; got != productanalytics.ModelTypeHosted {
		t.Errorf("modelType = %v, want %q", got, productanalytics.ModelTypeHosted)
	}
	if got := properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceSystem {
		t.Errorf("submittedFrom = %v, want %q", got, httputil.SurfaceSystem)
	}
	if got := properties[productanalytics.PropModelKind]; got != taxonomy.ModelKindChat {
		t.Errorf("modelKind = %v, want %q", got, taxonomy.ModelKindChat)
	}
}

func TestSeedingReportsOncePerConfigurationAndNotPerStart(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "openai/gpt-4o")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")

	first := newSeedUnderTest(t)
	first.seed(t)
	if got := modelConfiguredEvents(first.delivered(t)); len(got) != 1 {
		t.Fatalf("the first start reported %d model:configured events, want 1", len(got))
	}

	second := newSeedUnderTest(t)
	second.repo = first.repo
	second.seed(t)
	second.seed(t)
	if got := modelConfiguredEvents(second.delivered(t)); len(got) != 0 {
		t.Errorf("restarting reported %d model:configured events, want none — this is a milestone, not a heartbeat", len(got))
	}
}

func TestUpgradingAPlaceholderReportsTheConfigurationOnce(t *testing.T) {
	s := newSeedUnderTest(t)

	s.seed(t)

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "claude-sonnet-4-5")
	t.Setenv("LLM_SERVICE_ENDPOINT", "http://localhost:11434/v1")
	s.seed(t)
	s.seed(t)

	events := modelConfiguredEvents(s.delivered(t))
	if len(events) != 1 {
		t.Fatalf("reported %d model:configured events across three starts, want 1 for the one upgrade: %v", len(events), events)
	}
	if got := events[0].Properties[productanalytics.PropModel]; got != "claude-sonnet" {
		t.Errorf("model = %v, want the family claude-sonnet", got)
	}
	if got := events[0].Properties[productanalytics.PropModelType]; got != productanalytics.ModelTypeLocal {
		t.Errorf("modelType = %v, want %q", got, productanalytics.ModelTypeLocal)
	}
}

func TestSeedingWithNoModelInTheEnvironmentReportsNothing(t *testing.T) {
	s := newSeedUnderTest(t)
	s.seed(t)

	if got := s.delivered(t); len(got) != 0 {
		t.Errorf("a start with no model configured reported %v, want nothing", namesOf(got))
	}
}
