// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	rbacBuilt   = true
	rbacUnbuilt = false
)

func newWorkflowEvalsPlatform(t *testing.T, workflowEvals, rbacState bool) (*http.ServeMux, *platpkg.PlatformCore) {
	t.Helper()
	t.Setenv("SAM_FEATURE_WORKFLOW_EVALS", "")
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	if _, err := NewModelConfigService(store.NewModelConfigRepo(core.DB()), testLogger()).Create(t.Context(), &CreateModelConfigRequest{
		Alias: "gpt", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-test"},
	}, "alice"); err != nil {
		t.Fatalf("create model: %v", err)
	}
	workflow := &store.Workflow{Name: "order_flow", AppConfig: `{}`, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewWorkflowRepo(core.DB()).Create(t.Context(), workflow, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowEvals, Default: workflowEvals})
	mux := http.NewServeMux()
	yamlState, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, testLogger())
	if err != nil {
		t.Fatalf("build rbac yaml state: %v", err)
	}
	if rbacState == rbacUnbuilt {
		if _, err := core.DB().ExecContext(t.Context(), `DROP TABLE rbac_role_scopes`); err != nil {
			t.Fatalf("break the rbac snapshot: %v", err)
		}
	}
	deps := &RouteDeps{FeatureRegistry: reg, RBACYAMLState: yamlState}
	RegisterRoutesWithDeps(mux, core, deps)
	return mux, core
}
