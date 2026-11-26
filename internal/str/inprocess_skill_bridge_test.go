// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type bridgeToolParams struct {
	Message string `json:"message"`
}

func newBridgeFixtureTool(name string) *sdk.ToolDef {
	return sdk.NewTool(name, "in-process bridge fixture tool",
		func(_ context.Context, _ bridgeToolParams, _ *sdk.ToolContext) (*sdk.Result, error) {
			return sdk.OK("ok"), nil
		})
}

func withGlobalInProcessTool(t *testing.T, td *sdk.ToolDef) {
	t.Helper()
	saved := registeredInProcessTools
	registeredInProcessTools = append(append([]*sdk.ToolDef(nil), saved...), td)
	t.Cleanup(func() { registeredInProcessTools = saved })
}

func newBridgeWorker(t *testing.T, skillsDir string, workerTools []*sdk.ToolDef) *Worker {
	t.Helper()
	manifestPath := writeEmptyManifest(t)
	w := NewWorker(&testServices{brk: newTestBroker(t)}, WorkerConfig{
		WorkerID:       "test-inproc-skill-bridge",
		Namespace:      "test-ns",
		ManifestPaths:  []string{manifestPath},
		SkillsDir:      skillsDir,
		Sandbox:        SandboxConfig{Mode: "direct"},
		InProcessTools: workerTools,
	})
	ctx := context.Background()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(ctx) })
	return w
}

func TestInProcessSkillToolBridge(t *testing.T) {
	const builtinSkill = "sam-bridgetest"
	const toolName = "mytool"

	t.Run("global_registry_binds_builtin_skill_in_process", func(t *testing.T) {
		withGlobalInProcessTool(t, newBridgeFixtureTool(toolName))

		skillsDir := t.TempDir()
		writeRuntimeTestSkillMetadataOnly(t, skillsDir, builtinSkill, toolName)

		w := newBridgeWorker(t, skillsDir, nil)

		prefixed := builtinSkill + SkillToolSep + toolName
		entry := w.manifest.GetTool(prefixed)
		if entry == nil {
			t.Fatalf("bundled skill tool %q not registered; the in-process bridge did not bind it", prefixed)
		}
		if entry.inProcess == nil {
			t.Fatalf("bundled skill tool %q not bound in-process (Executable=%q) — invoke would fall back to the subprocess path", prefixed, entry.Executable)
		}
		if entry.Parameters == nil {
			t.Errorf("expected schema merged from the in-process definition, but Parameters is nil")
		}
	})

	t.Run("worker_config_binds_builtin_skill_in_process", func(t *testing.T) {
		skillsDir := t.TempDir()
		writeRuntimeTestSkillMetadataOnly(t, skillsDir, builtinSkill, toolName)

		w := newBridgeWorker(t, skillsDir, []*sdk.ToolDef{newBridgeFixtureTool(toolName)})

		prefixed := builtinSkill + SkillToolSep + toolName
		entry := w.manifest.GetTool(prefixed)
		if entry == nil || entry.inProcess == nil {
			t.Fatalf("bundled skill tool %q not bound in-process via worker config", prefixed)
		}
	})

	t.Run("user_skill_not_hijacked_by_matching_in_process_tool", func(t *testing.T) {
		withGlobalInProcessTool(t, newBridgeFixtureTool(toolName))

		skillsDir := t.TempDir()
		writeRuntimeTestSkill(t, skillsDir, "userskill", toolName)

		w := newBridgeWorker(t, skillsDir, nil)

		prefixed := "userskill" + SkillToolSep + toolName
		entry := w.manifest.GetTool(prefixed)
		if entry == nil {
			t.Fatalf("user skill tool %q not registered via the exec path", prefixed)
		}
		if entry.inProcess != nil {
			t.Fatalf("user skill tool %q was hijacked by the compiled-in tool (inProcess set); provenance gate failed", prefixed)
		}
		if entry.Executable == "" {
			t.Errorf("user skill tool %q should retain its own executable", prefixed)
		}
	})
}
