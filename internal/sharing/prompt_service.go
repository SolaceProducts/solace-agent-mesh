// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sharing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type PromptService struct {
	storeProvider func() *store.DB
	audit         auth.AuditLogger
}

var _ entrypoint.PromptSharingService = (*PromptService)(nil)

func NewPromptService(storeProvider func() *store.DB, audit auth.AuditLogger) *PromptService {
	if audit == nil {
		audit = auth.NoopAuditLogger{}
	}
	return &PromptService{storeProvider: storeProvider, audit: audit}
}

func (s *PromptService) shareRepo() *store.ResourceAccessShareRepo {
	return store.NewResourceAccessShareRepo(s.storeProvider())
}

func (s *PromptService) withLockedTx(
	ctx context.Context,
	resourceID string,
	fn func(*dbutil.Tx, *store.ResourceAccessShareRepo) error,
) error {
	db := s.storeProvider()
	repo := store.NewResourceAccessShareRepo(db)
	return db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := repo.LockResourceTx(ctx, tx, resourceID, store.ResourceTypePromptGroup); err != nil {
			return fmt.Errorf("lock resource: %w", err)
		}
		return fn(tx, repo)
	})
}

func (s *PromptService) BatchShare(ctx context.Context, promptGroupID string, shares []entrypoint.ShareRequest, sharedBy string) (*entrypoint.BatchPromptShareResponse, error) {
	inputs := make([]store.ShareInput, len(shares))
	for i, sr := range shares {
		email := strings.ToLower(strings.TrimSpace(sr.UserEmail))
		if email == "" {
			return nil, fmt.Errorf("BatchShare: empty userEmail at shares[%d]", i)
		}
		accessLevel := sr.AccessLevel
		if accessLevel == "" {
			accessLevel = "resource_viewer"
		}
		inputs[i] = store.ShareInput{
			UserEmail:   email,
			AccessLevel: accessLevel,
		}
	}

	var (
		created   []store.ResourceAccessShare
		updated   []store.ResourceAccessShare
		prevLevel map[string]string
	)
	txErr := s.withLockedTx(ctx, promptGroupID, func(tx *dbutil.Tx, repo *store.ResourceAccessShareRepo) error {
		current, err := repo.GetCollaboratorsTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup)
		if err != nil {
			return fmt.Errorf("read collaborators: %w", err)
		}
		prevLevel = make(map[string]string, len(current))
		for _, c := range current {
			prevLevel[c.SharedWithUserEmail] = c.AccessLevel
		}
		created, updated, err = repo.BatchShareTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup, inputs, sharedBy)
		if err != nil {
			return fmt.Errorf("upsert collaborators: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("BatchShare: %w", txErr)
	}

	createdResp := make([]entrypoint.PromptShareResponse, 0, len(created))
	for _, c := range created {
		createdResp = append(createdResp, promptShareToResponse(c))
		s.emitGrant(ctx, sharedBy, promptGroupID, c.SharedWithUserEmail, c.AccessLevel)
	}
	updatedResp := make([]entrypoint.PromptShareResponse, 0, len(updated))
	for _, u := range updated {
		updatedResp = append(updatedResp, promptShareToResponse(u))
		if prevLevel[u.SharedWithUserEmail] == u.AccessLevel {
			continue
		}
		s.emitLevelChange(ctx, sharedBy, promptGroupID, u.SharedWithUserEmail, u.AccessLevel)
	}
	return &entrypoint.BatchPromptShareResponse{
		Created:        createdResp,
		Updated:        updatedResp,
		TotalProcessed: len(createdResp) + len(updatedResp),
	}, nil
}

func (s *PromptService) UpdateShare(ctx context.Context, promptGroupID, recipientEmail, accessLevel, actor string) (*entrypoint.PromptShareResponse, error) {
	email := strings.ToLower(strings.TrimSpace(recipientEmail))

	var updated *store.ResourceAccessShare
	txErr := s.withLockedTx(ctx, promptGroupID, func(tx *dbutil.Tx, repo *store.ResourceAccessShareRepo) error {
		row, err := repo.UpdateAccessLevelTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup, email, accessLevel)
		if err != nil {
			return err
		}
		updated = row
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("UpdateShare(%s/%s): %w", promptGroupID, email, txErr)
	}
	if updated == nil {
		return nil, entrypoint.ErrShareNotFound
	}
	s.emitLevelChange(ctx, actor, promptGroupID, email, accessLevel)
	resp := promptShareToResponse(*updated)
	return &resp, nil
}

func (s *PromptService) DeleteAllShares(ctx context.Context, promptGroupID, actor string) error {
	var emails []string
	txErr := s.withLockedTx(ctx, promptGroupID, func(tx *dbutil.Tx, repo *store.ResourceAccessShareRepo) error {
		recipients, err := repo.GetCollaboratorsTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup)
		if err != nil {
			return fmt.Errorf("read collaborators: %w", err)
		}
		if err := repo.DeleteAllForResourceTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup); err != nil {
			return fmt.Errorf("delete shares: %w", err)
		}
		emails = make([]string, 0, len(recipients))
		for _, r := range recipients {
			emails = append(emails, r.SharedWithUserEmail)
		}
		return nil
	})
	if txErr != nil {
		return fmt.Errorf("DeleteAllShares(%s): %w", promptGroupID, txErr)
	}
	if len(emails) == 0 {
		return nil
	}
	s.audit.LogTemplateDeleted(ctx, auth.TemplateDeletedEvent{
		Actor:           actor,
		ResourceType:    store.ResourceTypePromptGroup,
		ResourceID:      promptGroupID,
		RecipientEmails: emails,
	})
	return nil
}

func (s *PromptService) DeleteShare(ctx context.Context, promptGroupID, recipientEmail, actor string) error {
	email := strings.ToLower(strings.TrimSpace(recipientEmail))

	var count int
	txErr := s.withLockedTx(ctx, promptGroupID, func(tx *dbutil.Tx, repo *store.ResourceAccessShareRepo) error {
		n, _, err := repo.BatchUnshareTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup, []string{email})
		if err != nil {
			return err
		}
		count = n
		return nil
	})
	if txErr != nil {
		return fmt.Errorf("DeleteShare(%s/%s): %w", promptGroupID, email, txErr)
	}
	if count == 0 {
		return entrypoint.ErrShareNotFound
	}
	s.emitRevoke(ctx, actor, promptGroupID, email)
	return nil
}

func (s *PromptService) BulkReplaceShares(ctx context.Context, promptGroupID string, shares []entrypoint.ShareRequest, sharedBy string) (*entrypoint.PromptShareReplaceResult, error) {
	var (
		created       []store.ResourceAccessShare
		updated       []store.ResourceAccessShare
		revokedEmails []string
	)

	txErr := s.withLockedTx(ctx, promptGroupID, func(tx *dbutil.Tx, repo *store.ResourceAccessShareRepo) error {
		current, err := repo.GetCollaboratorsTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup)
		if err != nil {
			return fmt.Errorf("read current collaborators: %w", err)
		}
		currentByEmail := make(map[string]store.ResourceAccessShare, len(current))
		for _, c := range current {
			currentByEmail[c.SharedWithUserEmail] = c
		}

		inputEmails := make(map[string]struct{}, len(shares))
		upserts := make([]store.ShareInput, 0, len(shares))
		for i, sr := range shares {
			email := strings.ToLower(strings.TrimSpace(sr.UserEmail))
			if email == "" {
				return fmt.Errorf("empty userEmail at shares[%d]", i)
			}
			inputEmails[email] = struct{}{}
			accessLevel := sr.AccessLevel
			if accessLevel == "" {
				accessLevel = "resource_viewer"
			}
			if existing, ok := currentByEmail[email]; ok && existing.AccessLevel == accessLevel {
				continue
			}
			upserts = append(upserts, store.ShareInput{UserEmail: email, AccessLevel: accessLevel})
		}

		revokeEmails := make([]string, 0)
		for email := range currentByEmail {
			if _, ok := inputEmails[email]; !ok {
				revokeEmails = append(revokeEmails, email)
			}
		}

		if len(upserts) > 0 {
			created, updated, err = repo.BatchShareTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup, upserts, sharedBy)
			if err != nil {
				return fmt.Errorf("upsert collaborators: %w", err)
			}
		}
		if len(revokeEmails) > 0 {
			_, revokedEmails, err = repo.BatchUnshareTx(ctx, tx, promptGroupID, store.ResourceTypePromptGroup, revokeEmails)
			if err != nil {
				return fmt.Errorf("revoke collaborators: %w", err)
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("BulkReplaceShares: %w", txErr)
	}

	createdResp := make([]entrypoint.PromptShareResponse, 0, len(created))
	for _, c := range created {
		createdResp = append(createdResp, promptShareToResponse(c))
		s.emitGrant(ctx, sharedBy, promptGroupID, c.SharedWithUserEmail, c.AccessLevel)
	}
	updatedResp := make([]entrypoint.PromptShareResponse, 0, len(updated))
	for _, u := range updated {
		updatedResp = append(updatedResp, promptShareToResponse(u))
		s.emitLevelChange(ctx, sharedBy, promptGroupID, u.SharedWithUserEmail, u.AccessLevel)
	}
	if revokedEmails == nil {
		revokedEmails = []string{}
	}
	for _, email := range revokedEmails {
		s.emitRevoke(ctx, sharedBy, promptGroupID, email)
	}
	return &entrypoint.PromptShareReplaceResult{
		Created:       createdResp,
		Updated:       updatedResp,
		RevokedEmails: revokedEmails,
	}, nil
}

func (s *PromptService) GetShares(ctx context.Context, promptGroupID string) ([]entrypoint.PromptShareResponse, error) {
	collaborators, err := s.shareRepo().GetCollaborators(ctx, promptGroupID, store.ResourceTypePromptGroup)
	if err != nil {
		return nil, fmt.Errorf("GetShares: %w", err)
	}
	shares := make([]entrypoint.PromptShareResponse, 0, len(collaborators))
	for _, c := range collaborators {
		shares = append(shares, promptShareToResponse(c))
	}
	return shares, nil
}

const iso8601Format = "2006-01-02T15:04:05.000000+00:00"

func promptShareToResponse(s store.ResourceAccessShare) entrypoint.PromptShareResponse {
	createdAt := time.UnixMilli(s.SharedAt).UTC().Format(iso8601Format)
	updatedAt := createdAt
	if s.UpdatedAt != nil {
		updatedAt = time.UnixMilli(*s.UpdatedAt).UTC().Format(iso8601Format)
	}
	return entrypoint.PromptShareResponse{
		ID:            s.ID,
		UserEmail:     s.SharedWithUserEmail,
		AccessLevel:   s.AccessLevel,
		SharedByEmail: s.SharedByUserEmail,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
}

func (s *PromptService) emitGrant(ctx context.Context, actor, resourceID, recipientEmail, accessLevel string) {
	s.audit.LogShareGrant(ctx, auth.ShareGrantEvent{
		Actor:          actor,
		ResourceType:   store.ResourceTypePromptGroup,
		ResourceID:     resourceID,
		RecipientEmail: recipientEmail,
		AccessLevel:    accessLevel,
	})
}

func (s *PromptService) emitLevelChange(ctx context.Context, actor, resourceID, recipientEmail, accessLevel string) {
	s.audit.LogShareLevelChange(ctx, auth.ShareLevelChangeEvent{
		Actor:          actor,
		ResourceType:   store.ResourceTypePromptGroup,
		ResourceID:     resourceID,
		RecipientEmail: recipientEmail,
		AccessLevel:    accessLevel,
	})
}

func (s *PromptService) emitRevoke(ctx context.Context, actor, resourceID, recipientEmail string) {
	s.audit.LogShareRevoke(ctx, auth.ShareRevokeEvent{
		Actor:          actor,
		ResourceType:   store.ResourceTypePromptGroup,
		ResourceID:     resourceID,
		RecipientEmail: recipientEmail,
	})
}
