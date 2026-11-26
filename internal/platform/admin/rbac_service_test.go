// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type counterMetrics struct {
	mu       sync.Mutex
	counters map[string]int64
	labels   map[string][]string
}

func newCounterMetrics() *counterMetrics {
	return &counterMetrics{counters: map[string]int64{}, labels: map[string][]string{}}
}

func (m *counterMetrics) Counter(name string, delta int64, labels ...string) {
	m.mu.Lock()
	m.counters[name] += delta
	if len(labels) > 0 {
		m.labels[name] = labels
	}
	m.mu.Unlock()
}

func (m *counterMetrics) labelsFor(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.labels[name]
}

func (m *counterMetrics) Gauge(string, float64, ...string)           {}
func (m *counterMetrics) Histogram(string, time.Duration, ...string) {}

func (m *counterMetrics) get(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name]
}

type failingDBSource struct{}

func (failingDBSource) Snapshot(_ context.Context) (*rbac.DBState, error) {
	return nil, errors.New("simulated DB failure")
}

func TestRBACService_RebuildFailure_CounterIncremented(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	composer := rbac.NewComposer(rbac.YAMLState{}, failingDBSource{}, logger)

	metrics := newCounterMetrics()
	svc := NewRBACService(repo, composer, nil, metrics, logger)

	_, err := svc.CreateRole(context.Background(), CreateRBACRoleRequest{
		Name:   "test-role",
		Scopes: []string{"agent_builder:*:read"},
	}, "test-user", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole returned error despite successful DB write: %v", err)
	}

	const metricName = "rbac_composer_rebuild_failures_total"
	got := metrics.get(metricName)
	if got != 1 {
		t.Errorf("%s = %d, want 1", metricName, got)
	}
}

func TestRBACService_SetAndGetDefaultRoles(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "limited", Scopes: []string{"agent:*:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if err = svc.SetDefaultRoles(ctx, []string{role.ID}, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetDefaultRoles: %v", err)
	}
	resp, err := svc.GetDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("GetDefaultRoles: %v", err)
	}
	if resp.Origin != RBACOriginCustom {
		t.Errorf("origin = %q, want %q", resp.Origin, RBACOriginCustom)
	}
	if len(resp.RoleIDs) != 1 || resp.RoleIDs[0] != role.ID {
		t.Errorf("roleIds = %v, want [%s]", resp.RoleIDs, role.ID)
	}

	if err = svc.SetDefaultRoles(ctx, nil, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetDefaultRoles(clear): %v", err)
	}
	resp, err = svc.GetDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("GetDefaultRoles after clear: %v", err)
	}
	if resp.Origin != RBACOriginYAML {
		t.Errorf("origin after clear = %q, want %q", resp.Origin, RBACOriginYAML)
	}
}

func TestRBACService_SetDefaultRoles_RejectsUnknownRole(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)

	const badA = "c0ffee00-0000-0000-0000-000000000000"
	const badB = "c0ffee00-0000-0000-0000-000000000001"
	err := svc.SetDefaultRoles(context.Background(), []string{badA, badB}, "tester", []string{"*"})
	if err == nil {
		t.Fatal("expected error for unknown default role id")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %T, want *ValidationError", err)
	}
	if !strings.Contains(verr.Message, badA) || !strings.Contains(verr.Message, badB) {
		t.Errorf("message = %q, want it to name both unknown ids", verr.Message)
	}
}

func TestRBACService_BadTargetsBeatTheGuard(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	const unknownID = "00000000-0000-0000-0000-0000000000ff"

	var nf *NotFoundError
	if err := svc.DeleteRole(ctx, unknownID, "tester", []string{"*"}); !errors.As(err, &nf) {
		t.Errorf("DeleteRole(unknown) = %T (%v), want *NotFoundError", err, err)
	}

	const alsoUnknownID = "00000000-0000-0000-0000-0000000000fe"
	var verr *ValidationError
	_, err := svc.CreateRole(ctx, CreateRBACRoleRequest{
		Name: "orphan", Scopes: []string{"agent:*:read"}, Inherits: []string{unknownID, alsoUnknownID},
	}, "tester", []string{"*"})
	if !errors.As(err, &verr) {
		t.Errorf("CreateRole(unknown inherits) = %T (%v), want *ValidationError", err, err)
	} else if !strings.Contains(verr.Message, unknownID) || !strings.Contains(verr.Message, alsoUnknownID) {
		t.Errorf("message = %q, want it to name both unknown parents", verr.Message)
	}

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "real", Scopes: []string{"agent:*:read"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	_, err = svc.UpdateRole(ctx, role.ID, UpdateRBACRoleRequest{
		Name: "real", Scopes: []string{"agent:*:read"}, Inherits: []string{unknownID},
	}, "tester", []string{"*"})
	if !errors.As(err, &verr) {
		t.Errorf("UpdateRole(unknown inherits) = %T (%v), want *ValidationError", err, err)
	}

	privID := createRoleWithScopes(ctx, t, svc, "priv", []string{"tool:_:read"}, nil)
	_, err = svc.UpdateClaimMapping(ctx, unknownID, UpdateRBACClaimMappingRequest{
		Name: "Engineers", OIDCProvider: "azure", ClaimKey: "groups",
		ClaimValue: "engineering", RoleIDs: []string{privID},
	}, "tester", []string{"rbac:_:update"})
	if !errors.As(err, &nf) {
		t.Errorf("UpdateClaimMapping(unknown) = %T (%v), want *NotFoundError", err, err)
	}

	if err := svc.DeleteClaimMapping(ctx, unknownID, "tester", nil); !errors.As(err, &nf) {
		t.Errorf("DeleteClaimMapping(unknown) = %T (%v), want *NotFoundError", err, err)
	}

	if err := svc.DeleteGrant(ctx, unknownID, "tester", nil); !errors.As(err, &nf) {
		t.Errorf("DeleteGrant(unknown) = %T (%v), want *NotFoundError", err, err)
	}
}

func TestRBACMessages_AreSentenceCase(t *testing.T) {
	messages := map[string]string{
		"errYAMLImmutable":              errYAMLImmutable.Message,
		"errBuiltinImmutable":           errBuiltinImmutable.Message,
		"errBuiltinSystemUserImmutable": errBuiltinSystemUserImmutable.Message,
		"errRoleIsDefault":              errRoleIsDefault.Message,
	}
	for _, resource := range []string{auditResourceRole, resourceClaimMapping, auditResourceGrant, auditResourceSystemUser, "unrecognized"} {
		messages["uniqueConflictMessage("+resource+")"] = uniqueConflictMessage(resource)
	}

	_, sortErr := sortAndPage[RBACRoleResponse](nil, 1, 20, "bogus", "asc", "name", rbacRoleSortComparators)
	var invalidSort *ValidationError
	if !errors.As(sortErr, &invalidSort) {
		t.Fatalf("sortAndPage with an unknown sort field = %T (%v), want *ValidationError", sortErr, sortErr)
	}
	messages["sortAndPage(unknown sort field)"] = invalidSort.Message

	for name, msg := range messages {
		first := []rune(msg)[0]
		if !unicode.IsUpper(first) {
			t.Errorf("%s = %q, want it to start with an upper-case letter", name, msg)
		}
		if strings.Contains(msg, "YAML") {
			t.Errorf("%s = %q, want no YAML reference in user-facing copy", name, msg)
		}
	}
}

func TestRBACService_ZeroScopeRole_EmitsEmptyScopeArray(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	ctx := context.Background()

	created, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "no-scopes"}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole with no scopes: %v", err)
	}

	fetched, err := svc.GetRole(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	listed, err := svc.ListRoles(ctx)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	var fromList *RBACRoleResponse
	for i := range listed {
		if listed[i].ID == created.ID {
			fromList = &listed[i]
		}
	}
	if fromList == nil {
		t.Fatalf("role %s missing from ListRoles", created.ID)
	}

	for _, tc := range []struct {
		path string
		resp *RBACRoleResponse
	}{
		{"CreateRole", created},
		{"GetRole", fetched},
		{"ListRoles", fromList},
	} {
		if tc.resp.Scopes == nil {
			t.Errorf("%s: Scopes is nil, want an empty slice (nil marshals to null)", tc.path)
		}
		body, err := json.Marshal(tc.resp)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.path, err)
		}
		if !strings.Contains(string(body), `"scopes":[]`) {
			t.Errorf("%s: body = %s, want it to carry \"scopes\":[]", tc.path, body)
		}
	}
}

func TestRBACService_ZeroScopeYAMLRole_EmitsEmptyScopeArray(t *testing.T) {
	const roleName = "sam_no_scopes"
	svc, _ := newClaimMappingStack(t, rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			roleName: {Description: "operator-managed, no scopes yet"},
		}},
	})
	ctx := context.Background()

	fetched, err := svc.GetRole(ctx, roleName)
	if err != nil {
		t.Fatalf("GetRole(%s): %v", roleName, err)
	}
	listed, err := svc.ListRoles(ctx)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	var fromList *RBACRoleResponse
	for i := range listed {
		if listed[i].ID == roleName {
			fromList = &listed[i]
		}
	}
	if fromList == nil {
		t.Fatalf("yaml role %s missing from ListRoles", roleName)
	}

	for _, tc := range []struct {
		path string
		resp *RBACRoleResponse
	}{
		{"GetRole", fetched},
		{"ListRoles", fromList},
	} {
		if tc.resp.Origin != RBACOriginYAML {
			t.Errorf("%s: origin = %q, want %q", tc.path, tc.resp.Origin, RBACOriginYAML)
		}
		body, err := json.Marshal(tc.resp)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.path, err)
		}
		if !strings.Contains(string(body), `"scopes":[]`) {
			t.Errorf("%s: body = %s, want it to carry \"scopes\":[]", tc.path, body)
		}
	}
}

func TestRBACService_ListRolesHidesDeprecatedChannelRole(t *testing.T) {
	svc, _ := newClaimMappingStack(t, rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			rbac.RoleSystemInvokeAll:     {Scopes: []string{"agent:*:invoke", "workflow:*:invoke"}},
			rbac.RoleSystemChannelInvoke: {Scopes: []string{"agent:Orchestrator:invoke"}},
		}},
	})

	listed, err := svc.ListRoles(context.Background())
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	var ids []string
	for _, r := range listed {
		ids = append(ids, r.ID)
	}
	if slices.Contains(ids, rbac.RoleSystemChannelInvoke) {
		t.Errorf("ListRoles = %v, want %s hidden (deprecated)", ids, rbac.RoleSystemChannelInvoke)
	}
	if !slices.Contains(ids, rbac.RoleSystemInvokeAll) {
		t.Errorf("ListRoles = %v, want %s still listed", ids, rbac.RoleSystemInvokeAll)
	}
}

func TestRBACService_GetDefaultRoles_EmitsEmptyRoleIDArray(t *testing.T) {
	svc, _ := newClaimMappingStack(t, rbac.YAMLState{})

	resp, err := svc.GetDefaultRoles(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultRoles: %v", err)
	}
	if resp.Origin != RBACOriginYAML {
		t.Errorf("origin = %q, want %q", resp.Origin, RBACOriginYAML)
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"roleIds":[]`) {
		t.Errorf("body = %s, want it to carry \"roleIds\":[]", body)
	}
}

func TestRBACService_DeleteRole_RejectsRoleInDefaultSet(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	ctx := context.Background()

	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "floor", Scopes: []string{"agent:*:read"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err = svc.SetDefaultRoles(ctx, []string{role.ID}, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetDefaultRoles: %v", err)
	}

	var cerr *ConflictError
	if err = svc.DeleteRole(ctx, role.ID, "tester", []string{"*"}); !errors.As(err, &cerr) {
		t.Fatalf("DeleteRole on a default role = %T (%v), want *ConflictError", err, err)
	}
	defaults, err := repo.ListDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("ListDefaultRoles: %v", err)
	}
	if len(defaults) != 1 || defaults[0] != role.ID {
		t.Errorf("default roles = %v, want the refused role still present", defaults)
	}

	if err = svc.SetDefaultRoles(ctx, nil, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetDefaultRoles(clear): %v", err)
	}
	if err = svc.DeleteRole(ctx, role.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("delete after clearing the default-role set: %v", err)
	}
}
