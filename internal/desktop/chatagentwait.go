// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func listsChatAgent(cards []a2a.AgentCard) bool {
	for _, card := range cards {
		if !a2a.IsInternalCard(card) && a2a.AgentTypeFromCard(card) != "workflow" {
			return true
		}
	}
	return false
}

func chatPageCanOpen(ctx context.Context, client *http.Client, agentCardsURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentCardsURL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusOK:
	default:
		return false
	}
	var list struct {
		Data []a2a.AgentCard `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return false
	}
	return listsChatAgent(list.Data)
}

func awaitChatAgent(ctx context.Context, client *http.Client, agentCardsURL string, limit, poll time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if chatPageCanOpen(ctx, client, agentCardsURL) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
