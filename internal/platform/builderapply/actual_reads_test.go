// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestActualProtos_AgentCarriesItsSkillConfigs(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("configured_skills_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	agent := agentNamed(ctx, t, svc, "configured_skills_agent")
	skillID := seedSkill(ctx, t, svc, "credential_bearing_skill")

	stored := `{"cfg_tool":{"api_token":"live-secret-token"}}`
	repo := store.NewSkillRepo(svc.deps.DB)
	if err := repo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{
		{AgentID: agent.ID, SkillID: skillID, ConfigValues: &stored},
	}); err != nil {
		t.Fatalf("seed skill config: %v", err)
	}

	protos, err := svc.actualProtos(ctx, kindAgent, map[string]struct{}{"configured_skills_agent": {}}, nil, nil, disregardedIDs{})
	if err != nil {
		t.Fatalf("actualProtos: %v", err)
	}
	if len(protos) != 1 {
		t.Fatalf("read %d agents, want the one named", len(protos))
	}
	got, isAgent := protos[0].raw.(entplatform.AgentResponse)
	if !isAgent {
		t.Fatalf("raw is %T, want AgentResponse", protos[0].raw)
	}
	if len(got.SkillConfigs) == 0 {
		t.Fatal("the agent read for the overlay carries no skillConfigs.\n" +
			"The overlay writes what it reads through a total-replace update, so an empty read here deletes every per-agent skill config on the next save — credentials included.")
	}
	if !strings.Contains(mustJSON(t, got.SkillConfigs), "live-secret-token") {
		t.Errorf("skillConfigs = %s, want the stored value", mustJSON(t, got.SkillConfigs))
	}
}

func TestListAll_ReadsPastTheFirstPage(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const count = 120
	for i := range count {
		if _, err := svc.deps.Connectors.Create(ctx, entplatform.CreateConnectorRequest{
			Name:        connectorProbeName(i),
			Description: "A connector seeded to push the listing past one page.",
			Type:        "sql",
			Subtype:     "postgres",
			Values:      map[string]any{"hostname": "db"},
		}, "page-probe"); err != nil {
			t.Fatalf("seed connector %d: %v", i, err)
		}
	}

	all, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if len(all) != count {
		t.Fatalf("read %d of %d connectors — a resource the listing does not reach plans as a create, and the create then collides with the row that was there all along", len(all), count)
	}

	last := connectorProbeName(count - 1)
	protos, err := svc.actualProtos(ctx, kindConnector, map[string]struct{}{last: {}}, nil, nil, disregardedIDs{})
	if err != nil {
		t.Fatalf("actualProtos: %v", err)
	}
	if len(protos) != 1 {
		t.Fatalf("actualProtos found %d resources named %q, want 1", len(protos), last)
	}
}

func connectorProbeName(i int) string {
	return "page_probe_connector_" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

func TestExecute_ConnectorUpdateRedeploysItsConsumers(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	consumer := agentComponent("connector_consumer")
	consumer.Connectors = []string{"rotating"}
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("rotating", "postgres", fullSQLValues()), consumer},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	bound := agentNamed(ctx, t, svc, "connector_consumer")
	if len(bound.Connectors) == 0 {
		t.Fatalf("the consumer is not bound to the connector, so no redeploy could be expected of it")
	}

	rotated := fullSQLValues()
	rotated["password"] = "rotated-password"
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("rotating", "postgres", rotated)},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "rotating"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want the rotation applied", op)
	}
	events, err := store.NewOutboxRepo(svc.deps.DB).GetDispatchable(ctx, 50)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if !slices.ContainsFunc(events, func(e store.OutboxEvent) bool { return e.EventType == "connector.updated" }) {
		types := make([]string, 0, len(events))
		for _, e := range events {
			types = append(types, e.EventType)
		}
		t.Errorf("outbox holds %v, want a connector.updated redeploy event.\n"+
			"Without it the connector row is rewritten and nothing else happens: every agent bound to it keeps running the credentials it started with, so a rotation reports success while the revoked secret stays live in every consumer.", types)
	}
}
