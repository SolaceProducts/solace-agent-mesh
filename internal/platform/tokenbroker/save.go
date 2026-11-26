// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var ErrGrantInUse = fmt.Errorf("OAuth grant is in use by a model: %w", control.ErrResourceConflict)

type InUseFunc func(ctx context.Context) (bool, error)

func (s *Service) Save(ctx context.Context, grant *store.ModelOAuthGrant, actor string, inUse InUseFunc) error {
	if err := validateNewGrant(grant, s.loopbackHTTP); err != nil {
		return err
	}
	grant.SignInFingerprint = llm.TokenFingerprint(grant.RefreshToken)
	err := s.save(ctx, grant, actor, inUse)
	if err != nil && s.alreadySaved(ctx, grant) {
		return nil
	}
	return err
}

func (s *Service) save(ctx context.Context, grant *store.ModelOAuthGrant, actor string, inUse InUseFunc) error {
	existing, err := s.Get(ctx, grant.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		return s.repo.Create(ctx, grant, actor)
	case err != nil:
		return fmt.Errorf("read OAuth grant %s: %w", grant.ID, err)
	case savedFrom(existing, grant):
		return nil
	case sameProviderAndAccount(existing, grant):
		return s.Replace(ctx, grant.ID, grant.AccountSubject, grant.AccountLabel, store.ModelOAuthGrantTokens{
			RefreshToken:           grant.RefreshToken,
			AccessToken:            grant.AccessToken,
			AccessTokenExpiresTime: grant.AccessTokenExpiresTime,
		}, actor)
	}
	used, err := inUse(ctx)
	if err != nil {
		return fmt.Errorf("check whether a model names OAuth grant %s: %w", grant.ID, err)
	}
	if used {
		return ErrGrantInUse
	}
	found, err := s.repo.Overwrite(ctx, grant, actor)
	if err != nil {
		return fmt.Errorf("overwrite OAuth grant %s: %w", grant.ID, err)
	}
	if !found {
		return s.repo.Create(ctx, grant, actor)
	}
	return nil
}

func (s *Service) alreadySaved(ctx context.Context, grant *store.ModelOAuthGrant) bool {
	row, err := s.repo.Get(ctx, grant.ID)
	return err == nil && row != nil && savedFrom(row, grant)
}

func savedFrom(stored, grant *store.ModelOAuthGrant) bool {
	return sameProviderAndAccount(stored, grant) && stored.SignInFingerprint == grant.SignInFingerprint
}

func sameProviderAndAccount(a, b *store.ModelOAuthGrant) bool {
	return a.Flow == b.Flow &&
		a.Issuer == b.Issuer &&
		a.TokenEndpoint == b.TokenEndpoint &&
		a.ClientID == b.ClientID &&
		a.Audience == b.Audience &&
		a.Scopes == b.Scopes &&
		a.APIBase == b.APIBase &&
		a.AccountSubject == b.AccountSubject
}
