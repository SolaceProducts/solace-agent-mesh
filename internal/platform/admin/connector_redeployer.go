// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type connectorRedeployer struct {
	db           *store.DB
	svc          *ConnectorService
	agentRepo    *store.AgentRepo
	workflowRepo *store.WorkflowRepo
	outboxRepo   *store.OutboxRepo
	logger       *slog.Logger
}

var connectorTypeFeatureKeys = map[string]string{
	"event_mesh":  "event_mesh_connector",
	"email":       "email_connector",
	"document_db": "document_db_connector",
	"graph_db":    "graph_db_connector",
	"search":      "search_connector",
	"web_search":  "web_search_connector",
}

func connectorSubtypeIsExperimental(typ, subtype string) bool {
	if typ == "event_mesh" || typ == "web_search" || (typ == "document_db" && subtype == "mongodb") {
		return false
	}
	_, ok := connectorTypeFeatureKeys[typ]
	return ok
}

const MsgAgentRegistrationsIncomplete = "agent registration status could not be fully retrieved; the list may be incomplete"

func (h *connectorRedeployer) findAgentsByConnector(ctx context.Context, connectorID string) []string {
	return findAgentsByConnector(ctx, h.agentRepo, h.logger, connectorID)
}

func (h *connectorRedeployer) findWorkflowsByConnector(ctx context.Context, connectorID string) []string {
	return findWorkflowsByConnector(ctx, h.workflowRepo, h.logger, connectorID)
}

func (h *connectorRedeployer) writeRedeployEventInTx(ctx context.Context, tx *dbutil.Tx, eventType, connectorID, userID string, agentIDs, workflowIDs []string) error {
	return writeRedeployEventInTx(ctx, h.outboxRepo, tx, redeployEvent{eventType, connectorID, userID, agentIDs, workflowIDs})
}

func contentTypeFromKey(key string) string {
	switch {
	case strings.HasSuffix(key, ".json"):
		return "application/json"
	case strings.HasSuffix(key, ".yaml"), strings.HasSuffix(key, ".yml"):
		return "application/x-yaml"
	default:
		return "application/octet-stream"
	}
}
