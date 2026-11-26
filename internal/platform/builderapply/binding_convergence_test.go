// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestApply_TwoConnectorAgentConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	build := ApplyBuildRequest{Components: []BuildComponentInput{
		sqlConnector("zeta_db", "postgres", fullSQLValues()),
		mcpConnector("alpha_tools"),
		func() BuildComponentInput {
			a := agentComponent("two_connector_agent")
			a.Connectors = []string{"zeta_db", "alpha_tools"}
			return a
		}(),
	}}

	if _, err := svc.Apply(ctx, build, testActor); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := svc.Apply(ctx, build, testActor); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	resp, err := svc.Apply(ctx, build, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "two_connector_agent"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — nothing was edited", op.Action, op.ChangedFields)
	}

	if bound := agentNamed(ctx, t, svc, "two_connector_agent").Connectors; len(bound) != 2 {
		t.Errorf("connectors = %v, want both bindings", bound)
	}
}

func mcpConnector(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindConnector,
		Name: name,
		Content: `{"name":"` + name + `","description":"An MCP connector used in tests.",
		  "type":"mcp","subtype":"remote","values":{"server_url":"https://mcp.example.com"}}`,
	}
}

func TestApply_NameDivergentConnectorConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	connector := sqlConnector("my_conn_actual", "postgres", fullSQLValues())
	connector.Name = "MyConn"

	agent := agentComponent("binding_agent")
	agent.Connectors = []string{"MyConn"}

	build := ApplyBuildRequest{Components: []BuildComponentInput{connector, agent}}

	for pass := 1; pass <= 2; pass++ {
		if _, err := svc.Apply(ctx, build, testActor); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	resp, err := svc.Apply(ctx, build, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "binding_agent"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — the reference resolves to a stored id, so nothing differs",
			op.Action, op.ChangedFields)
	}
}

func TestApply_NameDivergentConnectorNeedsNoUpdateScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	values := fullSQLValues()
	values["password"] = "<<__SAM_REQUIRED__>>"
	connector := sqlConnector("my_conn_actual", "postgres", values)
	connector.Name = "MyConn"
	agent := agentComponent("binding_agent")
	agent.Connectors = []string{"MyConn"}
	build := ApplyBuildRequest{Components: []BuildComponentInput{connector, agent}}

	if _, err := svc.Apply(ctx, build, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	createOnly := identityWith(append([]string{
		rbac.ScopeConnectorCreate, rbac.ScopeAgentBuilderCreate,
	}, buildReadScopes...)...)
	resp, err := svc.Apply(ctx, build, createOnly)
	if err == nil {
		for _, op := range resp.Operations {
			if op.Status == statusFailed {
				t.Errorf("op %s/%s failed on a re-save that changes nothing: %+v", op.Kind, op.Name, op)
			}
		}
	}
	if err != nil {
		t.Errorf("a re-save that changes nothing demanded an update scope: %v", err)
	}
}

func TestApply_ConnectorAliasCannotHijackAnotherConnector(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
		sqlConnector("alpha_conn", "postgres", fullSQLValues()),
		mcpConnector("beta_conn"),
	}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	betaID := connectorNamed(t, svc, "beta_conn").ID

	hijack := sqlConnector("alpha_conn", "postgres", fullSQLValues())
	hijack.Name = "beta_conn"
	agent := agentComponent("bound_agent")
	agent.Connectors = []string{"beta_conn"}

	_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{hijack, agent}}, testActor)
	if err == nil {
		if bound := agentNamed(ctx, t, svc, "bound_agent").Connectors; len(bound) == 1 && bound[0] != betaID {
			t.Fatalf("agent bound %v, but the manifest said beta_conn (%s)", bound, betaID)
		}
		t.Fatal("an ambiguous connector name must be refused rather than resolved by guesswork")
	}
	if _, ok := err.(*entplatform.ValidationError); !ok {
		t.Errorf("err is %T, want *ValidationError naming the ambiguity", err)
	}
	if !strings.Contains(err.Error(), "beta_conn") {
		t.Errorf("err = %q, want it to name the ambiguous component", err)
	}
}

func TestApply_UnreferencedNameCoincidenceIsNotRefused(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
		sqlConnector("alpha_conn", "postgres", fullSQLValues()),
		mcpConnector("beta_conn"),
	}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	betaID := connectorNamed(t, svc, "beta_conn").ID

	coincidence := sqlConnector("alpha_conn", "postgres", fullSQLValues())
	coincidence.Name = "beta_conn"

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{coincidence, agentComponent("unbound_agent")},
	}, testActor); err != nil {
		t.Fatalf("an unreferenced name coincidence must not fail the build: %v", err)
	}

	if got := connectorNamed(t, svc, "beta_conn").ID; got != betaID {
		t.Errorf("beta_conn id = %q, want %q — the alias overwrote a live connector", got, betaID)
	}
}

func TestApply_A2AAuthRemovalLands(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const removed = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthType":"none","agentAuthConfig":{"type":"none"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: removed}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := storedAuthType(ctx, t, svc); got != "none" {
		t.Errorf("agentAuthType = %q, want none", got)
	}
	if got := storedAuthToken(ctx, t, svc); got != "" {
		t.Errorf("stored token = %q, want it removed — an explicit removal that leaves the credential at rest is worse than refusing", got)
	}
}

func TestApply_A2ABasicAuthConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const basic = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"svc","password":"<<__SAM_REQUIRED__>>"}}`
	comp := BuildComponentInput{Kind: kindA2AAgent, Name: "proxy", Content: basic}

	for pass := 1; pass <= 2; pass++ {
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindA2AAgent, "proxy"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — username is configuration, not a credential",
			op.Action, op.ChangedFields)
	}
}

func TestApply_A2AAuthTypeSwitchLands(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const switched = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"svc","password":"a-real-password"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: switched}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := storedAuthType(ctx, t, svc); got != "basic" {
		t.Errorf("agentAuthType = %q, want basic — the switch was pinned to the stored type", got)
	}
}
