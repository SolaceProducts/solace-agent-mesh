// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

type Service struct {
	cfg         ConfigSource
	scopes      ScopeResolver
	enricher    Enricher
	logger      *slog.Logger
	scopeCache  *ttlCache[[]string]
	profCache   *ttlCache[*Profile]
	searchCache *ttlCache[[]*Profile]
	availCache  *ttlCache[bool]
}

func New(cfg ConfigSource, scopes ScopeResolver, enricher Enricher, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:         cfg,
		scopes:      scopes,
		enricher:    enricher,
		logger:      logger.With("logger_name", "identity-service"),
		scopeCache:  newTTLCache[[]string](scopeCacheTTL, 0),
		profCache:   newTTLCache[*Profile](profileCacheTTL, coldCacheWait),
		searchCache: newTTLCache[[]*Profile](searchCacheTTL, 0),
		availCache:  newTTLCache[bool](searchConfiguredCacheTTL, 0),
	}
}

func (s *Service) Resolve(ctx context.Context, claims map[string]any) (*Resolution, error) {
	res := &Resolution{}

	scopes, err := s.resolveScopes(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("identity: resolve scopes: %w", err)
	}
	res.Scopes = scopes

	toolsetID, lookupKey, configured, err := s.cfg.Get(ctx)
	if err != nil {
		sub, _ := claims["sub"].(string)
		s.logger.Warn("identity config read failed; treating as unconfigured",
			"sub", sub, "error", err)
		res.EnrichmentStatus = EnrichmentUnavailable
		res.EnrichmentReason = "identity config unavailable"
		return res, nil
	}
	if !configured || s.enricher == nil {
		res.EnrichmentStatus = EnrichmentNotConfigured
		return res, nil
	}

	lookupValue := extractLookupValue(claims, lookupKey)
	if lookupValue == "" {
		res.EnrichmentStatus = EnrichmentUnavailable
		res.EnrichmentReason = fmt.Sprintf("claim %q absent or empty", lookupKey)
		return res, nil
	}

	profile, err := s.profCache.getOrLoadCold(ctx, lookupValue, func(ctx context.Context) (*Profile, error) {
		return s.enricher.EnrichAuthenticatedUser(ctx, lookupValue)
	})
	if err != nil {
		switch {
		case errors.Is(err, errColdCacheTimeout):
			res.EnrichmentStatus = EnrichmentUnavailable
			res.EnrichmentReason = "profile lookup timed out"
		default:
			res.EnrichmentStatus = EnrichmentUnavailable
			res.EnrichmentReason = err.Error()
		}
		s.logger.Warn("profile enrichment failed; proceeding without profile data (RBAC scopes unaffected)",
			"lookupKey", lookupKey,
			"toolsetId", toolsetID,
			"error", err,
		)
		return res, nil
	}
	res.Profile = profile
	res.EnrichmentStatus = EnrichmentOK
	return res, nil
}

func (s *Service) resolveScopes(ctx context.Context, claims map[string]any) ([]string, error) {
	if s.scopes == nil {
		return nil, nil
	}
	key := scopeCacheKey(claims)
	if key == "" {
		return s.scopes.Resolve(ctx, claims)
	}
	return s.scopeCache.getOrLoad(ctx, key, func(ctx context.Context) ([]string, error) {
		return s.scopes.Resolve(ctx, claims)
	})
}

func (s *Service) SearchUsers(ctx context.Context, query string, limit int) ([]*Profile, error) {
	if s.enricher == nil {
		return []*Profile{}, nil
	}
	key := fmt.Sprintf("%s|%d", strings.ToLower(strings.TrimSpace(query)), limit)
	return s.searchCache.getOrLoad(ctx, key, func(ctx context.Context) ([]*Profile, error) {
		return s.enricher.SearchUsers(ctx, query, limit)
	})
}

const searchUsersTool = "search_users"

type toolPresenceChecker interface {
	HasTool(ctx context.Context, toolsetID, bareName string) (bool, error)
}

func (s *Service) SearchConfigured(ctx context.Context) (bool, error) {
	return s.availCache.getOrLoad(ctx, "", s.loadSearchConfigured)
}

func (s *Service) loadSearchConfigured(ctx context.Context) (bool, error) {
	toolsetID, _, configured, err := s.cfg.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("identity: read config: %w", err)
	}
	if !configured || s.enricher == nil {
		return false, nil
	}
	checker, ok := s.enricher.(toolPresenceChecker)
	if !ok {
		return true, nil
	}
	return checker.HasTool(ctx, toolsetID, searchUsersTool)
}

func (s *Service) GetUserProfile(ctx context.Context, userID string) (map[string]any, error) {
	if s.enricher == nil {
		return nil, nil
	}
	return s.enricher.GetUserProfile(ctx, userID)
}

func (s *Service) GetTimeOff(ctx context.Context, userID, start, end string) ([]TimeOffEntry, error) {
	if s.enricher == nil {
		return nil, nil
	}
	return s.enricher.GetTimeOff(ctx, userID, start, end)
}

func (s *Service) GetUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error) {
	if s.enricher == nil {
		return nil, "", nil
	}
	return s.enricher.GetUserProfilePicture(ctx, userID)
}

func extractLookupValue(claims map[string]any, lookupKey string) string {
	if lookupKey == "" || claims == nil {
		return ""
	}
	raw, ok := claims[lookupKey]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func scopeCacheKey(claims map[string]any) string {
	for _, k := range []string{"sub", "user_id", "userId", "email"} {
		if v, ok := claims[k].(string); ok && v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

var _ IdentityService = (*Service)(nil)
