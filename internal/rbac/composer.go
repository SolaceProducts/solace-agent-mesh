// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type DBRole struct {
	ID          string
	Name        string
	Description string
	Scopes      []string
	Parents     []string
}

type DBClaimMapping struct {
	OIDCProvider string
	ClaimKey     string
	ClaimValue   string
	RoleID       string
}

type DBUserRole struct {
	UserID       string
	RoleID       string
	SubjectType  string
	SubjectValue string
	Issuer       string
}

const subjectTypeSub = "sub"

type DBClaimMappingEntity struct {
	OIDCProvider string
	ClaimKey     string
	ClaimValue   string
	RoleIDs      []string
}

type DBState struct {
	Roles          []DBRole
	UserRoles      []DBUserRole
	ClaimMappings  []DBClaimMappingEntity
	DefaultRoleIDs []string
}

const snapshotTimeout = 10 * time.Second

type DBSource interface {
	Snapshot(ctx context.Context) (*DBState, error)
}

type Composer struct {
	yaml   YAMLState
	source DBSource
	logger *slog.Logger
	state  atomic.Pointer[composedState]

	rebuildMu       sync.Mutex
	snapshotTimeout time.Duration
}

type YAMLState struct {
	Roles             RolesConfig
	Users             UsersConfig
	DefaultRoles      []string
	ClaimMappings     []DBClaimMapping
	ClaimOIDCProvider string
	ClaimKey          string
}

type composedState struct {
	engine                     *RoleEngine
	defaults                   []string
	directAssignByUser         map[string][]string
	directAssignBySubject      map[string][]string
	claimLookup                map[claimLookupKey][]string
	bareNameToKey              map[string]string
	displayName                map[string]string
	emailFold                  *EmailFoldIndex
	collisions                 []string
	unresolvedRefs             []string
	inertClaimMappings         []inertClaimMapping
	mappingsInheritingSentinel int
}

type inertClaimMapping struct {
	provider   string
	claimValue string
}

func (s *composedState) resolveBareName(name string) string {
	return resolveBareNameKey(s.bareNameToKey, name)
}

func (s *composedState) roleNames(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	var out []string
	for _, k := range keys {
		name := s.displayName[k]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

type claimLookupKey struct {
	oidcProvider string
	claimKey     string
	claimValue   string
}

const (
	yamlKeyPrefix = "yaml:"
	dbKeyPrefix   = "db:"

	deprecatedGatewayPrefix = "gateway_specific_identities:"
)

func yamlRoleKey(name string) string { return yamlKeyPrefix + name }
func dbRoleKey(id string) string     { return dbKeyPrefix + id }

func resolveBareNameKey(bareNameToKey map[string]string, name string) string {
	if key, ok := bareNameToKey[name]; ok {
		return key
	}
	return yamlRoleKey(name)
}

const msgRoleNameCollision = "rbac role name defined in both YAML and DB; roles kept distinct so each origin's assignments resolve independently"

const msgUnresolvedRoleRef = "rbac YAML role reference matches no defined role; the assignment grants no scopes (check for a typo)"

const msgShadowedReservedSub = "rbac DB grants target a built-in service account; its embedded roles union with them, so the principal holds more than the grants give — move the grants to a new service account and repoint run_as at it"

const msgInertClaimMapping = "rbac claim mapping oidcProvider is a placeholder, not the configured provider; the mapping grants no roles"

const msgSentinelConfiguredProvider = "rbac claim mappings inherited a placeholder oidcProvider from idp_claims_config; they grant no roles unless the deployment's IdP is genuinely named that"

type Resolution struct {
	Roles        []string
	Scopes       []string
	DefaultRoles bool
}

func NewComposer(yaml YAMLState, source DBSource, logger *slog.Logger) *Composer {
	c := &Composer{
		yaml:   yaml,
		source: source,
		logger: logger.With("logger_name", "rbac-composer"),

		snapshotTimeout: snapshotTimeout,
	}
	warnOnDeprecatedYAMLKeys(yaml.Users.Users, c.logger)
	return c
}

func warnOnDeprecatedYAMLKeys(users map[string]UserAssignment, logger *slog.Logger) {
	const maxKeyLen = 128
	for key := range users {
		if strings.HasPrefix(key, deprecatedGatewayPrefix) {
			loggedKey := key
			if len(loggedKey) > maxKeyLen {
				loggedKey = loggedKey[:maxKeyLen] + "..."
			}
			logger.Warn("gateway-scoped YAML identity key ignored; gateway-scoped role assignment is deprecated and has no effect",
				"key", loggedKey)
		}
	}
}

func (c *Composer) YAMLState() YAMLState { return c.yaml }

func (c *Composer) Rebuild(ctx context.Context) error {
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	var db *DBState
	if c.source != nil {
		snapCtx, cancel := context.WithTimeout(ctx, c.snapshotTimeout)
		var err error
		db, err = c.source.Snapshot(snapCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("composer: snapshot db: %w", err)
		}
	}
	if db == nil {
		db = &DBState{}
	}

	state, err := compose(c.yaml, db)
	if err != nil {
		return fmt.Errorf("composer: compose: %w", err)
	}
	c.state.Store(state)
	for _, name := range state.collisions {
		c.logger.Warn(msgRoleNameCollision, "role", name)
	}
	for _, name := range state.unresolvedRefs {
		c.logger.Warn(msgUnresolvedRoleRef, "role", name)
	}
	shadowed := shadowedReservedSubs(state.directAssignBySubject)
	for _, sub := range shadowed {
		c.logger.Warn(msgShadowedReservedSub, "systemUserSub", sub)
	}
	for _, m := range state.inertClaimMappings {
		c.logger.Warn(msgInertClaimMapping,
			"oidcProvider", m.provider,
			"claimValue", m.claimValue,
			"configuredOidcProvider", c.yaml.ClaimOIDCProvider)
	}
	if state.mappingsInheritingSentinel > 0 {
		c.logger.Warn(msgSentinelConfiguredProvider,
			"configuredOidcProvider", c.yaml.ClaimOIDCProvider,
			"affectedClaimMappings", state.mappingsInheritingSentinel)
	}
	c.logger.Info("rbac composer rebuilt",
		"yamlRoles", len(c.yaml.Roles.Roles),
		"dbRoles", len(db.Roles),
		"yamlUsers", len(c.yaml.Users.Users),
		"dbUserRoles", len(db.UserRoles),
		"defaults", len(state.defaults),
		"nameCollisions", len(state.collisions),
		"unresolvedRoleRefs", len(state.unresolvedRefs),
		"shadowedReservedSubs", len(shadowed),
		"inertClaimMappings", len(state.inertClaimMappings),
		"claimMappingsInheritingSentinel", state.mappingsInheritingSentinel,
	)
	return nil
}

func shadowedReservedSubs(directBySubject map[string][]string) []string {
	var out []string
	for sub := range directBySubject {
		if IsReservedSystemUserSub(sub) {
			out = append(out, sub)
		}
	}
	slices.Sort(out)
	return out
}

const (
	SourceDirect  = "direct"
	SourceClaim   = "claim"
	SourceYAML    = "yaml"
	SourceDefault = "default"
)

type RoleAssignment struct {
	RoleID string
	Source string
}

func (c *Composer) ResolveRoleAssignments(userID, gatewayID string, gatewayContext map[string]any) ([]RoleAssignment, error) {
	state := c.state.Load()
	if state == nil {
		return nil, fmt.Errorf("rbac composer: not initialized (Rebuild not called)")
	}

	var out []RoleAssignment
	seen := make(map[string]bool)
	emit := func(source string, keys ...string) {
		for _, k := range keys {
			if k == "" {
				continue
			}
			id := unqualifyRoleKey(k)
			dedup := source + "|" + id
			if seen[dedup] {
				continue
			}
			seen[dedup] = true
			out = append(out, RoleAssignment{RoleID: id, Source: source})
		}
	}

	if assignment, ok := state.emailFold.ResolveBare(c.yaml.Users.Users, userID); ok {
		for _, name := range assignment.Roles {
			emit(SourceYAML, state.resolveBareName(name))
		}
	}
	if samUserID, ok := gatewayContext["sam_user_id"].(string); ok && samUserID != "" {
		emit(SourceDirect, state.directAssignByUser[samUserID]...)
	}
	if IsSystemPrincipal(userID) {
		emit(SourceDirect, state.directAssignBySubject[userID]...)
	}
	emit(SourceClaim, claimRoles(state, gatewayContext)...)
	if len(out) == 0 && !IsSystemPrincipal(userID) {
		emit(SourceDefault, state.defaults...)
	}
	return out, nil
}

func (c *Composer) ResolveRoleRef(name string) string {
	state := c.state.Load()
	if state == nil {
		return name
	}
	return unqualifyRoleKey(resolveBareNameKey(state.bareNameToKey, name))
}

func unqualifyRoleKey(key string) string {
	if v := strings.TrimPrefix(key, dbKeyPrefix); v != key {
		return v
	}
	return strings.TrimPrefix(key, yamlKeyPrefix)
}

func (c *Composer) Lookup(userID, gatewayID string, gatewayContext map[string]any) (Resolution, error) {
	state := c.state.Load()
	if state == nil {
		return Resolution{}, fmt.Errorf("rbac composer: not initialized (Rebuild not called)")
	}

	seen := make(map[string]bool)
	var keys []string
	add := func(ks ...string) {
		for _, k := range ks {
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}

	if assignment, ok := state.emailFold.ResolveBare(c.yaml.Users.Users, userID); ok {
		for _, name := range assignment.Roles {
			add(state.resolveBareName(name))
		}
	}

	samUserID, _ := gatewayContext["sam_user_id"].(string)
	if samUserID != "" {
		add(state.directAssignByUser[samUserID]...)
	}

	if IsSystemPrincipal(userID) {
		add(state.directAssignBySubject[userID]...)
	}

	add(claimRoles(state, gatewayContext)...)

	defaultRoles := len(keys) == 0 && !IsSystemPrincipal(userID)
	if defaultRoles {
		c.logDefaultRolesApplied(samUserID, len(state.defaults))
		add(state.defaults...)
	}

	scopes := state.engine.ResolveRoleScopes(keys)
	return Resolution{Roles: state.roleNames(keys), Scopes: scopes, DefaultRoles: defaultRoles}, nil
}

func (c *Composer) logDefaultRolesApplied(samUserID string, defaultRoles int) {
	attrs := []any{"defaultRoles", defaultRoles}
	if samUserID != "" {
		attrs = append(attrs, "samUserID", samUserID)
	}
	c.logger.Debug("identity matched no YAML or DB role assignment; applying default roles", attrs...)
}

func (c *Composer) ScopesForRoleIDs(roleIDs []string) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("rbac: composer unavailable")
	}
	state := c.state.Load()
	if state == nil {
		return nil, fmt.Errorf("rbac: role state not initialized")
	}
	keys := make([]string, 0, len(roleIDs))
	for _, id := range roleIDs {
		key := dbRoleKey(id)
		if !state.engine.HasRole(key) {
			return nil, fmt.Errorf("rbac: role %s absent from the composed state", id)
		}
		keys = append(keys, key)
	}
	return state.engine.ResolveRoleScopes(keys), nil
}

var errClaimSourceNotJSON = errors.New("claims are not valid JSON")

func (c *Composer) MappedClaimsChanged(beforeSource, afterSource string) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("rbac: composer unavailable")
	}
	state := c.state.Load()
	if state == nil {
		return false, fmt.Errorf("rbac: role state not initialized")
	}
	before, err := parseClaimSource(beforeSource)
	if err != nil {
		return false, fmt.Errorf("rbac: parse stored claims: %w", err)
	}
	after, err := parseClaimSource(afterSource)
	if err != nil {
		return false, fmt.Errorf("rbac: parse incoming claims: %w", err)
	}
	checked := make(map[string]bool, len(state.claimLookup))
	for lookupKey := range state.claimLookup {
		if checked[lookupKey.claimKey] {
			continue
		}
		checked[lookupKey.claimKey] = true
		if !sameClaimValues(before[lookupKey.claimKey], after[lookupKey.claimKey]) {
			return true, nil
		}
	}
	return false, nil
}

func parseClaimSource(source string) (map[string][]string, error) {
	if source == "" {
		return nil, nil
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(source), &claims); err != nil {
		return nil, errClaimSourceNotJSON
	}
	return claimsAsMap(claims), nil
}

func sameClaimValues(before, after []string) bool {
	if len(before) != len(after) {
		return false
	}
	if len(before) == 0 {
		return true
	}
	sortedBefore := slices.Sorted(slices.Values(before))
	sortedAfter := slices.Sorted(slices.Values(after))
	return slices.Equal(sortedBefore, sortedAfter)
}

func claimRoles(state *composedState, gatewayContext map[string]any) []string {
	if state == nil || gatewayContext == nil {
		return nil
	}
	provider, _ := gatewayContext["oidc_provider"].(string)
	claims, ok := gatewayContext["idp_claims"].(map[string]any)
	if !ok {
		return nil
	}

	seen := make(map[string]bool)
	var out []string
	for key, vals := range claimsAsMap(claims) {
		for _, val := range vals {
			lookupKey := claimLookupKey{oidcProvider: provider, claimKey: key, claimValue: val}
			for _, role := range state.claimLookup[lookupKey] {
				if !seen[role] {
					seen[role] = true
					out = append(out, role)
				}
			}
		}
	}
	return out
}

func claimsAsMap(claims map[string]any) map[string][]string {
	out := make(map[string][]string, len(claims))
	for k, v := range claims {
		switch val := v.(type) {
		case string:
			out[k] = []string{val}
		case []string:
			out[k] = val
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok {
					out[k] = append(out[k], s)
				}
			}
		}
	}
	return out
}

func compose(yaml YAMLState, db *DBState) (*composedState, error) {
	merged := RolesConfig{Roles: make(map[string]RoleDefinition)}
	displayName := make(map[string]string)
	bareNameToKey := make(map[string]string)

	for name := range yaml.Roles.Roles {
		key := yamlRoleKey(name)
		displayName[key] = name
		bareNameToKey[name] = key
	}

	roleKeyByID := make(map[string]string, len(db.Roles))
	for _, r := range db.Roles {
		if r.Name == "" {
			return nil, fmt.Errorf("compose: db role %s has empty name", r.ID)
		}
		key := dbRoleKey(r.ID)
		roleKeyByID[r.ID] = key
		displayName[key] = r.Name
		if _, ok := bareNameToKey[r.Name]; !ok {
			bareNameToKey[r.Name] = key
		}
	}

	resolveBareName := func(name string) string {
		return resolveBareNameKey(bareNameToKey, name)
	}

	for name, def := range yaml.Roles.Roles {
		inherits := make([]string, 0, len(def.Inherits))
		for _, parent := range def.Inherits {
			inherits = append(inherits, resolveBareName(parent))
		}
		merged.Roles[yamlRoleKey(name)] = RoleDefinition{
			Description: def.Description,
			Scopes:      def.Scopes,
			Inherits:    inherits,
		}
	}
	for _, r := range db.Roles {
		var inherits []string
		for _, pid := range r.Parents {
			if key, ok := roleKeyByID[pid]; ok {
				inherits = append(inherits, key)
			}
		}
		merged.Roles[dbRoleKey(r.ID)] = RoleDefinition{
			Description: r.Description,
			Scopes:      r.Scopes,
			Inherits:    inherits,
		}
	}

	dbNames := make(map[string]bool, len(db.Roles))
	for _, r := range db.Roles {
		dbNames[r.Name] = true
	}
	var collisions []string
	for name := range yaml.Roles.Roles {
		if dbNames[name] {
			collisions = append(collisions, name)
		}
	}
	slices.Sort(collisions)

	var unresolvedRefs []string
	seenUnresolved := make(map[string]bool)
	collectUnresolved := func(name string) {
		if name == "" || seenUnresolved[name] {
			return
		}
		if _, ok := bareNameToKey[name]; ok {
			return
		}
		seenUnresolved[name] = true
		unresolvedRefs = append(unresolvedRefs, name)
	}
	for identity, assignment := range yaml.Users.Users {
		if strings.HasPrefix(identity, deprecatedGatewayPrefix) {
			continue
		}
		for _, name := range assignment.Roles {
			collectUnresolved(name)
		}
	}
	for _, m := range yaml.ClaimMappings {
		collectUnresolved(m.RoleID)
	}
	slices.Sort(unresolvedRefs)

	var defaults []string
	if len(db.DefaultRoleIDs) > 0 {
		for _, id := range db.DefaultRoleIDs {
			if key, ok := roleKeyByID[id]; ok {
				defaults = appendUnique(defaults, key)
			}
		}
	} else {
		for _, name := range yaml.DefaultRoles {
			defaults = appendUnique(defaults, resolveBareName(name))
		}
	}

	engine, err := NewRoleEngine(merged, UsersConfig{}, defaults)
	if err != nil {
		return nil, err
	}

	directByUser := make(map[string][]string, len(db.UserRoles))
	directBySubject := make(map[string][]string)
	for _, ur := range db.UserRoles {
		key, ok := roleKeyByID[ur.RoleID]
		if !ok {
			continue
		}
		directByUser[ur.UserID] = append(directByUser[ur.UserID], key)
		if ur.SubjectType == subjectTypeSub && IsSystemPrincipal(ur.SubjectValue) {
			directBySubject[ur.SubjectValue] = append(directBySubject[ur.SubjectValue], key)
		}
	}

	claimMap := buildClaimLookup(yaml, db, roleKeyByID, bareNameToKey)

	var inert []inertClaimMapping
	for _, m := range db.ClaimMappings {
		if ProviderCannotMatch(m.OIDCProvider, yaml.ClaimOIDCProvider) {
			inert = append(inert, inertClaimMapping{provider: m.OIDCProvider, claimValue: m.ClaimValue})
		}
	}

	inheritedSentinel := 0
	if IsSentinelProvider(yaml.ClaimOIDCProvider) {
		for _, m := range yaml.ClaimMappings {
			if m.OIDCProvider == yaml.ClaimOIDCProvider {
				inheritedSentinel++
			}
		}
		for _, m := range db.ClaimMappings {
			if m.OIDCProvider == yaml.ClaimOIDCProvider {
				inheritedSentinel++
			}
		}
	}

	return &composedState{
		engine:                     engine,
		defaults:                   defaults,
		directAssignByUser:         directByUser,
		directAssignBySubject:      directBySubject,
		claimLookup:                claimMap,
		bareNameToKey:              bareNameToKey,
		displayName:                displayName,
		emailFold:                  BuildEmailFoldIndex(yaml.Users.Users),
		collisions:                 collisions,
		unresolvedRefs:             unresolvedRefs,
		inertClaimMappings:         inert,
		mappingsInheritingSentinel: inheritedSentinel,
	}, nil
}

func buildClaimLookup(yaml YAMLState, db *DBState, roleKeyByID, bareNameToKey map[string]string) map[claimLookupKey][]string {
	claimMap := make(map[claimLookupKey][]string)
	for _, m := range yaml.ClaimMappings {
		lk := claimLookupKey{oidcProvider: m.OIDCProvider, claimKey: m.ClaimKey, claimValue: m.ClaimValue}
		claimMap[lk] = appendUnique(claimMap[lk], resolveBareNameKey(bareNameToKey, m.RoleID))
	}
	for _, m := range db.ClaimMappings {
		lk := claimLookupKey{oidcProvider: m.OIDCProvider, claimKey: m.ClaimKey, claimValue: m.ClaimValue}
		for _, roleID := range m.RoleIDs {
			key, ok := roleKeyByID[roleID]
			if !ok {
				continue
			}
			claimMap[lk] = appendUnique(claimMap[lk], key)
		}
	}
	return claimMap
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
