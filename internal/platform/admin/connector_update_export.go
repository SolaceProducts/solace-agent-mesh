// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ConnectorRedeployUpdate struct {
	ID      string
	Request UpdateConnectorRequest
	UserID  string
	Read    *UpdateRead
}

func UpdateConnectorWithRedeploy(
	ctx context.Context,
	db *store.DB,
	svc *ConnectorService,
	update ConnectorRedeployUpdate,
	logger *slog.Logger,
) (*ConnectorResponse, error) {
	id, req, userID := update.ID, update.Request, update.UserID
	if db == nil {
		if logger != nil {
			logger.Warn("connector updated without a redeploy event; dependents keep the old config until redeployed",
				"resourceKind", "connector", "resourceId", id)
		}
		return svc.Update(ctx, id, req, userID)
	}

	h := &connectorRedeployer{
		db:           db,
		svc:          svc,
		agentRepo:    store.NewAgentRepo(db),
		workflowRepo: store.NewWorkflowRepo(db),
		outboxRepo:   store.NewOutboxRepo(db),
		logger:       logger,
	}

	agentIDs := h.findAgentsByConnector(ctx, id)
	workflowIDs := h.findWorkflowsByConnector(ctx, id)

	read, err := updateReadFor(ctx, svc, update)
	if err != nil {
		return nil, err
	}
	urlSpec, err := svc.urlSpecForUpdate(ctx, id, req, read.knownSpec, read.MCPManifest)
	if err != nil {
		return nil, err
	}

	var resp *ConnectorResponse
	err = db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var inErr error
		resp, inErr = svc.UpdateInTx(ctx, tx, id, req, userID, ConnectorUpdateOptions{URLSpec: urlSpec, MCPManifest: read.MCPManifest})
		if inErr != nil {
			return inErr
		}
		return h.writeRedeployEventInTx(ctx, tx, "connector.updated", id, userID, agentIDs, workflowIDs)
	})
	if err != nil {
		return nil, err
	}
	resp.Warnings = slices.Concat(read.Warnings, resp.Warnings)
	return resp, nil
}

type redeployUpdateRead struct {
	UpdateRead
	knownSpec *URLSpec
}

func updateReadFor(ctx context.Context, svc *ConnectorService, update ConnectorRedeployUpdate) (redeployUpdateRead, error) {
	if update.Read != nil {
		return redeployUpdateRead{UpdateRead: *update.Read, knownSpec: &update.Read.Spec}, nil
	}
	refresh, err := svc.readMCPManifest(ctx, update.ID, update.Request)
	if err != nil {
		return redeployUpdateRead{}, err
	}
	return redeployUpdateRead{UpdateRead: UpdateRead{MCPManifest: refresh.manifest, Warnings: refresh.warnings}}, nil
}
