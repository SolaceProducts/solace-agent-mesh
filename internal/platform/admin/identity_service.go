// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type IdentityConfigService struct {
	repo    *store.PlatformIdentityConfigRepo
	pkgRepo *store.ToolPackageRepo
	logger  *slog.Logger
}

func NewIdentityConfigService(repo *store.PlatformIdentityConfigRepo, pkgRepo *store.ToolPackageRepo, logger *slog.Logger) *IdentityConfigService {
	if logger == nil {
		logger = slog.Default()
	}
	return &IdentityConfigService{
		repo:    repo,
		pkgRepo: pkgRepo,
		logger:  logger.With("logger_name", "identity-config"),
	}
}

const requiredProfileProviderTool = "enrich_authenticated_user"

const profileProviderCapabilityKey = "profileProvider"

const defaultLookupKey = "email"

func (s *IdentityConfigService) Get(ctx context.Context) (*ProfileProviderResponse, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get identity config: %w", err)
	}
	if cfg == nil {
		return &ProfileProviderResponse{LookupKey: defaultLookupKey}, nil
	}
	return toProfileProviderResponse(cfg), nil
}

func (s *IdentityConfigService) Set(ctx context.Context, req UpdateProfileProviderRequest, actor string) (*ProfileProviderResponse, error) {
	if req.ToolsetID == "" {
		return nil, &ValidationError{
			Message: "toolsetId is required",
			Details: map[string][]string{"toolsetId": {"toolsetId is required"}},
		}
	}

	pkg, err := s.pkgRepo.GetByID(ctx, req.ToolsetID)
	if err != nil {
		return nil, fmt.Errorf("load toolset %s: %w", req.ToolsetID, err)
	}
	if pkg == nil {
		return nil, &ValidationError{
			Message: "toolset not found",
			Details: map[string][]string{"toolsetId": {"toolset not found"}},
		}
	}
	if pkg.DiscoveryStatus != "ready" {
		return nil, &ValidationError{
			Message: "toolset not yet processed by STR; try again shortly",
			Details: map[string][]string{"toolsetId": {fmt.Sprintf("toolset discovery status is %q; expected \"ready\"", pkg.DiscoveryStatus)}},
		}
	}

	caps := decodeCapabilities(pkg.Capabilities)
	if v, _ := caps[profileProviderCapabilityKey].(bool); !v {
		return nil, &ValidationError{
			Message: "toolset does not declare profileProvider capability",
			Details: map[string][]string{"toolsetId": {"toolset must declare capabilities.profileProvider: true"}},
		}
	}

	files, err := s.pkgRepo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		return nil, fmt.Errorf("load toolset files for %s: %w", req.ToolsetID, err)
	}
	if !hasTool(files, requiredProfileProviderTool) {
		return nil, &ValidationError{
			Message: "toolset is missing the required enrich_authenticated_user tool",
			Details: map[string][]string{"toolsetId": {fmt.Sprintf("toolset must expose the %q tool", requiredProfileProviderTool)}},
		}
	}

	lookupKey := req.LookupKey
	if lookupKey == "" {
		existing, err := s.repo.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("load existing identity config: %w", err)
		}
		if existing != nil && existing.LookupKey != "" {
			lookupKey = existing.LookupKey
		} else {
			lookupKey = defaultLookupKey
		}
	}

	toolsetID := pkg.ID
	if err := s.repo.Set(ctx, &toolsetID, lookupKey, actor); err != nil {
		return nil, fmt.Errorf("persist identity config: %w", err)
	}

	s.audit(ctx, "set", actor, pkg.ID, caps, true, "")

	return s.Get(ctx)
}

func (s *IdentityConfigService) Clear(ctx context.Context, actor string) (*ProfileProviderResponse, error) {
	prior, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("load identity config: %w", err)
	}
	if err := s.repo.Clear(ctx, actor); err != nil {
		return nil, fmt.Errorf("clear identity config: %w", err)
	}
	priorID := ""
	if prior != nil && prior.ToolsetID != nil {
		priorID = *prior.ToolsetID
	}
	s.audit(ctx, "clear", actor, priorID, nil, true, "")
	return s.Get(ctx)
}

func (s *IdentityConfigService) audit(_ context.Context, action, actor, toolsetID string, capabilitiesSnapshot map[string]any, accepted bool, reason string) {
	outcome := "accepted"
	if !accepted {
		outcome = "rejected"
	}
	attrs := []any{
		"event", "platform_identity_config_mutation",
		"action", action,
		"actor", actor,
		"toolsetId", toolsetID,
		"outcome", outcome,
	}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	if capabilitiesSnapshot != nil {
		if b, err := json.Marshal(capabilitiesSnapshot); err == nil {
			attrs = append(attrs, "capabilitiesSnapshot", truncateForAudit(string(b)))
		}
	}
	s.logger.Info("identity config audit", attrs...)
}

const auditCapabilitiesSnapshotLimit = 4096

func truncateForAudit(s string) string {
	if len(s) <= auditCapabilitiesSnapshotLimit {
		return s
	}
	return s[:auditCapabilitiesSnapshotLimit] + "...(truncated)"
}

func decodeCapabilities(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func hasTool(files []store.ToolPackageFile, name string) bool {
	for _, f := range files {
		if f.ToolName == name {
			return true
		}
	}
	return false
}

func toProfileProviderResponse(c *store.PlatformIdentityConfig) *ProfileProviderResponse {
	return &ProfileProviderResponse{
		ToolsetID: c.ToolsetID,
		LookupKey: c.LookupKey,
		CreatedBy: c.CreatedBy,
		UpdatedBy: c.UpdatedBy,
		CreatedAt: Timestamp(c.CreatedTime),
		UpdatedAt: Timestamp(c.UpdatedTime),
	}
}
