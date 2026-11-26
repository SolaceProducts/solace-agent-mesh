// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestReadSystemUserSpec(t *testing.T) {
	name, display, roles, err := readSystemUserSpec(resolve.ResolvedResource{
		Name:       "helpdesk-file",
		SourceFile: "rbac/system-users/helpdesk.yaml",
		Spec: map[string]any{
			"name":        "helpdesk",
			"displayName": "Helpdesk",
			"roleNames":   []any{"orchestrator_only", ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "helpdesk" || display != "Helpdesk" {
		t.Errorf("name/display = %q/%q, want helpdesk/Helpdesk", name, display)
	}
	if len(roles) != 1 || roles[0] != "orchestrator_only" {
		t.Errorf("roles = %v, want [orchestrator_only] (blank dropped)", roles)
	}

	name, _, _, err = readSystemUserSpec(resolve.ResolvedResource{
		Name: "fallback", Spec: map[string]any{"roleNames": []any{"r"}},
	})
	if err != nil || name != "fallback" {
		t.Errorf("default name = %q (err=%v), want fallback", name, err)
	}

	if _, _, _, err := readSystemUserSpec(resolve.ResolvedResource{Name: "x", Spec: map[string]any{}}); err == nil {
		t.Error("expected error when spec.roleNames is missing")
	}
}

func TestReadSystemUserSpec_RejectsBuiltInSystemUsers(t *testing.T) {
	for _, name := range []string{"default", "system:default", "channel", "system:channel"} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := readSystemUserSpec(resolve.ResolvedResource{
				Name:       name,
				SourceFile: "rbac/system-users/" + name + ".yaml",
				Spec:       map[string]any{"name": name, "roleNames": []any{"orchestrator_only"}},
			})
			if err == nil {
				t.Fatalf("readSystemUserSpec(%q) succeeded, want a built-in rejection", name)
			}
			if !strings.Contains(err.Error(), "built-in service account") {
				t.Errorf("error = %v, want it to name the built-in conflict", err)
			}
		})
	}

	if _, _, _, err := readSystemUserSpec(resolve.ResolvedResource{
		Name: "default-reporting", Spec: map[string]any{"roleNames": []any{"r"}},
	}); err != nil {
		t.Errorf("default-reporting rejected: %v", err)
	}
}

func TestPlanSystemUsers(t *testing.T) {
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{"orchestrator_only": "11111111-1111-1111-1111-111111111111"},
		idToName: map[string]string{},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
	}
	desired := []resolve.ResolvedResource{
		{Name: "helpdesk", SourceFile: "systemUsers/helpdesk.yaml", Spec: map[string]any{"name": "helpdesk", "roleNames": []any{"orchestrator_only"}}},
	}

	plan, err := planSystemUsers(desired, nil, xref)
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(plan.Creates))
	}
	if ops := (&PlanResult{SystemUsers: plan}).Ops(); ops[0].Source != "systemUsers/helpdesk.yaml" {
		t.Errorf("source = %q, want the declaring file", ops[0].Source)
	}
	op := plan.Creates[0]
	if op.Create.Name != "helpdesk" {
		t.Errorf("name = %q, want helpdesk", op.Create.Name)
	}
	if len(op.Create.RoleIDs) != 1 || op.Create.RoleIDs[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("roleIds = %v, want the resolved UUID", op.Create.RoleIDs)
	}

	dup := []resolve.ResolvedResource{
		desired[0],
		{Name: "helpdesk-2", Spec: map[string]any{"name": "helpdesk", "roleNames": []any{"orchestrator_only"}}},
	}
	if _, err := planSystemUsers(dup, nil, xref); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestListSystemUsers_WalksEveryPage(t *testing.T) {
	mux := http.NewServeMux()
	var requested []string
	mux.HandleFunc("GET /api/v2/platform/rbac/systemUsers", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("pageNumber")
		requested = append(requested, page)
		resp := entplatform.PaginatedResponse[wire.SystemUserResponse]{}
		next := 2
		switch page {
		case "1":
			resp.Data = []wire.SystemUserResponse{{Name: "one"}}
			resp.Meta.Pagination.NextPage = &next
		default:
			resp.Data = []wire.SystemUserResponse{{Name: "two"}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	c, _ := newRBACTestClient(t, mux)

	users, err := c.ListSystemUsers(t.Context())
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	if len(users) != 2 || users[0].Name != "one" || users[1].Name != "two" {
		t.Errorf("got %+v; want both pages in order", users)
	}
	if len(requested) != 2 {
		t.Errorf("requested pages %v; want exactly two", requested)
	}
}

func TestListSystemUsers_StopsOnFirstEmptyPageEvenWhenNextPageAdvertised(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("GET /api/v2/platform/rbac/systemUsers", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		next := calls + 1
		resp := entplatform.PaginatedResponse[wire.SystemUserResponse]{}
		resp.Meta.Pagination.NextPage = &next
		if calls == 1 {
			resp.Data = []wire.SystemUserResponse{{Name: "one"}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	c, _ := newRBACTestClient(t, mux)

	users, err := c.ListSystemUsers(t.Context())
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("got %d users; want the one real page", len(users))
	}
	if calls != 2 {
		t.Errorf("made %d requests; want 2 (stop on the first empty page)", calls)
	}
}

const (
	helpdeskRoleID      = "11111111-1111-1111-1111-111111111111"
	helpdeskID          = "33333333-3333-3333-3333-333333333333"
	helpdeskExtraRoleID = "22222222-2222-2222-2222-222222222222"
)

func helpdeskXRef() rbacRoleXRefAdapter {
	return rbacRoleXRefAdapter{
		nameToID: map[string]string{"orchestrator_only": helpdeskRoleID, "extra": helpdeskExtraRoleID},
		idToName: map[string]string{helpdeskRoleID: "orchestrator_only", helpdeskExtraRoleID: "extra"},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
	}
}

func helpdeskActual(displayName string, roleIDs ...string) wire.SystemUserResponse {
	return wire.SystemUserResponse{
		ID: helpdeskID, Name: "helpdesk", Sub: "system:helpdesk", DisplayName: new(displayName),
		RoleIDs: roleIDs, Origin: entplatform.RBACOriginCustom,
	}
}

func helpdeskDesired(displayName string, roleNames ...string) []resolve.ResolvedResource {
	names := make([]any, 0, len(roleNames))
	for _, n := range roleNames {
		names = append(names, n)
	}
	spec := map[string]any{"name": "helpdesk", "roleNames": names}
	if displayName != "" {
		spec["displayName"] = displayName
	}
	return []resolve.ResolvedResource{{Name: "helpdesk", Spec: spec}}
}

func TestPlanSystemUsers_SatisfiedPrincipalPlansUnchanged(t *testing.T) {
	actual := []wire.SystemUserResponse{
		helpdeskActual("Helpdesk", helpdeskRoleID),
	}
	plan, err := planSystemUsers(helpdeskDesired("Helpdesk", "orchestrator_only"), actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Creates) != 0 {
		t.Errorf("creates = %d, want 0 — the platform already satisfies this file", len(plan.Creates))
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("unchanged = %d, want 1", len(plan.Unchanged))
	}
}

func TestPlanSystemUsers_PendingChangesStillPlanAWrite(t *testing.T) {
	base := helpdeskActual("Helpdesk", helpdeskRoleID)
	cases := []struct {
		name        string
		actual      wire.SystemUserResponse
		desired     []resolve.ResolvedResource
		wantCreates int
		wantUpdates int
	}{
		{
			name:        "displayName edited",
			actual:      base,
			desired:     helpdeskDesired("Helpdesk v2", "orchestrator_only"),
			wantUpdates: 1,
		},
		{
			name:        "displayName dropped",
			actual:      base,
			desired:     helpdeskDesired("", "orchestrator_only"),
			wantUpdates: 1,
		},
		{
			name:        "role added",
			actual:      base,
			desired:     helpdeskDesired("Helpdesk", "orchestrator_only", "extra"),
			wantUpdates: 1,
		},
		{
			name:        "principal does not exist yet",
			actual:      wire.SystemUserResponse{ID: "other", Name: "someone-else", Origin: entplatform.RBACOriginCustom},
			desired:     helpdeskDesired("Helpdesk", "orchestrator_only"),
			wantCreates: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planSystemUsers(tc.desired, []wire.SystemUserResponse{tc.actual}, helpdeskXRef())
			if err != nil {
				t.Fatalf("planSystemUsers: %v", err)
			}
			if len(plan.Creates) != tc.wantCreates || len(plan.Updates) != tc.wantUpdates {
				t.Errorf("creates=%d updates=%d, want creates=%d updates=%d",
					len(plan.Creates), len(plan.Updates), tc.wantCreates, tc.wantUpdates)
			}
		})
	}
}

func TestPlanSystemUsers_NarrowedRolesPlanARevokingUpdate(t *testing.T) {
	const droppedRole = helpdeskExtraRoleID
	actual := []wire.SystemUserResponse{helpdeskActual("Helpdesk", helpdeskRoleID, droppedRole)}
	plan, err := planSystemUsers(helpdeskDesired("Helpdesk", "orchestrator_only"), actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Updates) != 1 || len(plan.Unchanged) != 0 {
		t.Fatalf("a narrowed role list must plan an update: updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
	if got := plan.Updates[0].RevokedRoles; !slices.Equal(got, []string{"extra"}) {
		t.Errorf("RevokedRoles = %v, want [extra] — an operator must see what the apply takes away", got)
	}
	if got := plan.Updates[0].ID; got != helpdeskID {
		t.Errorf("update targets ID %q, want %q", got, helpdeskID)
	}
}

func TestPlanSystemUsers_DisjointRolesPlanAnUpdate(t *testing.T) {
	actual := []wire.SystemUserResponse{
		helpdeskActual("Helpdesk", helpdeskRoleID),
	}
	plan, err := planSystemUsers(helpdeskDesired("Helpdesk", "extra"), actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Updates) != 1 || len(plan.Unchanged) != 0 {
		t.Fatalf("a disjoint role set must plan an update: updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
	if got := plan.Updates[0].RevokedRoles; !slices.Equal(got, []string{"orchestrator_only"}) {
		t.Errorf("RevokedRoles = %v, want [orchestrator_only]", got)
	}
}

func TestPlanSystemUsers_UncreatedRoleStillPlansAWrite(t *testing.T) {
	xref := helpdeskXRef()
	xref.desired = map[string]struct{}{"brand_new": {}}
	actual := []wire.SystemUserResponse{
		helpdeskActual("Helpdesk", helpdeskRoleID),
	}
	plan, err := planSystemUsers(helpdeskDesired("Helpdesk", "brand_new"), actual, xref)
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("updates = %d, want 1 — the role does not exist on the platform yet", len(plan.Updates))
	}
}

func TestApplySystemUsers(t *testing.T) {
	plan := &systemUserPlan{
		Kind:    "systemUser",
		Creates: []systemUserOp{{Header: "helpdesk", Name: "helpdesk", Create: wire.CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{"orchestrator_only"}}}},
	}
	roleNameToID := map[string]string{"orchestrator_only": "11111111-1111-1111-1111-111111111111"}

	skip := applySystemUsers(context.Background(), nil, plan, ApplyOptions{SkipRBACWrites: true}, nil)
	if len(skip.Skipped) != 1 || len(skip.Created) != 0 {
		t.Fatalf("skip outcome = %+v, want 1 skipped", skip)
	}

	if out := applySystemUsers(context.Background(), nil, nil, ApplyOptions{}, nil); len(out.Created)+len(out.Skipped)+len(out.Failures) != 0 {
		t.Errorf("nil plan outcome = %+v, want empty", out)
	}

	var gotReq wire.CreateSystemUserRequest
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		writeV2Single(w, http.StatusCreated, wire.SystemUserResponse{ID: "u1", Sub: "system:helpdesk", Name: "helpdesk"})
	}))
	defer srv.Close()
	ok := applySystemUsers(context.Background(), newClientAgainstServer(srv), plan, ApplyOptions{}, roleNameToID)
	if len(ok.Created) != 1 || len(ok.Failures) != 0 {
		t.Fatalf("apply outcome = %+v, want 1 created", ok)
	}
	if len(gotReq.RoleIDs) != 1 || gotReq.RoleIDs[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("server saw roleIds %v, want the resolved UUID", gotReq.RoleIDs)
	}

	errSrv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusUnprocessableEntity, "bad", nil)
	}))
	defer errSrv.Close()
	fail := applySystemUsers(context.Background(), newClientAgainstServer(errSrv), plan, ApplyOptions{}, roleNameToID)
	if len(fail.Failures) != 1 || len(fail.Created) != 0 {
		t.Fatalf("failure outcome = %+v, want 1 failure", fail)
	}
}

func TestPlanSystemUsers_PulledSpecShapePlansUnchanged(t *testing.T) {
	pulled := []resolve.ResolvedResource{{
		Name: "helpdesk",
		Spec: map[string]any{
			"name":        "helpdesk",
			"displayName": "Helpdesk",
			"roleNames":   []any{"orchestrator_only"},
		},
	}}
	plan, err := planSystemUsers(pulled, []wire.SystemUserResponse{helpdeskActual("Helpdesk", helpdeskRoleID)}, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("a pulled repo does not plan clean: creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("unchanged = %d, want 1", len(plan.Unchanged))
	}
}

func TestPlanSystemUsers_UndeclaredCustomPrincipalPlansDelete(t *testing.T) {
	actual := []wire.SystemUserResponse{
		helpdeskActual("Helpdesk", helpdeskRoleID),
		{ID: rbac.DefaultSystemUserID, Name: "default", Sub: rbac.DefaultSystemUserSub, Origin: entplatform.RBACOriginBuiltin},
	}
	plan, err := planSystemUsers(nil, actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Deletes) != 1 {
		t.Fatalf("deletes = %d, want 1 (only the custom principal)", len(plan.Deletes))
	}
	if got := plan.Deletes[0].Name; got != "helpdesk" {
		t.Errorf("delete targets %q, wanted the custom principal", got)
	}
}

func TestPlanSystemUsers_UnknownOriginIsNeverPruned(t *testing.T) {
	actual := []wire.SystemUserResponse{
		{ID: helpdeskID, Name: "helpdesk", Sub: "system:helpdesk", Origin: ""},
		{ID: "x", Name: "future", Sub: "system:future", Origin: "something-new"},
	}
	plan, err := planSystemUsers(nil, actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("deletes = %+v, want none — an unrecognized origin must stay out of the prune set", plan.Deletes)
	}
}

func TestApplySystemUsers_DeleteIsPruneGated(t *testing.T) {
	plan := &systemUserPlan{
		Kind:    "systemUser",
		Deletes: []systemUserOp{{Header: "helpdesk", Name: "helpdesk", ID: helpdeskID}},
	}

	var deleted []string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			deleted = append(deleted, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	withoutPrune := applySystemUsers(context.Background(), newClientAgainstServer(srv), plan, ApplyOptions{}, nil)
	if len(withoutPrune.Skipped) != 1 || len(withoutPrune.Deleted) != 0 {
		t.Fatalf("without --prune: outcome = %+v, want 1 skipped and 0 deleted", withoutPrune)
	}
	if len(deleted) != 0 {
		t.Fatalf("without --prune the client still called %v", deleted)
	}

	withPrune := applySystemUsers(context.Background(), newClientAgainstServer(srv), plan, ApplyOptions{Prune: true}, nil)
	if len(withPrune.Deleted) != 1 || len(withPrune.Failures) != 0 {
		t.Fatalf("with --prune: outcome = %+v, want 1 deleted", withPrune)
	}
	if want := platform.APIV2Prefix + serviceAccountsResource.path + "/" + helpdeskID; len(deleted) != 1 || deleted[0] != want {
		t.Errorf("deleted %v, want [%s]", deleted, want)
	}
}

func TestApplySystemUsers_UpdateTargetsTheRowAndReplacesRoles(t *testing.T) {
	plan := &systemUserPlan{
		Kind: "systemUser",
		Updates: []systemUserOp{{
			Header: "helpdesk", Name: "helpdesk", ID: helpdeskID,
			Create:       wire.CreateSystemUserRequest{Name: "helpdesk", DisplayName: "Helpdesk", RoleIDs: []string{"orchestrator_only"}},
			RevokedRoles: []string{"extra"},
		}},
	}

	var gotMethod, gotPath string
	var gotReq wire.ReplaceSystemUserRequest
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		writeV2Single(w, http.StatusOK, wire.SystemUserResponse{ID: helpdeskID, Sub: "system:helpdesk", Name: "helpdesk"})
	}))
	defer srv.Close()

	out := applySystemUsers(context.Background(), newClientAgainstServer(srv), plan, ApplyOptions{},
		map[string]string{"orchestrator_only": helpdeskRoleID})
	if len(out.Updated) != 1 || len(out.Failures) != 0 {
		t.Fatalf("outcome = %+v, want 1 updated", out)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if want := platform.APIV2Prefix + serviceAccountsResource.path + "/" + helpdeskID; gotPath != want {
		t.Errorf("path = %s, want %s", gotPath, want)
	}
	if !slices.Equal(gotReq.RoleIDs, []string{helpdeskRoleID}) {
		t.Errorf("roleIds = %v, want the resolved UUID %s", gotReq.RoleIDs, helpdeskRoleID)
	}
	if gotReq.DisplayName != "Helpdesk" {
		t.Errorf("displayName = %q, want Helpdesk", gotReq.DisplayName)
	}
}

func TestRenderSystemUser_NamesRevokedRoles(t *testing.T) {
	const revoked = "22222222-2222-2222-2222-222222222222"
	plan := &systemUserPlan{
		Kind:    "systemUser",
		Updates: []systemUserOp{{Header: "helpdesk", Name: "helpdesk", ID: helpdeskID, RevokedRoles: []string{revoked}}},
	}
	var buf bytes.Buffer
	renderSystemUserPlan(&buf, plan, FormatOptions{})
	if !strings.Contains(buf.String(), "revokes "+revoked) {
		t.Errorf("plan output does not name the revoked role:\n%s", buf.String())
	}

	buf.Reset()
	renderSystemUserApply(&buf, &systemUserOutcome{
		Kind: "systemUser", Updated: []systemUserOpResult{{Op: plan.Updates[0]}},
	}, FormatOptions{})
	if !strings.Contains(buf.String(), "revokes "+revoked) {
		t.Errorf("apply output does not name the revoked role:\n%s", buf.String())
	}
}

func TestRenderSystemUser(t *testing.T) {
	plan := &systemUserPlan{Kind: "systemUser", Creates: []systemUserOp{{Header: "helpdesk", Name: "helpdesk"}}}
	var planBuf bytes.Buffer
	renderSystemUserPlan(&planBuf, plan, FormatOptions{})
	if !strings.Contains(planBuf.String(), "helpdesk") {
		t.Errorf("plan render = %q, want to mention helpdesk", planBuf.String())
	}

	outcome := &systemUserOutcome{
		Kind:     "systemUser",
		Created:  []systemUserOpResult{{Op: systemUserOp{Name: "helpdesk"}}},
		Skipped:  []systemUserOpResult{{Op: systemUserOp{Name: "skipped-one"}, Reason: skipReasonMiniIDP}},
		Failures: []systemUserOpResult{{Op: systemUserOp{Name: "boom"}, Err: errors.New("nope")}},
	}
	var applyBuf bytes.Buffer
	renderSystemUserApply(&applyBuf, outcome, FormatOptions{})
	got := applyBuf.String()
	for _, want := range []string{"helpdesk", "skipped-one", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("apply render missing %q in %q", want, got)
		}
	}

	var empty bytes.Buffer
	renderSystemUserApply(&empty, &systemUserOutcome{}, FormatOptions{})
	if empty.Len() != 0 {
		t.Errorf("empty outcome rendered %q, want nothing", empty.String())
	}
}

func TestRenderSystemUser_ShowsDeletesDistinctlyFromUpdates(t *testing.T) {
	plan := &systemUserPlan{
		Kind:      "systemUser",
		Updates:   []systemUserOp{{Header: "kept", Name: "kept", ID: helpdeskID}},
		Deletes:   []systemUserOp{{Header: "retired-bot", Name: "retired-bot", ID: helpdeskID}},
		Unchanged: []systemUserOp{{Header: "steady", Name: "steady"}},
	}
	var planBuf bytes.Buffer
	renderSystemUserPlan(&planBuf, plan, FormatOptions{})
	planOut := planBuf.String()
	for _, want := range []string{"retired-bot", "delete", "kept", "steady"} {
		if !strings.Contains(planOut, want) {
			t.Errorf("plan render missing %q:\n%s", want, planOut)
		}
	}
	if strings.Contains(planOut, "revokes") {
		t.Errorf("plan labelled a non-revoking update as revoking:\n%s", planOut)
	}

	var applyBuf bytes.Buffer
	renderSystemUserApply(&applyBuf, &systemUserOutcome{
		Kind:    "systemUser",
		Deleted: []systemUserOpResult{{Op: plan.Deletes[0]}},
	}, FormatOptions{})
	applyOut := applyBuf.String()
	for _, want := range []string{"retired-bot", "deleted"} {
		if !strings.Contains(applyOut, want) {
			t.Errorf("apply render missing %q:\n%s", want, applyOut)
		}
	}
}

func TestPlanSystemUsers_ShadowRowIsNotAPruneCandidate(t *testing.T) {
	shadow := wire.SystemUserResponse{
		ID: "019f0000-0000-7000-8000-00000000dead", Name: "default", Sub: rbac.DefaultSystemUserSub,
		DisplayName: new("Shadow"), RoleIDs: []string{helpdeskRoleID}, Origin: entplatform.RBACOriginCustom,
	}
	actual := []wire.SystemUserResponse{shadow, helpdeskActual("Helpdesk", helpdeskRoleID)}

	plan, err := planSystemUsers(helpdeskDesired("Helpdesk", "orchestrator_only"), actual, helpdeskXRef())
	if err != nil {
		t.Fatalf("planSystemUsers: %v", err)
	}
	for _, op := range plan.Deletes {
		if op.Name == "default" {
			t.Errorf("plan proposes deleting the shadow row; --prune cannot remove it and the repo cannot declare it")
		}
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("deletes = %+v, want none", plan.Deletes)
	}
}

func TestApplySystemUsers_MiniIDPReportsSkippedDeletes(t *testing.T) {
	plan := &systemUserPlan{
		Kind:    "systemUser",
		Creates: []systemUserOp{{Header: "a", Name: "a"}},
		Updates: []systemUserOp{{Header: "b", Name: "b", ID: helpdeskID}},
		Deletes: []systemUserOp{{Header: "c", Name: "c", ID: helpdeskID}},
	}
	out := applySystemUsers(context.Background(), nil, plan, ApplyOptions{SkipRBACWrites: true}, nil)

	if len(out.Skipped) != 3 {
		t.Fatalf("skipped = %d, want 3 (create, update, delete)", len(out.Skipped))
	}
	var names []string
	for _, r := range out.Skipped {
		names = append(names, r.Op.Name)
		if r.Reason != skipReasonMiniIDP {
			t.Errorf("%s reason = %q, want %q", r.Op.Name, r.Reason, skipReasonMiniIDP)
		}
	}
	if !slices.Contains(names, "c") {
		t.Errorf("skipped = %v, missing the delete", names)
	}
}

func TestListSystemUsers_FallsBackToThePreRenamePathWhenThePlatformLacksTheNewOne(t *testing.T) {
	var newPathCalls int
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, platform.APIV2Prefix+serviceAccountsResource.path) {
			newPathCalls++
			writeV2Error(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeV2Page(w, []wire.SystemUserResponse{{ID: "u1", Name: "bot", Sub: "system:bot"}})
	}))
	defer srv.Close()
	c := newClientAgainstServer(srv)

	for range 2 {
		users, err := c.ListSystemUsers(t.Context())
		if err != nil || len(users) != 1 || users[0].Name != "bot" {
			t.Fatalf("ListSystemUsers = %+v, %v; want the one user from /rbac/systemUsers", users, err)
		}
	}
	if newPathCalls != 1 {
		t.Errorf("new path probed %d times; want once per client, then the old path", newPathCalls)
	}
}

func TestListSystemUsers_DoesNotRememberAFailedProbe(t *testing.T) {
	var probes int
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		probes++
		if probes == 1 {
			writeV2Error(w, http.StatusServiceUnavailable, "warming up", nil)
			return
		}
		writeV2Page(w, []wire.SystemUserResponse{{ID: "u1", Name: "bot", Sub: "system:bot"}})
	}))
	defer srv.Close()
	c := newClientAgainstServer(srv)

	_, err := c.ListSystemUsers(t.Context())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable || !strings.HasSuffix(httpErr.Path, "/rbac/serviceAccounts?pageSize=1") {
		t.Fatalf("first call err = %v; want the probe's own 503 on the new path", err)
	}
	users, err := c.ListSystemUsers(t.Context())
	if err != nil || len(users) != 1 {
		t.Fatalf("second call = %+v, %v; want the probe retried and the list served", users, err)
	}
}
