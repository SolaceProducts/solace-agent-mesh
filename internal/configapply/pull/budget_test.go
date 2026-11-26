// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newBudgetServer(h http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == reconcile.APIV2ProbePath {
			writeV2Page(w, []struct{}{})
			return
		}
		h.ServeHTTP(w, r)
	}))
}

func budgetPlatform(t *testing.T) *httptest.Server {
	t.Helper()
	page := entplatform.PaginatedResponse[wire.BudgetResponse]{Data: []wire.BudgetResponse{
		{ID: "m", Name: "Agent Mesh", SubjectType: "mesh", IsDefault: true},
		{ID: "d", Name: "Default User", SubjectType: "user", IsDefault: true},
		{ID: "e", Name: "Engineering", SubjectType: "user", Amount: new("19.90"), WarningPercentage: new(80)},
	}}
	srv := newBudgetServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/platform/budgets" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pulledBudgetSpecs(t *testing.T, entries []fileEntry) map[string]map[string]any {
	t.Helper()
	specs := map[string]map[string]any{}
	for _, e := range entries {
		var doc struct {
			Kind string         `yaml:"kind"`
			Name string         `yaml:"name"`
			Spec map[string]any `yaml:"spec"`
		}
		if err := yaml.Unmarshal(e.content, &doc); err != nil {
			t.Fatalf("%s is not valid YAML: %v", e.relPath, err)
		}
		if doc.Kind != "budget" || e.relPath != filepath.Join("budgets", doc.Name+".yaml") {
			t.Errorf("%s: kind %q name %q, want a budget file named after the budget", e.relPath, doc.Kind, doc.Name)
		}
		specs[doc.Name] = doc.Spec
	}
	return specs
}

func TestPullBudgets_EmitsTheSpecShapeApplyReadsBack(t *testing.T) {
	c := reconcile.NewClient(target.Resolved{URL: budgetPlatform(t).URL})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullBudgets: %v", err)
	}
	if len(names) != 3 || len(res.Warnings) != 0 {
		t.Fatalf("names = %v, warnings = %v; want all three budgets and no warnings", names, res.Warnings)
	}
	specs := pulledBudgetSpecs(t, entries)

	mesh := specs["Agent Mesh"]
	if _, has := mesh["isDefault"]; has || mesh["subjectType"] != "mesh" {
		t.Errorf("mesh spec = %v, want subjectType mesh and no isDefault", mesh)
	}
	if amount, has := mesh["amount"]; !has || amount != nil {
		t.Errorf("mesh amount = %v (present %v), want an explicit null", amount, has)
	}
	if def := specs["Default User"]; def["isDefault"] != true {
		t.Errorf("Default User spec = %v, want isDefault: true", def)
	}
	eng := specs["Engineering"]
	if eng["amount"] != "19.90" || eng["warningPercentage"] != 80 {
		t.Errorf("Engineering spec = %v, want amount \"19.90\" as a string and warningPercentage 80", eng)
	}
	if _, has := eng["isDefault"]; has {
		t.Errorf("Engineering spec = %v, want no isDefault on a budget that is not the default", eng)
	}
}

func TestPullBudgets_PlatformWithoutBudgets(t *testing.T) {
	srv := newBudgetServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil || len(entries) != 0 || len(res.Warnings) != 0 {
		t.Errorf("full pull: entries %d, warnings %v, err %v; want a silent skip", len(entries), res.Warnings, err)
	}
	if _, _, err := pullBudgets(t.Context(), c, Options{OnlyKind: "budget"}, res); err == nil || !errors.Is(err, reconcile.ErrBudgetsUnavailable) {
		t.Errorf("--only budget err = %v, want %v", err, reconcile.ErrBudgetsUnavailable)
	}
}

func TestPullBudgets_FullPullSkipsBudgetsTheTokenCannotRead(t *testing.T) {
	srv := newBudgetServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusForbidden, "forbidden")
	}))
	t.Cleanup(srv.Close)
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil || len(entries) != 0 {
		t.Errorf("full pull: entries %d, err %v; want budgets skipped", len(entries), err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "budget:*:read") {
		t.Errorf("warnings = %v, want one naming the missing scope", res.Warnings)
	}
	if _, _, err := pullBudgets(t.Context(), c, Options{OnlyKind: "budget"}, &Result{Counts: map[string]int{}, Skipped: map[string]int{}}); err == nil {
		t.Error("--only budget err = nil, want the refusal")
	}
}

func TestPull_BudgetIsInScope(t *testing.T) {
	if !slices.Contains(InScopeKinds(), "budget") {
		t.Error("InScopeKinds omits budget; the --only help would hide a default-on kind")
	}

	out := t.TempDir()
	res, err := Run(t.Context(), target.Target{URL: budgetPlatform(t).URL}, Options{OutputDir: out, OnlyKind: "budget", ManifestName: "pulled.yaml"})
	if err != nil {
		t.Fatalf("Run --only budget: %v", err)
	}
	if res.Counts["budget"] != 3 {
		t.Errorf("budget count = %d, want 3", res.Counts["budget"])
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifests", "pulled.yaml"))
	if err != nil {
		t.Fatalf("read pulled manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "budgets:") {
		t.Errorf("pulled manifest does not list budgets; apply would ignore the budget files:\n%s", manifest)
	}

	var summary bytes.Buffer
	RenderResult(&summary, res)
	if !strings.Contains(summary.String(), "3 budgets") {
		t.Errorf("summary %q does not count the budgets", summary.String())
	}
}

func budgetPlatformWith(t *testing.T, rows ...wire.BudgetResponse) *reconcile.Client {
	t.Helper()
	srv := newBudgetServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(entplatform.PaginatedResponse[wire.BudgetResponse]{Data: rows})
	}))
	t.Cleanup(srv.Close)
	return reconcile.NewClient(target.Resolved{URL: srv.URL})
}

func TestPullBudgets_WritesEveryNameThePlatformAdmits(t *testing.T) {
	c := budgetPlatformWith(t,
		wire.BudgetResponse{ID: "s", Name: "Sales/EMEA", SubjectType: "user"},
		wire.BudgetResponse{ID: "w", Name: `Ops\West`, SubjectType: "user"},
		wire.BudgetResponse{ID: "h", Name: ".hidden ops", SubjectType: "user"},
	)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil || len(names) != 3 || len(res.Warnings) != 0 {
		t.Fatalf("names = %v, warnings = %v, err = %v; want all three pulled", names, res.Warnings, err)
	}
	want := map[string]string{
		filepath.Join("budgets", "Sales_EMEA.yaml"): "Sales/EMEA",
		filepath.Join("budgets", "Ops_West.yaml"):   `Ops\West`,
		filepath.Join("budgets", "hidden ops.yaml"): ".hidden ops",
	}
	for _, e := range entries {
		var doc struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(e.content, &doc); err != nil {
			t.Fatal(err)
		}
		if want[e.relPath] != doc.Name {
			t.Errorf("%s holds %q, want %q", e.relPath, doc.Name, want[e.relPath])
		}
	}
}

func TestPullBudgets_SuffixesBudgetsThatShareAFile(t *testing.T) {
	c := budgetPlatformWith(t,
		wire.BudgetResponse{ID: "b", Name: "sales_emea", SubjectType: "user"},
		wire.BudgetResponse{ID: "r", Name: "budget", SubjectType: "user"},
		wire.BudgetResponse{ID: "a", Name: "Sales/EMEA", SubjectType: "user"},
		wire.BudgetResponse{ID: "d", Name: "...", SubjectType: "user"},
	)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullBudgets: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		var doc struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(e.content, &doc); err != nil {
			t.Fatal(err)
		}
		got[e.relPath] = doc.Name
	}
	want := map[string]string{
		filepath.Join("budgets", "Sales_EMEA.yaml"):   "Sales/EMEA",
		filepath.Join("budgets", "sales_emea-2.yaml"): "sales_emea",
		filepath.Join("budgets", "budget.yaml"):       "...",
		filepath.Join("budgets", "budget-2.yaml"):     "budget",
	}
	if !maps.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func budgetUsersPlatform(t *testing.T, forbidden bool) *httptest.Server {
	t.Helper()
	budgets := entplatform.PaginatedResponse[wire.BudgetResponse]{Data: []wire.BudgetResponse{
		{ID: "d", Name: "Default User", SubjectType: "user", IsDefault: true},
		{ID: "e", Name: "Engineering", SubjectType: "user", AssignedCount: 2, PendingCount: 1},
		{ID: "o", Name: "Operations", SubjectType: "user", AssignedCount: 2},
	}}
	assignment := func(id, budgetID, state string, identity *apiv2.BudgetAssignmentIdentity) wire.BudgetAssignmentResponse {
		return wire.BudgetAssignmentResponse{ID: id, State: state, Subject: apiv2.BudgetAssignmentSubject{Type: "user", Identity: identity}, Budget: apiv2.BudgetRef{ID: budgetID}}
	}
	assignments := entplatform.PaginatedResponse[wire.BudgetAssignmentResponse]{Data: []wire.BudgetAssignmentResponse{
		assignment("a1", "e", "concrete", &apiv2.BudgetAssignmentIdentity{Type: "sub", Value: "u-1", Issuer: new("https://idp")}),
		assignment("a2", "e", "pending", &apiv2.BudgetAssignmentIdentity{Type: "email", Value: "new@example.com"}),
		assignment("a3", "e", "concrete", &apiv2.BudgetAssignmentIdentity{Type: "system", Value: "ci"}),
		assignment("a4", "o", "concrete", &apiv2.BudgetAssignmentIdentity{Type: "email", Value: "ops@example.com"}),
		assignment("a5", "o", "concrete", nil),
	}}
	srv := newBudgetServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/platform/budgets":
			_ = json.NewEncoder(w).Encode(budgets)
		case r.URL.Path == "/api/v2/platform/budgetAssignments" && forbidden:
			writeV2Error(w, http.StatusForbidden, "forbidden")
		case r.URL.Path == "/api/v2/platform/budgetAssignments":
			_ = json.NewEncoder(w).Encode(assignments)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPullBudgets_WritesEachAssignedBudgetsUsers(t *testing.T) {
	c := reconcile.NewClient(target.Resolved{URL: budgetUsersPlatform(t, false).URL})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullBudgets: %v", err)
	}
	specs := pulledBudgetSpecs(t, entries)

	want := []any{"new@example.com", map[string]any{"type": "sub", "value": "u-1", "issuer": "https://idp"}, "system:ci"}
	if got := specs["Engineering"]["users"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if _, has := specs["Default User"]["users"]; has {
		t.Error("an unassigned budget got a users key; it would become config-managed")
	}
	if _, has := specs["Operations"]["users"]; has {
		t.Error("a budget with an unwritable assignee got a partial users list")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], `"Operations"`) {
		t.Errorf("warnings = %v, want one naming Operations", res.Warnings)
	}
}

func TestPullBudgets_PullsBudgetsWithoutUsersWhenAssignmentsAreForbidden(t *testing.T) {
	c := reconcile.NewClient(target.Resolved{URL: budgetUsersPlatform(t, true).URL})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullBudgets(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullBudgets: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("names = %v, want all three budgets", names)
	}
	for name, spec := range pulledBudgetSpecs(t, entries) {
		if _, has := spec["users"]; has {
			t.Errorf("%s got users without permission to read them", name)
		}
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "rbac:*:read") {
		t.Errorf("warnings = %v, want one naming the missing scope", res.Warnings)
	}
}

func pullBudgetUsersFrom(t *testing.T, assignments ...wire.BudgetAssignmentResponse) (map[string]map[string]any, []string) {
	t.Helper()
	budgets := entplatform.PaginatedResponse[wire.BudgetResponse]{Data: []wire.BudgetResponse{
		{ID: "e", Name: "Engineering", SubjectType: "user", AssignedCount: 1, PendingCount: 1},
		{ID: "o", Name: "Operations", SubjectType: "user", AssignedCount: 1},
	}}
	page := entplatform.PaginatedResponse[wire.BudgetAssignmentResponse]{Data: assignments}
	srv := newBudgetServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/platform/budgets":
			_ = json.NewEncoder(w).Encode(budgets)
		case "/api/v2/platform/budgetAssignments":
			_ = json.NewEncoder(w).Encode(page)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullBudgets(t.Context(), reconcile.NewClient(target.Resolved{URL: srv.URL}), Options{}, res)
	if err != nil {
		t.Fatalf("pullBudgets: %v", err)
	}
	return pulledBudgetSpecs(t, entries), res.Warnings
}

func concreteUser(id, budgetID, email string, shared bool, identity apiv2.BudgetAssignmentIdentity) wire.BudgetAssignmentResponse {
	return wire.BudgetAssignmentResponse{ID: id, State: "concrete", Budget: apiv2.BudgetRef{ID: budgetID}, Subject: apiv2.BudgetAssignmentSubject{
		Type: "user", ID: new("user-" + id), Identity: &identity,
		User: &apiv2.BudgetAssignmentUser{Email: new(email), EmailShared: new(shared), Sub: new("sub-" + id), Issuer: new("https://idp")},
	}}
}

func pendingUser(id, budgetID string, identity apiv2.BudgetAssignmentIdentity) wire.BudgetAssignmentResponse {
	return wire.BudgetAssignmentResponse{ID: id, State: "pending", Budget: apiv2.BudgetRef{ID: budgetID}, Subject: apiv2.BudgetAssignmentSubject{Type: "user", Identity: &identity}}
}

func TestPullBudgets_LeavesOutAPendingAssignmentForAnAssignedUser(t *testing.T) {
	specs, warnings := pullBudgetUsersFrom(t,
		concreteUser("a1", "o", "Alice@Example.com", false, apiv2.BudgetAssignmentIdentity{Type: "sub", Value: "sub-a1", Issuer: new("https://idp")}),
		pendingUser("p1", "e", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "alice@example.com"}),
		pendingUser("p2", "e", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "bob@example.com"}),
	)
	if got, want := specs["Engineering"]["users"], []any{"bob@example.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if got, want := specs["Operations"]["users"], []any{map[string]any{"type": "sub", "value": "sub-a1", "issuer": "https://idp"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Operations users = %#v, want %#v", got, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"Engineering"`) || !strings.Contains(warnings[0], "p1") {
		t.Errorf("warnings = %v, want one naming Engineering's pending p1", warnings)
	}
}

func TestPullBudgets_WritesTheEmailAnAssignmentWasMadeBy(t *testing.T) {
	changed := concreteUser("a1", "e", "new@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "old@x"})
	shared := concreteUser("a1", "e", "old@x", true, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "old@x"})
	for name, a := range map[string]wire.BudgetAssignmentResponse{"email changed": changed, "email shared": shared} {
		t.Run(name, func(t *testing.T) {
			specs, warnings := pullBudgetUsersFrom(t, a)
			if got, want := specs["Engineering"]["users"], []any{"old@x"}; !reflect.DeepEqual(got, want) {
				t.Errorf("Engineering users = %#v, want %#v", got, want)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
		})
	}
}

func TestPullBudgets_ARecordedEmailOutranksAnotherUsersCurrentOne(t *testing.T) {
	specs, warnings := pullBudgetUsersFrom(t,
		concreteUser("a1", "e", "moved@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
		concreteUser("a2", "o", "k@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "earlier@x"}),
	)
	if got, want := specs["Engineering"]["users"], []any{"k@x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if got, want := specs["Operations"]["users"], []any{"earlier@x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Operations users = %#v, want %#v", got, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestPullBudgets_AnEmailTwoAssignmentsWereMadeByIsWrittenAsSubs(t *testing.T) {
	specs, warnings := pullBudgetUsersFrom(t,
		concreteUser("a1", "e", "one@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
		concreteUser("a2", "o", "two@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
	)
	if got, want := specs["Engineering"]["users"], []any{map[string]any{"type": "sub", "value": "sub-a1", "issuer": "https://idp"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	noSub := concreteUser("a2", "o", "two@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"})
	noSub.Subject.User.Sub, noSub.Subject.User.Issuer = nil, nil
	specs, warnings = pullBudgetUsersFrom(t,
		concreteUser("a1", "e", "one@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
		noSub,
	)
	if _, has := specs["Operations"]["users"]; has {
		t.Error("a budget with an unwritable assignee got a users list")
	}
	if _, has := specs["Engineering"]["users"]; !has {
		t.Error("Engineering's users were not pulled")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"Operations"`) {
		t.Errorf("warnings = %v, want one naming Operations", warnings)
	}
}

func TestPullBudgets_APendingRowSharingARecordedEmailIsNotPulled(t *testing.T) {
	specs, warnings := pullBudgetUsersFrom(t,
		concreteUser("a1", "e", "moved@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
		pendingUser("p1", "o", apiv2.BudgetAssignmentIdentity{Type: "email", Value: "k@x"}),
	)
	if got, want := specs["Engineering"]["users"], []any{map[string]any{"type": "sub", "value": "sub-a1", "issuer": "https://idp"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if _, has := specs["Operations"]["users"]; has {
		t.Error("Operations got users; its pending row cannot be named alone")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"Operations"`) {
		t.Errorf("warnings = %v, want one naming Operations", warnings)
	}
}

func TestPullBudgets_LeavesOutAHiddenUsersAssignment(t *testing.T) {
	channel := concreteUser("a-channel", "e", "", false, apiv2.BudgetAssignmentIdentity{Type: "system", Value: "channel"})
	channel.Subject.ID = new(rbac.ChannelSystemUserID)
	channel.Subject.User.IsSystemUser = true
	specs, warnings := pullBudgetUsersFrom(t,
		channel,
		concreteUser("a1", "e", "a1@x", false, apiv2.BudgetAssignmentIdentity{Type: "email", Value: "a1@x"}),
	)
	if got, want := specs["Engineering"]["users"], []any{"a1@x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Engineering users = %#v, want %#v", got, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}
