// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type InvokeScopeResolver func(ctx context.Context, componentID string) (string, error)

type UsersService struct {
	usersRepo           *store.UsersRepo
	rbacRepo            *store.RBACRepo
	identityClaims      *store.UserIdentityClaimsRepo
	identityBindings    *store.EntrypointIdentityBindingsRepo
	composer            *rbac.Composer
	authzLookupHandler  *AuthzLookupHandler
	changes             authzChangeOutbox
	metrics             runtime.Metrics
	agentInvokeScope    InvokeScopeResolver
	workflowInvokeScope InvokeScopeResolver
	budgets             *store.BudgetsRepo
	budgetAssignments   *store.BudgetAssignmentsRepo
	logger              *slog.Logger
	now                 func() time.Time
}

func (s *UsersService) SetGrantRefresh(composer *rbac.Composer, handler *AuthzLookupHandler, metrics runtime.Metrics) {
	s.composer = composer
	s.authzLookupHandler = handler
	s.metrics = metrics
}

func (s *UsersService) SetInvokeScopeResolvers(agentScope, workflowScope InvokeScopeResolver) {
	s.agentInvokeScope = agentScope
	s.workflowInvokeScope = workflowScope
}

func NewUsersService(usersRepo *store.UsersRepo, rbacRepo *store.RBACRepo, logger *slog.Logger) *UsersService {
	return &UsersService{
		usersRepo: usersRepo,
		rbacRepo:  rbacRepo,
		changes:   authzChangeOutbox{db: usersRepo},
		logger:    logger.With("logger_name", "platform-users"),
		now:       time.Now,
	}
}

func (s *UsersService) WithAuthzOutbox(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) {
	s.changes.repo = repo
	s.changes.notifier = notifier
}

func (s *UsersService) SetBudgets(budgets *store.BudgetsRepo, assignments *store.BudgetAssignmentsRepo) {
	s.budgets, s.budgetAssignments = budgets, assignments
}

func (s *UsersService) SetIdentityClaims(claims *store.UserIdentityClaimsRepo) {
	s.identityClaims = claims
}

func (s *UsersService) SetIdentityBindings(bindings *store.EntrypointIdentityBindingsRepo) {
	s.identityBindings = bindings
}

func (s *UsersService) GetUserIdentityClaims(ctx context.Context, userID string) (*PaginatedResponse[UserIdentityClaimResponse], error) {
	if err := s.requireIdentityClaims(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.identityClaims.ClaimsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	resp := unpagedResponse(toIdentityClaimResponses(rows))
	return &resp, nil
}

func (s *UsersService) GetUserIdentityClaimsPage(ctx context.Context, userID string, q ListQuery) (*PaginatedResponse[UserIdentityClaimResponse], error) {
	if err := s.requireIdentityClaims(ctx, userID); err != nil {
		return nil, err
	}
	rows, total, err := s.identityClaims.ClaimsForUserPage(ctx, userID, q.offset(), q.PageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[UserIdentityClaimResponse]{
		Data: toIdentityClaimResponses(rows),
		Meta: NewPaginationMeta(q.PageNumber, q.PageSize, total),
	}, nil
}

func (s *UsersService) requireIdentityClaims(ctx context.Context, userID string) error {
	if _, err := s.loadUser(ctx, userID); err != nil {
		return err
	}
	if s.identityClaims == nil {
		return &UnavailableError{Message: "the identity claim projection is not configured on this platform"}
	}
	return nil
}

func toIdentityClaimResponses(rows []store.UserIdentityClaim) []UserIdentityClaimResponse {
	out := make([]UserIdentityClaimResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserIdentityClaimResponse{
			ClaimKey:            row.ClaimKey,
			ClaimValueCanonical: row.ClaimValueCanonical,
			Verified:            row.Verified,
			UpdatedAt:           Timestamp(row.UpdatedTime),
		})
	}
	return out
}

func (s *UsersService) RevokeUserIdentityClaims(ctx context.Context, userID, actor string) (*RevokeIdentityClaimsResponse, error) {
	if _, err := s.loadUser(ctx, userID); err != nil {
		return nil, err
	}
	if s.identityClaims == nil {
		return nil, &UnavailableError{Message: "the identity claim projection is not configured on this platform"}
	}
	var bindingsReset, removed int64
	if err := s.changes.commit(ctx, authzOutboxUsersChanged, func(tx *dbutil.Tx) (string, error) {
		var err error
		if bindingsReset, err = s.resetIdentityBindingsInTx(ctx, tx, userID); err != nil {
			return "", err
		}
		if removed, err = s.identityClaims.DeleteForUserInTx(ctx, tx, userID); err != nil {
			return "", err
		}
		return userID, nil
	}); err != nil {
		return nil, err
	}
	if s.authzLookupHandler != nil {
		s.authzLookupHandler.InvalidateAll()
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionDeleted, auditResourceIdentityClaims, userID, actor,
		"revoked", removed, "bindingsReset", bindingsReset)
	return &RevokeIdentityClaimsResponse{Revoked: int(removed), BindingsReset: int(bindingsReset)}, nil
}

func (s *UsersService) resetIdentityBindingsInTx(ctx context.Context, tx *dbutil.Tx, userID string) (int64, error) {
	if s.identityBindings == nil {
		return 0, nil
	}
	claims, err := s.identityClaims.ClaimsForUserInTx(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	valuesByKey := make(map[string][]string, len(claims))
	for _, c := range claims {
		valuesByKey[c.ClaimKey] = append(valuesByKey[c.ClaimKey], c.ClaimValueCanonical)
	}
	var total int64
	for _, key := range slices.Sorted(maps.Keys(valuesByKey)) {
		n, delErr := s.identityBindings.DeleteForValuesInTx(ctx, tx, key, valuesByKey[key])
		if delErr != nil {
			return total, delErr
		}
		total += n
	}
	n, err := s.identityBindings.DeleteForUserInTx(ctx, tx, userID)
	if err != nil {
		return total, err
	}
	return total + n, nil
}

func (s *UsersService) GetUserRoleGrants(ctx context.Context, userID string) (*UserRoleGrantsResponse, error) {
	if _, err := s.loadUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.rbacRepo.ListRolesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	roleIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		roleIDs = append(roleIDs, row.RoleID)
	}
	return &UserRoleGrantsResponse{RoleIDs: roleIDs}, nil
}

func (s *UsersService) SetUserRoleGrants(ctx context.Context, userID string, roleIDs []string, actor string, actorScopes []string) (*UserRoleGrantsResponse, error) {
	u, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rbac.IsReservedSystemUserSub(u.Sub) {
		logRBACAudit(s.logger, auditActionRejected, auditResourceGrant, userID, actor)
		return nil, errBuiltinSystemUserImmutable
	}
	deduped, err := s.validateRoleIDs(ctx, roleIDs)
	if err != nil {
		return nil, err
	}
	current, err := s.rbacRepo.ListRolesForUser(ctx, userID)
	if err != nil {
		return nil, mapRepoError(err, auditResourceGrant, userID)
	}
	priorRoleIDs := make([]string, 0, len(current))
	for _, row := range current {
		priorRoleIDs = append(priorRoleIDs, row.RoleID)
	}
	if regrantErr := (regrantCheck{
		composer: s.composer, logger: withAuditCredential(ctx, s.logger),
		actor: actor, actorScopes: actorScopes,
		resource: auditResourceGrant, roleIDs: deduped,
	}).enforceRoleSetChange(priorRoleIDs, deduped); regrantErr != nil {
		return nil, regrantErr
	}
	var subject store.RoleGrantSubject
	if u.ProvisionedVia == rbac.ProvisionedViaSystem {
		subject = store.RoleGrantSubject{Type: store.PendingSubjectSub, Value: u.Sub, Issuer: u.Issuer}
	}
	if err := s.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		return userID, s.rbacRepo.SetUserRolesWithSubjectInTx(ctx, tx, userID, deduped, actor, subject)
	}); err != nil {
		return nil, mapRepoError(err, auditResourceGrant, userID)
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionUpdated, auditResourceGrant, userID, actor)
	refreshRBACState(ctx, s.composer, s.authzLookupHandler, s.metrics, s.logger)
	return &UserRoleGrantsResponse{RoleIDs: deduped}, nil
}

func (s *UsersService) loadUser(ctx context.Context, userID string) (*store.User, error) {
	u, _, err := s.usersRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, &NotFoundError{Resource: "user", ID: userID}
	}
	return u, nil
}

func (s *UsersService) validateRoleIDs(ctx context.Context, roleIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(roleIDs))
	deduped := make([]string, 0, len(roleIDs))
	var unknown []string
	for _, id := range roleIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		role, _, _, err := s.rbacRepo.GetRoleByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if role == nil {
			unknown = append(unknown, id)
			continue
		}
		deduped = append(deduped, id)
	}
	if len(unknown) > 0 {
		return nil, &ValidationError{
			Message: fmt.Sprintf("Unknown roleId(s): %v", unknown),
			Details: map[string][]string{"roleIds": {fmt.Sprintf("Not known role id(s): %v", unknown)}},
		}
	}
	return deduped, nil
}

type UserListQuery struct {
	PageNumber   int
	PageSize     int
	SortBy       string
	SortOrder    string
	Search       string
	Group        string
	OIDCProvider string
	RoleID       string
	BudgetID     string
	ExpandBudget bool
}

type ComponentAccessQuery struct {
	PageNumber int
	PageSize   int
	Search     string
}

var usersSortColumns = map[string]string{
	"displayName": "display_name",
	"email":       "email",
	"lastLoginAt": "last_login_at",
	"createdAt":   "created_time",
	"updatedAt":   "updated_time",
}

func resolveUsersSort(sortBy, sortOrder string) (column, order string, err error) {
	if sortBy == "" {
		return "updated_time", "DESC", nil
	}
	column, ok := usersSortColumns[sortBy]
	if !ok {
		return "", "", &ValidationError{Message: fmt.Sprintf("Unknown sort field %q", sortBy)}
	}
	order = "ASC"
	if strings.EqualFold(sortOrder, "desc") {
		order = "DESC"
	}
	return column, order, nil
}

func (s *UsersService) List(ctx context.Context, q UserListQuery) (*PaginatedResponse[UserResponse], error) {
	sortColumn, sortOrder, err := resolveUsersSort(q.SortBy, q.SortOrder)
	if err != nil {
		return nil, err
	}

	var budget *store.UserBudgetMatch
	if q.BudgetID != "" {
		budget, err = s.budgetMatch(ctx, q.BudgetID)
		if err != nil {
			return nil, err
		}
		if budget == nil {
			return &PaginatedResponse[UserResponse]{Data: []UserResponse{}, Meta: NewPaginationMeta(q.PageNumber, q.PageSize, 0)}, nil
		}
	}

	var users []store.User
	var total int
	if q.RoleID != "" {
		users, total, err = s.listUsersByRole(ctx, q, budget, sortColumn, sortOrder)
	} else {
		opts := store.UserListOptions{
			PageNumber:   q.PageNumber,
			PageSize:     q.PageSize,
			SortColumn:   sortColumn,
			SortOrder:    sortOrder,
			Search:       q.Search,
			Group:        q.Group,
			OIDCProvider: q.OIDCProvider,
			ExcludeSubs:  rbac.UserListingHiddenSubs(),
			Budget:       budget,
		}
		users, total, err = s.usersRepo.ListUsers(ctx, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	ids := make([]string, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	groupsByID, err := s.usersRepo.GroupsForUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}

	data := make([]UserResponse, len(users))
	for i := range users {
		u := &users[i]
		data[i] = toUserResponse(u, groupsByID[u.ID], s.userRoleAssignments(u))
	}
	if q.ExpandBudget {
		if err := s.AttachBudgets(ctx, data); err != nil {
			return nil, err
		}
	}

	return &PaginatedResponse[UserResponse]{
		Data: data,
		Meta: NewPaginationMeta(q.PageNumber, q.PageSize, total),
	}, nil
}

func (s *UsersService) AttachBudgets(ctx context.Context, users []UserResponse) error {
	ids := make([]string, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	applied, err := appliedUserBudgets(ctx, s.budgetAssignments, s.budgets, ids, s.now().UnixMilli())
	if err != nil {
		return err
	}
	for i := range users {
		subject := applied[users[i].ID]
		users[i].Budget = expandedTo(subject.Budget)
		users[i].BudgetTopUp = expandedTo(subject.TopUp)
		users[i].EffectiveBudgetAmount = expandedTo(subject.EffectiveAmount)
	}
	return nil
}

func (s *UsersService) GetByID(ctx context.Context, id string) (*UserResponse, error) {
	user, groups, err := s.usersRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user == nil {
		return nil, &NotFoundError{Resource: "user", ID: id}
	}
	resp := toUserResponse(user, groups, s.userRoleAssignments(user))
	return &resp, nil
}

func (s *UsersService) listUsersByRole(ctx context.Context, q UserListQuery, budget *store.UserBudgetMatch, sortColumn, sortOrder string) ([]store.User, int, error) {
	dbRole, _, _, err := s.rbacRepo.GetRoleByID(ctx, q.RoleID)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve roleId: %w", err)
	}
	if dbRole == nil && s.composer != nil && !yamlHasRoleName(s.composer.YAMLState(), q.RoleID) {
		return []store.User{}, 0, nil
	}
	if s.composer == nil {
		s.logger.Error("roleId filter cannot resolve role assignments: composer not configured", "roleId", q.RoleID)
		return nil, 0, &UnavailableError{Message: errComposerUnavailable}
	}
	if _, ok := sortColumnComparators[sortColumn]; !ok {
		return nil, 0, fmt.Errorf("listUsersByRole: unsupported sort column %q", sortColumn)
	}

	users, err := s.listableUsers(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list all users: %w", err)
	}
	var groupMembers map[string]struct{}
	if q.Group != "" {
		ids, groupErr := s.usersRepo.UserIDsByGroups(ctx, []string{q.Group})
		if groupErr != nil {
			return nil, 0, fmt.Errorf("resolve group membership: %w", groupErr)
		}
		groupMembers = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			groupMembers[id] = struct{}{}
		}
	}

	var budgetByUser map[string]string
	if budget != nil {
		budgetByUser, err = s.budgetAssignments.UserBudgetIDs(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("resolve budget assignments: %w", err)
		}
	}

	search := strings.TrimSpace(q.Search)
	matched := make([]store.User, 0)
	for i := range users {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		u := &users[i]
		if groupMembers != nil {
			if _, ok := groupMembers[u.ID]; !ok {
				continue
			}
		}
		if q.OIDCProvider != "" && u.OIDCProvider != q.OIDCProvider {
			continue
		}
		if search != "" && !containsFold(derefStr(u.DisplayName), search) && !containsFold(derefStr(u.Email), search) {
			continue
		}
		if budget != nil && !budgetApplies(budget, budgetByUser, u.ID) {
			continue
		}
		matches, err := s.hasRole(u, q.RoleID)
		if err != nil {
			s.logger.Error("roleId filter cannot resolve role assignments; refusing the request",
				"roleId", q.RoleID, "userId", u.ID, "error", causeOrErr(err))
			return nil, 0, err
		}
		if !matches {
			continue
		}
		matched = append(matched, *u)
	}

	slices.SortFunc(matched, func(a, b store.User) int {
		return compareUsersForSort(a, b, sortColumn, sortOrder)
	})

	total := len(matched)
	return pageOf(matched, q.PageNumber, q.PageSize), total, nil
}

func (s *UsersService) budgetMatch(ctx context.Context, budgetID string) (*store.UserBudgetMatch, error) {
	if err := validatePathUUID(budgetID); err != nil {
		return nil, &ValidationError{Message: "budgetId must be a UUID"}
	}
	return s.budgets.UserMatch(ctx, budgetID)
}

func budgetApplies(m *store.UserBudgetMatch, assignedBudgets map[string]string, userID string) bool {
	if assigned, ok := assignedBudgets[strings.ToLower(userID)]; ok {
		return assigned == m.BudgetID
	}
	return m.IsDefault
}

func (s *UsersService) hasRole(u *store.User, roleID string) (bool, error) {
	assignments, err := s.resolveRoleAssignments(u)
	if err != nil {
		return false, err
	}
	for _, a := range assignments {
		if a.RoleID == roleID {
			return true, nil
		}
	}
	return false, nil
}

var sortColumnComparators = map[string]func(a, b store.User) int{
	"display_name": func(a, b store.User) int {
		return compareFold(cmp.Or(derefStr(a.DisplayName), derefStr(a.Email)), cmp.Or(derefStr(b.DisplayName), derefStr(b.Email)))
	},
	"email": func(a, b store.User) int {
		return compareFold(derefStr(a.Email), derefStr(b.Email))
	},
	"last_login_at": func(a, b store.User) int {
		av, bv := int64(math.MaxInt64), int64(math.MaxInt64)
		if a.LastLoginAt != nil {
			av = *a.LastLoginAt
		}
		if b.LastLoginAt != nil {
			bv = *b.LastLoginAt
		}
		return cmp.Compare(av, bv)
	},
	"created_time": func(a, b store.User) int { return cmp.Compare(a.CreatedTime, b.CreatedTime) },
	"updated_time": func(a, b store.User) int { return cmp.Compare(a.UpdatedTime, b.UpdatedTime) },
}

func compareUsersForSort(a, b store.User, sortColumn, sortOrder string) int {
	primary := sortColumnComparators[sortColumn](a, b)
	if sortOrder == "DESC" {
		primary = -primary
	}
	if primary != 0 {
		return primary
	}
	return strings.Compare(b.ID, a.ID)
}

func (s *UsersService) ListAgentAccess(ctx context.Context, agentID string, q ComponentAccessQuery) (*PaginatedResponse[ComponentAccessUser], error) {
	return s.listComponentAccess(ctx, s.agentInvokeScope, agentID, q)
}

func (s *UsersService) ListWorkflowAccess(ctx context.Context, workflowID string, q ComponentAccessQuery) (*PaginatedResponse[ComponentAccessUser], error) {
	return s.listComponentAccess(ctx, s.workflowInvokeScope, workflowID, q)
}

func (s *UsersService) listComponentAccess(ctx context.Context, resolve InvokeScopeResolver, componentID string, q ComponentAccessQuery) (*PaginatedResponse[ComponentAccessUser], error) {
	if resolve == nil {
		return nil, fmt.Errorf("invoke-scope resolver not configured")
	}
	required, err := resolve(ctx, componentID)
	if err != nil {
		return nil, err
	}
	if s.composer == nil {
		return nil, fmt.Errorf("invoke-scope reverse lookup not configured: composer unavailable")
	}
	users, err := s.listableUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	requiredScopes := []string{required}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	matched := make([]ComponentAccessUser, 0)
	for i := range users {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		scopes, scopeErr := s.userEffectiveScopes(&users[i])
		if scopeErr != nil {
			return nil, fmt.Errorf("resolve effective scopes: %w", scopeErr)
		}
		if !rbac.CheckScopes(scopes, requiredScopes) {
			continue
		}
		accessUser := toComponentAccessUser(&users[i])
		if matchesComponentAccessSearch(accessUser, search) {
			matched = append(matched, accessUser)
		}
	}
	slices.SortFunc(matched, func(a, b ComponentAccessUser) int {
		if byLabel := strings.Compare(strings.ToLower(componentAccessLabel(a)), strings.ToLower(componentAccessLabel(b))); byLabel != 0 {
			return byLabel
		}
		return strings.Compare(a.ID, b.ID)
	})

	return &PaginatedResponse[ComponentAccessUser]{
		Data: pageOf(matched, q.PageNumber, q.PageSize),
		Meta: NewPaginationMeta(q.PageNumber, q.PageSize, len(matched)),
	}, nil
}

func toComponentAccessUser(u *store.User) ComponentAccessUser {
	return ComponentAccessUser{
		ID:           u.ID,
		DisplayName:  derefStr(u.DisplayName),
		UserName:     derefStr(u.UserName),
		Email:        derefStr(u.Email),
		IsSystemUser: u.ProvisionedVia == rbac.ProvisionedViaSystem,
	}
}

func componentAccessLabel(u ComponentAccessUser) string {
	return cmp.Or(u.DisplayName, u.UserName, u.Email, u.ID)
}

func matchesComponentAccessSearch(u ComponentAccessUser, lowerSearch string) bool {
	if lowerSearch == "" {
		return true
	}
	for _, field := range []string{u.DisplayName, u.UserName, u.Email} {
		if strings.Contains(strings.ToLower(field), lowerSearch) {
			return true
		}
	}
	return false
}

func pageOf[T any](all []T, pageNumber, pageSize int) []T {
	start := (pageNumber - 1) * pageSize
	if start < 0 || start >= len(all) {
		return []T{}
	}
	return all[start:min(start+pageSize, len(all))]
}

func (s *UsersService) userEffectiveScopes(u *store.User) ([]string, error) {
	identity, gctx, err := userLookupContext(u)
	if err != nil {
		s.logger.Warn("user claims unparseable; access reverse-lookup failed for this user", "userId", u.ID, "oidcProvider", u.OIDCProvider, "error", err)
		return nil, err
	}
	res, err := s.composer.Lookup(identity, "", gctx)
	if err != nil {
		return nil, fmt.Errorf("composer lookup: %w", err)
	}
	return res.Scopes, nil
}

var errClaimsNotJSON = errors.New("stored claims are not valid JSON, so claim-derived roles are absent")

func userLookupContext(u *store.User) (string, map[string]any, error) {
	identity := derefStr(u.Email)
	if identity == "" {
		identity = u.Sub
	}
	gctx := map[string]any{
		"sam_user_id":   u.ID,
		"oidc_provider": u.OIDCProvider,
	}
	if u.Source != "" {
		var claims map[string]any
		if err := json.Unmarshal([]byte(u.Source), &claims); err != nil {
			return identity, gctx, errClaimsNotJSON
		}
		gctx["idp_claims"] = claims
	}
	return identity, gctx, nil
}

func (s *UsersService) userRoleAssignments(u *store.User) []UserRoleAssignment {
	assignments, err := s.resolveRoleAssignments(u)
	if err != nil {
		s.logger.Warn("resolve role assignments failed", "userId", u.ID, "error", causeOrErr(err))
		return []UserRoleAssignment{}
	}
	return assignments
}

func causeOrErr(err error) error {
	if cause := errors.Unwrap(err); cause != nil {
		return cause
	}
	return err
}

const errComposerUnavailable = "Authorization state is unavailable on this instance"

func (s *UsersService) resolveRoleAssignments(u *store.User) ([]UserRoleAssignment, error) {
	if s.composer == nil {
		return nil, &UnavailableError{Message: errComposerUnavailable}
	}
	identity, gctx, err := userLookupContext(u)
	if err != nil {
		s.logger.Warn("user claims unusable; claim-derived roles skipped, direct/yaml/default still resolved", "userId", u.ID, "oidcProvider", u.OIDCProvider, "error", err)
	}
	assignments, err := s.composer.ResolveRoleAssignments(identity, "", gctx)
	if err != nil {
		return nil, &UnavailableError{Message: errComposerUnavailable, Cause: err}
	}
	out := make([]UserRoleAssignment, 0, len(assignments))
	for _, a := range assignments {
		out = append(out, UserRoleAssignment{
			RoleID:     a.RoleID,
			Source:     a.Source,
			Manageable: a.Source == rbac.SourceDirect && !rbac.IsReservedSystemUserSub(u.Sub),
		})
	}
	return out, nil
}

func (s *UsersService) listableUsers(ctx context.Context) ([]store.User, error) {
	users, err := s.usersRepo.ListAllUsers(ctx)
	if err != nil {
		return nil, err
	}
	hidden := rbac.UserListingHiddenSubs()
	return slices.DeleteFunc(users, func(u store.User) bool { return slices.Contains(hidden, u.Sub) }), nil
}

func toUserResponse(u *store.User, groups []string, roles []UserRoleAssignment) UserResponse {
	if groups == nil {
		groups = []string{}
	}
	if roles == nil {
		roles = []UserRoleAssignment{}
	}
	return UserResponse{
		ID:             u.ID,
		DisplayName:    derefStr(u.DisplayName),
		UserName:       derefStr(u.UserName),
		Email:          derefStr(u.Email),
		Issuer:         u.Issuer,
		Sub:            u.Sub,
		OIDCProvider:   u.OIDCProvider,
		ProvisionedVia: u.ProvisionedVia,
		Active:         u.Active,
		Groups:         groups,
		Roles:          roles,
		LastLoginAt:    optTimestamp(u.LastLoginAt),
		CreatedBy:      u.CreatedBy,
		UpdatedBy:      u.UpdatedBy,
		CreatedAt:      Timestamp(u.CreatedTime),
		UpdatedAt:      Timestamp(u.UpdatedTime),
	}
}

func optTimestamp(v *int64) Timestamp {
	if v == nil {
		return Timestamp(0)
	}
	return Timestamp(*v)
}
