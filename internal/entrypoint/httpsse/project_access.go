// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const projectNotFoundMsg = "project not found"

func resolveProjectOwner(
	ctx context.Context,
	projectRepo *store.ProjectRepo,
	sharing entrypoint.ProjectSharingService,
	projectID, callerUserID string,
) (ownerID string, allowed bool, err error) {
	if projectRepo == nil {
		return callerUserID, true, nil
	}
	project, err := projectRepo.GetByID(ctx, projectID)
	if err != nil {
		return "", false, err
	}
	if project == nil {
		return "", false, nil
	}
	if artifact.SameOwner(project.UserID, callerUserID) {
		return project.UserID, true, nil
	}
	if sharing != nil {
		hasAccess, accessErr := sharing.HasViewAccess(ctx, projectID, callerUserID)
		if accessErr != nil {
			return "", false, accessErr
		}
		if hasAccess {
			return project.UserID, true, nil
		}
	}
	return "", false, nil
}
