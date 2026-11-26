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
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	rekeyRoleName    = "hr_users"
	rekeyIssuer      = "https://idp.example.com"
	rekeyOtherIssuer = "https://other-idp.example.com"
)

type rekeyPlatform struct {
	client *Client
	users  *store.UsersRepo
	roleID string
	repo   string
}

func newRekeyPlatform(t *testing.T, wrap func(http.Handler) http.Handler) *rekeyPlatform {
	t.Helper()
	t.Parallel()
	ctx := context.Background()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": store.TestDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(logger)).Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "rbac", Default: true})
	mux := http.NewServeMux()
	deps := &entplatform.RouteDeps{IsRBACAuthority: true, FeatureRegistry: reg}
	entplatform.RegisterRoutesWithDeps(mux, core, deps)
	apiv2.RegisterRoutes(mux, deps, core, logger)
	var h http.Handler = mux
	if wrap != nil {
		h = wrap(h)
	}
	superAdmin := auth.NewTestSubject(auth.SubjectFields{UserID: "operator@example.com", Authenticated: true, Scopes: []string{"*"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, apiV1PlatformPrefix+"/") {
			t.Errorf("%s %s: config apply calls grants and budgets on /api/v2 only", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), superAdmin)))
	}))
	t.Cleanup(srv.Close)

	p := &rekeyPlatform{
		client: NewClient(target.Resolved{URL: srv.URL, Token: "tok"}),
		users:  store.NewUsersRepo(core.DB()),
		repo:   t.TempDir(),
	}
	var role wire.RoleResponse
	if err := p.client.callResource(ctx, rbacRolesResource, http.MethodPost, "", wire.CreateRoleRequest{
		Name: rekeyRoleName, Scopes: []string{"agent:*:read"},
	}, &role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	p.roleID = role.ID
	return p
}

func (p *rekeyPlatform) signIn(t *testing.T, issuer, sub, email string) string {
	t.Helper()
	u := &store.User{Issuer: issuer, Sub: sub, CreatedBy: "login", UpdatedBy: "login"}
	if email != "" {
		u.Email = &email
	}
	if err := p.users.UpsertUser(context.Background(), u, nil); err != nil {
		t.Fatalf("sign in %s: %v", sub, err)
	}
	return u.ID
}

func (p *rekeyPlatform) grant(t *testing.T, subject apiv2.CreateGrantSubject) {
	t.Helper()
	resp, err := p.client.CreateRBACGrant(context.Background(), wire.CreateGrantRequest{Subject: subject, RoleID: p.roleID})
	if err != nil {
		t.Fatalf("grant %+v: %v", subject, err)
	}
	if ptrString(resp.State) != entplatform.GrantStateConcrete {
		t.Fatalf("grant %+v is %s, want concrete", subject, ptrString(resp.State))
	}
}

func emailSubject(v string) apiv2.CreateGrantSubject {
	return apiv2.CreateGrantSubject{Type: store.PendingSubjectEmail, Value: v}
}

func subSubject(issuer, v string) apiv2.CreateGrantSubject {
	return apiv2.CreateGrantSubject{Type: store.PendingSubjectSub, Issuer: issuer, Value: v}
}

func (p *rekeyPlatform) declare(t *testing.T, users string) {
	t.Helper()
	dir := filepath.Join(p.repo, "rbac", "roles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "kind: rbacRole\nname: " + rekeyRoleName + "\nspec:\n  scopes:\n    - \"agent:*:read\"\n  users:\n" + users
	if err := os.WriteFile(filepath.Join(dir, rekeyRoleName+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (p *rekeyPlatform) plan(t *testing.T) *PlanResult {
	t.Helper()
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacRoles": {{Name: rekeyRoleName, From: rekeyRoleName}},
	}}
	out := &PlanResult{}
	if err := planAllRBAC(t.Context(), p.client, p.repo, m, out); err != nil {
		t.Fatalf("plan: %v", err)
	}
	return out
}

func (p *rekeyPlatform) apply(t *testing.T, plan *PlanResult, prune bool) *ApplyResult {
	t.Helper()
	out := &ApplyResult{Plan: plan}
	applyRBACPhase(t.Context(), p.client, plan, ApplyOptions{Prune: prune}, out)
	if anyFailures(out) {
		t.Fatalf("apply had failures: %+v", out.RBACGrants.Failures)
	}
	return out
}

func (p *rekeyPlatform) recorded(t *testing.T) map[string]apiv2.CreateGrantSubject {
	t.Helper()
	grants, err := p.client.ListRBACGrants(t.Context())
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	out := map[string]apiv2.CreateGrantSubject{}
	for _, g := range grants {
		if g.RoleID != p.roleID {
			continue
		}
		if ptrString(g.State) != entplatform.GrantStateConcrete || g.Subject == nil {
			t.Errorf("grant %s for %+v is %s; a declared user should keep their concrete grant", g.ID, g.Subject, ptrString(g.State))
			continue
		}
		out[ptrString(g.UserID)] = apiv2.CreateGrantSubject{Type: g.Subject.Type, Value: g.Subject.Value, Issuer: ptrString(g.Subject.Issuer)}
	}
	return out
}

func (p *rekeyPlatform) holders(t *testing.T) []string {
	t.Helper()
	return slices.Sorted(maps.Keys(p.recorded(t)))
}

func sortedIDs(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func renderedPlan(plan *PlanResult) string {
	var b strings.Builder
	renderRBACGrantPlan(&b, plan.RBACGrants, FormatOptions{})
	return b.String()
}

func renderedApply(res *ApplyResult) string {
	var b strings.Builder
	renderRBACGrantApply(&b, res.RBACGrants, FormatOptions{})
	return b.String()
}

func assertPlan(t *testing.T, plan *PlanResult, creates, updates, deletes, unchanged int) {
	t.Helper()
	gp := plan.RBACGrants
	if gp == nil {
		t.Fatal("no grant plan")
	}
	got := []int{len(gp.Creates), len(gp.Updates), len(gp.Deletes), len(gp.Unchanged)}
	if want := []int{creates, updates, deletes, unchanged}; !slices.Equal(got, want) {
		t.Errorf("grant plan = %d create, %d update, %d delete, %d unchanged; want %v\n%s",
			got[0], got[1], got[2], got[3], want, renderedPlan(plan))
	}
}

func assertWarning(t *testing.T, plan *PlanResult, fragment string) {
	t.Helper()
	if !slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.Contains(w, fragment) }) {
		t.Errorf("plan warnings %q do not mention %q", plan.Warnings, fragment)
	}
}

func (p *rekeyPlatform) assertConverged(t *testing.T, unchanged int) {
	t.Helper()
	assertPlan(t, p.plan(t), 0, 0, 0, unchanged)
}

func TestApplyRBACGrants_PruneRebindsAGrantDeclaredUnderAnotherSubjectType(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	bob := p.signIn(t, rekeyIssuer, "sub-bob", "bob@newco.com")
	p.grant(t, emailSubject("alice@newco.com"))
	p.grant(t, emailSubject("bob@newco.com"))
	p.declare(t, "    - alice@newco.com\n    - Bob@NewCo.com\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 2, 0, 0)
	if want := "subject: email:alice@newco.com → alice@newco.com (same user)"; !strings.Contains(renderedPlan(plan), want) {
		t.Errorf("plan does not show %q:\n%s", want, renderedPlan(plan))
	}
	p.apply(t, plan, true)

	got := p.recorded(t)
	if holders := slices.Sorted(maps.Keys(got)); !slices.Equal(holders, sortedIDs(alice, bob)) {
		t.Fatalf("role holders after apply --prune = %v, want %v", holders, sortedIDs(alice, bob))
	}
	if s := got[bob]; s.Type != store.PendingSubjectLegacy || s.Value != "bob@newco.com" {
		t.Errorf("bob's grant records %+v, want the declared bare bob@newco.com", s)
	}
	p.assertConverged(t, 2)
}

func TestApplyRBACGrants_PruneRebindsAGrantWhoseUserNowSignsInWithTheDeclaredEmail(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	p.grant(t, emailSubject("alice@oldco.com"))
	if again := p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com"); again != alice {
		t.Fatalf("second sign-in made user %s, want the same user %s", again, alice)
	}
	p.declare(t, "    - {type: email, value: alice@newco.com}\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 1, 0, 0)
	p.apply(t, plan, true)

	if got := p.recorded(t); len(got) != 1 || got[alice] != emailSubject("alice@newco.com") {
		t.Fatalf("grants after apply --prune = %+v, want alice's recording email alice@newco.com", got)
	}
	p.assertConverged(t, 1)
}

func TestApplyRBACGrants_PruneRebindsAGrantDeclaredBySub(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	p.grant(t, emailSubject("alice@newco.com"))
	p.declare(t, "    - {type: sub, issuer: \""+rekeyIssuer+"\", value: sub-alice}\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 1, 0, 0)
	p.apply(t, plan, true)

	if got := p.recorded(t); got[alice] != subSubject(rekeyIssuer, "sub-alice") {
		t.Fatalf("alice's grant records %+v, want her sub", got[alice])
	}
	p.assertConverged(t, 1)
}

func TestApplyRBACGrants_PruneStillRevokesAnUndeclaredUser(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	p.signIn(t, rekeyIssuer, "sub-bob", "bob@newco.com")
	p.grant(t, emailSubject("alice@newco.com"))
	p.grant(t, emailSubject("bob@newco.com"))
	p.declare(t, "    - alice@newco.com\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 1, 1, 0)
	p.apply(t, plan, true)

	if got, want := p.holders(t), sortedIDs(alice); !slices.Equal(got, want) {
		t.Fatalf("role holders after apply --prune = %v, want %v", got, want)
	}
}

func TestApplyRBACGrants_ABareSubNamesAUserWithNoEmail(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	carol := p.signIn(t, rekeyIssuer, "sub-carol", "")
	p.grant(t, subSubject(rekeyIssuer, "sub-carol"))
	p.declare(t, "    - sub-carol\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 1, 0, 0)
	p.apply(t, plan, true)

	if got := p.recorded(t); got[carol] != (apiv2.CreateGrantSubject{Type: store.PendingSubjectLegacy, Value: "sub-carol"}) {
		t.Fatalf("carol's grant records %+v, want the bare sub-carol", got[carol])
	}
	p.assertConverged(t, 1)
}

func TestPlanRBACGrants_ABareSubDoesNotNameAUserWithAnEmail(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	p.grant(t, subSubject(rekeyIssuer, "sub-alice"))
	p.declare(t, "    - sub-alice\n")

	assertPlan(t, p.plan(t), 1, 0, 1, 0)
}

func TestPlanRBACGrants_ASubUnderAnotherIssuerNamesNoOne(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	p.grant(t, emailSubject("alice@newco.com"))
	p.declare(t, "    - {type: sub, issuer: \""+rekeyOtherIssuer+"\", value: sub-alice}\n")

	assertPlan(t, p.plan(t), 1, 0, 1, 0)
}

func TestPlanRBACGrants_ASharedEmailNamesNoOne(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "sub-alice", "shared@newco.com")
	p.grant(t, subSubject(rekeyIssuer, "sub-alice"))
	p.signIn(t, rekeyIssuer, "sub-dave", "shared@newco.com")
	p.declare(t, "    - shared@newco.com\n")

	plan := p.plan(t)
	assertPlan(t, plan, 1, 0, 1, 0)
	assertWarning(t, plan, "shared@newco.com → "+rekeyRoleName+" is an email more than one user has")
}

func TestPlanRBACGrants_AKeyNamingTwoGrantsMatchesNeither(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	p.signIn(t, rekeyIssuer, "shared-sub", "")
	p.signIn(t, rekeyOtherIssuer, "shared-sub", "")
	p.grant(t, subSubject(rekeyIssuer, "shared-sub"))
	p.grant(t, subSubject(rekeyOtherIssuer, "shared-sub"))
	p.declare(t, "    - shared-sub\n")

	assertPlan(t, p.plan(t), 1, 0, 2, 0)
}

func TestApplyRBACGrants_AUserDeclaredTwiceKeepsOneRecordedSubject(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@oldco.com")
	p.grant(t, emailSubject("alice@oldco.com"))
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	p.declare(t, "    - {type: email, value: alice@newco.com}\n    - {type: sub, issuer: \""+rekeyIssuer+"\", value: sub-alice}\n")

	plan := p.plan(t)
	assertPlan(t, plan, 0, 1, 0, 1)
	assertWarning(t, plan, "name the same user")
	p.apply(t, plan, true)

	if got := p.recorded(t); len(got) != 1 || got[alice] != emailSubject("alice@newco.com") {
		t.Fatalf("grants after apply --prune = %+v, want alice's recording email alice@newco.com", got)
	}
	again := p.plan(t)
	assertPlan(t, again, 0, 0, 0, 2)
	assertWarning(t, again, "name the same user")
}

func withoutGrantHolders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != rbacGrantsResource.apiPath() || !r.URL.Query().Has("expand") {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		var page map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rows, _ := page["data"].([]any)
		for _, row := range rows {
			if grant, ok := row.(map[string]any); ok {
				grant["user"] = nil
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.Code)
		_ = json.NewEncoder(w).Encode(page)
	})
}

func failGrantsExpandUser(next http.Handler, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == rbacGrantsResource.apiPath() && r.URL.Query().Has("expand") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"cannot expand user"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestApplyRBACGrants_AnUnresolvedConflictKeepsTheRolesDeletes(t *testing.T) {
	p := newRekeyPlatform(t, withoutGrantHolders)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
	carol := p.signIn(t, rekeyIssuer, "sub-carol", "carol@newco.com")
	p.grant(t, emailSubject("alice@newco.com"))
	p.grant(t, emailSubject("carol@newco.com"))
	p.declare(t, "    - alice@newco.com\n")

	plan := p.plan(t)
	assertPlan(t, plan, 1, 0, 2, 0)
	out := &ApplyResult{Plan: plan}
	applyRBACPhase(t.Context(), p.client, plan, ApplyOptions{Prune: true}, out)

	if got := out.RBACGrants.Failures; len(got) != 1 || got[0].Verb != "create" {
		t.Fatalf("failures = %+v, want alice's create refused", got)
	}
	if got, want := p.holders(t), sortedIDs(alice, carol); !slices.Equal(got, want) {
		t.Fatalf("role holders after apply --prune = %v, want %v", got, want)
	}
	for _, s := range out.RBACGrants.Skipped {
		if s.Reason != skipReasonGrantCreateConflicted {
			t.Errorf("delete of %s skipped as %q, want %q", s.Op.Identity, s.Reason, skipReasonGrantCreateConflicted)
		}
	}
	if !strings.Contains(renderedApply(out), keptCreateConflictedLabel) {
		t.Errorf("apply output does not say why the deletes were kept:\n%s", renderedApply(out))
	}
}

func TestCreateRBACGrant_ResolvesAConflictOnlyToOneGrant(t *testing.T) {
	t.Run("the grant recorded under the subject", func(t *testing.T) {
		p := newRekeyPlatform(t, nil)
		p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
		p.grant(t, emailSubject("alice@newco.com"))
		grants, err := p.client.ListRBACGrants(t.Context())
		if err != nil || len(grants) != 1 {
			t.Fatalf("grants = %+v, %v", grants, err)
		}
		held, err := p.client.CreateRBACGrant(t.Context(), wire.CreateGrantRequest{Subject: emailSubject("alice@newco.com"), RoleID: p.roleID})
		if err != nil || held.ID != grants[0].ID {
			t.Fatalf("create = %s, %v; want the held grant %s", held.ID, err, grants[0].ID)
		}
	})
	t.Run("two grants under the subject", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST "+platpkg.APIV2Prefix+rbacGrantsResource.path, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"This role is already granted to that identity"}`))
		})
		mux.HandleFunc("GET "+rbacGrantsResource.apiPath(), func(w http.ResponseWriter, _ *http.Request) {
			row := `{"id":"%s","roleId":"role-1","subject":{"type":"email","value":"alice@newco.com"},"origin":"custom","user":null}`
			_, _ = fmt.Fprintf(w, `{"data":[`+row+`,`+row+`],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":2,"totalPages":1}}}`, "grant-1", "grant-2")
		})
		c := newV2TestClient(t, mux)
		_, err := c.CreateRBACGrant(t.Context(), wire.CreateGrantRequest{Subject: emailSubject("alice@newco.com"), RoleID: "role-1"})
		if herr, ok := errors.AsType[*HTTPError](err); !ok || herr.StatusCode != http.StatusConflict || strings.Contains(err.Error(), "looking up") {
			t.Fatalf("create = %v, want the bare 409 after a lookup that found two grants", err)
		}
	})
	t.Run("a failed lookup", func(t *testing.T) {
		p := newRekeyPlatform(t, func(next http.Handler) http.Handler {
			return failGrantsExpandUser(next, http.StatusInternalServerError)
		})
		p.signIn(t, rekeyIssuer, "sub-alice", "alice@newco.com")
		p.grant(t, emailSubject("alice@newco.com"))
		_, err := p.client.CreateRBACGrant(t.Context(), wire.CreateGrantRequest{Subject: emailSubject("alice@newco.com"), RoleID: p.roleID})
		if herr, ok := errors.AsType[*HTTPError](err); !ok || herr.StatusCode != http.StatusConflict {
			t.Fatalf("create = %v, want the 409", err)
		}
		if !strings.Contains(err.Error(), "looking up the held grant") {
			t.Errorf("create error %q does not say the lookup failed", err)
		}
	})
}

func TestApplyRBACGrants_PruneKeepsTheGrantACreateResolvedTo(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice.old@example.com")
	p.grant(t, emailSubject("alice.old@example.com"))
	p.declare(t, "    - alice@example.com\n")

	plan := p.plan(t)
	assertPlan(t, plan, 1, 0, 1, 0)
	p.signIn(t, rekeyIssuer, "sub-alice", "alice@example.com")
	applied := p.apply(t, plan, true)

	if got := p.holders(t); !slices.Equal(got, []string{alice}) {
		t.Fatalf("role holders after apply --prune = %v, want alice still holding the role", got)
	}
	if got := applied.RBACGrants.Skipped; len(got) != 1 || got[0].Reason != skipReasonGrantHeldByCreate {
		t.Fatalf("skipped = %+v, want the delete kept because the create resolved to it", got)
	}
	if out := renderedApply(applied); !strings.Contains(out, keptHeldByCreateLabel) {
		t.Errorf("apply output does not say why the delete was kept:\n%s", out)
	}

	again := p.plan(t)
	assertPlan(t, again, 0, 1, 0, 0)
	p.apply(t, again, true)
	if got := p.recorded(t); got[alice] != (apiv2.CreateGrantSubject{Type: store.PendingSubjectLegacy, Value: "alice@example.com"}) {
		t.Fatalf("grants after the next apply = %+v, want alice's grant recorded under the declared subject", got)
	}
	assertPlan(t, p.plan(t), 0, 0, 0, 1)
}

func TestApplyRBACGrants_AUserDeclaredTwiceBeforeHoldingTheRole(t *testing.T) {
	p := newRekeyPlatform(t, nil)
	alice := p.signIn(t, rekeyIssuer, "sub-alice", "alice@example.com")
	p.declare(t, "    - {type: email, value: alice@example.com}\n    - {type: sub, issuer: \""+rekeyIssuer+"\", value: sub-alice}\n")

	plan := p.plan(t)
	assertPlan(t, plan, 2, 0, 0, 0)
	res := p.apply(t, plan, true)
	if got := len(res.RBACGrants.Created); got != 2 {
		t.Errorf("created = %d, want both creates answered with alice's grant\n%s", got, renderedApply(res))
	}

	if got := p.holders(t); !slices.Equal(got, []string{alice}) {
		t.Fatalf("role holders after apply = %v, want only alice", got)
	}
	again := p.plan(t)
	assertPlan(t, again, 0, 0, 0, 2)
	assertWarning(t, again, "name the same user")
}
