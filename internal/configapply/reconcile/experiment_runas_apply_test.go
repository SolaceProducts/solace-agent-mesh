// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newWorkflowEvalPlatform(t *testing.T, enabledFlags ...string) *Client {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowEvals), "true")
	for _, flag := range enabledFlags {
		t.Setenv(features.EnvVarName(flag), "true")
	}
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
	workflow := &store.Workflow{Name: "order_flow", AppConfig: `{}`, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewWorkflowRepo(core.DB()).Create(ctx, workflow, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "rbac", Default: true})
	reg.Register(features.Definition{Key: features.KeyWorkflowEvals, Default: true})
	for _, flag := range enabledFlags {
		reg.Register(features.Definition{Key: flag, Default: true})
	}
	mux := http.NewServeMux()
	deps := &entplatform.RouteDeps{IsRBACAuthority: true, FeatureRegistry: reg}
	entplatform.RegisterRoutesWithDeps(mux, core, deps)
	apiv2.RegisterRoutes(mux, deps, core, logger)
	superAdmin := auth.NewTestSubject(auth.SubjectFields{UserID: "operator@example.com", Authenticated: true, Scopes: []string{"*"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), superAdmin)))
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL, Token: "tok"})
}

func writeRepoFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApply_WorkflowExperimentWithItsRunAsGrantConvergesInOneApply(t *testing.T) {
	c := newWorkflowEvalPlatform(t)
	repo := t.TempDir()
	writeRepoFiles(t, repo, map[string]string{
		"rbac/roles/orders_invoke.yaml":     "kind: rbacRole\nname: orders_invoke\nspec:\n  scopes:\n    - \"workflow:*:invoke\"\n",
		"rbac/system-users/orders-bot.yaml": "kind: systemUser\nname: orders-bot\nspec:\n  roleNames:\n    - orders_invoke\n",
		"datasets/orders.yaml":              "kind: dataset\nname: orders\ndescription: Orders.\nspec:\n  examples_file: orders.examples.csv\n",
		"datasets/orders.examples.csv":      "sequence_number,prompt,expected_response\n1,order O-1,\n",
		"experiments/order-regression.yaml": "kind: experiment\nname: order-regression\ndescription: Orders.\nspec:\n" +
			"  datasetId: orders\n  targetWorkflow: order_flow\n  evaluatorIds: [\"Valid JSON\"]\n  runAs: orders-bot\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"rbacRoles":   {{Name: "orders_invoke", From: "orders_invoke"}},
		"systemUsers": {{Name: "orders-bot", From: "orders-bot"}},
		"datasets":    {{Name: "orders", From: "orders"}},
		"experiments": {{Name: "order-regression", From: "order-regression"}},
	}}

	plan, err := planAll(t.Context(), c, repo, m, resolve.New(repo, m, resolve.Options{}), BuildOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	out := &ApplyResult{Plan: plan}
	if err := applyPlan(t.Context(), c, plan, ApplyOptions{}, out); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if anyFailures(out) {
		t.Fatalf("apply had failures: experiments %+v, system users %+v", out.Experiments, out.SystemUsers)
	}
	if out.Experiments == nil || len(out.Experiments.Created) != 1 {
		t.Errorf("experiment outcome = %+v, want it created", out.Experiments)
	}
}
