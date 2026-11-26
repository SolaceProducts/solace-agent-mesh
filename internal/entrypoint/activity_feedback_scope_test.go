// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestFeedbackScope_NilIdentityIsOwnData(t *testing.T) {
	got := resolveActivityFeedbackScope(nil, "u1", false)
	if got.ForcedUserID != "u1" {
		t.Fatalf("ForcedUserID = %q, want u1", got.ForcedUserID)
	}
}

func card(name, display string) a2a.AgentCard {
	c := a2a.AgentCard{Name: name}
	if display != "" {
		c.Capabilities.Extensions = []a2a.AgentExtension{{
			URI:    platpkg.ExtensionURIDisplayName,
			Params: map[string]any{"display_name": display},
		}}
	}
	return c
}

const (
	uuidA = "11111111-1111-1111-1111-111111111111"
	uuidB = "22222222-2222-2222-2222-222222222222"
	uuidW = "33333333-3333-3333-3333-333333333333"
)

func executorWithCards(cards ...a2a.AgentCard) *GatewayExecutor {
	reg := NewAgentRegistry()
	for _, c := range cards {
		reg.AddOrUpdate(c)
	}
	return &GatewayExecutor{gws: []*managedGateway{{core: &GatewayCore{agents: reg}}}}
}

func allVis() store.FeedbackVisibility {
	return store.FeedbackVisibility{Agents: store.SubjectScope{All: true}, Workflows: store.SubjectScope{All: true}}
}

func TestResolveSubjectNames_ResolvesByDisplayNameAndID(t *testing.T) {
	e := executorWithCards(
		card("agent_11111111_1111_1111_1111_111111111111", "HR Assistant"),
		card("workflow_33333333_3333_3333_3333_333333333333", "Onboarding"),
		card("PlainAgent", ""),
	)
	got := e.resolveSubjectNames([]string{"hr assistant", uuidW, "PlainAgent"}, allVis())
	if !reflect.DeepEqual(got.Agents, []string{uuidA, "PlainAgent"}) {
		t.Fatalf("Agents = %v", got.Agents)
	}
	if !reflect.DeepEqual(got.Workflows, []string{uuidW}) {
		t.Fatalf("Workflows = %v", got.Workflows)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("Candidates = %v", got.Candidates)
	}
}

func TestResolveSubjectNames_OutOfReachIsEmpty(t *testing.T) {
	e := executorWithCards(card("agent_11111111_1111_1111_1111_111111111111", "HR Assistant"))
	vis := store.FeedbackVisibility{Agents: store.SubjectScope{IDs: []string{uuidB}}}
	got := e.resolveSubjectNames([]string{"HR Assistant", uuidA}, vis)
	if len(got.Agents)+len(got.Workflows)+len(got.Candidates) != 0 {
		t.Fatalf("out-of-reach subject leaked: %+v", got)
	}
}

func TestResolveSubjectNames_DuplicateDisplayNameReturnsCandidates(t *testing.T) {
	e := executorWithCards(
		card("agent_11111111_1111_1111_1111_111111111111", "HR Assistant"),
		card("agent_22222222_2222_2222_2222_222222222222", "HR Assistant"),
	)
	got := e.resolveSubjectNames([]string{"HR Assistant"}, allVis())
	if len(got.Candidates) != 2 || len(got.Agents) != 0 {
		t.Fatalf("got %+v, want 2 candidates and no agents", got)
	}
	for _, c := range got.Candidates {
		if c.Name != "HR Assistant" || c.Type != "agent" || (c.ID != uuidA && c.ID != uuidB) {
			t.Fatalf("bad candidate %+v", c)
		}
	}
}

func TestResolveSubjectNames_DuplicateWithOneOutOfReach(t *testing.T) {
	e := executorWithCards(
		card("agent_11111111_1111_1111_1111_111111111111", "HR Assistant"),
		card("agent_22222222_2222_2222_2222_222222222222", "HR Assistant"),
	)
	vis := store.FeedbackVisibility{Agents: store.SubjectScope{IDs: []string{uuidB}}}
	got := e.resolveSubjectNames([]string{"HR Assistant"}, vis)
	if !reflect.DeepEqual(got.Agents, []string{uuidB}) || len(got.Candidates) != 0 {
		t.Fatalf("got %+v, want single id %s", got, uuidB)
	}
}
