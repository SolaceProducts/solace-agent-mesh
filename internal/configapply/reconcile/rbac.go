// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	rbacRolesResource         = platformResource{path: "/rbac/roles"}
	rbacGrantsResource        = platformResource{path: "/rbac/grants"}
	rbacClaimMappingsResource = platformResource{path: "/rbac/claimMappings"}
)

const (
	rbacPageSizeQuery = "?pageSize="

	userCapabilityManageRbac = "manageRbac"

	skipReasonMiniIDP = "mini_idp_mode"

	skipReasonGrantCreateConflicted = "create_conflicted"

	skipReasonGrantHeldByCreate = "held_by_create"
)

func init() {
	generic.Register(generic.Kind{
		Name:          "rbacRole",
		Plural:        "rbacRoles",
		Order:         60,
		CreateProto:   wire.CreateRoleRequest{},
		UpdateProto:   wire.ReplaceRoleRequest{},
		ResponseProto: wire.RoleResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateRoleRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.RoleResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.RoleResponse)
			return r.ID
		},
		ListFilter: func(v any) bool {
			r, _ := v.(wire.RoleResponse)
			return r.Origin == entplatform.RBACOriginCustom
		},
	})
	generic.Register(generic.Kind{
		Name:          "rbacGrant",
		Plural:        "rbacGrants",
		Order:         70,
		CreateProto:   wire.CreateGrantRequest{},
		UpdateProto:   wire.CreateGrantRequest{},
		ResponseProto: wire.GrantResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateGrantRequest)
			return grantKey(c.Subject.Type, c.Subject.Issuer, c.Subject.Value, c.RoleID)
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.GrantResponse)
			return grantResponseKey(r)
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.GrantResponse)
			return r.ID
		},
		ListFilter: func(v any) bool {
			r, _ := v.(wire.GrantResponse)
			return r.Origin != entplatform.RBACOriginYAML
		},
	})
	generic.Register(generic.Kind{
		Name:          "rbacClaimMapping",
		Plural:        "rbacClaimMappings",
		Order:         65,
		CreateProto:   wire.CreateClaimMappingRequest{},
		UpdateProto:   wire.ReplaceClaimMappingRequest{},
		ResponseProto: wire.ClaimMappingResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateClaimMappingRequest)
			return claimMappingKey(c.OIDCProvider, c.ClaimValue)
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.ClaimMappingResponse)
			return claimMappingKey(ptrString(r.OIDCProvider), r.ClaimValue)
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.ClaimMappingResponse)
			return r.ID
		},
		ListFilter: func(v any) bool {
			r, _ := v.(wire.ClaimMappingResponse)
			return r.Origin != entplatform.RBACOriginYAML
		},
	})
}

func grantKey(subjectType, issuer, value, roleID string) string {
	value = entplatform.NormalizeGrantSubjectValue(subjectType, value)
	return subjectType + "|" + issuer + "|" + value + "|" + roleID
}

func grantResponseKey(r wire.GrantResponse) string {
	var subjectType, issuer, value string
	if r.Subject != nil {
		subjectType, issuer, value = r.Subject.Type, ptrString(r.Subject.Issuer), r.Subject.Value
	}
	return grantKey(subjectType, issuer, value, r.RoleID)
}

func claimMappingKey(oidcProvider, claimValue string) string {
	return oidcProvider + "|" + claimValue
}

type rbacFeatureDisabledError struct {
	declaredKinds []string
}

func (e *rbacFeatureDisabledError) Error() string {
	return fmt.Sprintf("RBAC is not enabled on the platform (feature flag \"rbac\" is off). Manifest declares %d rbac* resource kinds. Enable the flag in the platform config or remove these resources from the manifest.", len(e.declaredKinds))
}

type rbacRoleXRefAdapter struct {
	nameToID map[string]string
	idToName map[string]string
	desired  map[string]struct{}
	yaml     map[string]struct{}
	builtin  map[string]struct{}
}

func (a rbacRoleXRefAdapter) LookupID(kind, name string) (string, error) {
	if kind != "rbacRole" {
		return name, nil
	}
	if id, ok := a.nameToID[name]; ok {
		return id, nil
	}
	if _, ok := a.desired[name]; ok {
		return name, nil
	}
	if _, ok := a.yaml[name]; ok {
		return "", fmt.Errorf("rbacRole %q is a YAML-defined role; DB-managed resources cannot reference it. Define a DB role of the same shape, or rely on the YAML-side claim mapping that grants it", name)
	}
	return "", fmt.Errorf("rbacRole %q not found on platform and not declared in the desired manifest", name)
}

func (a rbacRoleXRefAdapter) LookupName(kind, id string) (string, error) {
	if kind != "rbacRole" {
		return id, nil
	}
	if name, ok := a.idToName[id]; ok {
		return name, nil
	}
	return id, nil
}

type rbacRolePlan struct {
	Kind      string
	Creates   []rbacRoleOp
	Updates   []rbacRoleOp
	Unchanged []rbacRoleOp
	Deletes   []rbacRoleOp
}

type rbacRoleOp struct {
	Name       string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateRoleRequest
	Patch      wire.ReplaceRoleRequest
}

type rbacRoleOpResult struct {
	Op     rbacRoleOp
	Verb   string
	Err    error
	Reason string
}

type rbacRoleOutcome struct {
	Kind     string
	Created  []rbacRoleOpResult
	Updated  []rbacRoleOpResult
	Deleted  []rbacRoleOpResult
	Skipped  []rbacRoleOpResult
	Failures []rbacRoleOpResult
}

type rbacGrantPlan struct {
	Kind      string
	Creates   []rbacGrantOp
	Updates   []rbacGrantOp
	Unchanged []rbacGrantOp
	Deletes   []rbacGrantOp
	warnings  []string
}

type rbacGrantOp struct {
	Header     string
	Identity   string
	RoleID     string
	RoleName   string
	Source     string
	Provenance string
	ID         string
	Create     wire.CreateGrantRequest
	Recorded   apiv2.CreateGrantSubject
}

type rbacGrantOpResult struct {
	Op     rbacGrantOp
	Verb   string
	Err    error
	Reason string
}

type rbacGrantOutcome struct {
	Kind     string
	Created  []rbacGrantOpResult
	Updated  []rbacGrantOpResult
	Deleted  []rbacGrantOpResult
	Skipped  []rbacGrantOpResult
	Failures []rbacGrantOpResult
}

type rbacClaimMappingPlan struct {
	Kind      string
	Creates   []rbacClaimMappingOp
	Updates   []rbacClaimMappingOp
	Unchanged []rbacClaimMappingOp
	Deletes   []rbacClaimMappingOp
}

type rbacClaimMappingOp struct {
	Header       string
	Name         string
	OIDCProvider string
	ClaimValue   string
	RoleIDs      []string
	Source       string
	Provenance   string
	ID           string
	Create       wire.CreateClaimMappingRequest
	Patch        wire.ReplaceClaimMappingRequest
}

type rbacClaimMappingOpResult struct {
	Op     rbacClaimMappingOp
	Verb   string
	Err    error
	Reason string
}

type rbacClaimMappingOutcome struct {
	Kind     string
	Created  []rbacClaimMappingOpResult
	Updated  []rbacClaimMappingOpResult
	Deleted  []rbacClaimMappingOpResult
	Skipped  []rbacClaimMappingOpResult
	Failures []rbacClaimMappingOpResult
}

func (c *Client) ListRBACRoles(ctx context.Context) ([]wire.RoleResponse, error) {
	return listResource[wire.RoleResponse](ctx, c, rbacRolesResource)
}

func (c *Client) ListRBACGrants(ctx context.Context) ([]wire.GrantResponse, error) {
	return listResource[wire.GrantResponse](ctx, c, rbacGrantsResource)
}

type grantHolders map[string]*apiv2.GrantUser

func (c *Client) listRBACGrantsWithHolders(ctx context.Context) ([]wire.GrantResponse, grantHolders, error) {
	path := rbacGrantsResource.apiPath() + "?expand=user"
	rows, err := listAllPages[json.RawMessage](ctx, c, path)
	if err != nil {
		return nil, nil, err
	}
	grants := make([]wire.GrantResponse, len(rows))
	holders := grantHolders{}
	for i, row := range rows {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row, &fields); err != nil {
			return nil, nil, fmt.Errorf("GET %s: %w", path, err)
		}
		if _, ok := fields["user"]; !ok {
			return nil, nil, fmt.Errorf("the platform at %s predates GET %s, which this sam CLI needs to plan grants\n  %s",
				urlOrigin(c.resolved.URL), path, upgradeToThisCLIRelease)
		}
		if err := json.Unmarshal(row, &grants[i]); err != nil {
			return nil, nil, fmt.Errorf("GET %s: %w", path, err)
		}
		if grants[i].User != nil {
			holders[grants[i].ID] = grants[i].User
		}
	}
	return grants, holders, nil
}

func (c *Client) UpdateRBACGrant(ctx context.Context, id string, subject apiv2.CreateGrantSubject) error {
	req := wire.UpdateGrantRequest{Subject: wire.Patch[apiv2.CreateGrantSubject]{Set: true, Value: subject}}
	return c.callResource(ctx, rbacGrantsResource, http.MethodPatch, "/"+url.PathEscape(id), req, nil)
}

func (c *Client) CreateRBACGrant(ctx context.Context, req wire.CreateGrantRequest) (wire.GrantResponse, error) {
	var resp wire.GrantResponse
	err := c.callResource(ctx, rbacGrantsResource, http.MethodPost, "", req, &resp)
	var herr *HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusConflict {
		held, found, listErr := c.heldGrant(ctx, req)
		if found {
			return held, nil
		}
		if listErr != nil {
			return wire.GrantResponse{}, fmt.Errorf("%w (looking up the held grant: %v)", err, listErr)
		}
	}
	if err != nil {
		return wire.GrantResponse{}, err
	}
	return resp, nil
}

func (c *Client) heldGrant(ctx context.Context, req wire.CreateGrantRequest) (wire.GrantResponse, bool, error) {
	grants, holders, err := c.listRBACGrantsWithHolders(ctx)
	if err != nil {
		return wire.GrantResponse{}, false, err
	}
	key := grantKey(req.Subject.Type, req.Subject.Issuer, req.Subject.Value, req.RoleID)
	var ids []string
	for _, g := range grants {
		if grantResponseKey(g) == key {
			ids = append(ids, g.ID)
		}
	}
	if len(ids) == 0 {
		ids = grantIDsByHolderKey(grants, holders)[key]
	}
	if len(ids) != 1 {
		return wire.GrantResponse{}, false, nil
	}
	for _, g := range grants {
		if g.ID == ids[0] {
			return g, true, nil
		}
	}
	return wire.GrantResponse{}, false, nil
}

func (c *Client) DeleteRBACGrant(ctx context.Context, id string) error {
	return c.callResource(ctx, rbacGrantsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func RBACGrantDeletePath() string {
	return rbacGrantsResource.apiPath() + "/{id}"
}

func (c *Client) ListRBACClaimMappings(ctx context.Context) ([]wire.ClaimMappingResponse, error) {
	return listResource[wire.ClaimMappingResponse](ctx, c, rbacClaimMappingsResource)
}

func rbacFeatureProbe(ctx context.Context, c *Client) (enabled bool, err error) {
	probeErr := c.do(ctx, http.MethodGet, rbacRolesResource.apiPath()+rbacPageSizeQuery+"1", nil, nil)
	if probeErr == nil {
		return true, nil
	}
	var herr *HTTPError
	if errors.As(probeErr, &herr) && herr.StatusCode == http.StatusNotImplemented {
		return false, nil
	}
	return false, probeErr
}

const serverCapabilitiesPath = platform.APIV2Prefix + "/serverCapabilities"

func rbacManageProbe(ctx context.Context, c *Client) (allowed bool, err error) {
	return userCapability(ctx, c, userCapabilityManageRbac)
}

func userCapability(ctx context.Context, c *Client, key string) (allowed bool, err error) {
	allowed, probeErr := readUserCapability(ctx, c, key)
	var herr *HTTPError
	if errors.As(probeErr, &herr) && herr.StatusCode == http.StatusNotFound {
		return true, nil
	}
	return allowed, probeErr
}

const capabilityNotReportedAllowed = true

func readUserCapability(ctx context.Context, c *Client, key string) (bool, error) {
	var capabilities map[string]bool
	if err := c.doSingle(ctx, http.MethodGet, serverCapabilitiesPath, nil, &capabilities); err != nil {
		return false, err
	}
	if v, ok := capabilities[key]; ok {
		return v, nil
	}
	return capabilityNotReportedAllowed, nil
}

func loadRBACRoleRegistry(actual []wire.RoleResponse, desiredNames []string) rbacRoleXRefAdapter {
	a := rbacRoleXRefAdapter{
		nameToID: map[string]string{},
		idToName: map[string]string{},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
		builtin:  map[string]struct{}{},
	}
	for _, r := range actual {
		switch r.Origin {
		case entplatform.RBACOriginCustom:
			if r.Name != "" && r.ID != "" {
				a.nameToID[r.Name] = r.ID
				a.idToName[r.ID] = r.Name
			}
		case entplatform.RBACOriginBuiltin:
			if r.Name != "" && r.ID != "" {
				a.nameToID[r.Name] = r.ID
				a.idToName[r.ID] = r.Name
			}
			if r.Name != "" {
				a.builtin[r.Name] = struct{}{}
			}
		case entplatform.RBACOriginYAML:
			if r.Name != "" {
				a.yaml[r.Name] = struct{}{}
			}
		}
	}
	for _, n := range desiredNames {
		if n == "" {
			continue
		}
		a.desired[n] = struct{}{}
	}
	return a
}

func planRBACRoles(desired []resolve.ResolvedResource, actual []wire.RoleResponse) (*rbacRolePlan, error) {
	k, ok := generic.Lookup("rbacRole")
	if !ok {
		return nil, fmt.Errorf("rbacRole kind not registered")
	}

	dbRoles := filterRolesByOrigin(actual)

	desiredNames := make([]string, 0, len(desired))
	for _, r := range desired {
		desiredNames = append(desiredNames, r.Name)
	}
	xref := loadRBACRoleRegistry(actual, desiredNames)

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateRoleRequest, len(desired))
	inheritsByName := make(map[string][]string, len(desired))

	for _, r := range desired {
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate rbacRole name %q in resolved resources", r.Name)
		}
		if _, isYAML := xref.yaml[r.Name]; isYAML {
			return nil, fmt.Errorf("rbacRole %q: name collides with a YAML-defined role; pick a different name", r.Name)
		}
		if _, isBuiltin := xref.builtin[r.Name]; isBuiltin {
			return nil, fmt.Errorf("rbacRole %q: name collides with a Solace built-in role; pick a different name", r.Name)
		}
		create, err := translateRBACRole(r)
		if err != nil {
			return nil, err
		}
		if details, scopeOK := rbac.ValidateScopes(create.Scopes); !scopeOK {
			return nil, fmt.Errorf("rbacRole %q (%s): invalid scopes: %v", r.Name, r.SourceFile, details)
		}
		if details, shapeOK := rbac.ValidateScopeShape(create.Scopes); !shapeOK {
			return nil, fmt.Errorf("rbacRole %q (%s): invalid scopes: %v", r.Name, r.SourceFile, details)
		}
		inheritsByName[r.Name] = slices.Clone(create.Inherits)
		live[r.Name] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     create,
		})
	}

	mergeExistingInherits(inheritsByName, dbRoles, xref)

	if err := detectInheritsCycle(inheritsByName); err != nil {
		return nil, err
	}

	for name, create := range live {
		if err := generic.ResolveForward(&create, xref); err != nil {
			return nil, fmt.Errorf("rbacRole %q: %w", name, err)
		}
		live[name] = create
		for i := range entries {
			if entries[i].Name == name {
				entries[i].Create = create
				break
			}
		}
	}

	actualVals := make([]reflect.Value, 0, len(dbRoles))
	for _, a := range dbRoles {
		ac := a
		ac.Inherits = slices.Clone(a.Inherits)
		slices.Sort(ac.Inherits)
		ac.Scopes = slices.Clone(a.Scopes)
		slices.Sort(ac.Scopes)
		actualVals = append(actualVals, reflect.ValueOf(ac))
	}
	for i, e := range entries {
		c := e.Create.(wire.CreateRoleRequest)
		c.Inherits = slices.Clone(c.Inherits)
		slices.Sort(c.Inherits)
		c.Scopes = slices.Clone(c.Scopes)
		slices.Sort(c.Scopes)
		entries[i].Create = c
	}

	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	return convertRBACRolePlan(gp, live, k), nil
}

func filterRolesByOrigin(roles []wire.RoleResponse) []wire.RoleResponse {
	db := make([]wire.RoleResponse, 0, len(roles))
	for _, r := range roles {
		if r.Origin == entplatform.RBACOriginCustom {
			db = append(db, r)
		}
	}
	return db
}

func translateRBACRole(r resolve.ResolvedResource) (wire.CreateRoleRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	delete(in, roleUsersKey)
	delete(in, roleGroupsKey)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardRBACRoleV2(in, translate.RBACRoleV2Hooks{})
	if err != nil {
		return create, fmt.Errorf("translate rbacRole %q (%s): %w", r.Name, r.SourceFile, err)
	}
	return create, nil
}

const (
	roleUsersKey  = "users"
	roleGroupsKey = "groups"
)

var roleSpecAllowedKeys = []string{roleGroupsKey, "inherits", "scopes", roleUsersKey}

func validateRoleSpecKeys(r resolve.ResolvedResource) error {
	for _, key := range slices.Sorted(maps.Keys(r.Spec)) {
		if !slices.Contains(roleSpecAllowedKeys, key) {
			return fmt.Errorf("rbacRole %q (%s): unknown spec key %q (allowed: %s)", r.Name, r.SourceFile, key, strings.Join(roleSpecAllowedKeys, ", "))
		}
	}
	return nil
}

func extractRoleUsers(desired []resolve.ResolvedResource) ([]resolve.ResolvedResource, error) {
	var out []resolve.ResolvedResource
	for _, r := range desired {
		if err := validateRoleSpecKeys(r); err != nil {
			return nil, err
		}
		if _, hasGroups := r.Spec[roleGroupsKey]; hasGroups {
			return nil, fmt.Errorf("rbacRole %q (%s): spec.groups is not supported — author claim-driven access as a kind: rbacClaimMapping file", r.Name, r.SourceFile)
		}
		users, err := readRoleUsers(r)
		if err != nil {
			return nil, err
		}
		grants, err := grantsFromSubjects(r.Name, r.Name+"/users", r.SourceFile, r.Provenance, users)
		if err != nil {
			return nil, err
		}
		out = append(out, grants...)
	}
	return out, nil
}

var grantSpecAllowedKeys = []string{"roleName", roleUsersKey}

func extractGrantResources(desired []resolve.ResolvedResource) ([]resolve.ResolvedResource, error) {
	var out []resolve.ResolvedResource
	for _, r := range desired {
		for _, key := range slices.Sorted(maps.Keys(r.Spec)) {
			if !slices.Contains(grantSpecAllowedKeys, key) {
				return nil, fmt.Errorf("rbacGrant %q (%s): unknown spec key %q (allowed: %s)", r.Name, r.SourceFile, key, strings.Join(grantSpecAllowedKeys, ", "))
			}
		}
		roleName, _ := r.Spec["roleName"].(string)
		roleName = strings.TrimSpace(roleName)
		if roleName == "" {
			return nil, fmt.Errorf("rbacGrant %q (%s): spec.roleName is required", r.Name, r.SourceFile)
		}
		users, err := readRoleUsers(r)
		if err != nil {
			return nil, err
		}
		if len(users) == 0 {
			return nil, fmt.Errorf("rbacGrant %q (%s): spec.users must list at least one subject", r.Name, r.SourceFile)
		}
		grants, err := grantsFromSubjects(roleName, r.Name, r.SourceFile, r.Provenance, users)
		if err != nil {
			return nil, err
		}
		out = append(out, grants...)
	}
	return out, nil
}

func grantsFromSubjects(roleName, namePrefix, sourceFile string, prov *resolve.SourceProvenance, subjects []roleUserEntry) ([]resolve.ResolvedResource, error) {
	out := make([]resolve.ResolvedResource, 0, len(subjects))
	seen := make(map[string]struct{}, len(subjects))
	for _, e := range subjects {
		key := e.subjectType + "|" + e.subjectIssuer + "|" + e.identity
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("rbacGrant for role %q (%s): duplicate subject %q", roleName, sourceFile, e.identity)
		}
		seen[key] = struct{}{}
		spec := map[string]any{"identity": e.identity, "roleName": roleName}
		nameSuffix := e.identity
		if e.subjectType != "" {
			spec["subjectType"] = e.subjectType
			nameSuffix = e.subjectType + ":" + e.subjectIssuer + ":" + e.identity
		}
		if e.subjectIssuer != "" {
			spec["subjectIssuer"] = e.subjectIssuer
		}
		out = append(out, resolve.ResolvedResource{
			Kind:       "rbacGrant",
			Name:       namePrefix + "/" + nameSuffix,
			SourceFile: sourceFile,
			Provenance: prov,
			Spec:       spec,
		})
	}
	return out, nil
}

func configRoleGrantAdvisories(grantFileResources []resolve.ResolvedResource, configRoleNames []string) []string {
	cfg := make(map[string]struct{}, len(configRoleNames))
	for _, n := range configRoleNames {
		cfg[n] = struct{}{}
	}
	affected := map[string]struct{}{}
	for _, r := range grantFileResources {
		role, _ := r.Spec["roleName"].(string)
		if _, ok := cfg[role]; ok {
			affected[role] = struct{}{}
		}
	}
	if len(affected) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"rbac: standalone grant file(s) target the config-defined role(s) %s — prefer declaring those grants as spec.users on the role. A grant file is intended for managed/in-DB roles that have no config definition.",
		strings.Join(slices.Sorted(maps.Keys(affected)), ", "))}
}

type roleUserEntry struct {
	identity      string
	subjectType   string
	subjectIssuer string
}

func readRoleUsers(r resolve.ResolvedResource) ([]roleUserEntry, error) {
	raw, ok := r.Spec[roleUsersKey]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s %q (%s): spec.users must be a list", r.Kind, r.Name, r.SourceFile)
	}
	out := make([]roleUserEntry, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				return nil, fmt.Errorf("%s %q (%s): spec.users contains an empty identity", r.Kind, r.Name, r.SourceFile)
			}
			out = append(out, roleUserEntry{identity: s})
		case map[string]any:
			e, err := parseTypedRoleUser(r, v)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		default:
			return nil, fmt.Errorf("%s %q (%s): spec.users entries must be a string or a {type, value} object", r.Kind, r.Name, r.SourceFile)
		}
	}
	for _, e := range out {
		if rbac.IsSystemPrincipal(e.identity) {
			return nil, fmt.Errorf("%s %q (%s): spec.users cannot target the system principal %q — declare its roles on a kind: serviceAccount resource instead",
				r.Kind, r.Name, r.SourceFile, e.identity)
		}
	}
	return out, nil
}

func parseTypedRoleUser(r resolve.ResolvedResource, m map[string]any) (roleUserEntry, error) {
	for k := range m {
		if k != "type" && k != "value" && k != "issuer" {
			return roleUserEntry{}, fmt.Errorf("%s %q (%s): spec.users entry has unknown key %q (allowed: type, value, issuer)", r.Kind, r.Name, r.SourceFile, k)
		}
	}
	typ, _ := m["type"].(string)
	val, _ := m["value"].(string)
	issuer, _ := m["issuer"].(string)
	typ, val, issuer = strings.TrimSpace(typ), strings.TrimSpace(val), strings.TrimSpace(issuer)
	switch typ {
	case "email", "sub", "legacy":
	default:
		return roleUserEntry{}, fmt.Errorf("%s %q (%s): spec.users entry type must be one of email, sub, legacy (got %q)", r.Kind, r.Name, r.SourceFile, typ)
	}
	if val == "" {
		return roleUserEntry{}, fmt.Errorf("%s %q (%s): spec.users entry requires a non-empty value", r.Kind, r.Name, r.SourceFile)
	}
	if typ == "sub" && issuer == "" {
		return roleUserEntry{}, fmt.Errorf("%s %q (%s): spec.users entry of type sub requires an issuer", r.Kind, r.Name, r.SourceFile)
	}
	return roleUserEntry{identity: val, subjectType: typ, subjectIssuer: issuer}, nil
}

func mergeExistingInherits(inherits map[string][]string, dbRoles []wire.RoleResponse, xref rbacRoleXRefAdapter) {
	for _, r := range dbRoles {
		if r.Name == "" {
			continue
		}
		if _, isDesired := inherits[r.Name]; isDesired {
			continue
		}
		parents := make([]string, 0, len(r.Inherits))
		for _, parentID := range r.Inherits {
			if pname, ok := xref.idToName[parentID]; ok {
				parents = append(parents, pname)
			}
		}
		inherits[r.Name] = parents
	}
}

func detectInheritsCycle(inherits map[string][]string) error {
	visited := map[string]int{}
	var stack []string
	var dfs func(n string) error
	dfs = func(n string) error {
		switch visited[n] {
		case 1:
			return fmt.Errorf("rbacRole inheritance cycle: %s → %s", strings.Join(stack, " → "), n)
		case 2:
			return nil
		}
		visited[n] = 1
		stack = append(stack, n)
		for _, parent := range inherits[n] {
			if _, ok := inherits[parent]; !ok {
				continue
			}
			if err := dfs(parent); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		visited[n] = 2
		return nil
	}
	names := make([]string, 0, len(inherits))
	for n := range inherits {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if err := dfs(n); err != nil {
			return err
		}
	}
	return nil
}

func convertRBACRolePlan(gp *generic.KindPlan, live map[string]wire.CreateRoleRequest, k generic.Kind) *rbacRolePlan {
	out := &rbacRolePlan{Kind: "rbacRole"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, rbacRoleOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     live[op.Name],
		})
	}
	for _, op := range gp.Updates {
		patch, _ := generic.ToUpdate(live[op.Name], k.UpdateProto).(wire.ReplaceRoleRequest)
		out.Updates = append(out.Updates, rbacRoleOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     live[op.Name],
			Patch:      patch,
		})
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, rbacRoleOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
		})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, rbacRoleOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func IsSystemPrincipalGrant(g wire.GrantResponse) bool {
	return g.Subject != nil && rbac.IsSystemPrincipal(g.Subject.Value)
}

func withIssuerAsDeclared(g wire.GrantResponse) wire.GrantResponse {
	if g.Subject == nil || g.Subject.Issuer != nil {
		return g
	}
	subject := *g.Subject
	subject.Issuer = new("")
	g.Subject = &subject
	return g
}

func planRBACGrants(desired []resolve.ResolvedResource, actual []wire.GrantResponse, holders grantHolders, xref rbacRoleXRefAdapter) (*rbacGrantPlan, error) {
	k, ok := generic.Lookup("rbacGrant")
	if !ok {
		return nil, fmt.Errorf("rbacGrant kind not registered")
	}

	db := make([]wire.GrantResponse, 0, len(actual))
	for _, a := range actual {
		if a.Origin == entplatform.RBACOriginYAML || IsSystemPrincipalGrant(a) {
			continue
		}
		db = append(db, withIssuerAsDeclared(a))
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	headerByKey := make(map[string]string, len(desired))
	roleNameByKey := make(map[string]string, len(desired))
	for _, r := range desired {
		create, _, roleName, err := readGrantSpec(r)
		if err != nil {
			return nil, err
		}
		roleID, err := xref.LookupID("rbacRole", roleName)
		if err != nil {
			return nil, fmt.Errorf("rbacGrant %q (%s): %w", r.Name, r.SourceFile, err)
		}
		create.RoleID = roleID
		key := grantKey(create.Subject.Type, create.Subject.Issuer, create.Subject.Value, roleID)
		if existing, dup := headerByKey[key]; dup {
			return nil, fmt.Errorf("rbacGrant %q: duplicates %q — grants must be unique on (subject, roleName)", r.Name, existing)
		}
		headerByKey[key] = r.Name
		roleNameByKey[key] = roleName
		entries = append(entries, generic.DesiredEntry{
			Name:       key,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     create,
		})
	}

	gp, err := runCompositeKeyPlan(entries, db, k)
	if err != nil {
		return nil, err
	}
	describe := func(key string) string {
		parts := splitGrantKey(key)
		role := roleNameByKey[key]
		if role == "" {
			role = parts[3]
		}
		return grantSubjectLabel(apiv2.CreateGrantSubject{Type: parts[0], Issuer: parts[1], Value: parts[2]}) + " → " + role
	}
	warnings := rebindHeldGrants(gp, db, holders, describe)
	warnings = append(warnings, sharedEmailLegacyWarnings(gp.Creates, holders, describe)...)
	plan := convertRBACGrantPlan(gp, entries, headerByKey, db, xref.idToName, roleNameByKey)
	plan.warnings = warnings
	return plan, nil
}

func rebindHeldGrants(gp *generic.KindPlan, actual []wire.GrantResponse, holders grantHolders, describe func(key string) string) []string {
	heldBy := grantIDsByHolderKey(actual, holders)
	recordedBy := map[string]string{}
	for _, op := range gp.Unchanged {
		recordedBy[op.ID] = op.Name
	}
	byName := func(a, b generic.Op) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(gp.Creates, byName)
	var warnings []string
	creates := gp.Creates[:0]
	for _, op := range gp.Creates {
		ids := heldBy[op.Name]
		if len(ids) != 1 {
			creates = append(creates, op)
			continue
		}
		op.ID = ids[0]
		if kept, taken := recordedBy[op.ID]; taken {
			warnings = append(warnings, fmt.Sprintf(
				"rbac: %s and %s name the same user; the grant is recorded under the second. List the user once.",
				describe(op.Name), describe(kept)))
			gp.Unchanged = append(gp.Unchanged, op)
			continue
		}
		recordedBy[op.ID] = op.Name
		gp.Updates = append(gp.Updates, op)
	}
	gp.Creates = creates
	gp.Deletes = slices.DeleteFunc(gp.Deletes, func(op generic.Op) bool {
		_, kept := recordedBy[op.ID]
		return kept
	})
	slices.SortFunc(gp.Unchanged, byName)
	return warnings
}

func grantIDsByHolderKey(actual []wire.GrantResponse, holders grantHolders) map[string][]string {
	out := map[string][]string{}
	for _, g := range actual {
		u := holders[g.ID]
		if ptrString(g.State) != entplatform.GrantStateConcrete || u == nil {
			continue
		}
		keys := []string{grantKey(store.PendingSubjectSub, u.Issuer, u.Sub, g.RoleID)}
		switch {
		case u.Email == nil || *u.Email == "":
			keys = append(keys, grantKey(store.PendingSubjectLegacy, "", u.Sub, g.RoleID))
		case !u.EmailShared:
			keys = append(keys,
				grantKey(store.PendingSubjectEmail, "", *u.Email, g.RoleID),
				grantKey(store.PendingSubjectLegacy, "", *u.Email, g.RoleID))
		}
		for _, k := range keys {
			out[k] = append(out[k], g.ID)
		}
	}
	return out
}

func sharedEmailLegacyWarnings(creates []generic.Op, holders grantHolders, describe func(key string) string) []string {
	shared := map[string]bool{}
	for _, u := range holders {
		if u.Email != nil && u.EmailShared {
			shared[*u.Email] = true
		}
	}
	var warnings []string
	for _, op := range creates {
		parts := splitGrantKey(op.Name)
		if parts[0] == store.PendingSubjectLegacy && shared[parts[2]] {
			warnings = append(warnings, fmt.Sprintf(
				"rbac: %s is an email more than one user has, so the platform grants it to whichever of them was updated last. Name the user by { type: sub, issuer, value } instead.",
				describe(op.Name)))
		}
	}
	return warnings
}

func grantSubjectLabel(s apiv2.CreateGrantSubject) string {
	switch s.Type {
	case store.PendingSubjectLegacy:
		return s.Value
	case store.PendingSubjectSub:
		return s.Type + ":" + s.Issuer + ":" + s.Value
	}
	return s.Type + ":" + s.Value
}

func runCompositeKeyPlan[A any](entries []generic.DesiredEntry, actual []A, k generic.Kind) (*generic.KindPlan, error) {
	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		actualVals = append(actualVals, reflect.ValueOf(a))
	}
	return generic.Plan(entries, actualVals, k)
}

func readGrantSpec(r resolve.ResolvedResource) (wire.CreateGrantRequest, string, string, error) {
	identity, _ := r.Spec["identity"].(string)
	roleName, _ := r.Spec["roleName"].(string)
	if strings.TrimSpace(identity) == "" {
		return wire.CreateGrantRequest{}, "", "", fmt.Errorf("rbacGrant %q (%s): spec.identity is required", r.Name, r.SourceFile)
	}
	if strings.TrimSpace(roleName) == "" {
		return wire.CreateGrantRequest{}, "", "", fmt.Errorf("rbacGrant %q (%s): spec.roleName is required", r.Name, r.SourceFile)
	}
	subjectType, _ := r.Spec["subjectType"].(string)
	subjectIssuer, _ := r.Spec["subjectIssuer"].(string)
	if subjectType == "" {
		subjectType = "legacy"
	}
	return wire.CreateGrantRequest{
		Subject: apiv2.CreateGrantSubject{
			Type:   subjectType,
			Value:  entplatform.NormalizeGrantSubjectValue(subjectType, identity),
			Issuer: subjectIssuer,
		},
	}, identity, roleName, nil
}

func convertRBACGrantPlan(gp *generic.KindPlan, entries []generic.DesiredEntry, headerByKey map[string]string, actual []wire.GrantResponse, roleIDToName map[string]string, desiredRoleName map[string]string) *rbacGrantPlan {
	out := &rbacGrantPlan{Kind: "rbacGrant"}
	createByKey := map[string]wire.CreateGrantRequest{}
	for _, e := range entries {
		createByKey[e.Name] = e.Create.(wire.CreateGrantRequest)
	}
	for _, op := range gp.Creates {
		c := createByKey[op.Name]
		out.Creates = append(out.Creates, rbacGrantOp{
			Header:     headerByKey[op.Name],
			Identity:   c.Subject.Value,
			RoleID:     c.RoleID,
			RoleName:   desiredRoleName[op.Name],
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     c,
		})
	}
	recorded := make(map[string]apiv2.CreateGrantSubject, len(actual))
	for _, a := range actual {
		if a.Subject != nil {
			recorded[a.ID] = apiv2.CreateGrantSubject{Type: a.Subject.Type, Value: a.Subject.Value, Issuer: ptrString(a.Subject.Issuer)}
		}
	}
	for _, op := range gp.Updates {
		c := createByKey[op.Name]
		out.Updates = append(out.Updates, rbacGrantOp{
			Header:     headerByKey[op.Name],
			Identity:   c.Subject.Value,
			RoleID:     c.RoleID,
			RoleName:   desiredRoleName[op.Name],
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     c,
			Recorded:   recorded[op.ID],
		})
	}
	for _, op := range gp.Unchanged {
		c := createByKey[op.Name]
		out.Unchanged = append(out.Unchanged, rbacGrantOp{
			Header:     headerByKey[op.Name],
			Identity:   c.Subject.Value,
			RoleID:     c.RoleID,
			RoleName:   desiredRoleName[op.Name],
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
		})
	}
	for _, op := range gp.Deletes {
		parts := splitGrantKey(op.Name)
		out.Deletes = append(out.Deletes, rbacGrantOp{
			Identity: parts[2],
			RoleID:   parts[3],
			RoleName: roleIDToName[parts[3]],
			ID:       op.ID,
		})
	}
	return out
}

func splitGrantKey(k string) [4]string {
	parts := strings.SplitN(k, "|", 4)
	var out [4]string
	copy(out[:], parts)
	return out
}

func planRBACClaimMappings(desired []resolve.ResolvedResource, actual []wire.ClaimMappingResponse, xref rbacRoleXRefAdapter) (*rbacClaimMappingPlan, error) {
	k, ok := generic.Lookup("rbacClaimMapping")
	if !ok {
		return nil, fmt.Errorf("rbacClaimMapping kind not registered")
	}

	db := make([]wire.ClaimMappingResponse, 0, len(actual))
	for _, a := range actual {
		if a.Origin == entplatform.RBACOriginYAML {
			continue
		}
		ac := a
		ac.RoleIDs = slices.Clone(a.RoleIDs)
		slices.Sort(ac.RoleIDs)
		db = append(db, ac)
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateClaimMappingRequest, len(desired))
	headerByKey := make(map[string]string, len(desired))
	for _, r := range desired {
		create, roleNames, err := readClaimMappingSpec(r)
		if err != nil {
			return nil, err
		}
		roleIDs := make([]string, 0, len(roleNames))
		for _, name := range roleNames {
			id, xerr := xref.LookupID("rbacRole", name)
			if xerr != nil {
				return nil, fmt.Errorf("rbacClaimMapping %q (%s): %w", r.Name, r.SourceFile, xerr)
			}
			roleIDs = append(roleIDs, id)
		}
		slices.Sort(roleIDs)
		roleIDs = slices.Compact(roleIDs)
		create.RoleIDs = roleIDs
		key := claimMappingKey(create.OIDCProvider, create.ClaimValue)
		if existing, dup := headerByKey[key]; dup {
			return nil, fmt.Errorf("rbacClaimMapping %q: duplicates %q", r.Name, existing)
		}
		headerByKey[key] = r.Name
		live[key] = create
		entries = append(entries, generic.DesiredEntry{
			Name:       key,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     create,
		})
	}

	actualVals := make([]reflect.Value, 0, len(db))
	for _, a := range db {
		actualVals = append(actualVals, reflect.ValueOf(a))
	}

	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	return convertRBACClaimMappingPlan(gp, live, headerByKey, k), nil
}

var claimMappingSpecAllowedKeys = []string{"claimValue", "name", "oidcProvider", "roleNames"}

func readClaimMappingSpec(r resolve.ResolvedResource) (wire.CreateClaimMappingRequest, []string, error) {
	for _, k := range slices.Sorted(maps.Keys(r.Spec)) {
		if !slices.Contains(claimMappingSpecAllowedKeys, k) {
			return wire.CreateClaimMappingRequest{}, nil, fmt.Errorf("rbacClaimMapping %q (%s): unknown spec key %q (allowed: %s)", r.Name, r.SourceFile, k, strings.Join(claimMappingSpecAllowedKeys, ", "))
		}
	}
	oidcProvider, _ := r.Spec["oidcProvider"].(string)
	claimValue, _ := r.Spec["claimValue"].(string)
	name, _ := r.Spec["name"].(string)
	roleNames := specStringSlice(r.Spec["roleNames"])

	missing := []string{}
	for k, v := range map[string]string{"oidcProvider": oidcProvider, "claimValue": claimValue} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(roleNames) == 0 {
		missing = append(missing, "roleNames")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return wire.CreateClaimMappingRequest{}, nil, fmt.Errorf("rbacClaimMapping %q (%s): required spec fields missing: %s", r.Name, r.SourceFile, strings.Join(missing, ", "))
	}
	if strings.TrimSpace(name) == "" {
		name = r.Name
	}
	return wire.CreateClaimMappingRequest{
		Name:         name,
		OIDCProvider: oidcProvider,
		ClaimValue:   claimValue,
	}, roleNames, nil
}

func specStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return val
	case string:
		if strings.TrimSpace(val) != "" {
			return []string{val}
		}
	}
	return nil
}

func convertRBACClaimMappingPlan(gp *generic.KindPlan, live map[string]wire.CreateClaimMappingRequest, headerByKey map[string]string, k generic.Kind) *rbacClaimMappingPlan {
	out := &rbacClaimMappingPlan{Kind: "rbacClaimMapping"}
	mk := func(op generic.Op) rbacClaimMappingOp {
		c := live[op.Name]
		return rbacClaimMappingOp{
			Header:       headerByKey[op.Name],
			Name:         c.Name,
			OIDCProvider: c.OIDCProvider,
			ClaimValue:   c.ClaimValue,
			RoleIDs:      c.RoleIDs,
			Source:       op.Source,
			Provenance:   op.Provenance,
			ID:           op.ID,
			Create:       c,
		}
	}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, mk(op))
	}
	for _, op := range gp.Updates {
		o := mk(op)
		o.Patch, _ = generic.ToUpdate(live[op.Name], k.UpdateProto).(wire.ReplaceClaimMappingRequest)
		out.Updates = append(out.Updates, o)
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, mk(op))
	}
	for _, op := range gp.Deletes {
		parts := strings.SplitN(op.Name, "|", 2)
		dm := rbacClaimMappingOp{ID: op.ID}
		if len(parts) == 2 {
			dm.OIDCProvider, dm.ClaimValue = parts[0], parts[1]
		}
		out.Deletes = append(out.Deletes, dm)
	}
	return out
}

func applyRBACRoles(ctx context.Context, c *Client, plan *rbacRolePlan, opts ApplyOptions) *rbacRoleOutcome {
	outcome := &rbacRoleOutcome{Kind: "rbacRole"}
	if plan == nil {
		return outcome
	}
	if opts.SkipRBACWrites {
		for _, op := range plan.Creates {
			outcome.Skipped = append(outcome.Skipped, rbacRoleOpResult{Op: op, Verb: "create", Reason: skipReasonMiniIDP})
		}
		for _, op := range plan.Updates {
			outcome.Skipped = append(outcome.Skipped, rbacRoleOpResult{Op: op, Verb: "update", Reason: skipReasonMiniIDP})
		}
		return outcome
	}
	nameToID := map[string]string{}

	for _, op := range sortRoleCreatesByInherits(plan.Creates) {
		create := op.Create
		create.Inherits = rewriteInherits(create.Inherits, nameToID)
		var resp wire.RoleResponse
		err := c.callResource(ctx, rbacRolesResource, http.MethodPost, "", create, &resp)
		res := rbacRoleOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		nameToID[op.Name] = resp.ID
		outcome.Created = append(outcome.Created, res)
	}

	for _, op := range plan.Updates {
		patch := op.Patch
		patch.Inherits = rewriteInherits(patch.Inherits, nameToID)
		var resp wire.RoleResponse
		err := c.callResource(ctx, rbacRolesResource, http.MethodPut, "/"+url.PathEscape(op.ID), patch, &resp)
		res := rbacRoleOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}

	return outcome
}

func applyRBACPhase(ctx context.Context, c *Client, plan *PlanResult, opts ApplyOptions, out *ApplyResult) {
	if plan.RBACWritesBlocked {
		opts.SkipRBACWrites = true
	}
	if plan.RBACRoles != nil {
		out.RBACRoles = applyRBACRoles(ctx, c, plan.RBACRoles, opts)
	}
	roleNameToID := map[string]string{}
	if out.RBACRoles != nil {
		for _, r := range out.RBACRoles.Created {
			roleNameToID[r.Op.Name] = r.Op.ID
		}
		for _, r := range out.RBACRoles.Updated {
			roleNameToID[r.Op.Name] = r.Op.ID
		}
	}
	if plan.RBACClaimMappings != nil {
		out.RBACClaimMappings = applyRBACClaimMappings(ctx, c, plan.RBACClaimMappings, opts, roleNameToID)
	}
	if plan.RBACGrants != nil {
		out.RBACGrants = applyRBACGrants(ctx, c, plan.RBACGrants, opts, roleNameToID)
	}
	if plan.SystemUsers != nil {
		out.SystemUsers = applySystemUsers(ctx, c, plan.SystemUsers, opts, roleNameToID)
	}
	if plan.DefaultRoles != nil {
		out.DefaultRoles = applyDefaultRoles(ctx, c, plan.DefaultRoles, opts, roleNameToID)
	}
	if plan.RBACRoles != nil {
		applyRBACRoleDeletes(ctx, c, plan.RBACRoles, opts, out.RBACRoles)
	}
}

func applyRBACRoleDeletes(ctx context.Context, c *Client, plan *rbacRolePlan, opts ApplyOptions, outcome *rbacRoleOutcome) {
	if plan == nil || outcome == nil {
		return
	}
	for _, op := range plan.Deletes {
		switch {
		case opts.SkipRBACWrites:
			outcome.Skipped = append(outcome.Skipped, rbacRoleOpResult{Op: op, Verb: "delete", Reason: skipReasonMiniIDP})
		case !opts.Prune:
			outcome.Skipped = append(outcome.Skipped, rbacRoleOpResult{Op: op, Verb: "delete"})
		default:
			err := c.callResource(ctx, rbacRolesResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
			res := rbacRoleOpResult{Op: op, Verb: "delete", Err: err}
			if err != nil {
				outcome.Failures = append(outcome.Failures, res)
			} else {
				outcome.Deleted = append(outcome.Deleted, res)
			}
		}
	}
}

func sortRoleCreatesByInherits(creates []rbacRoleOp) []rbacRoleOp {
	byName := make(map[string]rbacRoleOp, len(creates))
	for _, op := range creates {
		byName[op.Name] = op
	}
	visited := make(map[string]bool, len(creates))
	ordered := make([]rbacRoleOp, 0, len(creates))
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		op, ok := byName[name]
		if !ok {
			return
		}
		for _, parent := range op.Create.Inherits {
			if !looksLikeUUID(parent) {
				visit(parent)
			}
		}
		ordered = append(ordered, op)
	}
	for _, op := range creates {
		visit(op.Name)
	}
	return ordered
}

func rewriteInherits(ids []string, nameToID map[string]string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if looksLikeUUID(id) {
			out[i] = id
			continue
		}
		if real, ok := nameToID[id]; ok {
			out[i] = real
			continue
		}
		out[i] = id
	}
	return out
}

func applyRBACGrants(ctx context.Context, c *Client, plan *rbacGrantPlan, opts ApplyOptions, roleNameToID map[string]string) *rbacGrantOutcome {
	outcome := &rbacGrantOutcome{Kind: "rbacGrant"}
	if plan == nil {
		return outcome
	}
	if opts.SkipRBACWrites {
		for _, op := range plan.Creates {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "create", Reason: skipReasonMiniIDP})
		}
		for _, op := range plan.Updates {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "update", Reason: skipReasonMiniIDP})
		}
		for _, op := range plan.Deletes {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "delete", Reason: skipReasonMiniIDP})
		}
		return outcome
	}
	conflictedRoleIDs := map[string]bool{}
	heldByCreate := map[string]bool{}
	for _, op := range plan.Creates {
		create := op.Create
		if !looksLikeUUID(create.RoleID) {
			if id, ok := roleNameToID[create.RoleID]; ok {
				create.RoleID = id
			}
		}
		resp, err := c.CreateRBACGrant(ctx, create)
		res := rbacGrantOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			if herr, ok := errors.AsType[*HTTPError](err); ok && herr.StatusCode == http.StatusConflict {
				conflictedRoleIDs[create.RoleID] = true
			}
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		heldByCreate[resp.ID] = true
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		err := c.UpdateRBACGrant(ctx, op.ID, op.Create.Subject)
		res := rbacGrantOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}
	for _, op := range plan.Deletes {
		if heldByCreate[op.ID] {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "delete", Reason: skipReasonGrantHeldByCreate})
			continue
		}
		if conflictedRoleIDs[op.RoleID] {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "delete", Reason: skipReasonGrantCreateConflicted})
			continue
		}
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, rbacGrantOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.DeleteRBACGrant(ctx, op.ID)
		res := rbacGrantOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}

func applyRBACClaimMappings(ctx context.Context, c *Client, plan *rbacClaimMappingPlan, opts ApplyOptions, roleNameToID map[string]string) *rbacClaimMappingOutcome {
	outcome := &rbacClaimMappingOutcome{Kind: "rbacClaimMapping"}
	if plan == nil {
		return outcome
	}
	if opts.SkipRBACWrites {
		for _, op := range plan.Creates {
			outcome.Skipped = append(outcome.Skipped, rbacClaimMappingOpResult{Op: op, Verb: "create", Reason: skipReasonMiniIDP})
		}
		for _, op := range plan.Updates {
			outcome.Skipped = append(outcome.Skipped, rbacClaimMappingOpResult{Op: op, Verb: "update", Reason: skipReasonMiniIDP})
		}
		for _, op := range plan.Deletes {
			outcome.Skipped = append(outcome.Skipped, rbacClaimMappingOpResult{Op: op, Verb: "delete", Reason: skipReasonMiniIDP})
		}
		return outcome
	}
	for _, op := range plan.Creates {
		create := op.Create
		create.RoleIDs = rewriteInherits(create.RoleIDs, roleNameToID)
		var resp wire.ClaimMappingResponse
		err := c.callResource(ctx, rbacClaimMappingsResource, http.MethodPost, "", create, &resp)
		res := rbacClaimMappingOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		patch := op.Patch
		patch.RoleIDs = rewriteInherits(patch.RoleIDs, roleNameToID)
		var resp wire.ClaimMappingResponse
		err := c.callResource(ctx, rbacClaimMappingsResource, http.MethodPut, "/"+url.PathEscape(op.ID), patch, &resp)
		res := rbacClaimMappingOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, rbacClaimMappingOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, rbacClaimMappingsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := rbacClaimMappingOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}

func renderRBACRolePlan(w io.Writer, p *rbacRolePlan, opts FormatOptions) {
	renderRBACGenericPlan(w, "rbac/roles", len(p.Creates), len(p.Updates), len(p.Deletes), len(p.Unchanged),
		func() {
			for _, op := range p.Creates {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(op.Name)), color(opts, ansiGreen, "create"))
			}
			for _, op := range p.Updates {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(op.Name)), color(opts, ansiBlue, "update"))
			}
			for _, op := range p.Deletes {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
			}
			for _, op := range p.Unchanged {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(op.Name)), color(opts, ansiDim, noChangeLabel))
			}
		})
}

func renderRBACGrantPlan(w io.Writer, p *rbacGrantPlan, opts FormatOptions) {
	createsByIdentity := map[string][]rbacGrantOp{}
	deletesByIdentity := map[string][]rbacGrantOp{}
	for _, op := range p.Creates {
		createsByIdentity[op.Identity] = append(createsByIdentity[op.Identity], op)
	}
	for _, op := range p.Deletes {
		deletesByIdentity[op.Identity] = append(deletesByIdentity[op.Identity], op)
	}
	grouped := map[string]bool{}
	groupedIdentities := make([]string, 0)
	for id := range createsByIdentity {
		if len(deletesByIdentity[id]) > 0 {
			grouped[id] = true
			groupedIdentities = append(groupedIdentities, id)
		}
	}
	slices.Sort(groupedIdentities)

	renderRBACGenericPlan(w, "rbac/grants", len(p.Creates), len(p.Updates), len(p.Deletes), len(p.Unchanged),
		func() {
			for _, id := range groupedIdentities {
				oldRoles := grantRoleLabels(deletesByIdentity[id])
				newRoles := grantRoleLabels(createsByIdentity[id])
				_, _ = fmt.Fprintf(w, rowSimple,
					color(opts, ansiBlue, "~ "+padName(id)),
					color(opts, ansiBlue, "role change"))
				_, _ = fmt.Fprintf(w, "      role: %s → %s\n",
					strings.Join(oldRoles, ", "), strings.Join(newRoles, ", "))
			}
			for _, op := range p.Creates {
				if grouped[op.Identity] {
					continue
				}
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(grantLabel(op))), color(opts, ansiGreen, "create"))
			}
			for _, op := range p.Updates {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(grantLabel(op))), color(opts, ansiBlue, "update"))
				_, _ = fmt.Fprintf(w, "      subject: %s → %s (same user)\n",
					grantSubjectLabel(op.Recorded), grantSubjectLabel(op.Create.Subject))
			}
			for _, op := range p.Deletes {
				if grouped[op.Identity] {
					continue
				}
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(grantLabel(op))), color(opts, ansiRed, planDeleteLabel(opts)))
			}
			for _, op := range p.Unchanged {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(grantLabel(op))), color(opts, ansiDim, noChangeLabel))
			}
		})
}

func grantLabel(op rbacGrantOp) string {
	label := grantName(op)
	if op.Source != "" {
		label += " (from " + op.Source + " users)"
	}
	return label
}

func grantRoleLabels(ops []rbacGrantOp) []string {
	labels := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.RoleName != "" {
			labels = append(labels, op.RoleName)
		} else {
			labels = append(labels, op.RoleID)
		}
	}
	slices.Sort(labels)
	return labels
}

func claimMappingLabel(op rbacClaimMappingOp) string {
	if op.Header != "" {
		return op.Header
	}
	return op.OIDCProvider + ":" + op.ClaimValue
}

func renderRBACClaimMappingPlan(w io.Writer, p *rbacClaimMappingPlan, opts FormatOptions) {
	renderRBACGenericPlan(w, "rbac/claim-mappings", len(p.Creates), len(p.Updates), len(p.Deletes), len(p.Unchanged),
		func() {
			for _, op := range p.Creates {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(claimMappingLabel(op))), color(opts, ansiGreen, "create"))
			}
			for _, op := range p.Updates {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(claimMappingLabel(op))), color(opts, ansiBlue, "update"))
			}
			for _, op := range p.Deletes {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(claimMappingLabel(op))), color(opts, ansiRed, planDeleteLabel(opts)))
			}
			for _, op := range p.Unchanged {
				_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(claimMappingLabel(op))), color(opts, ansiDim, noChangeLabel))
			}
		})
}

func renderRBACRoleApply(w io.Writer, o *rbacRoleOutcome, opts FormatOptions) {
	if o == nil || (len(o.Created)+len(o.Updated)+len(o.Deleted)+len(o.Skipped)+len(o.Failures)) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, "rbac/roles")
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.Name)), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, rbacSkipLabel(r.Reason)))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

func rbacSkipLabel(reason string) string {
	switch reason {
	case skipReasonMiniIDP:
		return skipMiniIDPLabel
	case skipReasonGrantCreateConflicted:
		return keptCreateConflictedLabel
	case skipReasonGrantHeldByCreate:
		return keptHeldByCreateLabel
	}
	return skipPruneLabel
}

func renderRBACGrantApply(w io.Writer, o *rbacGrantOutcome, opts FormatOptions) {
	if o == nil || (len(o.Created)+len(o.Updated)+len(o.Deleted)+len(o.Skipped)+len(o.Failures)) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, "rbac/grants")
	for _, r := range o.Created {
		label := r.Op.Header
		if label == "" {
			label = r.Op.Identity
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(label)), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		label := r.Op.Header
		if label == "" {
			label = r.Op.Identity
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(label)), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Identity)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		label := r.Op.Header
		if label == "" {
			label = r.Op.Identity
		}
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(label)), color(opts, ansiDim, rbacSkipLabel(r.Reason)))
	}
	for _, r := range o.Failures {
		label := r.Op.Header
		if label == "" {
			label = r.Op.Identity
		}
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(label)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

func renderRBACClaimMappingApply(w io.Writer, o *rbacClaimMappingOutcome, opts FormatOptions) {
	if o == nil || (len(o.Created)+len(o.Updated)+len(o.Deleted)+len(o.Skipped)+len(o.Failures)) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, "rbac/claim-mappings")
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(claimMappingLabel(r.Op))), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(claimMappingLabel(r.Op))), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(claimMappingLabel(r.Op))), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(claimMappingLabel(r.Op))), color(opts, ansiDim, rbacSkipLabel(r.Reason)))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(claimMappingLabel(r.Op))), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
}

func renderRBACGenericPlan(w io.Writer, dirName string, c, u, d, un int, body func()) {
	total := c + u + d + un
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}
	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	body()
	_, _ = fmt.Fprintf(w, totalsLine, total, dirName, c, u, d, un)
}
