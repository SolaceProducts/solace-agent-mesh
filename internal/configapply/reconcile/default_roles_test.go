// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type defaultRolesServer struct {
	current wire.DefaultRolesResponse
	roles   []wire.RoleResponse
	puts    atomic.Int32
	lastPut wire.ReplaceDefaultRolesRequest
}

func newDefaultRolesServer(t *testing.T) (*defaultRolesServer, *Client) {
	t.Helper()
	srv := &defaultRolesServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/defaultRoles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, srv.current)
	})
	mux.HandleFunc("PUT /api/v2/platform/rbac/defaultRoles", func(w http.ResponseWriter, r *http.Request) {
		var body wire.ReplaceDefaultRolesRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		srv.puts.Add(1)
		srv.lastPut = body
		srv.current = wire.DefaultRolesResponse{RoleIDs: body.RoleIDs, Origin: entplatform.RBACOriginCustom}
		writeV2Single(w, http.StatusOK, srv.current)
	})
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, srv.roles)
	})
	return srv, newV2PlatformClient(t, mux)
}

func TestPlanDefaultRoles_NoBlockIsUnmanaged(t *testing.T) {
	_, c := newDefaultRolesServer(t)
	plan, err := planDefaultRoles(t.Context(), c, nil)
	if err != nil {
		t.Fatalf("planDefaultRoles: %v", err)
	}
	if plan.Verb != defaultRolesVerbNone {
		t.Errorf("Verb = %q; want %q", plan.Verb, defaultRolesVerbNone)
	}
}

func TestPlanDefaultRoles_DriftFromYAMLFallback(t *testing.T) {
	srv, c := newDefaultRolesServer(t)
	srv.roles = []wire.RoleResponse{
		{ID: "uuid-1", Name: "limited", Origin: entplatform.RBACOriginCustom},
		{ID: "uuid-sam-user", Name: "sam_user", Origin: entplatform.RBACOriginYAML},
	}
	srv.current = wire.DefaultRolesResponse{RoleIDs: []string{"uuid-sam-user"}, Origin: entplatform.RBACOriginYAML}

	plan, err := planDefaultRoles(t.Context(), c, &manifest.DefaultRolesSpec{Roles: []string{"limited"}})
	if err != nil {
		t.Fatalf("planDefaultRoles: %v", err)
	}
	if plan.Verb != defaultRolesVerbPut {
		t.Errorf("Verb = %q; want %q", plan.Verb, defaultRolesVerbPut)
	}
}

func TestPlanDefaultRoles_Unchanged(t *testing.T) {
	srv, c := newDefaultRolesServer(t)
	srv.roles = []wire.RoleResponse{{ID: "uuid-1", Name: "limited", Origin: entplatform.RBACOriginCustom}}
	srv.current = wire.DefaultRolesResponse{RoleIDs: []string{"uuid-1"}, Origin: entplatform.RBACOriginCustom}

	plan, err := planDefaultRoles(t.Context(), c, &manifest.DefaultRolesSpec{Roles: []string{"limited"}})
	if err != nil {
		t.Fatalf("planDefaultRoles: %v", err)
	}
	if plan.Verb != defaultRolesVerbUnchanged {
		t.Errorf("Verb = %q; want %q", plan.Verb, defaultRolesVerbUnchanged)
	}
}

func TestApplyDefaultRoles_PutResolvesNamesToIDs(t *testing.T) {
	srv, c := newDefaultRolesServer(t)
	srv.roles = []wire.RoleResponse{{ID: "uuid-1", Name: "limited", Origin: entplatform.RBACOriginCustom}}
	plan := &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: []string{"limited"}}

	out := applyDefaultRoles(t.Context(), c, plan, ApplyOptions{}, map[string]string{})
	if out == nil || out.Applied == nil {
		t.Fatalf("expected an applied PUT; out = %+v", out)
	}
	if srv.puts.Load() != 1 {
		t.Errorf("puts = %d; want 1", srv.puts.Load())
	}
	if len(srv.lastPut.RoleIDs) != 1 || srv.lastPut.RoleIDs[0] != "uuid-1" {
		t.Errorf("PUT roleIds = %v; want [uuid-1]", srv.lastPut.RoleIDs)
	}
}

func TestApplyDefaultRoles_RejectsNonDBRole(t *testing.T) {
	srv, c := newDefaultRolesServer(t)
	srv.roles = []wire.RoleResponse{{ID: "sam_user", Name: "sam_user", Origin: entplatform.RBACOriginYAML}}
	plan := &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: []string{"sam_user"}}

	out := applyDefaultRoles(t.Context(), c, plan, ApplyOptions{}, map[string]string{})
	if out == nil || len(out.Failures) != 1 {
		t.Fatalf("expected a failure for a YAML role; out = %+v", out)
	}
	if srv.puts.Load() != 0 {
		t.Errorf("puts = %d; want 0 (no PUT on unresolved role)", srv.puts.Load())
	}
}

func TestApplyDefaultRoles_SkippedUnderMiniIDP(t *testing.T) {
	srv, c := newDefaultRolesServer(t)
	plan := &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: []string{"limited"}}

	out := applyDefaultRoles(t.Context(), c, plan, ApplyOptions{SkipRBACWrites: true}, map[string]string{})
	if out == nil || out.Skipped == nil {
		t.Fatalf("expected skipped outcome under mini-IDP; out = %+v", out)
	}
	if srv.puts.Load() != 0 {
		t.Errorf("puts = %d; want 0", srv.puts.Load())
	}
}
