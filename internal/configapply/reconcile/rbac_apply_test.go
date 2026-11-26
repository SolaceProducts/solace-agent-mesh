// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func newRBACTestClient(t *testing.T, mux *http.ServeMux) (*Client, *httptest.Server) {
	t.Helper()
	srv := newV2Platform(t, mux)
	return NewClient(target.Resolved{URL: srv.URL, Token: "tok"}), srv
}

func TestRBACFeatureProbe_Enabled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.RoleResponse{})
	})
	c, _ := newRBACTestClient(t, mux)
	enabled, err := rbacFeatureProbe(t.Context(), c)
	if err != nil {
		t.Fatalf("rbacFeatureProbe: %v", err)
	}
	if !enabled {
		t.Errorf("expected enabled=true on 200")
	}
}

func TestRBACFeatureProbe_DisabledOn501(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusNotImplemented, "feature off", nil)
	})
	c, _ := newRBACTestClient(t, mux)
	enabled, err := rbacFeatureProbe(t.Context(), c)
	if err != nil {
		t.Fatalf("rbacFeatureProbe: %v", err)
	}
	if enabled {
		t.Errorf("expected enabled=false on 501")
	}
}

func TestApplyRBACRoles_CreateUpdateDelete(t *testing.T) {
	var posts, puts, deletes int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		posts++
		writeV2Single(w, http.StatusCreated, wire.RoleResponse{ID: "uuid-new", Name: "new_role", Origin: entplatform.RBACOriginCustom})
	})
	mux.HandleFunc("PUT /api/v2/platform/rbac/roles/uuid-existing", func(w http.ResponseWriter, _ *http.Request) {
		puts++
		writeV2Single(w, http.StatusOK, wire.RoleResponse{ID: "uuid-existing", Name: "existing", Origin: entplatform.RBACOriginCustom})
	})
	mux.HandleFunc("DELETE /api/v2/platform/rbac/roles/uuid-old", func(w http.ResponseWriter, _ *http.Request) {
		deletes++
		w.WriteHeader(http.StatusNoContent)
	})

	c, _ := newRBACTestClient(t, mux)
	plan := &rbacRolePlan{
		Creates: []rbacRoleOp{{Name: "new_role", Create: wire.CreateRoleRequest{Name: "new_role"}}},
		Updates: []rbacRoleOp{{Name: "existing", ID: "uuid-existing", Patch: wire.ReplaceRoleRequest{Name: "existing"}}},
		Deletes: []rbacRoleOp{{Name: "old", ID: "uuid-old"}},
	}
	outcome := applyRBACRoles(t.Context(), c, plan, ApplyOptions{Prune: true})
	applyRBACRoleDeletes(t.Context(), c, plan, ApplyOptions{Prune: true}, outcome)

	if len(outcome.Created) != 1 || posts != 1 {
		t.Errorf("created=%d posts=%d", len(outcome.Created), posts)
	}
	if len(outcome.Updated) != 1 || puts != 1 {
		t.Errorf("updated=%d puts=%d", len(outcome.Updated), puts)
	}
	if len(outcome.Deleted) != 1 || deletes != 1 {
		t.Errorf("deleted=%d deletes=%d", len(outcome.Deleted), deletes)
	}
	if outcome.Created[0].Op.ID != "uuid-new" {
		t.Errorf("expected created ID propagated; got %q", outcome.Created[0].Op.ID)
	}
}

func TestRBACManageProbe(t *testing.T) {
	t.Run("manage_allowed", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
			writeV2Single(w, http.StatusOK, map[string]bool{"manageRbac": true})
		})
		c, _ := newRBACTestClient(t, mux)
		allowed, err := rbacManageProbe(t.Context(), c)
		if err != nil {
			t.Fatalf("rbacManageProbe: %v", err)
		}
		if !allowed {
			t.Errorf("expected allowed=true when manageRbac is true")
		}
	})

	t.Run("manage_blocked_by_mini_idp", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
			writeV2Single(w, http.StatusOK, map[string]bool{"manageRbac": false})
		})
		c, _ := newRBACTestClient(t, mux)
		allowed, err := rbacManageProbe(t.Context(), c)
		if err != nil {
			t.Fatalf("rbacManageProbe: %v", err)
		}
		if allowed {
			t.Errorf("expected allowed=false when manageRbac is false")
		}
	})

	t.Run("older_platform_404_assumes_allowed", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		c, _ := newRBACTestClient(t, mux)
		allowed, err := rbacManageProbe(t.Context(), c)
		if err != nil {
			t.Fatalf("rbacManageProbe: %v", err)
		}
		if !allowed {
			t.Errorf("expected allowed=true for older platform (404)")
		}
	})

	t.Run("missing_key_assumes_allowed", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
			writeV2Single(w, http.StatusOK, map[string]bool{})
		})
		c, _ := newRBACTestClient(t, mux)
		allowed, err := rbacManageProbe(t.Context(), c)
		if err != nil {
			t.Fatalf("rbacManageProbe: %v", err)
		}
		if !allowed {
			t.Errorf("expected allowed=true when manageRbac key is missing")
		}
	})

	t.Run("5xx_propagates", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		c, _ := newRBACTestClient(t, mux)
		if _, err := rbacManageProbe(t.Context(), c); err == nil {
			t.Errorf("expected error on 500")
		}
	})
}

func TestApplyRBACRoles_SkipsWritesWhenMiniIDP(t *testing.T) {
	var posts, puts, deletes int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/roles", func(http.ResponseWriter, *http.Request) { posts++ })
	mux.HandleFunc("PUT /api/v2/platform/rbac/roles/uuid-existing", func(http.ResponseWriter, *http.Request) { puts++ })
	mux.HandleFunc("DELETE /api/v2/platform/rbac/roles/uuid-old", func(http.ResponseWriter, *http.Request) { deletes++ })

	c, _ := newRBACTestClient(t, mux)
	plan := &rbacRolePlan{
		Creates: []rbacRoleOp{{Name: "new_role"}},
		Updates: []rbacRoleOp{{Name: "existing", ID: "uuid-existing"}},
		Deletes: []rbacRoleOp{{Name: "old", ID: "uuid-old"}},
	}
	opts := ApplyOptions{Prune: true, SkipRBACWrites: true}
	out := applyRBACRoles(t.Context(), c, plan, opts)
	applyRBACRoleDeletes(t.Context(), c, plan, opts, out)

	if posts+puts+deletes != 0 {
		t.Errorf("expected no HTTP writes; got posts=%d puts=%d deletes=%d", posts, puts, deletes)
	}
	if len(out.Skipped) != 3 {
		t.Errorf("expected 3 skipped, got %d", len(out.Skipped))
	}
	for _, r := range out.Skipped {
		if r.Reason != skipReasonMiniIDP {
			t.Errorf("expected Reason=%q, got %q", skipReasonMiniIDP, r.Reason)
		}
	}
}

func TestApplyRBACAssignments_SkipsWritesWhenMiniIDP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/grants", func(http.ResponseWriter, *http.Request) {
		t.Fatal("must not POST when SkipRBACWrites is on")
	})
	mux.HandleFunc("DELETE /api/v2/platform/rbac/grants/", func(http.ResponseWriter, *http.Request) {
		t.Fatal("must not DELETE when SkipRBACWrites is on")
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacGrantPlan{
		Creates: []rbacGrantOp{{Identity: "alice"}},
		Deletes: []rbacGrantOp{{Identity: "bob", ID: "assign-bob"}},
	}
	out := applyRBACGrants(t.Context(), c, plan, ApplyOptions{Prune: true, SkipRBACWrites: true}, nil)
	if len(out.Skipped) != 2 {
		t.Fatalf("expected 2 skipped, got %d", len(out.Skipped))
	}
	if len(out.Failures) != 0 || len(out.Created) != 0 || len(out.Deleted) != 0 {
		t.Errorf("expected skip-only outcome, got %+v", out)
	}
	for _, r := range out.Skipped {
		if r.Reason != skipReasonMiniIDP {
			t.Errorf("expected Reason=%q, got %q", skipReasonMiniIDP, r.Reason)
		}
	}
}

func TestApplyRBACClaimMappings_SkipsWritesWhenMiniIDP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/claimMappings", func(http.ResponseWriter, *http.Request) {
		t.Fatal("must not POST when SkipRBACWrites is on")
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacClaimMappingPlan{
		Creates: []rbacClaimMappingOp{{OIDCProvider: "azure", ClaimValue: "admins"}},
		Deletes: []rbacClaimMappingOp{{ID: "map-1", OIDCProvider: "okta", ClaimValue: "viewers"}},
	}
	out := applyRBACClaimMappings(t.Context(), c, plan, ApplyOptions{Prune: true, SkipRBACWrites: true}, nil)
	if len(out.Skipped) != 2 {
		t.Fatalf("expected 2 skipped, got %d", len(out.Skipped))
	}
	if len(out.Failures) != 0 || len(out.Created) != 0 || len(out.Deleted) != 0 {
		t.Errorf("expected skip-only outcome, got %+v", out)
	}
	for _, r := range out.Skipped {
		if r.Reason != skipReasonMiniIDP {
			t.Errorf("expected Reason=%q, got %q", skipReasonMiniIDP, r.Reason)
		}
	}
}

func TestRenderPlan_EmitsMiniIDPWarning(t *testing.T) {
	var buf bytes.Buffer
	RenderPlan(&buf, &PlanResult{RBACWritesBlocked: true}, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "mini_idp_mode: true") {
		t.Errorf("expected mini-IDP warning, got %q", out)
	}
	if !strings.Contains(out, "RBAC writes are blocked") {
		t.Errorf("expected blocked phrasing, got %q", out)
	}
}

func TestRenderRBACRoleApply_UsesMiniIDPLabelWhenReasonSet(t *testing.T) {
	var buf bytes.Buffer
	o := &rbacRoleOutcome{
		Skipped: []rbacRoleOpResult{
			{Op: rbacRoleOp{Name: "blocked-role"}, Verb: "create", Reason: skipReasonMiniIDP},
		},
	}
	renderRBACRoleApply(&buf, o, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "mini_idp_mode") {
		t.Errorf("expected mini_idp_mode label, got %q", out)
	}
	if strings.Contains(out, "use --prune") {
		t.Errorf("mini-IDP skip must not show prune notice; got %q", out)
	}
}

func TestApplyRBACPhase_RoleDeleteFollowsDefaultRolesPut(t *testing.T) {
	var defaultsPutSeen bool
	var deleteAttempts int
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v2/platform/rbac/defaultRoles", func(w http.ResponseWriter, _ *http.Request) {
		defaultsPutSeen = true
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v2/platform/rbac/roles/uuid-doomed", func(w http.ResponseWriter, _ *http.Request) {
		deleteAttempts++
		if !defaultsPutSeen {
			writeV2Error(w, http.StatusConflict, "Assigned as a default role; remove it from the default roles before deleting it", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	c, _ := newRBACTestClient(t, mux)
	plan := &PlanResult{
		RBACRoles:    &rbacRolePlan{Deletes: []rbacRoleOp{{Name: "doomed", ID: "uuid-doomed"}}},
		DefaultRoles: &defaultRolesPlan{Verb: defaultRolesVerbPut},
	}
	out := &ApplyResult{Plan: plan}
	applyRBACPhase(t.Context(), c, plan, ApplyOptions{Prune: true}, out)

	if !defaultsPutSeen {
		t.Fatal("defaults PUT never issued")
	}
	if deleteAttempts != 1 {
		t.Errorf("delete attempts = %d, want 1", deleteAttempts)
	}
	if out.RBACRoles == nil || len(out.RBACRoles.Deleted) != 1 {
		t.Fatalf("role delete did not succeed after the defaults PUT: %+v", out.RBACRoles)
	}
	if len(out.RBACRoles.Failures) != 0 {
		t.Errorf("unexpected failures: %+v", out.RBACRoles.Failures)
	}
}

func TestApplyRBACPhase_PromotesRBACWritesBlockedToSkip(t *testing.T) {
	var requests []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
	})

	c, _ := newRBACTestClient(t, mux)
	plan := &PlanResult{
		RBACWritesBlocked: true,
		RBACRoles: &rbacRolePlan{
			Creates: []rbacRoleOp{{Name: "fresh"}},
			Deletes: []rbacRoleOp{{Name: "doomed", ID: "uuid-doomed"}},
		},
		DefaultRoles: &defaultRolesPlan{Verb: defaultRolesVerbPut},
	}
	out := &ApplyResult{Plan: plan}
	applyRBACPhase(t.Context(), c, plan, ApplyOptions{Prune: true}, out)

	if len(requests) != 0 {
		t.Errorf("RBAC writes reached the platform despite RBACWritesBlocked: %v", requests)
	}
	if out.RBACRoles == nil || len(out.RBACRoles.Skipped) != 2 {
		t.Fatalf("want create and delete both skipped, got %+v", out.RBACRoles)
	}
	for _, s := range out.RBACRoles.Skipped {
		if s.Reason != skipReasonMiniIDP {
			t.Errorf("skip reason = %q, want %q", s.Reason, skipReasonMiniIDP)
		}
	}
	if len(out.RBACRoles.Failures) != 0 {
		t.Errorf("unexpected failures: %+v", out.RBACRoles.Failures)
	}
}

func TestApplyRBACRoles_DeleteSkippedWithoutPrune(t *testing.T) {
	c, _ := newRBACTestClient(t, http.NewServeMux())
	plan := &rbacRolePlan{Deletes: []rbacRoleOp{{Name: "old", ID: "uuid-old"}}}
	outcome := &rbacRoleOutcome{Kind: "rbacRole"}
	applyRBACRoleDeletes(t.Context(), c, plan, ApplyOptions{Prune: false}, outcome)
	if len(outcome.Skipped) != 1 || len(outcome.Deleted) != 0 {
		t.Errorf("skipped=%d deleted=%d", len(outcome.Skipped), len(outcome.Deleted))
	}
}

func TestApplyRBACRoles_RewritesInheritsPlaceholder(t *testing.T) {
	var capturedInherits []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/roles", func(w http.ResponseWriter, r *http.Request) {
		var body wire.CreateRoleRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "parent" {
			writeV2Single(w, http.StatusCreated, wire.RoleResponse{ID: "uuid-parent", Name: "parent", Origin: entplatform.RBACOriginCustom})
			return
		}
		capturedInherits = body.Inherits
		writeV2Single(w, http.StatusCreated, wire.RoleResponse{ID: "uuid-child", Name: "child", Origin: entplatform.RBACOriginCustom})
	})
	c, _ := newRBACTestClient(t, mux)

	plan := &rbacRolePlan{
		Creates: []rbacRoleOp{
			{Name: "parent", Create: wire.CreateRoleRequest{Name: "parent"}},
			{Name: "child", Create: wire.CreateRoleRequest{Name: "child", Inherits: []string{"parent"}}},
		},
	}
	_ = applyRBACRoles(t.Context(), c, plan, ApplyOptions{})

	if len(capturedInherits) != 1 || capturedInherits[0] != "uuid-parent" {
		t.Errorf("expected inherits rewritten to UUID, got %v", capturedInherits)
	}
}

func TestApplyRBACRoles_CreatesParentBeforeChildRegardlessOfPlanOrder(t *testing.T) {
	var capturedInherits []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/roles", func(w http.ResponseWriter, r *http.Request) {
		var body wire.CreateRoleRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "zzz_parent" {
			writeV2Single(w, http.StatusCreated, wire.RoleResponse{ID: "uuid-parent", Name: "zzz_parent", Origin: entplatform.RBACOriginCustom})
			return
		}
		capturedInherits = body.Inherits
		writeV2Single(w, http.StatusCreated, wire.RoleResponse{ID: "uuid-child", Name: "aaa_child", Origin: entplatform.RBACOriginCustom})
	})
	c, _ := newRBACTestClient(t, mux)

	plan := &rbacRolePlan{
		Creates: []rbacRoleOp{
			{Name: "aaa_child", Create: wire.CreateRoleRequest{Name: "aaa_child", Inherits: []string{"zzz_parent"}}},
			{Name: "zzz_parent", Create: wire.CreateRoleRequest{Name: "zzz_parent"}},
		},
	}
	outcome := applyRBACRoles(t.Context(), c, plan, ApplyOptions{})

	if len(outcome.Failures) != 0 {
		t.Fatalf("expected no failures, got %+v", outcome.Failures)
	}
	if len(capturedInherits) != 1 || capturedInherits[0] != "uuid-parent" {
		t.Errorf("expected child's inherits rewritten to parent's UUID despite alphabetical plan order, got %v", capturedInherits)
	}
}

func TestApplyRBACGrants_CreatePropagatesRoleID(t *testing.T) {
	var seenBody wire.CreateGrantRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/grants", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		writeV2Single(w, http.StatusCreated, wire.GrantResponse{ID: "grant-new", Subject: &apiv2.GrantSubject{Type: seenBody.Subject.Type, Value: seenBody.Subject.Value}, RoleID: seenBody.RoleID})
	})
	c, _ := newRBACTestClient(t, mux)

	plan := &rbacGrantPlan{
		Creates: []rbacGrantOp{{
			Identity: "alice",
			Create:   wire.CreateGrantRequest{Subject: apiv2.CreateGrantSubject{Type: "legacy", Value: "alice"}, RoleID: "sam_admin"},
		}},
	}
	out := applyRBACGrants(t.Context(), c, plan, ApplyOptions{}, map[string]string{"sam_admin": "uuid-admin"})
	if len(out.Created) != 1 {
		t.Fatalf("expected one Created, got %+v", out)
	}
	if seenBody.RoleID != "uuid-admin" {
		t.Errorf("expected roleID rewritten to UUID; got %q", seenBody.RoleID)
	}
}

func TestApplyRBACAssignments_DeleteSkippedWithoutPrune(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v2/platform/rbac/grants/", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("DELETE must not be issued for a grant delete without --prune")
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacGrantPlan{Deletes: []rbacGrantOp{{ID: "assign-1", Identity: "stale@x", RoleName: "sam_admin"}}}
	out := applyRBACGrants(t.Context(), c, plan, ApplyOptions{Prune: false}, nil)
	if len(out.Skipped) != 1 || len(out.Deleted) != 0 {
		t.Errorf("expected one Skipped and no Deleted without prune, got %+v", out)
	}
}

func TestApplyRBACClaimMappings_DeleteRespectsPrune(t *testing.T) {
	mux := http.NewServeMux()
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacClaimMappingPlan{Deletes: []rbacClaimMappingOp{{ID: "map-1"}}}
	out := applyRBACClaimMappings(t.Context(), c, plan, ApplyOptions{Prune: false}, nil)
	if len(out.Skipped) != 1 {
		t.Errorf("expected one Skipped without prune, got %+v", out)
	}
}

func TestRenderRBACRolePlan_EmitsTotalsLine(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacRolePlan{
		Creates: []rbacRoleOp{{Name: "a"}},
		Updates: []rbacRoleOp{{Name: "b"}},
		Deletes: []rbacRoleOp{{Name: "c"}},
	}
	renderRBACRolePlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "rbac/roles/") {
		t.Errorf("missing dir header: %q", out)
	}
	if !strings.Contains(out, "3 rbac/roles: 1 create, 1 update, 1 delete, 0 unchanged") {
		t.Errorf("missing totals line: %q", out)
	}
}

func TestRenderRBACRolePlan_EmptyShowsNoResources(t *testing.T) {
	var buf bytes.Buffer
	renderRBACRolePlan(&buf, &rbacRolePlan{}, FormatOptions{NoColor: true})
	if !strings.Contains(buf.String(), "(no resources)") {
		t.Errorf("missing empty marker: %q", buf.String())
	}
}

func TestRenderRBACAssignmentPlan_LabelsShowIdentityAndRole(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacGrantPlan{
		Creates: []rbacGrantOp{{
			Header:   "sam_admin/users/alice",
			Identity: "alice",
			RoleName: "sam_admin",
			Source:   "rbac/roles/sam_admin.yaml",
		}},
	}
	renderRBACGrantPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"alice → sam_admin", "rbac/roles/sam_admin.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %q", want, out)
		}
	}
}

func TestRenderRBACClaimMappingPlan_FallbackLabel(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacClaimMappingPlan{
		Deletes: []rbacClaimMappingOp{{OIDCProvider: "azure", ClaimValue: "admins"}},
	}
	renderRBACClaimMappingPlan(&buf, p, FormatOptions{NoColor: true})
	if !strings.Contains(buf.String(), "azure:admins") {
		t.Errorf("expected provider:claimValue fallback label: %q", buf.String())
	}
}

func TestRBACFeatureDisabledError_Message(t *testing.T) {
	err := &rbacFeatureDisabledError{declaredKinds: []string{"rbacRoles", "rbacAssignments"}}
	if !strings.Contains(err.Error(), "feature flag") {
		t.Errorf("error must mention feature flag: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "2 rbac*") {
		t.Errorf("error must include declared count: %q", err.Error())
	}
}

func TestSplitGrantKey(t *testing.T) {
	got := splitGrantKey("legacy||alice|uuid-admin")
	if got[0] != "legacy" || got[1] != "" || got[2] != "alice" || got[3] != "uuid-admin" {
		t.Errorf("splitGrantKey = %v", got)
	}
}

func TestRewriteInherits_PassesThroughUUIDs(t *testing.T) {
	uuid := "11111111-2222-3333-4444-555555555555"
	got := rewriteInherits([]string{uuid, "named"}, map[string]string{"named": "uuid-named"})
	if got[0] != uuid {
		t.Errorf("expected UUID pass-through, got %q", got[0])
	}
	if got[1] != "uuid-named" {
		t.Errorf("expected name rewritten, got %q", got[1])
	}
}

func TestListRBACGrants_HitsCorrectPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/grants", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.GrantResponse{{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "r1"}})
	})
	c, _ := newRBACTestClient(t, mux)
	items, err := c.ListRBACGrants(t.Context())
	if err != nil || len(items) != 1 || items[0].Subject == nil || items[0].Subject.Value != "alice" {
		t.Errorf("got items=%+v err=%v", items, err)
	}
}

func TestListRBACClaimMappings_HitsCorrectPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ClaimMappingResponse{{ID: "m1", OIDCProvider: new("azure")}})
	})
	c, _ := newRBACTestClient(t, mux)
	items, err := c.ListRBACClaimMappings(t.Context())
	if err != nil || len(items) != 1 || ptrString(items[0].OIDCProvider) != "azure" {
		t.Errorf("got items=%+v err=%v", items, err)
	}
}

func TestApplyRBACClaimMappings_Create(t *testing.T) {
	var seen wire.CreateClaimMappingRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/platform/rbac/claimMappings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		writeV2Single(w, http.StatusCreated, wire.ClaimMappingResponse{ID: "map-new", OIDCProvider: new(seen.OIDCProvider), RoleIDs: seen.RoleIDs})
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacClaimMappingPlan{
		Creates: []rbacClaimMappingOp{{
			Create: wire.CreateClaimMappingRequest{Name: "Admins", OIDCProvider: "azure", ClaimValue: "sam-admins", RoleIDs: []string{"sam_admin"}},
		}},
	}
	out := applyRBACClaimMappings(t.Context(), c, plan, ApplyOptions{}, map[string]string{"sam_admin": "uuid-admin"})
	if len(out.Created) != 1 {
		t.Fatalf("expected one Created, got %+v", out)
	}
	if !slices.Equal(seen.RoleIDs, []string{"uuid-admin"}) {
		t.Errorf("expected roleIds rewritten to [uuid-admin]; got %v", seen.RoleIDs)
	}
}

func TestApplyRBACAssignments_Delete(t *testing.T) {
	var deletes int
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v2/platform/rbac/grants/uuid-1", func(w http.ResponseWriter, _ *http.Request) {
		deletes++
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacGrantPlan{Deletes: []rbacGrantOp{{ID: "uuid-1"}}}
	out := applyRBACGrants(t.Context(), c, plan, ApplyOptions{Prune: true}, nil)
	if len(out.Deleted) != 1 || deletes != 1 {
		t.Errorf("deleted=%d server=%d", len(out.Deleted), deletes)
	}
}

func TestApplyRBACClaimMappings_Delete(t *testing.T) {
	var deletes int
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v2/platform/rbac/claimMappings/map-1", func(w http.ResponseWriter, _ *http.Request) {
		deletes++
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := newRBACTestClient(t, mux)
	plan := &rbacClaimMappingPlan{Deletes: []rbacClaimMappingOp{{ID: "map-1"}}}
	out := applyRBACClaimMappings(t.Context(), c, plan, ApplyOptions{Prune: true}, nil)
	if len(out.Deleted) != 1 || deletes != 1 {
		t.Errorf("deleted=%d server=%d", len(out.Deleted), deletes)
	}
}

func TestPlanRBACAssignments_RejectsMissingFields(t *testing.T) {
	xref := rbacRoleXRefAdapter{nameToID: map[string]string{}}
	desired := []resolve.ResolvedResource{{Kind: "rbacGrant", Name: "x", SourceFile: "x.yaml", Spec: map[string]any{"identity": ""}}}
	if _, err := planRBACGrants(desired, nil, nil, xref); err == nil {
		t.Errorf("expected error for missing identity")
	}
}

func TestPlanRBACClaimMappings_RejectsMissingFields(t *testing.T) {
	xref := rbacRoleXRefAdapter{nameToID: map[string]string{}}
	desired := []resolve.ResolvedResource{{Kind: "rbacClaimMapping", Name: "x", SourceFile: "x.yaml", Spec: map[string]any{"oidcProvider": "azure"}}}
	if _, err := planRBACClaimMappings(desired, nil, xref); err == nil {
		t.Errorf("expected error for missing fields")
	}
}

func TestRBACRoleXRefAdapter_LookupName_Found(t *testing.T) {
	x := rbacRoleXRefAdapter{idToName: map[string]string{"uuid-1": "sam_user"}}
	name, err := x.LookupName("rbacRole", "uuid-1")
	if err != nil || name != "sam_user" {
		t.Errorf("LookupName = %q err=%v", name, err)
	}
}

func TestRBACRoleXRefAdapter_LookupName_PassThroughUnknownKind(t *testing.T) {
	x := rbacRoleXRefAdapter{idToName: map[string]string{}}
	name, _ := x.LookupName("other", "uuid-x")
	if name != "uuid-x" {
		t.Errorf("expected pass-through for unknown kind, got %q", name)
	}
}

func TestRenderRBACAssignmentPlan_DeleteAndUnchanged(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacGrantPlan{
		Creates:   []rbacGrantOp{{Header: "alice", Identity: "alice"}},
		Deletes:   []rbacGrantOp{{Identity: "bob"}},
		Unchanged: []rbacGrantOp{{Header: "carol", Identity: "carol"}},
	}
	renderRBACGrantPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "alice") || !strings.Contains(out, "bob") || !strings.Contains(out, "carol") {
		t.Errorf("missing labels: %q", out)
	}
}

func TestRenderRBACAssignmentPlan_RoleChangeGrouped(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacGrantPlan{
		Creates: []rbacGrantOp{{Header: "alice-admin", Identity: "alice", RoleName: "sam_admin"}},
		Deletes: []rbacGrantOp{{Identity: "alice", RoleName: "sam_user"}},
	}
	renderRBACGrantPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "~ alice") {
		t.Errorf("expected grouped banner with ~ alice, got: %q", out)
	}
	if !strings.Contains(out, "role change") {
		t.Errorf("expected 'role change' label, got: %q", out)
	}
	if !strings.Contains(out, "role: sam_user → sam_admin") {
		t.Errorf("expected old → new role sub-line, got: %q", out)
	}
	if strings.Contains(out, "+ alice-admin") {
		t.Errorf("create row should not render independently for grouped pair: %q", out)
	}
	if strings.Contains(out, "- alice") && !strings.Contains(out, "~ alice") {
		t.Errorf("delete row should not render independently for grouped pair: %q", out)
	}
}

func TestRenderRBACAssignmentPlan_UnrelatedIdentitiesStaySeparate(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacGrantPlan{
		Creates: []rbacGrantOp{{Header: "alice-admin", Identity: "alice", RoleName: "sam_admin"}},
		Deletes: []rbacGrantOp{{Identity: "bob", RoleName: "sam_user"}},
	}
	renderRBACGrantPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if strings.Contains(out, "role change") {
		t.Errorf("unrelated identities must not group into role-change banner: %q", out)
	}
	if !strings.Contains(out, "+ alice → sam_admin") {
		t.Errorf("expected independent create row: %q", out)
	}
	if !strings.Contains(out, "- bob → sam_user") {
		t.Errorf("expected independent delete row: %q", out)
	}
}

func TestRenderRBACAssignmentPlan_RoleChangeMultiRole(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacGrantPlan{
		Creates: []rbacGrantOp{{Header: "alice-admin", Identity: "alice", RoleName: "sam_admin"}},
		Deletes: []rbacGrantOp{
			{Identity: "alice", RoleName: "sam_user"},
			{Identity: "alice", RoleName: "incident_responder"},
		},
	}
	renderRBACGrantPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "role: incident_responder, sam_user → sam_admin") {
		t.Errorf("expected sorted comma-joined old roles in sub-line, got: %q", out)
	}
}

func TestRenderRBACClaimMappingPlan_AllVariants(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacClaimMappingPlan{
		Creates:   []rbacClaimMappingOp{{Header: "azure-admins"}},
		Updates:   []rbacClaimMappingOp{{Header: "azure-editors"}},
		Deletes:   []rbacClaimMappingOp{{OIDCProvider: "okta", ClaimValue: "viewers"}},
		Unchanged: []rbacClaimMappingOp{{Header: "google-users"}},
	}
	renderRBACClaimMappingPlan(&buf, p, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "azure-admins") || !strings.Contains(out, "azure-editors") || !strings.Contains(out, "okta:viewers") || !strings.Contains(out, "google-users") {
		t.Errorf("missing labels: %q", out)
	}
}

func TestRenderRBACRolePlan_Unchanged(t *testing.T) {
	var buf bytes.Buffer
	p := &rbacRolePlan{Unchanged: []rbacRoleOp{{Name: "stable"}}}
	renderRBACRolePlan(&buf, p, FormatOptions{NoColor: true})
	if !strings.Contains(buf.String(), "stable") {
		t.Errorf("missing unchanged: %q", buf.String())
	}
}

func TestConvertRBACGrantPlan_DeleteSplitsKey(t *testing.T) {
	gp := &generic.KindPlan{Deletes: []generic.Op{{Name: "legacy||alice|uuid-admin", ID: "grant-1"}}}
	plan := convertRBACGrantPlan(gp, nil, nil,
		[]wire.GrantResponse{{ID: "grant-1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "uuid-admin"}},
		map[string]string{"uuid-admin": "sam_admin"}, nil)
	if len(plan.Deletes) != 1 || plan.Deletes[0].Identity != "alice" || plan.Deletes[0].RoleID != "uuid-admin" {
		t.Errorf("unexpected delete: %+v", plan.Deletes)
	}
	if plan.Deletes[0].RoleName != "sam_admin" {
		t.Errorf("expected RoleName resolved via idToName, got %q", plan.Deletes[0].RoleName)
	}
}

func TestConvertRBACClaimMappingPlan_DeleteSplitsKey(t *testing.T) {
	k, _ := generic.Lookup("rbacClaimMapping")
	gp := &generic.KindPlan{Deletes: []generic.Op{{Name: "azure|admins", ID: "map-1"}}}
	plan := convertRBACClaimMappingPlan(gp, nil, nil, k)
	if len(plan.Deletes) != 1 || plan.Deletes[0].OIDCProvider != "azure" || plan.Deletes[0].ClaimValue != "admins" {
		t.Errorf("unexpected delete: %+v", plan.Deletes)
	}
}

func TestPlanAllRBAC_FeatureDisabledSurfacesSingleError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	})
	c, _ := newRBACTestClient(t, mux)
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"rbacRoles": {{Name: "x"}}}}
	out := &PlanResult{}
	err := planAllRBAC(t.Context(), c, t.TempDir(), m, out)
	if err == nil {
		t.Fatalf("expected feature-disabled error")
	}
	var disabled *rbacFeatureDisabledError
	if !errors.As(err, &disabled) {
		t.Errorf("expected rbacFeatureDisabledError, got %T: %v", err, err)
	}
}

func TestPlanAllRBAC_NoOpWhenNoKindsDeclared(t *testing.T) {
	c, _ := newRBACTestClient(t, http.NewServeMux())
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{}}
	out := &PlanResult{}
	if err := planAllRBAC(t.Context(), c, t.TempDir(), m, out); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if out.RBACRoles != nil || out.RBACGrants != nil || out.RBACClaimMappings != nil {
		t.Errorf("expected no RBAC plans, got %+v", out)
	}
}

func TestPlanAllRBAC_EndToEnd(t *testing.T) {
	tmp := t.TempDir()
	writeYAML := func(rel, content string) {
		full := tmp + "/" + rel
		dir := full
		for i := len(full) - 1; i >= 0; i-- {
			if full[i] == '/' {
				dir = full[:i]
				break
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeYAML("rbac/roles/sam_admin.yaml", "kind: rbacRole\nname: sam_admin\nspec:\n  scopes:\n    - \"*\"\n  users:\n    - alice-sub\n")
	writeYAML("rbac/claim-mappings/azure-admins.yaml", "kind: rbacClaimMapping\nname: azure-admins\nspec:\n  oidcProvider: azure\n  claimValue: sam-admins\n  roleNames:\n    - sam_admin\n")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.RoleResponse{})
	})
	mux.HandleFunc("GET /api/v2/platform/rbac/grants", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.GrantResponse{})
	})
	mux.HandleFunc("GET /api/v2/platform/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ClaimMappingResponse{})
	})
	c, _ := newRBACTestClient(t, mux)

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacRoles":         {{Name: "sam_admin", From: "sam_admin"}},
		"rbacClaimMappings": {{Name: "azure-admins", From: "azure-admins"}},
	}}
	out := &PlanResult{}
	if err := planAllRBAC(t.Context(), c, tmp, m, out); err != nil {
		t.Fatalf("planAllRBAC: %v", err)
	}
	if out.RBACRoles == nil || len(out.RBACRoles.Creates) != 1 {
		t.Errorf("expected one role Create, got %+v", out.RBACRoles)
	}
	if out.RBACGrants == nil || len(out.RBACGrants.Creates) != 1 {
		t.Errorf("expected one assignment Create (from spec.users), got %+v", out.RBACGrants)
	}
	if len(out.RBACGrants.Creates) == 1 {
		if got := out.RBACGrants.Creates[0].Identity; got != "alice-sub" {
			t.Errorf("assignment identity = %q, want alice-sub", got)
		}
		if got := out.RBACGrants.Creates[0].RoleName; got != "sam_admin" {
			t.Errorf("assignment roleName = %q, want sam_admin", got)
		}
	}
	if out.RBACClaimMappings == nil || len(out.RBACClaimMappings.Creates) != 1 {
		t.Errorf("expected one claim-mapping Create, got %+v", out.RBACClaimMappings)
	}
}

func TestPlanAllRBAC_ClaimMappingsOnly(t *testing.T) {
	t.Run("planning resolves roleName against existing db roles", func(t *testing.T) {
		tmp := t.TempDir()
		dir := tmp + "/rbac/claim-mappings"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mapping := "kind: rbacClaimMapping\nname: azure-admins\nspec:\n  oidcProvider: azure\n  claimValue: sam-admins\n  roleNames:\n    - sam_admin\n"
		if err := os.WriteFile(dir+"/azure-admins.yaml", []byte(mapping), 0o644); err != nil {
			t.Fatal(err)
		}

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, []wire.RoleResponse{{ID: "uuid-admin", Name: "sam_admin", Origin: entplatform.RBACOriginCustom}})
		})
		mux.HandleFunc("GET /api/v2/platform/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, []wire.ClaimMappingResponse{})
		})
		c, _ := newRBACTestClient(t, mux)

		m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
			"rbacClaimMappings": {{Name: "azure-admins", From: "azure-admins"}},
		}}
		out := &PlanResult{}
		if err := planAllRBAC(t.Context(), c, tmp, m, out); err != nil {
			t.Fatalf("planAllRBAC: %v", err)
		}
		if out.RBACRoles != nil {
			t.Errorf("expected no role plan, got %+v", out.RBACRoles)
		}
		if out.RBACGrants != nil {
			t.Errorf("expected no assignment plan (no rbacRoles declared), got %+v", out.RBACGrants)
		}
		if out.RBACClaimMappings == nil || len(out.RBACClaimMappings.Creates) != 1 {
			t.Fatalf("expected one claim-mapping Create, got %+v", out.RBACClaimMappings)
		}
		if got := out.RBACClaimMappings.Creates[0].RoleIDs; !slices.Equal(got, []string{"uuid-admin"}) {
			t.Errorf("roleName should resolve to existing db role UUID, RoleIDs = %v, want [uuid-admin]", got)
		}
	})

	t.Run("feature disabled reports rbacClaimMappings alone", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotImplemented)
		})
		c, _ := newRBACTestClient(t, mux)
		m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
			"rbacClaimMappings": {{Name: "azure-admins", From: "azure-admins"}},
		}}
		err := planAllRBAC(t.Context(), c, t.TempDir(), m, &PlanResult{})
		var disabled *rbacFeatureDisabledError
		if !errors.As(err, &disabled) {
			t.Fatalf("expected rbacFeatureDisabledError, got %T: %v", err, err)
		}
		if len(disabled.declaredKinds) != 1 || disabled.declaredKinds[0] != "rbacClaimMappings" {
			t.Errorf("declaredKinds = %v, want [rbacClaimMappings]", disabled.declaredKinds)
		}
	})
}

func TestPlanAllRBAC_RejectsAssignmentsKind(t *testing.T) {
	c, _ := newRBACTestClient(t, http.NewServeMux())
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacAssignments": {{Name: "alice", From: "alice"}},
	}}
	err := planAllRBAC(t.Context(), c, t.TempDir(), m, &PlanResult{})
	if err == nil {
		t.Fatal("expected error for resources.rbacAssignments")
	}
	if !strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), "spec.users") {
		t.Errorf("error should point at the migration path, got: %v", err)
	}
}

func TestPlanAllRBAC_DetectsMiniIDP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.RoleResponse{})
	})
	mux.HandleFunc("GET /api/v2/platform/rbac/grants", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.GrantResponse{})
	})
	mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]bool{"manageRbac": false})
	})
	c, _ := newRBACTestClient(t, mux)
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"rbacRoles": {}}}
	out := &PlanResult{}
	if err := planAllRBAC(t.Context(), c, t.TempDir(), m, out); err != nil {
		t.Fatalf("planAllRBAC: %v", err)
	}
	if !out.RBACWritesBlocked {
		t.Errorf("expected RBACWritesBlocked=true when manageRbac is false")
	}
}

func TestAnyFailures_DetectsRBACFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *ApplyResult
		want bool
	}{
		{name: "roles failure", r: &ApplyResult{RBACRoles: &rbacRoleOutcome{Failures: []rbacRoleOpResult{{}}}}, want: true},
		{name: "assignments failure", r: &ApplyResult{RBACGrants: &rbacGrantOutcome{Failures: []rbacGrantOpResult{{}}}}, want: true},
		{name: "claim-mappings failure", r: &ApplyResult{RBACClaimMappings: &rbacClaimMappingOutcome{Failures: []rbacClaimMappingOpResult{{}}}}, want: true},
		{name: "no failures", r: &ApplyResult{RBACRoles: &rbacRoleOutcome{}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := anyFailures(tc.r); got != tc.want {
				t.Errorf("anyFailures = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveRBACRoles_LocalDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(tmp+"/rbac/roles", 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "kind: rbacRole\nname: r1\nspec:\n  scopes: []\n"
	if err := os.WriteFile(tmp+"/rbac/roles/r1.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacRoles": {{Name: "r1", From: "r1"}},
	}}
	got, err := resolve.ResolveRBACRoles(tmp, m)
	if err != nil {
		t.Fatalf("ResolveRBACRoles: %v", err)
	}
	if len(got) != 1 || got[0].Name != "r1" {
		t.Errorf("got %+v", got)
	}
}

func TestResolveRBACClaimMappings_LocalDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(tmp+"/rbac/claim-mappings", 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "kind: rbacClaimMapping\nname: azure-admins\nspec:\n  oidcProvider: azure\n  claimValue: sam-admins\n  roleNames:\n    - sam_admin\n"
	if err := os.WriteFile(tmp+"/rbac/claim-mappings/azure-admins.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacClaimMappings": {{Name: "azure-admins", From: "azure-admins"}},
	}}
	got, err := resolve.ResolveRBACClaimMappings(tmp, m)
	if err != nil || len(got) != 1 || got[0].Name != "azure-admins" {
		t.Errorf("got %+v err=%v", got, err)
	}
}

func TestRenderPlan_IncludesRBACSections(t *testing.T) {
	var buf bytes.Buffer
	plan := &PlanResult{
		RBACRoles:         &rbacRolePlan{Creates: []rbacRoleOp{{Name: "r1"}}},
		RBACGrants:        &rbacGrantPlan{Creates: []rbacGrantOp{{Header: "alice", Identity: "alice"}}},
		RBACClaimMappings: &rbacClaimMappingPlan{Creates: []rbacClaimMappingOp{{Header: "azure-admins"}}},
	}
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"rbac/roles", "rbac/grants", "rbac/claim-mappings"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %q", want, out)
		}
	}
}

func TestRenderApply_IncludesRBACOutcomes(t *testing.T) {
	var buf bytes.Buffer
	res := &ApplyResult{
		Plan: &PlanResult{},
		RBACRoles: &rbacRoleOutcome{
			Created: []rbacRoleOpResult{{Op: rbacRoleOp{Name: "sam_admin"}}},
		},
		RBACGrants: &rbacGrantOutcome{
			Created: []rbacGrantOpResult{{Op: rbacGrantOp{Header: "alice-admin", Identity: "alice"}}},
		},
		RBACClaimMappings: &rbacClaimMappingOutcome{
			Created: []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{Header: "azure-admins", OIDCProvider: "azure", ClaimValue: "sam-admins"}}},
		},
	}
	RenderApply(&buf, res, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"rbac/roles", "rbac/grants", "rbac/claim-mappings", "sam_admin", "alice-admin", "azure-admins"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %q", want, out)
		}
	}
}

func TestRenderRBACAssignmentApply_AllVariants(t *testing.T) {
	var buf bytes.Buffer
	o := &rbacGrantOutcome{
		Created:  []rbacGrantOpResult{{Op: rbacGrantOp{Header: "h1", Identity: "alice"}}},
		Deleted:  []rbacGrantOpResult{{Op: rbacGrantOp{Identity: "bob"}}},
		Skipped:  []rbacGrantOpResult{{Op: rbacGrantOp{Identity: "carol"}}},
		Failures: []rbacGrantOpResult{{Op: rbacGrantOp{Header: "h2", Identity: "dan"}, Verb: "create", Err: errors.New("x")}},
	}
	renderRBACGrantApply(&buf, o, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"h1", "bob", "carol", "h2", "create failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %q", want, out)
		}
	}
}

func TestRenderRBACClaimMappingApply_AllVariants(t *testing.T) {
	var buf bytes.Buffer
	o := &rbacClaimMappingOutcome{
		Created:  []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{Header: "h1"}}},
		Updated:  []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{Header: "h3"}}},
		Deleted:  []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{OIDCProvider: "okta", ClaimValue: "viewers"}}},
		Skipped:  []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{OIDCProvider: "azure", ClaimValue: "admins"}}},
		Failures: []rbacClaimMappingOpResult{{Op: rbacClaimMappingOp{OIDCProvider: "google", ClaimValue: "users"}, Verb: "create", Err: errors.New("x")}},
	}
	renderRBACClaimMappingApply(&buf, o, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"h1", "h3", "okta:viewers", "azure:admins", "google:users", "create failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %q", want, out)
		}
	}
}

func TestRenderRBACRoleApply_AllVariants(t *testing.T) {
	var buf bytes.Buffer
	o := &rbacRoleOutcome{
		Updated: []rbacRoleOpResult{{Op: rbacRoleOp{Name: "updated"}}},
		Deleted: []rbacRoleOpResult{{Op: rbacRoleOp{Name: "deleted"}}},
		Skipped: []rbacRoleOpResult{{Op: rbacRoleOp{Name: "skipped"}}},
	}
	renderRBACRoleApply(&buf, o, FormatOptions{NoColor: true})
	out := buf.String()
	for _, want := range []string{"updated", "deleted", "skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %q", want, out)
		}
	}
}

func TestRenderRBACRoleApply_Failure(t *testing.T) {
	var buf bytes.Buffer
	o := &rbacRoleOutcome{Failures: []rbacRoleOpResult{{Op: rbacRoleOp{Name: "x"}, Verb: "create", Err: errors.New("boom")}}}
	renderRBACRoleApply(&buf, o, FormatOptions{NoColor: true})
	if !strings.Contains(buf.String(), "create failed") {
		t.Errorf("expected failure label: %q", buf.String())
	}
}

func TestListRBACRoles_HitsCorrectPath(t *testing.T) {
	var seenPath string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		writeV2Page(w, []wire.RoleResponse{{ID: "u1", Name: "r1", Origin: entplatform.RBACOriginCustom}})
	})
	c, _ := newRBACTestClient(t, mux)
	roles, err := c.ListRBACRoles(t.Context())
	if err != nil {
		t.Fatalf("ListRBACRoles: %v", err)
	}
	if len(roles) != 1 || roles[0].Name != "r1" {
		t.Errorf("unexpected roles: %+v", roles)
	}
	if seenPath != "/api/v2/platform/rbac/roles" {
		t.Errorf("unexpected path: %q", seenPath)
	}
}
