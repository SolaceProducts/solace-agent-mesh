// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func (h *agentCardHandler) chatV2Routes() []apicontract.Route {
	return chatv2.AgentRoutes(chatv2.Deps{ListAgents: h.listChatAgents})
}

func (h *agentCardHandler) listChatAgents(ctx context.Context, req chatv2.ListAgentsRequest) (apicontract.List[chatv2.ChatAgent], error) {
	caller, _ := auth.SubjectFromContext(ctx)
	cards := h.visibleCards(ctx, caller)
	page := apicontract.PageOf(cards, req.Page)
	items := make([]chatv2.ChatAgent, 0, len(page.Items))
	for _, card := range page.Items {
		items = append(items, chatAgentOf(card))
	}
	return apicontract.List[chatv2.ChatAgent]{Items: items, Total: page.Total}, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sortedByDisplayName(cards []a2a.AgentCard) []a2a.AgentCard {
	slices.SortStableFunc(cards, func(a, b a2a.AgentCard) int {
		return cmp.Compare(strings.ToLower(platpkg.DisplayName(&a)), strings.ToLower(platpkg.DisplayName(&b)))
	})
	return cards
}

func chatAgentOf(card a2a.AgentCard) chatv2.ChatAgent {
	skills := make([]chatv2.ChatAgentSkill, 0, len(card.Skills))
	for _, s := range card.Skills {
		skills = append(skills, chatv2.ChatAgentSkill{Key: s.ID, Name: s.Name, Description: s.Description, Tags: nonNilList(s.Tags)})
	}
	return chatv2.ChatAgent{
		ID:          card.Name,
		Type:        "agent",
		DisplayName: platpkg.DisplayName(&card),
		Description: nilIfEmpty(card.Description),
		Version:     nilIfEmpty(card.Version),
		AgentType:   a2a.AgentTypeFromCard(card),
		Priority:    a2a.PriorityFromCard(card),
		Internal:    a2a.IsInternalCard(card),
		Skills:      skills,
	}
}
