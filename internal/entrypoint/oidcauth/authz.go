// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type RoleProvider interface {
	GetUserRoles(userID string, gatewayContext map[string]any) ([]string, error)
}

type AuthorizationService struct {
	serviceType  string
	roleEngine   *rbac.RoleEngine
	roleProvider RoleProvider
	yamlUsers    rbac.UsersConfig
	emailFold    *rbac.EmailFoldIndex
	defaultRoles []string
	gatewayID    string
	client       auth.AuthorizationClient
	logger       *slog.Logger
}

func NewAuthorizationService(cfg AuthorizationServiceConfig, gatewayID string, logger *slog.Logger) (*AuthorizationService, error) {
	svc := &AuthorizationService{
		serviceType:  cfg.Type,
		defaultRoles: cfg.DefaultRoles,
		gatewayID:    gatewayID,
		logger:       logger.With("logger_name", "authz-service"),
	}

	switch cfg.Type {
	case "deny_all", "":
		svc.serviceType = "deny_all"
		svc.logger.Info("authorization service: deny_all (no scopes granted)")
		return svc, nil

	case "none":
		svc.logger.Info("authorization service: none (all access granted)")
		return svc, nil

	case "default_rbac":
		return svc.initRBAC(cfg)

	default:
		return nil, fmt.Errorf("unknown authorization service type: %q", cfg.Type)
	}
}

func (s *AuthorizationService) initRBAC(cfg AuthorizationServiceConfig) (*AuthorizationService, error) {
	if cfg.RoleToScopeDefinitionsPath == "" {
		s.logger.Debug("authorization service: default_rbac (broker-only mode; awaiting AuthorizationClient wiring)")
		return s, nil
	}
	rolesCfg, err := rbac.LoadRolesFromFile(cfg.RoleToScopeDefinitionsPath)
	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}

	var usersCfg rbac.UsersConfig
	if cfg.UserToRoleAssignmentsPath != "" {
		usersCfg, err = rbac.LoadUsersFromFile(cfg.UserToRoleAssignmentsPath, s.logger)
		if err != nil {
			return nil, fmt.Errorf("load users: %w", err)
		}
	}
	embRoles, embUsers, err := rbac.EmbeddedDefaults()
	if err != nil {
		return nil, fmt.Errorf("rbac embedded defaults: %w", err)
	}
	for name, def := range rolesCfg.Roles {
		embRoles.Roles[name] = def
	}
	rolesCfg = embRoles
	for id, assignment := range usersCfg.Users {
		embUsers.Users[id] = assignment
	}
	usersCfg = embUsers
	s.yamlUsers = usersCfg
	s.emailFold = rbac.BuildEmailFoldIndex(usersCfg.Users)

	engine, err := rbac.NewRoleEngine(rolesCfg, rbac.UsersConfig{}, s.defaultRoles)
	if err != nil {
		return nil, fmt.Errorf("create role engine: %w", err)
	}
	s.roleEngine = engine

	switch cfg.UserToRoleProvider {
	case "":
	case "idp_claims":
		if cfg.IDPClaimsConfig == nil {
			return nil, fmt.Errorf("default_rbac: user_to_role_provider %q requires 'idp_claims_config'", cfg.UserToRoleProvider)
		}
		s.roleProvider = NewIdpClaimsProvider(*cfg.IDPClaimsConfig)
		s.logger.Debug("role provider: idp_claims",
			"oidcProvider", cfg.IDPClaimsConfig.OIDCProvider,
			"claimKey", cfg.IDPClaimsConfig.ClaimKey,
		)
	default:
		return nil, fmt.Errorf("default_rbac: user_to_role_provider %q is not supported (supported: idp_claims)", cfg.UserToRoleProvider)
	}

	s.logger.Debug("authorization service: default_rbac",
		"rolesCount", len(rolesCfg.Roles),
		"yamlUsersCount", len(usersCfg.Users),
		"defaultRoles", s.defaultRoles,
	)

	return s, nil
}

func (s *AuthorizationService) SetAuthorizationClient(client auth.AuthorizationClient) {
	s.client = client
}

type LookupResult struct {
	Roles            []string
	Scopes           []string
	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string

	Resolution     identity.LookupResolution
	ResolvedUserID string
	RefusalReason  string
	RolesSource    identity.RolesSource

	SystemUserID  string
	SystemUserRow auth.SystemUserRow
}

func (r LookupResult) MatchedUserID() string {
	if r.Resolution != identity.LookupMatched {
		return ""
	}
	return r.ResolvedUserID
}

func (s *AuthorizationService) Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (LookupResult, error) {
	switch s.serviceType {
	case "none":
		return LookupResult{Scopes: []string{"*"}, SystemUserRow: auth.SystemUserRowMissing}, nil
	case "deny_all":
		return LookupResult{SystemUserRow: auth.SystemUserRowMissing}, nil
	}

	if s.client != nil {
		res, err := s.client.Lookup(ctx, userID, gatewayContext)
		if err != nil {
			return LookupResult{}, fmt.Errorf("platform authz lookup: %w", err)
		}
		return LookupResult{
			Roles:            res.Roles,
			Scopes:           res.Scopes,
			Profile:          res.Profile,
			EnrichmentStatus: res.EnrichmentStatus,
			EnrichmentReason: res.EnrichmentReason,
			Resolution:       res.Resolution,
			ResolvedUserID:   res.ResolvedUserID,
			RefusalReason:    res.RefusalReason,
			RolesSource:      res.RolesSource,
			SystemUserID:     res.SystemUserID,
			SystemUserRow:    res.SystemUserRow,
		}, nil
	}

	if s.roleEngine == nil {
		return LookupResult{}, fmt.Errorf("default_rbac: AuthorizationClient not wired and no YAML resolver configured; refusing to mint")
	}

	roles := s.ResolveRoles(userID, gatewayContext)
	scopes := s.ResolveScopes(userID, roles, nil)
	return LookupResult{Roles: roles, Scopes: scopes, SystemUserRow: auth.SystemUserRowMissing}, nil
}

func (s *AuthorizationService) ResolveRoles(userID string, gatewayContext map[string]any) []string {
	var roles []string
	seen := make(map[string]bool)

	yamlRoles := s.getYAMLRoles(userID)
	for _, r := range yamlRoles {
		if !seen[r] {
			seen[r] = true
			roles = append(roles, r)
		}
	}

	if s.roleProvider != nil && gatewayContext != nil {
		providerRoles, err := s.roleProvider.GetUserRoles(userID, gatewayContext)
		if err != nil {
			s.logger.Warn("role provider error", "error", err, "userID", userID)
		} else {
			for _, r := range providerRoles {
				if !seen[r] {
					seen[r] = true
					roles = append(roles, r)
				}
			}
		}
	}

	if len(roles) == 0 {
		if rbac.IsSystemPrincipal(userID) {
			s.logger.Warn("system principal matched no role assignment; refusing rather than applying default roles",
				"userID", userID, "defaultRoles", len(s.defaultRoles))
			return nil
		}
		s.logger.Debug("identity matched no YAML or provider role; applying default roles",
			"userID", userID, "defaultRoles", len(s.defaultRoles))
		return s.defaultRoles
	}

	return roles
}

func (s *AuthorizationService) getYAMLRoles(userID string) []string {
	var roles []string

	if assignment, ok := s.emailFold.ResolveBare(s.yamlUsers.Users, userID); ok {
		roles = append(roles, assignment.Roles...)
	}

	gwKey := fmt.Sprintf("gateway_specific_identities:%s:%s", s.gatewayID, userID)
	if assignment, ok := s.emailFold.ResolveComposite(s.yamlUsers.Users, gwKey); ok {
		roles = append(roles, assignment.Roles...)
	}

	return roles
}

func (s *AuthorizationService) ResolveScopes(userID string, roles []string, gatewayContext map[string]any) []string {
	switch s.serviceType {
	case "deny_all":
		return nil
	case "none":
		return []string{"*"}
	}

	if len(roles) == 0 {
		roles = s.ResolveRoles(userID, gatewayContext)
	}

	if s.roleEngine == nil {
		return nil
	}

	return s.roleEngine.ResolveRoleScopes(roles)
}
