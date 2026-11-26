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

func TestApply_PreservesAdditionalConfigurations(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("keeper_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	idx := slices.IndexFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == "keeper_agent" })
	if idx < 0 {
		t.Fatal("keeper_agent was not created")
	}
	seeded := agents[idx]

	addl := json.RawMessage(`{"supports_streaming":true}`)
	skillIDs := seeded.SkillIDs
	if _, seedErr := svc.deps.Agents.Update(ctx, seeded.ID, entplatform.UpdateAgentRequest{
		Name:                     seeded.Name,
		Description:              seeded.Description,
		SystemPrompt:             seeded.SystemPrompt,
		SkillIDs:                 &skillIDs,
		Toolsets:                 seeded.Toolsets,
		AdditionalConfigurations: addl,
	}, "config-apply"); seedErr != nil {
		t.Fatalf("seed additionalConfigurations: %v", seedErr)
	}

	edited := agentComponent("keeper_agent")
	edited.Content = strings.Replace(edited.Content,
		"instruction: You are a test agent that answers questions.",
		"instruction: You are a test agent with a freshly edited instruction.", 1)

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "keeper_agent"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v", op)
	}

	after, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	i := slices.IndexFunc(after, func(a entplatform.AgentResponse) bool { return a.Name == "keeper_agent" })
	if len(after[i].AdditionalConfigurations) == 0 {
		t.Fatal("additionalConfigurations was destroyed by the update")
	}
	if !strings.Contains(string(after[i].AdditionalConfigurations), "supports_streaming") {
		t.Errorf("additionalConfigurations = %s, want the seeded runtime config intact", after[i].AdditionalConfigurations)
	}
}

func TestApply_PreservesRemoteAgentCredential(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const withAuth = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"bearer","token":"super-secret"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: withAuth}},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	before, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	if len(before) != 1 || before[0].AgentAuthType != "bearer" {
		t.Fatalf("seed did not store a bearer credential: %+v", before)
	}

	const noAuth = `{"name":"proxy","description":"A remote agent with an edited description.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known"}`

	if _, applyErr := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: noAuth}},
	}, testActor); applyErr != nil {
		t.Fatalf("second Apply: %v", applyErr)
	}

	after, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	if after[0].AgentAuthType != "bearer" {
		t.Errorf("AgentAuthType = %q, want bearer — the stored credential was stripped", after[0].AgentAuthType)
	}
}

func TestSummarize_DoesNotCountUnwrittenOperations(t *testing.T) {
	t.Parallel()
	got := summarize([]BuildOperationResult{
		{Action: actionCreate, Status: statusSucceeded},
		{Action: actionCreate, Status: statusFailed},
		{Action: actionCreate, Status: statusSkipped},
		{Action: actionUpdate, Status: statusSkipped},
		{Action: actionUnchanged, Status: statusSucceeded},
	})
	want := BuildApplySummary{Created: 1, Updated: 0, Unchanged: 1, Failed: 1, Skipped: 2}
	if got != want {
		t.Errorf("summarize = %+v, want %+v", got, want)
	}
}

func TestResolveToolsetBindings_IsSortedForHashStability(t *testing.T) {
	t.Parallel()
	cat := newToolsetCatalog()
	toolsets, _, err := resolveToolsetBindings([]string{"web_tools", "artifact_management"}, nil, cat)
	if err != nil {
		t.Fatalf("resolveToolsetBindings: %v", err)
	}
	if !slices.IsSorted(toolsets) {
		t.Errorf("toolsets = %v, want sorted", toolsets)
	}
}
