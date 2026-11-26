// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type RepoConfigSource struct {
	repo identityConfigLookup
}

func NewRepoConfigSource(repo *store.PlatformIdentityConfigRepo) *RepoConfigSource {
	return &RepoConfigSource{repo: repo}
}

func (s *RepoConfigSource) Get(ctx context.Context) (string, string, bool, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		return "", "", false, fmt.Errorf("read platform_identity_config: %w", err)
	}
	if cfg == nil || cfg.ToolsetID == nil || *cfg.ToolsetID == "" {
		return "", "", false, nil
	}
	return *cfg.ToolsetID, cfg.LookupKey, true, nil
}

var _ ConfigSource = (*RepoConfigSource)(nil)
