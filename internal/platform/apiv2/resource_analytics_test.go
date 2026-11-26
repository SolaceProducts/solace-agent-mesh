// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package apiv2

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type resourceAnalyticsSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *resourceAnalyticsSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

type resourceAnalytics struct {
	t    *testing.T
	svc  *productanalytics.Service
	sink *resourceAnalyticsSink
}

func newResourceAnalytics(t *testing.T) *resourceAnalytics {
	t.Helper()
	sink := &resourceAnalyticsSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-under-test", BindingKey: []byte("binding-key")}
		}),
		Logger: slog.New(slog.DiscardHandler),
	})
	testUsageReporter = svc
	t.Cleanup(func() { testUsageReporter = nil })
	return &resourceAnalytics{t: t, svc: svc, sink: sink}
}

func (r *resourceAnalytics) delivered(core *platpkg.PlatformCore) []productanalytics.Event {
	r.t.Helper()
	core.ResourceAnalytics().AwaitReports(r.t.Context())
	if err := r.svc.Stop(r.t.Context()); err != nil {
		r.t.Fatalf("stop analytics: %v", err)
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	return append([]productanalytics.Event(nil), r.sink.events...)
}

func resourceAnalyticsNames(events []productanalytics.Event) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Name)
	}
	return names
}

func resourceAnalyticsNamed(events []productanalytics.Event, name string) []productanalytics.Event {
	var found []productanalytics.Event
	for _, e := range events {
		if e.Name == name {
			found = append(found, e)
		}
	}
	return found
}

func resourceAnalyticsOnly(t *testing.T, events []productanalytics.Event, name string) productanalytics.Event {
	t.Helper()
	found := resourceAnalyticsNamed(events, name)
	if len(found) != 1 {
		t.Fatalf("got %d %s events in %v, want exactly 1", len(found), name, resourceAnalyticsNames(events))
	}
	return found[0]
}

func resourceAnalyticsExpectNoneOf(t *testing.T, event productanalytics.Event, authored ...string) {
	t.Helper()
	rendered, err := json.Marshal(event.Properties)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range authored {
		if strings.Contains(string(rendered), text) {
			t.Errorf("%q reached the %s event properties %s", text, event.Name, rendered)
		}
	}
}

func resourceAnalyticsExpectServer(t *testing.T, event productanalytics.Event) {
	t.Helper()
	if got := event.Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceServer {
		t.Errorf("%s interface = %v, want %q", event.Name, got, productanalytics.SurfaceServer)
	}
}

func resourceAnalyticsAgent(name string, toolsets ...string) map[string]any {
	return map[string]any{
		"name":         name,
		"description":  "An agent created without a browser anywhere in the picture.",
		"systemPrompt": "You help with tests.",
		"toolsets":     append([]string{}, toolsets...),
	}
}

func resourceAnalyticsWorkflow(name string) map[string]any {
	return map[string]any{"name": name, "description": "A workflow created without a browser.", "appConfig": workflowAppConfig("test")}
}

func resourceAnalyticsRemoteAgent(name string) map[string]any {
	return map[string]any{
		"name":              name,
		"description":       "A remote agent reached over A2A from a partner install.",
		"agentUrl":          "https://partner.example.com/a2a",
		"agentCardLocation": "well_known",
		"agentAuthType":     "none",
		"agentAuthConfig":   map[string]any{"type": "none"},
	}
}

func TestResourceAnalytics_CreatingAnAgentReportsOneServerEventWithItsToolsetCountAndNotItsName(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)

	expectStatus(t, api.call(http.MethodPost, "/agents", resourceAnalyticsAgent("acme-payroll-triage", "builtin_artifact_tools", "builtin_time_tools")), http.StatusCreated)

	events := analytics.delivered(core)
	if len(events) != 1 {
		t.Fatalf("one create produced %v, want exactly one event", resourceAnalyticsNames(events))
	}
	event := resourceAnalyticsOnly(t, events, productanalytics.EventAgentCreated)
	resourceAnalyticsExpectServer(t, event)
	if got, ok := event.Properties[productanalytics.PropToolsetCount].(int64); !ok || got != 2 {
		t.Errorf("toolsetCount = %#v, want the two toolsets the agent was saved with", event.Properties[productanalytics.PropToolsetCount])
	}
	resourceAnalyticsExpectNoneOf(t, event, "acme")
}

func TestResourceAnalytics_DeletingAnAgentReportsIt(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, _ := newAWEAPI(t)
	created := expectCreated[Agent](t, api.call(http.MethodPost, "/agents", resourceAnalyticsAgent("short-lived-agent")), "agents")
	expectStatus(t, api.call(http.MethodDelete, "/agents/"+created.ID, nil), http.StatusNoContent)

	resourceAnalyticsOnly(t, analytics.delivered(api.core), productanalytics.EventAgentDeleted)
}

func TestResourceAnalytics_ARemoteAgentsCreateAndDeleteBothCarryItsKind(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, _ := newAWEAPI(t)
	created := createRemoteAgent(api, resourceAnalyticsRemoteAgent("acme-partner-agent"))
	expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+created.ID, nil), http.StatusNoContent)

	events := analytics.delivered(api.core)
	for _, name := range []string{productanalytics.EventAgentCreated, productanalytics.EventAgentDeleted} {
		if got := resourceAnalyticsOnly(t, events, name).Properties[productanalytics.PropAgentType]; got != productanalytics.AgentTypeRemote {
			t.Errorf("%s agentType = %#v, want %q", name, got, productanalytics.AgentTypeRemote)
		}
	}
}

func TestResourceAnalytics_AuthoredAndRemoteAgentCreationsAreToldApart(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, _ := newAWEAPI(t)
	expectStatus(t, api.call(http.MethodPost, "/agents", resourceAnalyticsAgent("authored-here")), http.StatusCreated)
	createRemoteAgent(api, resourceAnalyticsRemoteAgent("connected-from-away"))

	kinds := map[any]int{}
	for _, event := range resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventAgentCreated) {
		kinds[event.Properties[productanalytics.PropAgentType]]++
	}
	want := map[any]int{productanalytics.AgentTypeUserCreated: 1, productanalytics.AgentTypeRemote: 1}
	if len(kinds) != len(want) || kinds[productanalytics.AgentTypeUserCreated] != 1 || kinds[productanalytics.AgentTypeRemote] != 1 {
		t.Errorf("the two creations reported kinds %v, want one of each of %v", kinds, want)
	}
}

func TestResourceAnalytics_AConnectorsLifecycleReportsItsCategoryAndNotItsName(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	created := expectCreated[Connector](t, api.call(http.MethodPost, "/connectors", map[string]any{
		"name":             "acme-customer-ledger",
		"description":      "The connector an operator named after the customer's own system.",
		"connectorType":    "knowledge_base",
		"connectorSubtype": "bedrock",
		"values":           map[string]any{"kb_id": "KB1234567890", "region": "us-east-1", "auth_type": "iam"},
	}), "connectors")
	expectStatus(t, api.call(http.MethodDelete, "/connectors/"+created.ID, nil), http.StatusNoContent)

	events := analytics.delivered(core)
	event := resourceAnalyticsOnly(t, events, productanalytics.EventConnectorCreated)
	if got := event.Properties[productanalytics.PropCategory]; got != "knowledge_base" {
		t.Errorf("category = %v, want the saved connector type", got)
	}
	resourceAnalyticsExpectNoneOf(t, event, "acme")
	resourceAnalyticsOnly(t, events, productanalytics.EventConnectorDeleted)
}

func TestResourceAnalytics_AnEntrypointsLifecycleReportsItsTypeAndNotItsName(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api := newEntrypointAPI(t, nil)
	created := expectCreated[Entrypoint](t, api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           "acme-edi-intake",
		"description":    "The entrypoint an operator named after the system that calls it.",
		"entrypointType": "slack",
		"values":         map[string]any{"bot_token": slackBotToken, "app_token": "xapp-app-token"},
	}), "entrypoints")
	expectStatus(t, api.call(http.MethodDelete, "/entrypoints/"+created.ID, nil), http.StatusNoContent)

	events := analytics.delivered(api.core)
	event := resourceAnalyticsOnly(t, events, productanalytics.EventGatewayCreated)
	if got := event.Properties[productanalytics.PropGatewayType]; got != "slack" {
		t.Errorf("gatewayType = %v, want the saved entrypoint type", got)
	}
	resourceAnalyticsExpectNoneOf(t, event, "acme")
	resourceAnalyticsOnly(t, events, productanalytics.EventGatewayDeleted)
}

func TestResourceAnalytics_CreatingAToolsetReportsItAndNotItsName(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	expectStatus(t, newAPIClient(t, core).call(http.MethodPost, "/toolsets", map[string]any{
		"name": "acme_payroll_tools", "description": "A toolset named after the customer's own system.",
	}), http.StatusCreated)

	resourceAnalyticsExpectNoneOf(t, resourceAnalyticsOnly(t, analytics.delivered(core), productanalytics.EventToolsetCreated), "acme")
}

func TestResourceAnalytics_ConfiguringAModelReportsItsFamilyAndNotItsIdentifier(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	expectStatus(t, newAPIClient(t, core).call(http.MethodPost, "/models", map[string]any{
		"alias":     "analytics-under-test",
		"provider":  "openai",
		"modelName": "ft:gpt-4o-2024-08-06:acme-corp:legal-triage:AbCd1234",
		"apiBase":   "http://localhost:11434/v1",
	}), http.StatusCreated)

	event := resourceAnalyticsOnly(t, analytics.delivered(core), productanalytics.EventModelConfigured)
	for prop, want := range map[string]any{
		productanalytics.PropProvider:  "openai",
		productanalytics.PropModel:     "gpt-4o",
		productanalytics.PropModelType: productanalytics.ModelTypeLocal,
	} {
		if got := event.Properties[prop]; got != want {
			t.Errorf("%s = %v, want %v", prop, got, want)
		}
	}
	resourceAnalyticsExpectNoneOf(t, event, "acme-corp", "legal-triage", "AbCd1234", "localhost")
}

func TestResourceAnalytics_ATestConnectionOfAnUnsavedModelReportsNothing(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	expectStatus(t, newAPIClient(t, core).call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": fakeProvider(t, answerWith(`{}`)),
		"authType": "apikey", "authConfig": keyAuth("sk"),
	}), http.StatusOK)

	if events := analytics.delivered(core); len(events) != 0 {
		t.Fatalf("a connection test reported %v, want nothing", resourceAnalyticsNames(events))
	}
}

func TestResourceAnalytics_ARejectedCreateReportsNothing(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	expectStatus(t, newAPIClient(t, core).call(http.MethodPost, "/agents", map[string]any{
		"name": "x", "description": "too short a name to be accepted",
	}), http.StatusUnprocessableEntity)

	if events := analytics.delivered(core); len(events) != 0 {
		t.Fatalf("a rejected create reported %v, want nothing", resourceAnalyticsNames(events))
	}
}

func TestResourceAnalytics_ReadsReportNothing(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	for _, path := range []string{"/agents", "/connectors", "/entrypoints", "/toolsets", "/models"} {
		expectStatus(t, api.call(http.MethodGet, path, nil), http.StatusOK)
	}

	if events := analytics.delivered(core); len(events) != 0 {
		t.Fatalf("reads reported %v, want nothing", resourceAnalyticsNames(events))
	}
}

func TestResourceAnalytics_ACreatedAgentCarriesTheClientThatAskedForIt(t *testing.T) {
	for _, c := range []struct {
		name, declared, want string
	}{
		{"the web UI", httputil.SurfaceWebUI, httputil.SurfaceWebUI},
		{"the CLI", httputil.SurfaceCLI, httputil.SurfaceCLI},
		{"a direct call declaring nothing", "", httputil.SurfaceAPI},
		{"a request forging a relay", httputil.SurfaceSlack, httputil.SurfaceAPI},
		{"a request naming something invented", "acme-portal", httputil.SurfaceAPI},
	} {
		t.Run(c.name, func(t *testing.T) {
			analytics := newResourceAnalytics(t)
			core, _ := newTestCore(t)
			api := newAPIClient(t, core)
			checked := api.handler
			api.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.declared != "" {
					r.Header.Set(httputil.HeaderClientSurface, c.declared)
				}
				checked.ServeHTTP(w, r)
			})
			expectStatus(t, api.call(http.MethodPost, "/agents", resourceAnalyticsAgent("surface-attributed-agent")), http.StatusCreated)

			event := resourceAnalyticsOnly(t, analytics.delivered(core), productanalytics.EventAgentCreated)
			if got := event.Properties[productanalytics.PropSubmittedFrom]; got != c.want {
				t.Errorf("submittedFrom = %#v, want %q", got, c.want)
			}
		})
	}
}

func TestResourceAnalytics_CreatingAWorkflowReportsItFromTheServerAndNotItsName(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	expectStatus(t, newAPIClient(t, core).call(http.MethodPost, "/workflows", resourceAnalyticsWorkflow("acme-payroll-run")), http.StatusCreated)

	event := resourceAnalyticsOnly(t, analytics.delivered(core), productanalytics.EventWorkflowCreated)
	resourceAnalyticsExpectServer(t, event)
	resourceAnalyticsExpectNoneOf(t, event, "acme")
}

func TestResourceAnalytics_ARefusedWorkflowCreateIsNotReported(t *testing.T) {
	analytics := newResourceAnalytics(t)
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	expectStatus(t, api.call(http.MethodPost, "/workflows", resourceAnalyticsWorkflow("repeatedly-applied-workflow")), http.StatusCreated)
	for range 2 {
		expectStatus(t, api.call(http.MethodPost, "/workflows", resourceAnalyticsWorkflow("repeatedly-applied-workflow")), http.StatusConflict)
	}

	if events := analytics.delivered(core); len(events) != 1 || events[0].Name != productanalytics.EventWorkflowCreated {
		t.Fatalf("three creates of one workflow reported %v, want one create", resourceAnalyticsNames(events))
	}
}

func resourceAnalyticsDeployments(api *entrypointAPI, collection, idField, id string, actions ...string) {
	api.t.Helper()
	for _, action := range actions {
		expectStatus(api.t, api.call(http.MethodPost, "/"+collection, map[string]any{idField: id, "action": action}), http.StatusCreated)
	}
}

func TestResourceAnalytics_AnAgentReportsOneDeployForItsLife(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, _ := newAWEAPI(t)
	created := createAgent(api, "deployable-agent")
	resourceAnalyticsDeployments(api, "agentDeployments", "agentId", created.ID, "deploy", "update", "undeploy", "deploy")

	if deploys := resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventAgentDeployed); len(deploys) != 1 {
		t.Fatalf("a deploy, an update, an undeploy and a redeploy reported %d deploys, want 1", len(deploys))
	}
}

func TestResourceAnalytics_AFailedAgentDeployIsNotADeploy(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, awe := newAWEAPI(t)
	created := createAgent(api, "failing-agent")
	awe.failAdds.Store(true)
	expectFailedDeployment[AgentDeployment](api, deployAgent(api, created.ID), msgAgentDeployFailed, "agentDeployments")

	if deploys := resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventAgentDeployed); len(deploys) != 0 {
		t.Fatalf("a failed deploy reported %d deploys, want none", len(deploys))
	}
}

func TestResourceAnalytics_AnAgentDeployAfterAFailedOneIsStillTheFirst(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, awe := newAWEAPI(t)
	created := createAgent(api, "retried-agent")
	awe.failAdds.Store(true)
	expectFailedDeployment[AgentDeployment](api, deployAgent(api, created.ID), msgAgentDeployFailed, "agentDeployments")
	awe.failAdds.Store(false)
	expectCreated[AgentDeployment](t, deployAgent(api, created.ID), "agentDeployments")

	if deploys := resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventAgentDeployed); len(deploys) != 1 {
		t.Fatalf("a failed deploy then a successful one reported %d deploys, want 1", len(deploys))
	}
}

func TestResourceAnalytics_AWorkflowReportsOneDeployForItsLife(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, _ := newAWEAPI(t)
	created := createWorkflow(api, "deployable-workflow")
	resourceAnalyticsDeployments(api, "workflowDeployments", "workflowId", created.ID, "deploy", "update", "undeploy", "deploy")

	if deploys := resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventWorkflowDeployed); len(deploys) != 1 {
		t.Fatalf("a workflow's whole deployment life reported %d deploys, want 1", len(deploys))
	}
}

func TestResourceAnalytics_AFailedWorkflowDeployIsNotADeploy(t *testing.T) {
	analytics := newResourceAnalytics(t)
	api, awe := newAWEAPI(t)
	created := createWorkflow(api, "failing-workflow")
	awe.failAdds.Store(true)
	expectFailedDeployment[WorkflowDeployment](api, deployWorkflow(api, created.ID), msgWorkflowDeployFailed, "workflowDeployments")

	if deploys := resourceAnalyticsNamed(analytics.delivered(api.core), productanalytics.EventWorkflowDeployed); len(deploys) != 0 {
		t.Fatalf("a failed workflow deploy reported %d deploys, want none", len(deploys))
	}
}

func TestResourceAnalytics_AnExperimentReportsFromTheServerAndNotItsNames(t *testing.T) {
	analytics := newResourceAnalytics(t)
	f := newWorkflowEvalsFixture(t, true)
	expectStatus(t, f.api.call(http.MethodPost, experimentsPath, f.body("nightly-regression", "some-agent")), http.StatusCreated)
	expectStatus(t, f.api.call(http.MethodPost, experimentsPath, f.workflowBody("order-flow-regression", "order_flow")), http.StatusCreated)

	targets := map[any]bool{}
	for _, event := range resourceAnalyticsNamed(analytics.delivered(f.api.core), productanalytics.EventEvalCreated) {
		resourceAnalyticsExpectServer(t, event)
		resourceAnalyticsExpectNoneOf(t, event, "some-agent", "nightly-regression", "order_flow", "order-flow-regression")
		targets[event.Properties[productanalytics.PropTargetType]] = true
	}
	if len(targets) != 2 || !targets[taxonomy.EvalTargetTypeAgent] || !targets[taxonomy.EvalTargetTypeWorkflow] {
		t.Errorf("experiments reported targets %v, want one agent and one workflow", targets)
	}
}
