// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	rbacRolesDir           = "rbac/roles"
	rbacClaimMappingsDir   = "rbac/claim-mappings"
	rbacGrantsDir          = "rbac/grants"
	rbacSystemUsersDir     = "rbac/system-users"
	rbacServiceAccountsDir = "rbac/service-accounts"

	rbacYAMLPreviewSubdir = "_yaml-preview"
)

func pullRBACRoles(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacRoles: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	idToName := dbRoleIDToName(roles)
	builtinIDToName := builtinRoleIDToName(roles)
	resolveIDToName := roleIDToName(roles)
	previewIDToName := anyRoleIDToName(roles)

	usersByRole, err := rbacUsersByRole(ctx, c, opts, idToName, builtinIDToName, res)
	if err != nil {
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, r := range roles {
		if opts.Name != "" && r.Name != opts.Name {
			continue
		}
		if r.Origin == entplatform.RBACOriginBuiltin {
			if opts.Name == r.Name {
				res.Warnings = append(res.Warnings, fmt.Sprintf("rbacRole %q is a Solace built-in role; built-in roles are shipped and reused by name, not pulled as authorable files", r.Name))
			}
			continue
		}
		isYAML := r.Origin == entplatform.RBACOriginYAML
		if isYAML && !opts.IncludeYAMLPreview {
			continue
		}
		var users []pulledSubject
		if !isYAML {
			users = usersByRole[r.Name]
		}
		inheritsIDToName := resolveIDToName
		if isYAML {
			inheritsIDToName = previewIDToName
		}
		entry, ok, warn := buildRBACRoleEntry(r, inheritsIDToName, isYAML, users)
		if !ok {
			res.skip(isYAML, warn)
			continue
		}
		entries = append(entries, entry)
		if !isYAML {
			names = append(names, r.Name)
		}
	}
	return entries, names, nil
}

type pulledSubject struct {
	identity      string
	subjectType   string
	subjectIssuer string
}

func (p pulledSubject) sortKey() string {
	return p.subjectType + "|" + p.subjectIssuer + "|" + p.identity
}

func groupCustomGrantsByRole(grants []wire.GrantResponse, resolve func(wire.GrantResponse) (string, bool)) map[string][]pulledSubject {
	byRole := map[string][]pulledSubject{}
	seen := map[string]map[string]struct{}{}
	for _, g := range grants {
		if g.Origin != entplatform.RBACOriginCustom || g.Subject == nil {
			continue
		}
		if reconcile.IsSystemPrincipalGrant(g) {
			continue
		}
		roleName, ok := resolve(g)
		if !ok {
			continue
		}
		subjectType := g.Subject.Type
		if subjectType == "legacy" {
			subjectType = ""
		}
		s := pulledSubject{identity: strings.TrimSpace(g.Subject.Value), subjectType: subjectType, subjectIssuer: stringValue(g.Subject.Issuer)}
		if seen[roleName] == nil {
			seen[roleName] = map[string]struct{}{}
		}
		if _, dup := seen[roleName][s.sortKey()]; dup {
			continue
		}
		seen[roleName][s.sortKey()] = struct{}{}
		byRole[roleName] = append(byRole[roleName], s)
	}
	return byRole
}

func rbacUsersByRole(ctx context.Context, c *reconcile.Client, opts Options, idToName, builtinIDToName map[string]string, res *Result) (map[string][]pulledSubject, error) {
	grants, err := c.ListRBACGrants(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacGrant fold into users: %s", err))
			return map[string][]pulledSubject{}, nil
		}
		return nil, err
	}
	byRole := groupCustomGrantsByRole(grants, func(g wire.GrantResponse) (string, bool) {
		if roleName, ok := idToName[g.RoleID]; ok {
			return roleName, true
		}
		if _, isBuiltin := builtinIDToName[g.RoleID]; isBuiltin {
			return "", false
		}
		if opts.Name != "" {
			return "", false
		}
		res.drop("rbacGrant %s: roleId %q does not resolve to a known DB role", g.Subject.Value, g.RoleID)
		return "", false
	})
	for role := range byRole {
		slices.SortFunc(byRole[role], func(a, b pulledSubject) int {
			return strings.Compare(a.sortKey(), b.sortKey())
		})
	}
	return byRole, nil
}

func pullRBACClaimMappings(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacClaimMappings: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	idToName := roleIDToName(roles)
	previewIDToName := anyRoleIDToName(roles)

	mappings, err := c.ListRBACClaimMappings(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacClaimMappings: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	used := map[string]struct{}{}
	for _, m := range mappings {
		isYAML := m.Origin == entplatform.RBACOriginYAML
		if isYAML && !opts.IncludeYAMLPreview {
			continue
		}
		header := dedupeFileName(uniqueSlug(stringValue(m.OIDCProvider), m.ClaimValue), used)
		if opts.Name != "" && header != opts.Name {
			continue
		}
		roleNames := make([]string, 0, len(m.RoleIDs))
		var warned bool
		for _, rid := range m.RoleIDs {
			rowIDToName := idToName
			if isYAML {
				rowIDToName = previewIDToName
			}
			roleName, warn := resolveRoleNameForRow(rid, isYAML, rowIDToName)
			if warn != "" {
				res.skip(isYAML, "rbacClaimMapping "+stringValue(m.OIDCProvider)+":"+m.ClaimValue+": "+warn)
				warned = true
				break
			}
			roleNames = append(roleNames, roleName)
		}
		if warned {
			continue
		}
		spec := map[string]any{
			"oidcProvider": stringValue(m.OIDCProvider),
			"claimValue":   m.ClaimValue,
			"roleNames":    roleNames,
		}
		if name := stringValue(m.Name); name != "" {
			spec["name"] = name
		}
		entry, ok, embedWarn := buildRBACEntry("rbacClaimMapping", rbacClaimMappingsDir, header, spec, isYAML)
		if !ok {
			res.skip(isYAML, embedWarn)
			continue
		}
		entries = append(entries, entry)
		if !isYAML {
			names = append(names, header)
		}
	}
	return entries, names, nil
}

type serviceAccountLayout struct {
	current, legacy bool
}

func (l serviceAccountLayout) writesLegacy() bool { return l.legacy && !l.current }

func detectServiceAccountLayout(outputDir string) (serviceAccountLayout, error) {
	var l serviceAccountLayout
	var err error
	if l.current, err = dirExists(filepath.Join(outputDir, rbacServiceAccountsDir)); err != nil {
		return l, err
	}
	l.legacy, err = dirExists(filepath.Join(outputDir, rbacSystemUsersDir))
	return l, err
}

func dirExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
}

func pullServiceAccounts(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	layout, err := detectServiceAccountLayout(opts.OutputDir)
	if err != nil || layout.writesLegacy() {
		return nil, nil, err
	}
	switch {
	case layout.legacy && opts.Force:
		res.Warnings = append(res.Warnings, rbacSystemUsersDir+"/ was removed: service accounts are written to "+rbacServiceAccountsDir+"/")
	case layout.legacy:
		res.Warnings = append(res.Warnings, rbacSystemUsersDir+"/ is no longer written: sam config pull writes service accounts to "+rbacServiceAccountsDir+"/. Manifests that list resources.systemUsers still read "+rbacSystemUsersDir+"/; to retire it, move those entries to resources.serviceAccounts in every manifest, move any file under it that this pull did not write to "+rbacServiceAccountsDir+"/ with kind: serviceAccount, and delete the rest")
	}
	return pullServiceAccountsAs(ctx, c, opts, res, resolve.ServiceAccountKind, rbacServiceAccountsDir)
}

func pullLegacySystemUsers(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	layout, err := detectServiceAccountLayout(opts.OutputDir)
	if err != nil || !layout.writesLegacy() {
		return nil, nil, err
	}
	return pullServiceAccountsAs(ctx, c, opts, res, resolve.LegacyServiceAccountKind, rbacSystemUsersDir)
}

func pullServiceAccountsAs(ctx context.Context, c *reconcile.Client, opts Options, res *Result, kind, dir string) ([]fileEntry, []string, error) {
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping %ss: %s", kind, err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	idToName := roleIDToName(roles)

	users, err := c.ListSystemUsers(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping %ss: %s", kind, err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	managed := reconcile.ConfigManagedSystemUsers(users)

	managedIDs := make(map[string]struct{}, len(managed))
	for _, u := range managed {
		managedIDs[u.ID] = struct{}{}
	}
	for _, u := range users {
		if u.Origin != entplatform.RBACOriginCustom {
			continue
		}
		if _, ok := managedIDs[u.ID]; ok {
			continue
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"skipping %s %q (%s): declarative config cannot author or remove it; use the platform API if it is unwanted", kind,
			u.Name, u.Sub))
	}
	grantedUserIDs, err := dbGrantHolders(ctx, c, users)
	if err != nil {
		return nil, nil, err
	}
	for _, u := range users {
		if _, granted := grantedUserIDs[u.ID]; granted && u.Origin == entplatform.RBACOriginBuiltin && rbac.IsReservedSystemUserSub(u.Sub) {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"built-in %s %q (%s) holds DB role grants that widen its shipped roles; declarative config cannot manage them; remove each with DELETE %s",
				kind, u.Name, u.Sub, reconcile.RBACGrantDeletePath()))
		}
	}

	var entries []fileEntry
	var names []string
	for _, u := range managed {
		if opts.Name != "" && u.Name != opts.Name {
			continue
		}
		roleNames := make([]string, 0, len(u.RoleIDs))
		var warned bool
		for _, rid := range u.RoleIDs {
			roleName, warn := resolveRoleNameForRow(rid, false, idToName)
			if warn != "" {
				res.drop("%s %q: %s", kind, u.Name, warn)
				warned = true
				break
			}
			roleNames = append(roleNames, roleName)
		}
		if warned {
			continue
		}
		spec := map[string]any{"name": u.Name, "roleNames": roleNames}
		if displayName := stringValue(u.DisplayName); displayName != "" {
			spec["displayName"] = displayName
		}
		entry, ok, embedWarn := buildRBACEntry(kind, dir, u.Name, spec, false)
		if !ok {
			res.drop("%s", embedWarn)
			continue
		}
		entries = append(entries, entry)
		names = append(names, u.Name)
	}
	return entries, names, nil
}

func dbRoleIDToName(roles []wire.RoleResponse) map[string]string {
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		if r.Origin == entplatform.RBACOriginCustom && r.ID != "" {
			out[r.ID] = r.Name
		}
	}
	return out
}

func dbGrantHolders(ctx context.Context, c *reconcile.Client, users []wire.SystemUserResponse) (map[string]struct{}, error) {
	if !slices.ContainsFunc(users, func(u wire.SystemUserResponse) bool { return u.Origin == entplatform.RBACOriginBuiltin }) {
		return nil, nil
	}
	grants, err := c.ListRBACGrants(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, g := range grants {
		if g.Origin != entplatform.RBACOriginYAML && g.UserID != nil {
			out[*g.UserID] = struct{}{}
		}
	}
	return out, nil
}

func roleIDToName(roles []wire.RoleResponse) map[string]string {
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		if (r.Origin == entplatform.RBACOriginCustom || r.Origin == entplatform.RBACOriginBuiltin) && r.ID != "" {
			out[r.ID] = r.Name
		}
	}
	return out
}

func anyRoleIDToName(roles []wire.RoleResponse) map[string]string {
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		if r.ID != "" {
			out[r.ID] = r.Name
		}
	}
	return out
}

func builtinRoleIDToName(roles []wire.RoleResponse) map[string]string {
	out := make(map[string]string)
	for _, r := range roles {
		if r.Origin == entplatform.RBACOriginBuiltin && r.ID != "" {
			out[r.ID] = r.Name
		}
	}
	return out
}

func pulledSubjectsToItems(users []pulledSubject) []any {
	items := make([]any, 0, len(users))
	for _, u := range users {
		if u.subjectType == "" {
			items = append(items, u.identity)
			continue
		}
		m := map[string]any{"type": u.subjectType, "value": u.identity}
		if u.subjectIssuer != "" {
			m["issuer"] = u.subjectIssuer
		}
		items = append(items, m)
	}
	return items
}

func pullRBACGrants(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacGrants: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	builtinIDToName := builtinRoleIDToName(roles)
	if len(builtinIDToName) == 0 {
		return nil, nil, nil
	}

	grants, err := c.ListRBACGrants(ctx)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping rbacGrants: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	byRole := groupCustomGrantsByRole(grants, func(g wire.GrantResponse) (string, bool) {
		name, ok := builtinIDToName[g.RoleID]
		return name, ok
	})

	roleNames := make([]string, 0, len(byRole))
	for roleName := range byRole {
		roleNames = append(roleNames, roleName)
	}
	slices.Sort(roleNames)

	var entries []fileEntry
	var names []string
	for _, roleName := range roleNames {
		if opts.Name != "" && roleName != opts.Name {
			continue
		}
		users := byRole[roleName]
		slices.SortFunc(users, func(a, b pulledSubject) int {
			return strings.Compare(a.sortKey(), b.sortKey())
		})
		entry, ok, warn := buildRBACGrantEntry(roleName, users)
		if !ok {
			res.drop("%s", warn)
			continue
		}
		entries = append(entries, entry)
		names = append(names, roleName)
	}
	return entries, names, nil
}

func buildRBACGrantEntry(roleName string, users []pulledSubject) (fileEntry, bool, string) {
	safe := resourceFileName(roleName)
	if safe == "" {
		return fileEntry{}, false, fmt.Sprintf("rbacGrant: role name %q is not a safe file name", roleName)
	}
	spec := map[string]any{
		"roleName": roleName,
		"users":    pulledSubjectsToItems(users),
	}
	doc := resourceDoc{
		Kind: "rbacGrant",
		Name: roleName,
		Spec: dropEmpty(spec),
	}
	body, err := emitYAML(doc)
	if err != nil {
		return fileEntry{}, false, fmt.Sprintf("rbacGrant %q: marshal: %s", roleName, err)
	}
	return fileEntry{
		relPath: filepath.Join(rbacGrantsDir, safe+".yaml"),
		content: body,
	}, true, ""
}

func resolveRoleNameForRow(roleIDField string, isYAML bool, idToName map[string]string) (string, string) {
	if isYAML {
		if roleIDField == "" {
			return "", "YAML row has empty roleId — skipping"
		}
		if name, ok := idToName[roleIDField]; ok {
			return name, ""
		}
		if _, err := uuid.Parse(roleIDField); err == nil {
			return "", fmt.Sprintf("roleId %q names no role in the platform's role list", roleIDField)
		}
		return roleIDField, ""
	}
	if name, ok := idToName[roleIDField]; ok {
		return name, ""
	}
	return "", fmt.Sprintf("roleId %q does not resolve to a known DB role (the platform may be in an inconsistent state)", roleIDField)
}

func buildRBACRoleEntry(r wire.RoleResponse, idToName map[string]string, preview bool, users []pulledSubject) (fileEntry, bool, string) {
	rawSpec, err := translate.ReverseRBACRoleV2(r, translate.RBACRoleV2Hooks{})
	if err != nil {
		return fileEntry{}, false, fmt.Sprintf("rbacRole %q: reverse: %s", r.Name, err)
	}
	name, _ := takeString(rawSpec, "name")
	if name == "" {
		name = r.Name
	}
	description, _ := takeString(rawSpec, "description")

	if rawInherits, ok := rawSpec["inherits"]; ok {
		if names := rewriteInheritsIDsToNames(rawInherits, idToName); names != nil {
			rawSpec["inherits"] = names
		} else {
			delete(rawSpec, "inherits")
		}
	}

	if len(users) > 0 {
		rawSpec["users"] = pulledSubjectsToItems(users)
	}

	safe := resourceFileName(name)
	if safe == "" {
		return fileEntry{}, false, fmt.Sprintf("rbacRole %q: name is not a safe file name", name)
	}

	doc := resourceDoc{
		Kind:        "rbacRole",
		Name:        name,
		Description: description,
		Spec:        dropEmpty(rawSpec),
	}
	body, err := emitYAML(doc)
	if err != nil {
		return fileEntry{}, false, fmt.Sprintf("rbacRole %q: marshal: %s", name, err)
	}
	if preview {
		body = wrapYAMLPreview("rbacRole", name, body)
		return fileEntry{
			relPath: filepath.Join(rbacRolesDir, rbacYAMLPreviewSubdir, safe+".yaml"),
			content: body,
		}, true, ""
	}
	return fileEntry{
		relPath: filepath.Join(rbacRolesDir, safe+".yaml"),
		content: body,
	}, true, ""
}

func buildRBACEntry(kind, dir, header string, spec map[string]any, preview bool) (fileEntry, bool, string) {
	safe := resourceFileName(header)
	if safe == "" {
		return fileEntry{}, false, fmt.Sprintf("%s %q: name is not a safe file name", kind, header)
	}
	doc := resourceDoc{
		Kind: kind,
		Name: header,
		Spec: dropEmpty(spec),
	}
	body, err := emitYAML(doc)
	if err != nil {
		return fileEntry{}, false, fmt.Sprintf("%s %q: marshal: %s", kind, header, err)
	}
	if preview {
		body = wrapYAMLPreview(kind, header, body)
		return fileEntry{
			relPath: filepath.Join(dir, rbacYAMLPreviewSubdir, safe+".yaml"),
			content: body,
		}, true, ""
	}
	return fileEntry{
		relPath: filepath.Join(dir, safe+".yaml"),
		content: body,
	}, true, ""
}

func rewriteInheritsIDsToNames(raw any, idToName map[string]string) []string {
	ids, ok := asStringSlice(raw)
	if !ok || len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := idToName[id]; ok {
			out = append(out, name)
			continue
		}
		out = append(out, id)
	}
	return out
}

func wrapYAMLPreview(kind, name string, body []byte) []byte {
	name = strings.ReplaceAll(name, "\n", " ")
	name = strings.ReplaceAll(name, "\r", " ")
	var out bytes.Buffer
	out.WriteString("# ─── YAML-defined ")
	out.WriteString(kind)
	out.WriteString(" (")
	out.WriteString(name)
	out.WriteString(") — read-only ───\n")
	out.WriteString("# This block is operator-owned and lives in the platform's\n")
	out.WriteString("# authorization_service YAML config. `sam config apply` cannot\n")
	out.WriteString("# create, modify, or delete it; the platform rejects any DB-side\n")
	out.WriteString("# attempt to do so with HTTP 409 (name collision) or via the FK\n")
	out.WriteString("# constraints on rbac_roles.id. Edit the platform's YAML to\n")
	out.WriteString("# change it.\n")
	out.WriteString("#\n")
	for _, line := range bytes.Split(body, []byte("\n")) {
		if len(line) == 0 {
			out.WriteString("#\n")
			continue
		}
		out.WriteString("# ")
		out.Write(line)
		out.WriteString("\n")
	}
	return out.Bytes()
}

func uniqueSlug(parts ...string) string {
	pieces := make([]string, 0, len(parts))
	for _, p := range parts {
		s := slugifyField(p)
		if s == "" {
			s = "-"
		}
		pieces = append(pieces, s)
	}
	joined := strings.Join(pieces, "__")
	joined = strings.TrimLeft(joined, ".")
	if joined == "" {
		return ""
	}
	return joined
}

func slugifyField(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if ok {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func dedupeName(name string, used map[string]struct{}) string {
	if name == "" {
		return ""
	}
	if _, dup := used[name]; !dup {
		used[name] = struct{}{}
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if _, dup := used[candidate]; !dup {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
