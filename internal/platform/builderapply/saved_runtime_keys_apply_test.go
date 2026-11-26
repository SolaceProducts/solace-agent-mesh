// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func agentComponentWithKey(t *testing.T, name, line string) BuildComponentInput {
	t.Helper()
	c := agentComponent(name)
	edited := strings.Replace(c.Content, "      tools:\n", "      "+line+"\n      tools:\n", 1)
	if edited == c.Content {
		t.Fatalf("fixture has no tools: line to insert %q before", line)
	}
	c.Content = edited
	return c
}

func seedStoredRuntimeConfig(t *testing.T, svc *Service, name string, additional json.RawMessage) {
	t.Helper()
	ctx := t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent(name)}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	idx := slices.IndexFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == name })
	if idx < 0 {
		t.Fatalf("%s was not created", name)
	}
	seeded := agents[idx]
	skillIDs := seeded.SkillIDs
	if _, err := svc.deps.Agents.Update(ctx, seeded.ID, entplatform.UpdateAgentRequest{
		Name:                     seeded.Name,
		Description:              seeded.Description,
		SystemPrompt:             seeded.SystemPrompt,
		SkillIDs:                 &skillIDs,
		Toolsets:                 seeded.Toolsets,
		AdditionalConfigurations: additional,
	}, "config-apply"); err != nil {
		t.Fatalf("seed additionalConfigurations: %v", err)
	}
}
