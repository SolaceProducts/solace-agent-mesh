// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestModelConfiguredNeverBuildsAnOperatorAuthoredIdentifierIntoTheEvent(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		modelName  string
		wantFamily string
		secrets    []string
	}{
		{
			name:       "an inference-profile ARN carries an AWS account id",
			provider:   "bedrock",
			modelName:  "arn:aws:bedrock:us-east-1:123456789012:inference-profile/acme-legal",
			wantFamily: modelfamily.Other,
			secrets:    []string{"123456789012", "acme-legal", "us-east-1"},
		},
		{
			name:       "a fine-tune id names the organization and the use case",
			provider:   "openai",
			modelName:  "ft:gpt-4o-2024-08-06:acme-corp:legal-triage:AbCd1234",
			wantFamily: modelfamily.GPT4o,
			secrets:    []string{"acme-corp", "legal-triage", "AbCd1234"},
		},
		{
			name:       "an Azure deployment name is chosen by the operator",
			provider:   "azure",
			modelName:  "acme-prod-gpt-4o-westeurope",
			wantFamily: modelfamily.GPT4o,
			secrets:    []string{"acme-prod", "westeurope"},
		},
		{
			name:       "a self-hosted provider name identifies the install",
			provider:   "acme-internal-vllm-cluster",
			modelName:  "llama-3.1-70b",
			wantFamily: modelfamily.Llama,
			secrets:    []string{"acme"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			props := modelConfiguredProperties(c.provider, c.modelName, "https://acme-llm.internal.example.com/v1", taxonomy.ModelKindChat, httputil.SurfaceWebUI)

			if props[productanalytics.PropModel] != c.wantFamily {
				t.Errorf("model = %v, want the family %q", props[productanalytics.PropModel], c.wantFamily)
			}
			rendered := fmt.Sprintf("%v", props)
			for _, secret := range c.secrets {
				if strings.Contains(rendered, secret) {
					t.Errorf("%q reached the built properties %s", secret, rendered)
				}
			}
		})
	}
}

func TestModelConfiguredReportsTheProviderTheRunReports(t *testing.T) {
	cases := []struct {
		provider  string
		modelName string
		kind      string
		want      string
	}{
		{"custom", "gpt-4o", taxonomy.ModelKindChat, "openai"},
		{"custom", "claude-sonnet-4-5", taxonomy.ModelKindChat, "anthropic"},
		{"azure_openai", "gpt-4o", taxonomy.ModelKindChat, "azure"},
		{"vertex_ai", "gemini-2.5-pro", taxonomy.ModelKindChat, "vertex"},
		{"google_ai_studio", "gemini-2.5-pro", taxonomy.ModelKindChat, "gemini"},
		{"bedrock", "anthropic.claude-sonnet-4-5", taxonomy.ModelKindChat, "bedrock"},
		{"ollama", "llama3.1", taxonomy.ModelKindChat, "ollama"},
		{"anthropic", "claude-sonnet-4-5", taxonomy.ModelKindChat, "anthropic"},
		{"openai", "gpt-4o", taxonomy.ModelKindChat, "openai"},
		{"typesafe", "typesafe-decision", taxonomy.ModelKindDecision, "typesafe"},
	}
	for _, c := range cases {
		t.Run(c.provider+" "+c.modelName, func(t *testing.T) {
			props := modelConfiguredProperties(c.provider, c.modelName, "", c.kind, httputil.SurfaceWebUI)
			if props[productanalytics.PropProvider] != c.want {
				t.Errorf("provider = %v, want %q, the provider agent:run_completed reports for this model", props[productanalytics.PropProvider], c.want)
			}
		})
	}
}

func TestModelConfiguredDropsAnUnapprovedDecisionProvider(t *testing.T) {
	props := modelConfiguredProperties("acme-internal-router", "acme-router-v2", "", taxonomy.ModelKindDecision, httputil.SurfaceWebUI)
	if got, kept := props[productanalytics.PropProvider]; kept {
		t.Errorf("provider = %v, want it absent", got)
	}
}

func TestModelTypeReadsTheApiBaseWithoutCarryingIt(t *testing.T) {
	cases := []struct {
		provider string
		apiBase  string
		want     string
	}{
		{"openai", "", productanalytics.ModelTypeHosted},
		{"openai", "https://api.openai.com/v1", productanalytics.ModelTypeHosted},
		{"openai", "http://localhost:11434/v1", productanalytics.ModelTypeLocal},
		{"openai", "http://127.0.0.1:8000/v1", productanalytics.ModelTypeLocal},
		{"openai", "http://0.0.0.0:8000/v1", productanalytics.ModelTypeLocal},
		{"openai", "http://[::1]:8000/v1", productanalytics.ModelTypeLocal},
		{"openai", "http://host.docker.internal:11434/v1", productanalytics.ModelTypeLocal},
		{"openai", "https://acme-llm.corp.example.com/v1", productanalytics.ModelTypeHosted},
		{"openai", "not a url at all", productanalytics.ModelTypeHosted},
		{"ollama", "", productanalytics.ModelTypeLocal},
		{"ollama", "https://acme-llm.corp.example.com/v1", productanalytics.ModelTypeLocal},
	}
	for _, c := range cases {
		t.Run(c.provider+" "+c.apiBase, func(t *testing.T) {
			props := modelConfiguredProperties(c.provider, "gpt-4o", c.apiBase, taxonomy.ModelKindChat, httputil.SurfaceWebUI)
			if props[productanalytics.PropModelType] != c.want {
				t.Errorf("modelType = %v, want %q", props[productanalytics.PropModelType], c.want)
			}
			if c.apiBase != "" && strings.Contains(fmt.Sprintf("%v", props), c.apiBase) {
				t.Errorf("the apiBase itself reached the built properties %v", props)
			}
		})
	}
}

func TestDiscriminatorsOutsideTheirVocabularyAreNeverBuilt(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		value      string
		vocabulary []string
		want       bool
	}{
		{"an approved connector category", productanalytics.PropCategory, "knowledge_base", productanalytics.ConnectorCategories, true},
		{"an operator-named connector type", productanalytics.PropCategory, "acme_customer_ledger", productanalytics.ConnectorCategories, false},
		{"an approved entrypoint type", productanalytics.PropGatewayType, "webhook", productanalytics.GatewayTypes, true},
		{"an entrypoint type this build does not know", productanalytics.PropGatewayType, "acme_edi_feed", productanalytics.GatewayTypes, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			props := approvedOnly(c.key, c.value, c.vocabulary)
			got, kept := props[c.key]
			if kept != c.want {
				t.Fatalf("kept = %v, want %v (props %v)", kept, c.want, props)
			}
			if c.want && got != c.value {
				t.Errorf("%s = %v, want %q", c.key, got, c.value)
			}
		})
	}
}

func TestNilReporterIsSafeToCall(t *testing.T) {
	if r := NewResourceReporter(nil, stubDeployHistory{}, slog.New(slog.DiscardHandler)); r != nil {
		t.Fatal("a reporter should not be built without an analytics service")
	}
	var r *ResourceReporter
	r.AgentCreated(t.Context(), 3, httputil.SurfaceWebUI)
	r.AgentDeployed(t.Context(), "deployment-id")
	r.AgentDeleted(t.Context())
	r.WorkflowCreated(t.Context())
	r.WorkflowDeployed(t.Context(), "deployment-id")
	r.ConnectorCreated(t.Context(), "sql")
	r.ConnectorDeleted(t.Context())
	r.EntrypointCreated(t.Context(), "webhook")
	r.EntrypointDeleted(t.Context())
	r.ToolsetCreated(t.Context())
	r.EvaluatorCreated(t.Context(), taxonomy.EvaluatorTypeDecision, "choice", taxonomy.EscalationOnError, httputil.SurfaceWebUI)
	r.EvalRun(t.Context(), taxonomy.OutcomeSuccess, "", taxonomy.EvalTargetTypeAgent, &EvalRunShape{}, &EvalDecisionFacts{Evaluators: 1, Decided: 1})
	r.ModelConfigured(t.Context(), "openai", "gpt-4o", "", taxonomy.ModelKindChat, httputil.SurfaceSystem)
}

type stubDeployHistory struct {
	first bool
	err   error
}

func (s stubDeployHistory) IsFirstAgentDeploy(context.Context, string) (bool, error) {
	return s.first, s.err
}

func (s stubDeployHistory) IsFirstWorkflowDeploy(context.Context, string) (bool, error) {
	return s.first, s.err
}

func TestAgentCreatedCarriesOnlyAnApprovedSurface(t *testing.T) {
	for _, surface := range httputil.SubmitterSurfaces {
		props := agentCreatedProperties(productanalytics.AgentTypeUserCreated, 2, surface)
		if props[productanalytics.PropSubmittedFrom] != surface {
			t.Errorf("submittedFrom = %v, want the approved surface %q", props[productanalytics.PropSubmittedFrom], surface)
		}
		if props[productanalytics.PropToolsetCount] != 2 {
			t.Errorf("toolsetCount = %v, want 2", props[productanalytics.PropToolsetCount])
		}
	}
	for _, bogus := range []string{"", "acme-portal", "UI", "carrier-pigeon"} {
		props := agentCreatedProperties(productanalytics.AgentTypeUserCreated, 2, bogus)
		if got, kept := props[productanalytics.PropSubmittedFrom]; kept {
			t.Errorf("submittedFrom = %v for input %q, want the field absent", got, bogus)
		}
		if props[productanalytics.PropToolsetCount] != 2 {
			t.Errorf("toolsetCount = %v, want the count kept regardless of the surface", props[productanalytics.PropToolsetCount])
		}
	}
}

func TestModelConfiguredSeparatesTheSystemFromAPerson(t *testing.T) {
	for _, surface := range []string{httputil.SurfaceSystem, httputil.SurfaceWebUI, httputil.SurfaceCLI, httputil.SurfaceAPI} {
		props := modelConfiguredProperties("openai", "gpt-4o", "", taxonomy.ModelKindChat, surface)
		if got := props[productanalytics.PropSubmittedFrom]; got != surface {
			t.Errorf("submittedFrom = %v, want %q", got, surface)
		}
	}
	props := modelConfiguredProperties("openai", "gpt-4o", "", taxonomy.ModelKindChat, "acme-provisioner")
	if got, kept := props[productanalytics.PropSubmittedFrom]; kept {
		t.Errorf("submittedFrom = %v, want the field absent", got)
	}
}

func TestModelConfiguredProducesEveryApprovedField(t *testing.T) {
	produced := modelConfiguredProperties("openai", "gpt-4o", "", taxonomy.ModelKindChat, httputil.SurfaceSystem)
	for _, field := range productanalytics.ApprovedFields(productanalytics.SurfaceServer, productanalytics.EventModelConfigured) {
		if _, ok := produced[field]; !ok {
			t.Errorf("model:configured approves %q but the emitter never produces it", field)
		}
	}
}

func TestAgentCreatedProducesEveryApprovedField(t *testing.T) {
	produced := agentCreatedProperties(productanalytics.AgentTypeUserCreated, 1, httputil.SurfaceWebUI)
	for _, field := range productanalytics.ApprovedFields(productanalytics.SurfaceServer, productanalytics.EventAgentCreated) {
		if _, ok := produced[field]; !ok {
			t.Errorf("agent:created approves %q but the emitter never produces it", field)
		}
	}
}

func TestAgentDeletedProducesEveryApprovedField(t *testing.T) {
	produced := agentDeletedProperties(productanalytics.AgentTypeUserCreated)
	for _, field := range productanalytics.ApprovedFields(productanalytics.SurfaceServer, productanalytics.EventAgentDeleted) {
		if _, ok := produced[field]; !ok {
			t.Errorf("agent:deleted approves %q but the emitter never produces it", field)
		}
	}
}

func TestEveryAgentCreationKindHasAProducer(t *testing.T) {
	produced := map[any]bool{
		agentCreatedProperties(productanalytics.AgentTypeUserCreated, 0, httputil.SurfaceWebUI)[productanalytics.PropAgentType]: true,
		agentCreatedProperties(productanalytics.AgentTypeRemote, 0, httputil.SurfaceWebUI)[productanalytics.PropAgentType]:      true,
		agentDeletedProperties(productanalytics.AgentTypeUserCreated)[productanalytics.PropAgentType]:                           true,
		agentDeletedProperties(productanalytics.AgentTypeRemote)[productanalytics.PropAgentType]:                                true,
	}
	for _, kind := range productanalytics.AgentCreationTypes {
		if !produced[kind] {
			t.Errorf("agent kind %q is approved on a creation and nothing produces it", kind)
		}
	}
}

func TestOnlyAFirstDeployIsReported(t *testing.T) {
	cases := []struct {
		name    string
		history DeployHistory
		want    int
	}{
		{"the first deploy", stubDeployHistory{first: true}, 1},
		{"a redeploy", stubDeployHistory{first: false}, 0},
		{"an unreadable history", stubDeployHistory{first: true, err: errUnreadableHistory}, 0},
		{"no history at all", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, sink := newCapturingAnalytics()
			r := NewResourceReporter(svc, c.history, slog.New(slog.DiscardHandler))
			r.AgentDeployed(t.Context(), "deployment-id")
			r.WorkflowDeployed(t.Context(), "deployment-id")

			r.AwaitReports(t.Context())
			if err := svc.Stop(t.Context()); err != nil {
				t.Fatalf("stop analytics: %v", err)
			}
			events := sink.all()
			agents, workflows := 0, 0
			for _, ev := range events {
				switch ev.Name {
				case productanalytics.EventAgentDeployed:
					agents++
				case productanalytics.EventWorkflowDeployed:
					workflows++
				}
			}
			if agents != c.want || workflows != c.want {
				t.Errorf("reported %d agent:deployed and %d workflow:deployed, want %d of each", agents, workflows, c.want)
			}
			if len(events) != agents+workflows {
				t.Errorf("delivered unexpected events: %v", events)
			}
		})
	}
}

func TestADeployWithNoRowIsNotReported(t *testing.T) {
	svc, sink := newCapturingAnalytics()
	r := NewResourceReporter(svc, stubDeployHistory{first: true}, slog.New(slog.DiscardHandler))
	r.AgentDeployed(t.Context(), "")
	r.WorkflowDeployed(t.Context(), "")

	r.AwaitReports(t.Context())
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	if events := sink.all(); len(events) != 0 {
		t.Errorf("delivered %v, want nothing", events)
	}
}

var errUnreadableHistory = errors.New("deployment history unavailable")

func TestReportSurvivesACancelledRequestContext(t *testing.T) {
	svc, sink := newCapturingAnalytics()
	r := NewResourceReporter(svc, stubDeployHistory{}, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.ToolsetCreated(ctx)

	r.AwaitReports(t.Context())
	if err := svc.Stop(t.Context()); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	events := sink.all()
	if len(events) != 1 || events[0].Name != productanalytics.EventToolsetCreated {
		t.Fatalf("delivered %v, want one toolset:created", events)
	}
}
