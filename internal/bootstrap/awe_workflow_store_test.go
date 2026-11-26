// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestNewWorkflowInstance_StoreFallsBackToTheAWEBaseSessionService(t *testing.T) {
	tests := []struct {
		name      string
		appBlock  bool
		wantStore string
	}{
		{name: "an app without a block uses the AWE's", appBlock: false, wantStore: "base.db"},
		{name: "an app block wins", appBlock: true, wantStore: "app.db"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			appConfig := map[string]any{
				"workflow": map[string]any{
					"output_mapping": map[string]any{"r": "x"},
					"nodes":          []any{map[string]any{"id": "a", "type": "agent", "agent_name": "A"}},
				},
			}
			if tc.appBlock {
				appConfig["session_service"] = map[string]any{"type": "sql", "database_url": "sqlite:///" + filepath.Join(dir, "app.db")}
			}
			var o aweOptions
			WithAWEBaseConfig(map[string]any{
				"session_service": map[string]any{"type": "sql", "database_url": "sqlite:///" + filepath.Join(dir, "base.db")},
			})(&o)

			brk := broker.NewDevBroker()
			if err := brk.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
			reg := features.NewRegistry()
			reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
			rt := runtime.NewRuntime(brk, config.FromMap(nil), runtime.WithFeatureRegistry(reg))

			inst, err := newWorkflowInstance(newSpendPublishers(rt.Services()), nil, config.FromMap(map[string]any{
				"kind":              "workflow",
				"name":              "my-workflow",
				config.KeyAppConfig: appConfig,
			}), o.workflowDatabaseURL)
			if err != nil {
				t.Fatalf("newWorkflowInstance: %v", err)
			}
			if err := inst.Init(t.Context(), config.FromMap(nil), rt.Services()); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(func() { _ = inst.Stop(context.Background()) })

			for _, name := range []string{"base.db", "app.db"} {
				_, statErr := os.Stat(filepath.Join(dir, name))
				if opened := statErr == nil; opened != (name == tc.wantStore) {
					t.Errorf("%s opened = %v, want the workflow's store in %s", name, opened, tc.wantStore)
				}
			}
		})
	}
}
