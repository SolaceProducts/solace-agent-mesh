// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const yamlGatewayPrefix = "gateway_specific_identities:"

type sayableConflict struct{ *ConflictError }

func (e sayableConflict) Unwrap() error { return e.ConflictError }

var errYAMLImmutable = &ConflictError{
	Message: "System roles are defined in your configuration; they cannot be modified or deleted through the API",
}

var errBuiltinImmutable = &ConflictError{
	Message: "Built-in role shipped and maintained by Solace; it cannot be modified or deleted through the API",
}

var errBuiltinSystemUserImmutable = &ConflictError{
	Message: "Built-in service account shipped and maintained by Solace; it cannot be modified, deleted, or granted roles through the API",
}

var errRoleIsDefault = &ConflictError{
	Message: "Assigned as a default role; remove it from the default roles before deleting it",
}

const (
	resourceClaimMapping = "claim-mapping"
)

type RBACService struct {
	repo               *store.RBACRepo
	usersRepo          *store.UsersRepo
	apiKeysRepo        *store.APIKeysRepo
	budgetsChanged     *BudgetsChangedSignal
	composer           *rbac.Composer
	authzLookupHandler *AuthzLookupHandler
	changes            authzChangeOutbox
	metrics            runtime.Metrics
	logger             *slog.Logger
}

func (s *RBACService) WithAuthzOutbox(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) {
	s.changes.repo = repo
	s.changes.notifier = notifier
}

func (s *RBACService) SetUsersRepo(repo *store.UsersRepo) { s.usersRepo = repo }

func (s *RBACService) SetAPIKeysRepo(repo *store.APIKeysRepo) { s.apiKeysRepo = repo }

func (s *RBACService) SetBudgetsChanged(signal *BudgetsChangedSignal) { s.budgetsChanged = signal }

func NewRBACService(repo *store.RBACRepo, composer *rbac.Composer, lookup *AuthzLookupHandler, metrics runtime.Metrics, logger *slog.Logger) *RBACService {
	return &RBACService{
		repo:               repo,
		composer:           composer,
		authzLookupHandler: lookup,
		changes:            authzChangeOutbox{db: repo},
		metrics:            runtime.MetricsOrNoop(metrics),
		logger:             logger.With("logger_name", "rbac-service"),
	}
}

func (s *RBACService) yamlState() rbac.YAMLState {
	if s.composer == nil {
		return rbac.YAMLState{}
	}
	return s.composer.YAMLState()
}

func (s *RBACService) dbRoleByID(ctx context.Context, roleID string) (*store.RBACRole, error) {
	role, _, _, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		return nil, mapRepoError(err, "role", roleID)
	}
	if role == nil {
		return nil, &ValidationError{
			Message: "Invalid roleId",
			Details: map[string][]string{"roleId": {fmt.Sprintf("%q is not a known role id; system roles are defined in your configuration and cannot be targeted through the API", roleID)}},
		}
	}
	return role, nil
}

func (s *RBACService) validateInherits(ctx context.Context, inherits []string) error {
	unknown, err := s.unknownRoleIDs(ctx, inherits)
	if err != nil {
		return err
	}
	if len(unknown) == 0 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf("Unknown inherits role id(s): %v", unknown),
		Details: map[string][]string{"inherits": {fmt.Sprintf("Not known role id(s): %v", unknown)}},
	}
}

func (s *RBACService) validateDefaultRoleIDs(ctx context.Context, roleIDs []string) error {
	unknown, err := s.unknownRoleIDs(ctx, roleIDs)
	if err != nil {
		return err
	}
	if len(unknown) == 0 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf("Unknown default role id(s): %v", unknown),
		Details: map[string][]string{"roleIds": {fmt.Sprintf("Not known role id(s): %v", unknown)}},
	}
}

func (s *RBACService) unknownRoleIDs(ctx context.Context, roleIDs []string) ([]string, error) {
	var unknown []string
	for _, id := range roleIDs {
		role, _, _, err := s.repo.GetRoleByID(ctx, id)
		if err != nil {
			return nil, mapRepoError(err, auditResourceRole, id)
		}
		if role == nil {
			unknown = append(unknown, id)
		}
	}
	return unknown, nil
}

const (
	auditActionCreated      = "created"
	auditActionUpdated      = "updated"
	auditActionDeleted      = "deleted"
	auditActionMaterialized = "materialized"
	auditActionRejected     = "rejected"

	auditResourceRole           = "role"
	auditResourceGrant          = "grant"
	auditResourceClaimMapping   = "claim_mapping"
	auditResourceDefaultRoles   = "default_roles"
	auditResourceSystemUser     = "system_user"
	auditResourceIdentityClaims = "identity_claims"
	auditResourceAPIKey         = "api_key"
)

const (
	resourceSystemUser      = "service account"
	resourceSystemUserGrant = "service account grant"
)

var errUsersRepoNotConfigured = errors.New("rbac: users repo not configured")

func (s *RBACService) audit(ctx context.Context, action, resource, resourceID, actor string) {
	logRBACAudit(withAuditCredential(ctx, s.logger), action, resource, resourceID, actor)
}

func withAuditCredential(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if id, ok := auth.SubjectFromContext(ctx); ok && id != nil && id.CredentialID() != "" {
		return logger.With("credentialID", id.CredentialID())
	}
	return logger
}

func logRBACAudit(logger *slog.Logger, action, resource, resourceID, actor string, extra ...any) {
	logger.Info("rbac audit", rbacAuditArgs(action, resource, resourceID, actor, extra...)...)
}

func logRBACAuditDenied(logger *slog.Logger, resource, actor string, extra ...any) {
	logger.Warn("rbac audit", rbacAuditArgs(auditActionRejected, resource, "", actor, extra...)...)
}

func rbacAuditArgs(action, resource, resourceID, actor string, extra ...any) []any {
	args := []any{
		"event", "rbac_mutation",
		"action", action,
		"resource", resource,
		"resourceId", resourceID,
		"actor", actor,
	}
	return append(args, extra...)
}

func (s *RBACService) rebuildAndInvalidate(ctx context.Context) {
	refreshRBACState(ctx, s.composer, s.authzLookupHandler, s.metrics, s.logger)
}

func refreshRBACState(ctx context.Context, composer *rbac.Composer, handler *AuthzLookupHandler, metrics runtime.Metrics, logger *slog.Logger) {
	if composer != nil {
		if err := composer.Rebuild(ctx); err != nil {
			if metrics != nil {
				metrics.Counter("rbac_composer_rebuild_failures_total", 1)
			}
			logger.Error("composer rebuild after rbac write failed; lookups will keep returning pre-write state until a later rebuild succeeds", "error", err)
		}
	}
	if handler != nil {
		handler.InvalidateAll()
	}
}

func (s *RBACService) CreateRole(ctx context.Context, req CreateRBACRoleRequest, userID string, actorScopes []string) (*RBACRoleResponse, error) {
	if err := validateScopes(req.Scopes); err != nil {
		return nil, err
	}
	if yamlHasRoleName(s.yamlState(), req.Name) {
		return nil, sayableConflict{&ConflictError{
			Message: fmt.Sprintf("A system role named %q already exists; pick a different name", req.Name),
		}}
	}
	if err := s.validateInherits(ctx, req.Inherits); err != nil {
		return nil, err
	}

	role := &store.RBACRole{
		ID:          idgen.New(),
		Name:        req.Name,
		Description: req.Description,
		CreatedBy:   userID,
		UpdatedBy:   userID,
	}
	if err := s.detectCycleSimulated(ctx, role.ID, req.Inherits); err != nil {
		return nil, err
	}

	check := regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceRole, roleIDs: req.Inherits,
	}
	if err := check.enforceRoleScopeChange(nil, nil, req.Scopes, req.Inherits); err != nil {
		return nil, err
	}

	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return role.ID, s.repo.CreateRoleInTx(ctx, tx, role, req.Scopes, req.Inherits)
	}); err != nil {
		return nil, mapRepoError(err, "role", "")
	}

	s.audit(ctx, auditActionCreated, auditResourceRole, role.ID, userID)
	s.rebuildAndInvalidate(ctx)
	return roleToResponse(role, req.Scopes, req.Inherits), nil
}

func (s *RBACService) UpdateRole(ctx context.Context, id string, req UpdateRBACRoleRequest, userID string, actorScopes []string) (*RBACRoleResponse, error) {
	if err := validateScopes(req.Scopes); err != nil {
		return nil, err
	}
	yaml := s.yamlState()
	if yamlHasRoleName(yaml, id) {
		return nil, errYAMLImmutable
	}
	existing, existingScopes, existingParents, getErr := s.repo.GetRoleByID(ctx, id)
	if getErr != nil {
		return nil, mapRepoError(getErr, "role", id)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: "role", ID: id}
	}
	if existing.Builtin {
		s.audit(ctx, auditActionRejected, auditResourceRole, id, userID)
		return nil, errBuiltinImmutable
	}
	if yamlHasRoleName(yaml, req.Name) && existing.Name != req.Name {
		return nil, sayableConflict{&ConflictError{
			Message: fmt.Sprintf("A system role named %q already exists; pick a different name", req.Name),
		}}
	}
	if existing.Name != req.Name {
		for _, name := range []string{existing.Name, req.Name} {
			if kind := yamlReferenceKind(yaml, name); kind != "" {
				return nil, sayableConflict{&ConflictError{
					Message: fmt.Sprintf("Role %q is referenced by system %s; remove the reference before renaming", name, kind),
				}}
			}
		}
	}
	if err := s.validateInherits(ctx, req.Inherits); err != nil {
		return nil, err
	}
	if err := s.detectCycleSimulated(ctx, id, req.Inherits); err != nil {
		return nil, err
	}

	check := regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceRole, roleIDs: []string{id},
	}
	if err := check.enforceRoleScopeChange(existingScopes, existingParents, req.Scopes, req.Inherits); err != nil {
		return nil, err
	}

	role := &store.RBACRole{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		UpdatedBy:   userID,
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return id, s.repo.UpdateRoleInTx(ctx, tx, role, req.Scopes, req.Inherits)
	}); err != nil {
		return nil, mapRepoError(err, "role", id)
	}

	s.audit(ctx, auditActionUpdated, auditResourceRole, id, userID)
	s.rebuildAndInvalidate(ctx)

	stored, scopes, parents, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, "role", id)
	}
	if stored == nil {
		return nil, &NotFoundError{Resource: "role", ID: id}
	}
	resp := roleToResponse(stored, scopes, parents)
	resp.NameConflict = yamlHasRoleName(yaml, stored.Name)
	return resp, nil
}

func (s *RBACService) DeleteRole(ctx context.Context, id, userID string, actorScopes []string) error {
	if yamlHasRoleName(s.yamlState(), id) {
		return errYAMLImmutable
	}
	existing, _, _, getErr := s.repo.GetRoleByID(ctx, id)
	if getErr != nil {
		return mapRepoError(getErr, "role", id)
	}
	if existing == nil {
		return &NotFoundError{Resource: "role", ID: id}
	}
	if existing.Builtin {
		s.audit(ctx, auditActionRejected, auditResourceRole, id, userID)
		return errBuiltinImmutable
	}
	defaults, defaultsErr := s.repo.ListDefaultRoles(ctx)
	if defaultsErr != nil {
		return mapRepoError(defaultsErr, auditResourceDefaultRoles, "")
	}
	if slices.Contains(defaults, id) {
		s.audit(ctx, auditActionRejected, auditResourceRole, id, userID)
		return errRoleIsDefault
	}
	if err := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceRole, roleIDs: []string{id},
	}).enforceRoleSetChange([]string{id}, nil); err != nil {
		return err
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return id, s.repo.DeleteRoleInTx(ctx, tx, id)
	}); err != nil {
		return mapRepoError(err, "role", id)
	}
	s.audit(ctx, auditActionDeleted, auditResourceRole, id, userID)
	s.rebuildAndInvalidate(ctx)
	return nil
}

func (s *RBACService) GetRole(ctx context.Context, id string) (*RBACRoleResponse, error) {
	role, scopes, parents, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, "role", id)
	}
	if role != nil {
		resp := roleToResponse(role, scopes, parents)
		resp.NameConflict = yamlHasRoleName(s.yamlState(), role.Name)
		return resp, nil
	}
	if yamlRole := yamlRoleByName(s.yamlState(), id); yamlRole != nil {
		dbRow, err := s.repo.GetRoleByName(ctx, id)
		if err != nil {
			return nil, mapRepoError(err, "role", id)
		}
		yamlRole.NameConflict = dbRow != nil
		return yamlRole, nil
	}
	return nil, &NotFoundError{Resource: "role", ID: id}
}

func (s *RBACService) ListRoles(ctx context.Context) ([]RBACRoleResponse, error) {
	roles, err := s.repo.ListRoles(ctx)
	if err != nil {
		return nil, mapRepoError(err, "role", "")
	}
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, mapRepoError(err, "role", "")
	}
	yamlState := s.yamlState()
	dbNames := make(map[string]bool, len(roles))
	for _, r := range roles {
		dbNames[r.Name] = true
	}
	conflict := make(map[string]bool)
	for name := range yamlState.Roles.Roles {
		if dbNames[name] {
			conflict[name] = true
		}
	}

	yaml := yamlRoleResponses(yamlState)
	out := make([]RBACRoleResponse, 0, len(roles)+len(yaml))
	for i := range yaml {
		yaml[i].NameConflict = conflict[yaml[i].Name]
	}
	out = append(out, yaml...)
	for _, r := range roles {
		resp := *roleToResponse(r, snap.RoleScopes[r.ID], snap.RoleInheritance[r.ID])
		resp.NameConflict = conflict[r.Name]
		out = append(out, resp)
	}
	return out, nil
}

type regrantCheck struct {
	composer    *rbac.Composer
	logger      *slog.Logger
	actor       string
	actorScopes []string
	resource    string
	roleIDs     []string
}

func (c regrantCheck) enforce(before, after []string) error {
	delta := rbac.ScopeSetDelta(before, after)
	if len(delta) == 0 {
		return nil
	}
	missing := rbac.UnsatisfiedScopes(c.actorScopes, delta)
	if len(missing) == 0 {
		return nil
	}
	logRBACAuditDenied(c.logger, c.resource, c.actor,
		"roleIds", strings.Join(c.roleIDs, ","),
		"missingScopes", strings.Join(missing, ","))
	return &ForbiddenError{
		Message: fmt.Sprintf("Cannot %s scopes you do not hold: %s",
			changeVerb(missing, after), strings.Join(missing, ", ")),
	}
}

func changeVerb(missing, after []string) string {
	var adding, removing bool
	for _, s := range missing {
		if slices.Contains(after, s) {
			adding = true
		} else {
			removing = true
		}
	}
	switch {
	case adding && removing:
		return "change"
	case removing:
		return "remove"
	default:
		return "grant"
	}
}

func (c regrantCheck) resolveRoleScopes(roleIDs []string) ([]string, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	scopes, err := c.composer.ScopesForRoleIDs(roleIDs)
	if err != nil {
		c.logger.Error("re-grant guard cannot resolve conferred scopes; refusing the write",
			"error", err,
			"actor", c.actor,
			"resource", c.resource,
			"roleIds", strings.Join(roleIDs, ","))
		return nil, &UnavailableError{
			Message: "Authorization state is unavailable on this instance",
			Cause:   err,
		}
	}
	return scopes, nil
}

func (c regrantCheck) enforceRoleScopeChange(beforeScopes, beforeParents, scopes, inherits []string) error {
	beforeInherited, err := c.resolveRoleScopes(beforeParents)
	if err != nil {
		return err
	}
	afterInherited, err := c.resolveRoleScopes(inherits)
	if err != nil {
		return err
	}
	return c.enforce(unionScopes(beforeScopes, beforeInherited), unionScopes(scopes, afterInherited))
}

func (c regrantCheck) enforceRoleSetChange(beforeRoleIDs, afterRoleIDs []string) error {
	before, err := c.resolveRoleScopes(beforeRoleIDs)
	if err != nil {
		return err
	}
	after, err := c.resolveRoleScopes(afterRoleIDs)
	if err != nil {
		return err
	}
	return c.enforce(before, after)
}

func (c regrantCheck) enforceClaimMappingChange(beforeRoleIDs, afterRoleIDs []string, subjectChanged bool) error {
	if !subjectChanged {
		return c.enforceRoleSetChange(beforeRoleIDs, afterRoleIDs)
	}
	if err := c.forRoles(beforeRoleIDs).enforceRoleSetChange(beforeRoleIDs, nil); err != nil {
		return err
	}
	return c.forRoles(afterRoleIDs).enforceRoleSetChange(nil, afterRoleIDs)
}

func (c regrantCheck) forRoles(roleIDs []string) regrantCheck {
	c.roleIDs = roleIDs
	return c
}

func unionScopes(lists ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range lists {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func (s *RBACService) CreateGrant(ctx context.Context, req CreateGrantRequest, actor string, actorScopes []string) (*GrantResponse, error) {
	return s.createGrant(ctx, req, actor, actorScopes, false)
}

func (s *RBACService) createGrant(ctx context.Context, req CreateGrantRequest, actor string, actorScopes []string, requireNew bool) (*GrantResponse, error) {
	role, err := s.dbRoleByID(ctx, req.RoleID)
	if err != nil {
		return nil, err
	}
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}

	subject := req.Subject
	subject.Value = NormalizeGrantSubjectValue(subject.Type, subject.Value)
	if rbac.IsReservedSystemUserSub(subject.Value) {
		s.audit(ctx, auditActionRejected, auditResourceGrant, subject.Value, actor)
		return nil, errBuiltinSystemUserImmutable
	}
	if yamlHasGrant(s.yamlState(), subject.Type, subject.Value, role.Name) {
		return nil, sayableConflict{&ConflictError{
			Message:    fmt.Sprintf("Grant (identity=%q, role=%q) is defined in your configuration", subject.Value, role.Name),
			LogMessage: "grant is defined in the configuration",
		}}
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: actor, actorScopes: actorScopes,
		resource: auditResourceGrant, roleIDs: []string{role.ID},
	}).enforceRoleSetChange(nil, []string{role.ID}); regrantErr != nil {
		return nil, regrantErr
	}

	userID, err := s.resolveGrantSubject(ctx, subject)
	if err != nil {
		return nil, err
	}
	if userID != "" {
		addGrant := s.repo.AddUserRoleWithSubjectInTx
		if requireNew {
			addGrant = s.repo.AddNewUserRoleWithSubjectInTx
		}
		var grantID string
		if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
			var addErr error
			grantID, addErr = addGrant(ctx, tx, userID, role.ID, actor,
				store.RoleGrantSubject{Type: subject.Type, Value: subject.Value, Issuer: subject.Issuer})
			return userID, addErr
		}); err != nil {
			return nil, mapRepoError(err, "grant", "")
		}
		s.audit(ctx, auditActionCreated, auditResourceGrant, userID+"|"+role.ID, actor)
		s.rebuildAndInvalidate(ctx)
		return &GrantResponse{ID: grantID, Subject: &subject, UserID: userID, RoleID: role.ID, State: GrantStateConcrete, Origin: RBACOriginCustom}, nil
	}

	p := &store.RBACPendingUserRole{
		SubjectType:  subject.Type,
		SubjectValue: subject.Value,
		Issuer:       subject.Issuer,
		RoleID:       role.ID,
		CreatedBy:    actor,
		UpdatedBy:    actor,
	}
	if err := s.repo.CreatePendingUserRole(ctx, p); err != nil {
		return nil, mapRepoError(err, "grant", "")
	}
	s.audit(ctx, auditActionCreated, auditResourceGrant, p.ID, actor)
	return &GrantResponse{ID: p.ID, Subject: &subject, RoleID: role.ID, State: GrantStatePending, Origin: RBACOriginCustom}, nil
}

func (s *RBACService) CreateSystemUser(ctx context.Context, req CreateSystemUserRequest, actor string) (*SystemUserResponse, error) {
	return s.createSystemUser(ctx, req, actor, false)
}

var errSystemUserTaken = errors.New("service account already exists")

func (s *RBACService) createSystemUser(ctx context.Context, req CreateSystemUserRequest, actor string, requireNew bool) (*SystemUserResponse, error) {
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}
	sub := rbac.SystemUserPrincipal(req.Name)
	if rbac.IsReservedSystemUserSub(sub) {
		s.audit(ctx, auditActionRejected, auditResourceSystemUser, sub, actor)
		return nil, errBuiltinSystemUserImmutable
	}

	roleIDs := make([]string, 0, len(req.RoleIDs))
	for _, rid := range req.RoleIDs {
		role, err := s.dbRoleByID(ctx, rid)
		if err != nil {
			return nil, err
		}
		roleIDs = append(roleIDs, role.ID)
	}

	u := &store.User{
		Issuer:         rbac.SystemUserIssuer,
		Sub:            sub,
		ProvisionedVia: rbac.ProvisionedViaSystem,
		CreatedBy:      actor,
		UpdatedBy:      actor,
	}
	if req.DisplayName != "" {
		u.DisplayName = &req.DisplayName
	}
	minted := ""
	if requireNew {
		minted = idgen.New()
		u.ID = minted
	}
	failedResource := resourceSystemUser
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		if err := s.usersRepo.UpsertUserInTx(ctx, tx, u, nil); err != nil {
			return "", err
		}
		if requireNew && u.ID != minted {
			return "", errSystemUserTaken
		}
		failedResource = resourceSystemUserGrant
		for _, rid := range roleIDs {
			if _, err := s.repo.AddUserRoleWithSubjectInTx(ctx, tx, u.ID, rid, actor, systemUserGrantSubject(sub)); err != nil {
				return "", err
			}
		}
		return u.ID, nil
	}); err != nil {
		if errors.Is(err, errSystemUserTaken) {
			return nil, &ConflictError{Message: uniqueConflictMessage(auditResourceSystemUser)}
		}
		return nil, mapRepoError(err, failedResource, "")
	}

	effectiveRoles, err := s.repo.ListRolesForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	effectiveRoleIDs := make([]string, 0, len(effectiveRoles))
	for _, r := range effectiveRoles {
		effectiveRoleIDs = append(effectiveRoleIDs, r.RoleID)
	}

	s.audit(ctx, auditActionCreated, auditResourceSystemUser, u.ID, actor)
	s.rebuildAndInvalidate(ctx)
	return &SystemUserResponse{ID: u.ID, Sub: sub, Name: req.Name, DisplayName: req.DisplayName, RoleIDs: effectiveRoleIDs, Origin: RBACOriginCustom}, nil
}

func (s *RBACService) UpdateSystemUser(ctx context.Context, id string, req UpdateSystemUserRequest, actor string, actorScopes []string) (*SystemUserResponse, error) {
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}
	u, err := s.systemUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rbac.IsReservedSystemUserSub(u.Sub) {
		s.audit(ctx, auditActionRejected, auditResourceSystemUser, id, actor)
		return nil, errBuiltinSystemUserImmutable
	}

	roleIDs := make([]string, 0, len(req.RoleIDs))
	seen := make(map[string]struct{}, len(req.RoleIDs))
	for _, rid := range req.RoleIDs {
		role, roleErr := s.dbRoleByID(ctx, rid)
		if roleErr != nil {
			return nil, roleErr
		}
		if _, dup := seen[role.ID]; dup {
			continue
		}
		seen[role.ID] = struct{}{}
		roleIDs = append(roleIDs, role.ID)
	}

	prior, err := s.repo.ListRolesForUser(ctx, u.ID)
	if err != nil {
		return nil, mapRepoError(err, resourceSystemUser, id)
	}
	priorRoleIDs := make([]string, 0, len(prior))
	for _, r := range prior {
		priorRoleIDs = append(priorRoleIDs, r.RoleID)
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: actor, actorScopes: actorScopes,
		resource: auditResourceSystemUser, roleIDs: roleIDs,
	}).enforceRoleSetChange(priorRoleIDs, roleIDs); regrantErr != nil {
		return nil, regrantErr
	}

	var displayName *string
	if req.DisplayName != "" {
		displayName = &req.DisplayName
	}
	failedResource := resourceSystemUserGrant
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		if err := s.repo.SetUserRolesWithSubjectInTx(ctx, tx, u.ID, roleIDs, actor, systemUserGrantSubject(u.Sub)); err != nil {
			return "", err
		}
		failedResource = resourceSystemUser
		return u.ID, s.usersRepo.UpdateSystemUserInTx(ctx, tx, u.ID, displayName, actor)
	}); err != nil {
		return nil, mapRepoError(err, failedResource, id)
	}

	s.audit(ctx, auditActionUpdated, auditResourceSystemUser, u.ID, actor)
	s.rebuildAndInvalidate(ctx)
	return &SystemUserResponse{
		ID:          u.ID,
		Sub:         u.Sub,
		Name:        strings.TrimPrefix(u.Sub, rbac.SystemSubPrefix),
		DisplayName: req.DisplayName,
		RoleIDs:     roleIDs,
		Origin:      RBACOriginCustom,
	}, nil
}

func (s *RBACService) DeleteSystemUser(ctx context.Context, id, actor string, actorScopes []string) error {
	if s.usersRepo == nil {
		return errUsersRepoNotConfigured
	}
	u, err := s.systemUserByID(ctx, id)
	if err != nil {
		return err
	}
	if rbac.IsReservedSystemUserSub(u.Sub) {
		s.audit(ctx, auditActionRejected, auditResourceSystemUser, id, actor)
		return errBuiltinSystemUserImmutable
	}

	prior, err := s.repo.ListRolesForUser(ctx, u.ID)
	if err != nil {
		return mapRepoError(err, resourceSystemUser, id)
	}
	priorRoleIDs := make([]string, 0, len(prior))
	for _, r := range prior {
		priorRoleIDs = append(priorRoleIDs, r.RoleID)
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: actor, actorScopes: actorScopes,
		resource: auditResourceSystemUser, roleIDs: priorRoleIDs,
	}).enforceRoleSetChange(priorRoleIDs, nil); regrantErr != nil {
		return regrantErr
	}

	keysRemoved := s.countAPIKeys(ctx, u.ID)
	failedResource := resourceSystemUserGrant
	var budgetRowsRemoved store.SystemUserBudgetRowsDeleted
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		if _, err := s.repo.DeletePendingUserRolesForSubjectInTx(ctx, tx, store.PendingSubjectSub, u.Sub, rbac.SystemUserIssuer); err != nil {
			return "", err
		}
		failedResource = resourceSystemUser
		var err error
		budgetRowsRemoved, err = s.usersRepo.DeleteSystemUserInTx(ctx, tx, u.ID)
		return u.ID, err
	}); err != nil {
		return mapRepoError(err, failedResource, id)
	}
	if budgetRowsRemoved.Assignments > 0 || budgetRowsRemoved.TopUps > 0 {
		s.budgetsChanged.send(ctx)
	}

	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionDeleted, auditResourceSystemUser, u.ID, actor,
		"apiKeysDeleted", keysRemoved,
		"budgetAssignmentsDeleted", budgetRowsRemoved.Assignments,
		"budgetTopUpsDeleted", budgetRowsRemoved.TopUps)
	s.rebuildAndInvalidate(ctx)
	return nil
}

func (s *RBACService) countAPIKeys(ctx context.Context, userID string) int {
	if s.apiKeysRepo == nil {
		return -1
	}
	_, total, err := s.apiKeysRepo.ListPaginated(ctx, store.APIKeyListOptions{PageNumber: 1, PageSize: 1, UserID: userID})
	if err != nil {
		s.logger.Warn("count api keys before system-user delete failed", "error", err, "userID", userID)
		return -1
	}
	return total
}

func (s *RBACService) systemUserByID(ctx context.Context, id string) (*store.User, error) {
	u, _, err := s.usersRepo.GetByID(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, resourceSystemUser, id)
	}
	if u == nil || u.ProvisionedVia != rbac.ProvisionedViaSystem {
		return nil, &NotFoundError{Resource: resourceSystemUser, ID: id}
	}
	return u, nil
}

func (s *RBACService) ListSystemUsers(ctx context.Context) ([]SystemUserResponse, error) {
	if s.usersRepo == nil {
		return []SystemUserResponse{}, nil
	}
	users, err := s.usersRepo.ListByProvisionedVia(ctx, rbac.ProvisionedViaSystem)
	if err != nil {
		return nil, err
	}
	builtinRoles := s.builtinSystemUserRoles()
	out := make([]SystemUserResponse, 0, len(users))
	for _, u := range users {
		if reserved, isReserved := rbac.LookupReservedSystemUser(u.Sub); isReserved && reserved.HiddenFromRunAsPicker {
			continue
		}
		resp, err := s.systemUserResponse(ctx, &u, builtinRoles)
		if err != nil {
			return nil, err
		}
		out = append(out, *resp)
	}
	return out, nil
}

func (s *RBACService) builtinSystemUserRoles() func(sub string) []string {
	yamlUsers := s.yamlState().Users.Users
	embedded := sync.OnceValue(func() rbac.UsersConfig {
		_, users, err := rbac.EmbeddedDefaults()
		if err != nil {
			s.logger.Warn("resolve built-in service account roles from embedded YAML", "error", err)
		}
		return users
	})
	return func(sub string) []string {
		if assignment, ok := yamlUsers[sub]; ok {
			return assignment.Roles
		}
		return embedded().Users[sub].Roles
	}
}

func (s *RBACService) systemUserResponse(ctx context.Context, u *store.User, builtinRoles func(sub string) []string) (*SystemUserResponse, error) {
	roles, err := s.repo.ListRolesForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	resp := &SystemUserResponse{
		ID:      u.ID,
		Sub:     u.Sub,
		Name:    strings.TrimPrefix(u.Sub, rbac.SystemSubPrefix),
		RoleIDs: make([]string, 0, len(roles)),
		Origin:  RBACOriginCustom,
	}
	if u.DisplayName != nil {
		resp.DisplayName = *u.DisplayName
	}
	if reserved, isReserved := rbac.LookupReservedSystemUser(u.Sub); isReserved {
		resp.Origin = RBACOriginBuiltin
		resp.RoleIDs = append(resp.RoleIDs, builtinRoles(u.Sub)...)
		if resp.DisplayName == "" {
			resp.DisplayName = reserved.DisplayName
		}
	}
	for _, r := range roles {
		resp.RoleIDs = append(resp.RoleIDs, r.RoleID)
	}
	return resp, nil
}

func NormalizeGrantSubjectValue(subjectType, value string) string {
	v := strings.TrimSpace(value)
	switch subjectType {
	case store.PendingSubjectEmail:
		return strings.ToLower(v)
	case store.PendingSubjectLegacy:
		if strings.Contains(v, "@") {
			return strings.ToLower(v)
		}
	}
	return v
}

func (s *RBACService) resolveGrantSubject(ctx context.Context, subject GrantSubject) (string, error) {
	if subject.Type != store.PendingSubjectLegacy {
		return grantIdentity.resolveIdentity(ctx, s.usersRepo, subject.Type, subject.Value, subject.Issuer)
	}
	u, _, err := s.usersRepo.GetByEmail(ctx, subject.Value)
	if err != nil || u == nil {
		return "", err
	}
	return u.ID, nil
}

func (s *RBACService) ListGrants(ctx context.Context) ([]GrantResponse, error) {
	concrete, err := s.repo.ListUserRoles(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := s.repo.ListPendingUserRoles(ctx)
	if err != nil {
		return nil, err
	}
	out := yamlGrantResponses(s.yamlState())
	for _, c := range concrete {
		out = append(out, GrantResponse{
			ID:      c.ID,
			Subject: s.concreteGrantSubject(ctx, c),
			UserID:  c.UserID,
			RoleID:  c.RoleID,
			State:   GrantStateConcrete,
			Origin:  RBACOriginCustom,
		})
	}
	for _, p := range pending {
		out = append(out, GrantResponse{
			ID:      p.ID,
			Subject: &GrantSubject{Type: p.SubjectType, Value: p.SubjectValue, Issuer: p.Issuer},
			RoleID:  p.RoleID,
			State:   GrantStatePending,
			Origin:  RBACOriginCustom,
		})
	}
	return out, nil
}

var errGrantSubjectNamesAnotherUser = &ConflictError{
	Message: "The subject does not name this grant's user; delete the grant and create one for the other subject",
}

var errSystemUserGrantSubject = &ConflictError{
	Message: "A service account's grant is recorded under the service account; change its roles through the service account instead",
}

func (s *RBACService) UpdateGrantSubject(ctx context.Context, id string, req UpdateGrantRequest, actor string) (*GrantResponse, error) {
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}
	subject := req.Subject
	subject.Value = NormalizeGrantSubjectValue(subject.Type, subject.Value)
	if rbac.IsReservedSystemUserSub(subject.Value) {
		s.audit(ctx, auditActionRejected, auditResourceGrant, subject.Value, actor)
		return nil, errBuiltinSystemUserImmutable
	}
	grant, err := s.repo.GetUserRole(ctx, id)
	if err != nil {
		return nil, err
	}
	if grant == nil {
		return nil, &NotFoundError{Resource: auditResourceGrant, ID: id}
	}
	user, _, err := s.usersRepo.GetByID(ctx, grant.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || rbac.IsSystemPrincipal(user.Sub) || rbac.IsSystemPrincipal(grant.SubjectValue) {
		return nil, errSystemUserGrantSubject
	}
	role, err := s.dbRoleByID(ctx, grant.RoleID)
	if err != nil {
		return nil, err
	}
	if yamlHasGrant(s.yamlState(), subject.Type, subject.Value, role.Name) {
		return nil, sayableConflict{&ConflictError{
			Message:    fmt.Sprintf("Grant (identity=%q, role=%q) is defined in your configuration", subject.Value, role.Name),
			LogMessage: "grant is defined in the configuration",
		}}
	}
	if err := s.requireSubjectNamesUser(ctx, subject, user); err != nil {
		return nil, err
	}
	resp := &GrantResponse{ID: grant.ID, Subject: &subject, UserID: grant.UserID, RoleID: grant.RoleID, State: GrantStateConcrete, Origin: RBACOriginCustom}
	if grant.SubjectType == subject.Type && grant.SubjectValue == subject.Value && grant.Issuer == subject.Issuer {
		return resp, nil
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return grant.UserID, s.repo.SetUserRoleSubjectInTx(ctx, tx, id, actor,
			store.RoleGrantSubject{Type: subject.Type, Value: subject.Value, Issuer: subject.Issuer})
	}); err != nil {
		return nil, mapRepoError(err, auditResourceGrant, id)
	}
	s.audit(ctx, auditActionUpdated, auditResourceGrant, id, actor)
	s.rebuildAndInvalidate(ctx)
	return resp, nil
}

func (s *RBACService) requireSubjectNamesUser(ctx context.Context, subject GrantSubject, user *store.User) error {
	resolved, err := s.resolveGrantSubject(ctx, subject)
	if err != nil {
		return err
	}
	if resolved == user.ID {
		return nil
	}
	noEmail := user.Email == nil || *user.Email == ""
	if subject.Type == store.PendingSubjectLegacy && noEmail && subject.Value == user.Sub {
		return nil
	}
	return errGrantSubjectNamesAnotherUser
}

func (s *RBACService) grantUsersByID(ctx context.Context) (map[string]*GrantUser, error) {
	holders, err := s.repo.ListGrantHolders(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*GrantUser, len(holders))
	for _, h := range holders {
		byID[h.UserID] = &GrantUser{Email: h.Email, EmailShared: h.EmailShared, Sub: h.Sub, Issuer: h.Issuer}
	}
	return byID, nil
}

func (g *GrantResponse) setUser(usersByID map[string]*GrantUser) {
	if g.State == GrantStateConcrete {
		g.User = usersByID[g.UserID]
	}
}

func (s *RBACService) concreteGrantSubject(ctx context.Context, c *store.RBACUserRole) *GrantSubject {
	if c.SubjectType != "" {
		return &GrantSubject{Type: c.SubjectType, Value: c.SubjectValue, Issuer: c.Issuer}
	}
	if s.usersRepo == nil {
		return nil
	}
	u, _, err := s.usersRepo.GetByID(ctx, c.UserID)
	if err != nil || u == nil {
		return nil
	}
	if u.Email != nil && *u.Email != "" {
		return &GrantSubject{Type: store.PendingSubjectEmail, Value: *u.Email}
	}
	return &GrantSubject{Type: store.PendingSubjectSub, Value: u.Sub, Issuer: u.Issuer}
}

func (s *RBACService) grantTarget(ctx context.Context, id string) (roleID, userID string, err error) {
	concrete, err := s.repo.ListUserRoles(ctx)
	if err != nil {
		return "", "", mapRepoError(err, auditResourceGrant, id)
	}
	for _, row := range concrete {
		if row.ID == id {
			return row.RoleID, row.UserID, nil
		}
	}
	pending, err := s.repo.ListPendingUserRoles(ctx)
	if err != nil {
		return "", "", mapRepoError(err, auditResourceGrant, id)
	}
	for _, row := range pending {
		if row.ID == id {
			return row.RoleID, "", nil
		}
	}
	return "", "", nil
}

func (s *RBACService) enforceGrantRemoval(ctx context.Context, id, actor string, actorScopes []string) error {
	roleID, userID, err := s.grantTarget(ctx, id)
	if err != nil {
		return err
	}
	if roleID == "" {
		return nil
	}
	check := regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: actor, actorScopes: actorScopes,
		resource: auditResourceGrant, roleIDs: []string{roleID},
	}
	if userID == "" {
		return check.enforceRoleSetChange([]string{roleID}, nil)
	}
	current, err := s.repo.ListRolesForUser(ctx, userID)
	if err != nil {
		return mapRepoError(err, auditResourceGrant, id)
	}
	before := make([]string, 0, len(current))
	remaining := make([]string, 0, len(current))
	for _, row := range current {
		before = append(before, row.RoleID)
		if row.RoleID != roleID {
			remaining = append(remaining, row.RoleID)
		}
	}
	return check.enforceRoleSetChange(before, remaining)
}

func (s *RBACService) DeleteGrant(ctx context.Context, id, actor string, actorScopes []string) error {
	if err := s.enforceGrantRemoval(ctx, id, actor, actorScopes); err != nil {
		return err
	}
	concrete := false
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return id, s.repo.DeleteUserRoleInTx(ctx, tx, id)
	}); err == nil {
		concrete = true
	} else if !errors.Is(err, store.ErrRBACNotFound) {
		return mapRepoError(err, "grant", id)
	} else if err := s.repo.DeletePendingUserRole(ctx, id); err != nil {
		return mapRepoError(err, "grant", id)
	}
	grantState := "pending"
	if concrete {
		grantState = "concrete"
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionDeleted, auditResourceGrant, id, actor, "grantState", grantState)
	if concrete {
		s.rebuildAndInvalidate(ctx)
	}
	return nil
}

func (s *RBACService) resolveClaimRoles(ctx context.Context, provider, claimValue string, roleIDs []string) ([]string, error) {
	state := s.yamlState()
	out := make([]string, 0, len(roleIDs))
	seen := make(map[string]struct{}, len(roleIDs))
	for _, id := range roleIDs {
		role, err := s.dbRoleByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if yamlHasClaimMapping(state, provider, claimValue, role.Name) {
			return nil, sayableConflict{&ConflictError{
				Message: fmt.Sprintf("Claim mapping (%s=%s → %s) is defined in your configuration", provider, claimValue, role.Name),
			}}
		}
		if _, dup := seen[role.ID]; dup {
			continue
		}
		seen[role.ID] = struct{}{}
		out = append(out, role.ID)
	}
	return out, nil
}

func (s *RBACService) claimProvider(req string) string {
	if req == "" {
		return s.yamlState().ClaimOIDCProvider
	}
	return req
}

const (
	msgPlaceholderProviderStored = "claim mapping stored under a placeholder oidcProvider; it grants no roles unless the deployment's IdP is genuinely named that"
	msgUnsetProviderStored       = "claim mapping stored with no oidcProvider (idp_claims_config.oidc_provider is unset); it only matches tokens that carry no provider either"
)

func (s *RBACService) warnIfProviderInert(provider, name string) {
	switch {
	case rbac.IsSentinelProvider(provider):
		s.logger.Warn(msgPlaceholderProviderStored, "claimMappingName", name, "oidcProvider", provider)
	case provider == "":
		s.logger.Warn(msgUnsetProviderStored, "claimMappingName", name)
	}
}

func (s *RBACService) rejectUnmatchableProvider(provider string) error {
	configured := s.yamlState().ClaimOIDCProvider
	if !rbac.ProviderCannotMatch(provider, configured) {
		return nil
	}
	return &ValidationError{
		Message: "Invalid oidcProvider",
		Details: map[string][]string{"oidcProvider": {fmt.Sprintf(
			"%q is an internal placeholder, not this deployment's identity provider, so a mapping stored under it can never match a token. Use %q instead",
			provider, configured)}},
	}
}

func (s *RBACService) claimKey(req string) string {
	if req != "" {
		return req
	}
	return s.yamlState().ClaimKey
}

func (s *RBACService) resolveClaimKeyOrWarn(req, name string) string {
	claimKey := s.claimKey(req)
	if claimKey == "" {
		s.logger.Warn("claim mapping has no claim key (external_auth_claim_key unset and none supplied); the mapping will be inert until a claim key is configured", "claimMappingName", name)
	}
	return claimKey
}

func (s *RBACService) CreateClaimMapping(ctx context.Context, req CreateRBACClaimMappingRequest, userID string, actorScopes []string) (*RBACClaimMappingResponse, error) {
	provider := s.claimProvider(req.OIDCProvider)
	if err := s.rejectUnmatchableProvider(provider); err != nil {
		return nil, err
	}
	roleIDs, err := s.resolveClaimRoles(ctx, provider, req.ClaimValue, req.RoleIDs)
	if err != nil {
		return nil, err
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceClaimMapping, roleIDs: roleIDs,
	}).enforceRoleSetChange(nil, roleIDs); regrantErr != nil {
		return nil, regrantErr
	}
	m := &store.RBACIdpClaimMapping{
		OIDCProvider: provider,
		ClaimKey:     s.resolveClaimKeyOrWarn(req.ClaimKey, req.Name),
		ClaimValue:   req.ClaimValue,
		Name:         req.Name,
		CreatedBy:    userID,
		UpdatedBy:    userID,
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		if err := s.repo.CreateClaimMappingInTx(ctx, tx, m, roleIDs); err != nil {
			return "", err
		}
		return m.ID, nil
	}); err != nil {
		return nil, mapRepoError(err, resourceClaimMapping, "")
	}
	s.warnIfProviderInert(provider, req.Name)
	s.audit(ctx, auditActionCreated, auditResourceClaimMapping, m.ID, userID)
	s.rebuildAndInvalidate(ctx)
	return claimMappingToResponse(m, roleIDs), nil
}

func (s *RBACService) loadClaimMapping(ctx context.Context, id string) (*store.RBACIdpClaimMapping, []string, error) {
	stored, roleIDs, err := s.repo.GetClaimMappingByID(ctx, id)
	if err != nil {
		return nil, nil, mapRepoError(err, resourceClaimMapping, id)
	}
	if stored == nil {
		return nil, nil, &NotFoundError{Resource: resourceClaimMapping, ID: id}
	}
	return stored, roleIDs, nil
}

func (s *RBACService) UpdateClaimMapping(ctx context.Context, id string, req UpdateRBACClaimMappingRequest, userID string, actorScopes []string) (*RBACClaimMappingResponse, error) {
	return s.updateClaimMapping(ctx, id, req, userID, actorScopes, true)
}

func (s *RBACService) ReplaceClaimMapping(ctx context.Context, id string, req UpdateRBACClaimMappingRequest, userID string, actorScopes []string) (*RBACClaimMappingResponse, error) {
	return s.updateClaimMapping(ctx, id, req, userID, actorScopes, false)
}

func (s *RBACService) updateClaimMapping(ctx context.Context, id string, req UpdateRBACClaimMappingRequest, userID string, actorScopes []string, keepStoredProvider bool) (*RBACClaimMappingResponse, error) {
	stored, priorRoleIDs, err := s.loadClaimMapping(ctx, id)
	if err != nil {
		return nil, err
	}
	provider := s.claimProvider(req.OIDCProvider)
	if keepStoredProvider && req.OIDCProvider == "" {
		provider = stored.OIDCProvider
	}
	claimKey := s.resolveClaimKeyOrWarn(req.ClaimKey, req.Name)
	subjectChanged := stored.OIDCProvider != provider || stored.ClaimKey != claimKey || stored.ClaimValue != req.ClaimValue
	if subjectChanged {
		if rejectErr := s.rejectUnmatchableProvider(provider); rejectErr != nil {
			return nil, rejectErr
		}
	}
	roleIDs, err := s.resolveClaimRoles(ctx, provider, req.ClaimValue, req.RoleIDs)
	if err != nil {
		return nil, err
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceClaimMapping, roleIDs: roleIDs,
	}).enforceClaimMappingChange(priorRoleIDs, roleIDs, subjectChanged); regrantErr != nil {
		return nil, regrantErr
	}
	m := &store.RBACIdpClaimMapping{
		ID:           id,
		OIDCProvider: provider,
		ClaimKey:     claimKey,
		ClaimValue:   req.ClaimValue,
		Name:         req.Name,
		UpdatedBy:    userID,
	}
	if err = s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return id, s.repo.UpdateClaimMappingInTx(ctx, tx, m, roleIDs)
	}); err != nil {
		return nil, mapRepoError(err, resourceClaimMapping, id)
	}
	s.warnIfProviderInert(provider, req.Name)
	s.audit(ctx, auditActionUpdated, auditResourceClaimMapping, id, userID)
	s.rebuildAndInvalidate(ctx)

	stored, storedRoles, err := s.repo.GetClaimMappingByID(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, resourceClaimMapping, id)
	}
	if stored == nil {
		return nil, &NotFoundError{Resource: resourceClaimMapping, ID: id}
	}
	return claimMappingToResponse(stored, storedRoles), nil
}

func (s *RBACService) DeleteClaimMapping(ctx context.Context, id, userID string, actorScopes []string) error {
	stored, priorRoleIDs, err := s.repo.GetClaimMappingByID(ctx, id)
	if err != nil {
		return mapRepoError(err, resourceClaimMapping, id)
	}
	if stored == nil {
		return &NotFoundError{Resource: resourceClaimMapping, ID: id}
	}
	if err := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceClaimMapping, roleIDs: priorRoleIDs,
	}).enforceRoleSetChange(priorRoleIDs, nil); err != nil {
		return err
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return id, s.repo.DeleteClaimMappingInTx(ctx, tx, id)
	}); err != nil {
		return mapRepoError(err, resourceClaimMapping, id)
	}
	s.audit(ctx, auditActionDeleted, auditResourceClaimMapping, id, userID)
	s.rebuildAndInvalidate(ctx)
	return nil
}

func (s *RBACService) ListClaimMappings(ctx context.Context) ([]RBACClaimMappingResponse, error) {
	rows, roles, err := s.repo.ListClaimMappings(ctx)
	if err != nil {
		return nil, mapRepoError(err, resourceClaimMapping, "")
	}
	yaml := yamlClaimMappingResponses(s.yamlState())
	out := make([]RBACClaimMappingResponse, 0, len(rows)+len(yaml))
	out = append(out, yaml...)
	for _, r := range rows {
		out = append(out, *claimMappingToResponse(r, roles[r.ID]))
	}
	return out, nil
}

func (s *RBACService) GetDefaultRoles(ctx context.Context) (*DefaultRolesResponse, error) {
	ids, err := s.repo.ListDefaultRoles(ctx)
	if err != nil {
		return nil, mapRepoError(err, auditResourceDefaultRoles, "")
	}
	if len(ids) > 0 {
		return &DefaultRolesResponse{RoleIDs: ids, Origin: RBACOriginCustom}, nil
	}
	return &DefaultRolesResponse{
		RoleIDs: apicontract.NonNilList(slices.Clone(s.yamlState().DefaultRoles)),
		Origin:  RBACOriginYAML,
	}, nil
}

func (s *RBACService) SetDefaultRoles(ctx context.Context, roleIDs []string, userID string, actorScopes []string) error {
	if err := s.validateDefaultRoleIDs(ctx, roleIDs); err != nil {
		return err
	}
	priorDefaults, err := s.repo.ListDefaultRoles(ctx)
	if err != nil {
		return mapRepoError(err, auditResourceDefaultRoles, "")
	}
	if err := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: userID, actorScopes: actorScopes,
		resource: auditResourceDefaultRoles, roleIDs: roleIDs,
	}).enforceRoleSetChange(priorDefaults, roleIDs); err != nil {
		return err
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return authzOutboxAggregateDefaultRoles, s.repo.SetDefaultRolesInTx(ctx, tx, roleIDs)
	}); err != nil {
		return mapRepoError(err, auditResourceDefaultRoles, "")
	}
	s.audit(ctx, auditActionUpdated, auditResourceDefaultRoles, "", userID)
	s.rebuildAndInvalidate(ctx)
	return nil
}

func (s *RBACService) detectCycleSimulated(ctx context.Context, id string, proposedParents []string) error {
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		return mapRepoError(err, "role", id)
	}
	graph := make(map[string][]string, len(snap.RoleInheritance))
	for k, v := range snap.RoleInheritance {
		graph[k] = v
	}
	graph[id] = proposedParents
	return walkForCycle(graph, id, proposedParents)
}

func walkForCycle(graph map[string][]string, target string, startParents []string) error {
	visited := map[string]bool{}
	var walk func(node string) bool
	walk = func(node string) bool {
		if node == target && visited[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		for _, parent := range graph[node] {
			if parent == target {
				return true
			}
			if walk(parent) {
				return true
			}
		}
		return false
	}
	for _, parent := range startParents {
		if parent == target || walk(parent) {
			return &ValidationError{
				Message: "Role inheritance must not contain a cycle",
				Details: map[string][]string{"inherits": {"Introduces a cycle through " + parent}},
			}
		}
	}
	return nil
}

func validateScopes(scopes []string) error {
	if details, ok := rbac.ValidateScopes(scopes); !ok {
		return &ValidationError{Message: "Invalid scope(s)", Details: details}
	}
	if details, ok := rbac.ValidateScopeShape(scopes); !ok {
		return &ValidationError{Message: "Invalid scope(s)", Details: details}
	}
	return nil
}

func mapRepoError(err error, resource, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrRBACNotFound) {
		return &NotFoundError{Resource: resource, ID: id}
	}
	if errors.Is(err, store.ErrRBACUniqueConflict) {
		return &ConflictError{Message: uniqueConflictMessage(resource)}
	}
	return err
}

func uniqueConflictMessage(resource string) string {
	switch resource {
	case auditResourceRole:
		return "A role with this name already exists"
	case resourceClaimMapping:
		return "A claim mapping for this claim value already exists"
	case auditResourceGrant:
		return "This role is already granted to that identity"
	case auditResourceSystemUser:
		return "A service account with this name already exists"
	default:
		return "A resource with these values already exists"
	}
}

func roleToResponse(role *store.RBACRole, scopes, inherits []string) *RBACRoleResponse {
	origin := RBACOriginCustom
	if role.Builtin {
		origin = RBACOriginBuiltin
	}
	return &RBACRoleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		Scopes:      apicontract.NonNilList(scopes),
		Inherits:    inherits,
		CreatedBy:   role.CreatedBy,
		UpdatedBy:   role.UpdatedBy,
		CreatedAt:   ToTimestamp(role.CreatedTime),
		UpdatedAt:   ToTimestamp(role.UpdatedTime),
		Origin:      origin,
	}
}

func claimMappingToResponse(m *store.RBACIdpClaimMapping, roleIDs []string) *RBACClaimMappingResponse {
	if roleIDs == nil {
		roleIDs = []string{}
	}
	return &RBACClaimMappingResponse{
		ID:           m.ID,
		Name:         m.Name,
		OIDCProvider: m.OIDCProvider,
		ClaimKey:     m.ClaimKey,
		ClaimValue:   m.ClaimValue,
		RoleIDs:      roleIDs,
		CreatedBy:    m.CreatedBy,
		UpdatedBy:    m.UpdatedBy,
		CreatedAt:    ToTimestamp(m.CreatedTime),
		UpdatedAt:    ToTimestamp(m.UpdatedTime),
		Origin:       RBACOriginCustom,
	}
}

func systemUserGrantSubject(sub string) store.RoleGrantSubject {
	return store.RoleGrantSubject{Type: store.PendingSubjectSub, Value: sub, Issuer: rbac.SystemUserIssuer}
}
