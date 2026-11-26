// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sharing

import (
	"context"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type Service struct {
	storeProvider func() *store.DB
}

func NewService(storeProvider func() *store.DB) *Service {
	return &Service{storeProvider: storeProvider}
}

func (s *Service) shareRepo() *store.ResourceAccessShareRepo {
	return store.NewResourceAccessShareRepo(s.storeProvider())
}

func (s *Service) projectRepo() *store.ProjectRepo {
	return store.NewProjectRepo(s.storeProvider())
}

var _ entrypoint.ProjectSharingService = (*Service)(nil)

func (s *Service) GetSharedProjectIDs(ctx context.Context, userID string) ([]string, error) {
	return s.shareRepo().GetSharedResourceIDs(ctx, userID, "project")
}

func (s *Service) GetProjectShares(ctx context.Context, projectID string) (*entrypoint.ProjectSharesResponse, error) {
	project, err := s.projectRepo().GetByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	ownerEmail := ""
	if project != nil {
		ownerEmail = project.UserID
	}

	collaborators, err := s.shareRepo().GetCollaborators(ctx, projectID, "project")
	if err != nil {
		return nil, err
	}

	shares := make([]entrypoint.ShareResponse, 0, len(collaborators))
	for _, c := range collaborators {
		shares = append(shares, shareToResponse(c))
	}

	return &entrypoint.ProjectSharesResponse{
		ProjectID:  projectID,
		OwnerEmail: ownerEmail,
		Shares:     shares,
	}, nil
}

func (s *Service) BatchShare(ctx context.Context, projectID string, shares []entrypoint.ShareRequest, sharedBy string) (*entrypoint.BatchShareResponse, error) {
	inputs := make([]store.ShareInput, len(shares))
	for i, sr := range shares {
		accessLevel := sr.AccessLevel
		if accessLevel == "" {
			accessLevel = "resource_viewer"
		}
		inputs[i] = store.ShareInput{
			UserEmail:   strings.ToLower(sr.UserEmail),
			AccessLevel: accessLevel,
		}
	}

	created, updated, err := s.shareRepo().BatchShare(ctx, projectID, "project", inputs, sharedBy)
	if err != nil {
		return nil, err
	}

	createdResp := make([]entrypoint.ShareResponse, 0, len(created))
	for _, c := range created {
		createdResp = append(createdResp, shareToResponse(c))
	}
	updatedResp := make([]entrypoint.ShareResponse, 0, len(updated))
	for _, u := range updated {
		updatedResp = append(updatedResp, shareToResponse(u))
	}

	return &entrypoint.BatchShareResponse{
		ProjectID:      projectID,
		Created:        createdResp,
		Updated:        updatedResp,
		TotalProcessed: len(shares),
	}, nil
}

func (s *Service) BatchUnshare(ctx context.Context, projectID string, userEmails []string) (*entrypoint.BatchDeleteResponse, error) {
	count, deletedEmails, err := s.shareRepo().BatchUnshare(ctx, projectID, "project", userEmails)
	if err != nil {
		return nil, err
	}

	if len(deletedEmails) > 0 {
		_ = s.projectRepo().SoftDeleteSessionsByProjectForUsers(ctx, projectID, deletedEmails)
	}

	if deletedEmails == nil {
		deletedEmails = []string{}
	}

	return &entrypoint.BatchDeleteResponse{
		ProjectID:     projectID,
		DeletedCount:  count,
		DeletedEmails: deletedEmails,
	}, nil
}

func (s *Service) DeleteAllShares(ctx context.Context, projectID string) error {
	return s.shareRepo().DeleteAllForResource(ctx, projectID, "project")
}

func (s *Service) HasViewAccess(ctx context.Context, projectID, userID string) (bool, error) {
	return s.shareRepo().HasAccess(ctx, projectID, "project", userID)
}

func shareToResponse(s store.ResourceAccessShare) entrypoint.ShareResponse {
	createdTime := time.UnixMilli(s.SharedAt).UTC().Format(time.RFC3339)
	updatedTime := createdTime
	if s.UpdatedAt != nil {
		updatedTime = time.UnixMilli(*s.UpdatedAt).UTC().Format(time.RFC3339)
	}
	return entrypoint.ShareResponse{
		ID:            s.ID,
		ProjectID:     s.ResourceID,
		UserEmail:     s.SharedWithUserEmail,
		AccessLevel:   s.AccessLevel,
		SharedByEmail: s.SharedByUserEmail,
		CreatedTime:   createdTime,
		UpdatedTime:   updatedTime,
	}
}
