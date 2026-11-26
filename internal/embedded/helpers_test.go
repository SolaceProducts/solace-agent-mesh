// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func setupMinimalRuntime(t *testing.T, inst *Instance) {
	t.Helper()
	inst.sharedBroker = broker.NewDevBroker()
	inst.rt = runtime.NewRuntime(inst.sharedBroker, nil)
}

func TestAppendDiscoveredSTRTools_NoEnvVars(t *testing.T) {
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", "")
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry to watch platform toolsets_dir, got %d", len(classified.STR))
	}
	pkgDir, _ := classified.STR[0].AppConfig["toolsets_dir"].(string)
	if pkgDir != embeddedPlatformPackagesDir("test-ns") {
		t.Fatalf("toolsets_dir: got %q, want %q", pkgDir, embeddedPlatformPackagesDir("test-ns"))
	}
}

func TestAppendDiscoveredSTRTools_EmptyToolsDirs(t *testing.T) {
	toolsDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", toolsDir)
	t.Setenv("SAM_SKILLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	if _, ok := classified.STR[0].AppConfig["toolsets_dir"].(string); !ok {
		t.Fatal("expected toolsets_dir to be set so STR watches the platform write path")
	}
}

func TestAppendDiscoveredSTRTools_SkillsDirSetAppendsWorker(t *testing.T) {
	skillsDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", skillsDir)
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	entry := classified.STR[0]
	if entry.Name != "str_tools" {
		t.Fatalf("unexpected STR entry name: %q", entry.Name)
	}
	want := embeddedPlatformSkillsDir("test-ns")
	if got, _ := entry.AppConfig["skills_dir"].(string); got != want {
		t.Fatalf("skills_dir: got %q, want %q (env path %q had no skills)", got, want, skillsDir)
	}
	if _, ok := entry.AppConfig["artifact_service"].(map[string]any); !ok {
		t.Fatal("expected artifact_service when SAM_DATA_DIR is set")
	}
	if _, ok := entry.AppConfig["volume_root"].(string); !ok {
		t.Fatal("expected volume_root when SAM_DATA_DIR is set")
	}
}

func TestAppendDiscoveredSTRTools_PlatformOverridesSkillsDir(t *testing.T) {
	envSkillsDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", envSkillsDir)
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{
		Platform: []samapp.AppEntry{
			{Name: "platform", AppConfig: map[string]any{"namespace": "test-ns"}},
		},
	}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	want := embeddedPlatformSkillsDir("test-ns")
	if got, _ := classified.STR[0].AppConfig["skills_dir"].(string); got != want {
		t.Fatalf("skills_dir override: got %q, want %q (env was %q)", got, want, envSkillsDir)
	}
}

func TestAppendDiscoveredSTRTools_ExistingWorkerSkillsDirOverridden(t *testing.T) {
	envSkillsDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", envSkillsDir)
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{
		Platform: []samapp.AppEntry{
			{Name: "platform", AppConfig: map[string]any{"namespace": "test-ns"}},
		},
		STR: []samapp.AppEntry{
			{Name: "str_builtin_tools", AppConfig: map[string]any{
				"skills_dir": "/opt/sam/skills",
			}},
		},
	}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	want := embeddedPlatformSkillsDir("test-ns")
	if got, _ := classified.STR[0].AppConfig["skills_dir"].(string); got != want {
		t.Fatalf("skills_dir on pre-existing worker: got %q, want %q — uploaded skills would sit pending forever", got, want)
	}
}

func TestAppendDiscoveredSTRTools_RealSTRConfigSkillsDirOverridden(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", t.TempDir())
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	strConfig := filepath.Join("..", "..", "configs", "str", "str.yaml")
	inst := newTestInstance(Config{ConfigPaths: []string{strConfig}})
	loaded, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs(%s): %v", strConfig, err)
	}
	apps, _ := loaded["apps"].([]any)
	classified := ClassifyForEmbedded(apps)
	if len(classified.STR) != 1 {
		t.Fatalf("expected configs/str/str.yaml to classify as exactly 1 STR worker, got %d", len(classified.STR))
	}

	namespace, _ := classified.STR[0].AppConfig["namespace"].(string)
	if namespace == "" {
		t.Fatal("expected a namespace in the STR worker app_config")
	}
	yamlSkillsDir, _ := classified.STR[0].AppConfig["skills_dir"].(string)
	want := embeddedPlatformSkillsDir(namespace)
	if yamlSkillsDir == "" || yamlSkillsDir == want {
		t.Fatalf("premise of this fix no longer holds: str.yaml resolved skills_dir to %q, platform path is %q — a merge-if-empty guard would now be reachable, so re-check whether the unconditional override is still what we want", yamlSkillsDir, want)
	}

	inst.appendDiscoveredSTRTools(&classified, namespace)

	if got, _ := classified.STR[0].AppConfig["skills_dir"].(string); got != want {
		t.Fatalf("skills_dir from real str.yaml: got %q, want %q — uploaded skills would sit pending forever", got, want)
	}
}

func TestAppendDiscoveredSTRTools_BundledSkillsBecomeBuiltinDir(t *testing.T) {
	envSkillsDir := t.TempDir()
	skillDir := filepath.Join(envSkillsDir, "bundled-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: bundled-skill\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", envSkillsDir)
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{
		Platform: []samapp.AppEntry{
			{Name: "platform", AppConfig: map[string]any{"namespace": "test-ns"}},
		},
	}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	ac := classified.STR[0].AppConfig
	if got, want := ac["skills_dir"], embeddedPlatformSkillsDir("test-ns"); got != want {
		t.Errorf("skills_dir = %v, want platform upload dir %v", got, want)
	}
	if got, _ := ac["builtin_skills_dir"].(string); got != envSkillsDir {
		t.Errorf("builtin_skills_dir = %q, want bundled env dir %q", got, envSkillsDir)
	}
}

func TestAppendDiscoveredSTRTools_PlatformOverridesToolsetsDirAndDeprecatedAlias(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", dataDir)
	t.Setenv("SAM_VOLUME_ROOT", "")

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{
		Platform: []samapp.AppEntry{
			{Name: "platform", AppConfig: map[string]any{"namespace": "test-ns"}},
		},
		STR: []samapp.AppEntry{
			{Name: "str", AppConfig: map[string]any{
				"namespace":    "test-ns",
				"toolsets_dir": "/opt/sam/wrong",
				"tools_dir":    "/opt/sam/also-wrong",
			}},
		},
	}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")

	cfg := classified.STR[0].AppConfig
	want := embeddedPlatformPackagesDir("test-ns")
	if got, _ := cfg["toolsets_dir"].(string); got != want {
		t.Fatalf("toolsets_dir override: got %q, want %q", got, want)
	}
	if _, ok := cfg["tools_dir"]; ok {
		t.Error("deprecated tools_dir alias should be dropped after embedded override")
	}
	if _, ok := cfg["packages_dir"]; ok {
		t.Error("deprecated packages_dir alias should be dropped after embedded override")
	}
}

func TestEmbeddedPlatformSkillsDir(t *testing.T) {
	t.Setenv("SAM_SKILLS_DIR", "/tmp/should-be-ignored")
	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got := embeddedPlatformSkillsDir("my-ns")
	want := "/tmp/test-data/my-ns/str-runtime/skills"
	if got != want {
		t.Fatalf("data-dir path: got %q, want %q", got, want)
	}

	t.Setenv("SAM_SKILLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", "")
	abs, err := filepath.Abs("./sam-data")
	if err != nil {
		t.Fatal(err)
	}
	got = embeddedPlatformSkillsDir("my-ns")
	want = abs + "/my-ns/str-runtime/skills"
	if got != want {
		t.Fatalf("default fallback: got %q, want %q", got, want)
	}

	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got = embeddedPlatformSkillsDir("")
	want = "/tmp/test-data/str-runtime/skills"
	if got != want {
		t.Fatalf("empty-namespace: got %q, want %q", got, want)
	}
}

func TestEmbeddedGeneratedManifestDir(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got := embeddedGeneratedManifestDir("my-ns")
	want := "/tmp/test-data/my-ns/str-runtime/skill-manifests"
	if got != want {
		t.Fatalf("data-dir path: got %q, want %q", got, want)
	}

	t.Setenv("SAM_DATA_DIR", "")
	abs, err := filepath.Abs("./sam-data")
	if err != nil {
		t.Fatal(err)
	}
	got = embeddedGeneratedManifestDir("my-ns")
	want = abs + "/my-ns/str-runtime/skill-manifests"
	if got != want {
		t.Fatalf("default fallback: got %q, want %q", got, want)
	}

	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got = embeddedGeneratedManifestDir("")
	want = "/tmp/test-data/str-runtime/skill-manifests"
	if got != want {
		t.Fatalf("empty-namespace: got %q, want %q", got, want)
	}
}

func TestAppendDiscoveredSTRTools_VolumeRootOverride(t *testing.T) {
	skillsDir := t.TempDir()
	volRoot := t.TempDir()
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_SKILLS_DIR", skillsDir)
	t.Setenv("SAM_DATA_DIR", "")
	t.Setenv("SAM_VOLUME_ROOT", volRoot)

	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}
	inst.appendDiscoveredSTRTools(&classified, "test-ns")
	if len(classified.STR) != 1 {
		t.Fatalf("expected 1 STR entry, got %d", len(classified.STR))
	}
	if got, _ := classified.STR[0].AppConfig["volume_root"].(string); got != volRoot {
		t.Fatalf("volume_root: got %q, want %q", got, volRoot)
	}
}

func TestRegisterSTRWorkers_Empty(t *testing.T) {
	inst := newTestInstance(Config{})
	if err := inst.registerSTRWorkers(t.Context(), nil); err != nil {
		t.Fatalf("expected no error for empty STR entries, got %v", err)
	}
}

func TestBuildPlatformCore_InlineDefaults(t *testing.T) {
	inst := newTestInstance(Config{PlatformPort: 9001})
	setupMinimalRuntime(t, inst)

	core, component, _ := inst.buildPlatformCore(EmbeddedApps{}, "my-ns", 9001, nil)
	if core == nil {
		t.Fatal("expected non-nil core")
	}
	if component == nil {
		t.Fatal("expected non-nil component")
	}
}

func TestBuildPlatformCore_UsesClassifiedPlatformEntry(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Platform: []samapp.AppEntry{
			{
				Name: "platform",
				AppConfig: map[string]any{
					"fastapi_host": "0.0.0.0",
					"fastapi_port": 1234,
				},
			},
		},
	}
	core, _, _ := inst.buildPlatformCore(classified, "fill-me", 8001, nil)
	if core == nil {
		t.Fatal("expected non-nil core")
	}
	if ns, _ := classified.Platform[0].AppConfig["namespace"].(string); ns != "fill-me" {
		t.Fatalf("expected namespace filled from argument, got %q", ns)
	}
}

func TestRegisterGatewayExecutor_NoGateways(t *testing.T) {
	inst := newTestInstance(Config{})
	gwe, err := inst.registerGatewayExecutor(EmbeddedApps{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gwe != nil {
		t.Fatalf("expected nil executor, got %v", gwe)
	}
}

func TestBuildRuntime_NoAppsUsesLoggerOnly(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sharedBroker = broker.NewDevBroker()

	err := inst.buildRuntime(t.Context(), map[string]any{}, EmbeddedApps{}, "test-ns")
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	if inst.rt == nil {
		t.Fatal("expected inst.rt to be set")
	}
}

func TestBuildRuntime_RegistersModelBindingsWatcher(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sharedBroker = broker.NewDevBroker()

	if err := inst.buildRuntime(t.Context(), map[string]any{}, EmbeddedApps{}, "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	t.Cleanup(inst.bindings.Close)
	for _, c := range inst.rt.Components() {
		if c.Name != "model-bindings" {
			continue
		}
		if _, ok := c.Component.(*bindings.Watcher); !ok {
			t.Fatalf("model-bindings component is %T, want *bindings.Watcher", c.Component)
		}
		return
	}
	t.Fatal("model-bindings watcher is not registered")
}

func TestBuildRuntime_BuildsTheLeaderElectionForStartToRegisterLast(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sharedBroker = broker.NewDevBroker()

	if err := inst.buildRuntime(t.Context(), map[string]any{}, EmbeddedApps{}, "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	t.Cleanup(inst.bindings.Close)
	if inst.leader == nil {
		t.Fatal("buildRuntime built no leader election")
	}
	for _, c := range inst.rt.Components() {
		if c.Component == inst.leader {
			t.Fatal("buildRuntime registered the leader election before the components whose jobs it runs")
		}
	}
}

func TestBuildRuntime_AWEExecutorBranch_FallsBackToInMemorySessions(t *testing.T) {
	inst := newTestInstance(Config{})
	inst.sharedBroker = broker.NewDevBroker()

	classified := EmbeddedApps{Platform: []samapp.AppEntry{{Name: "platform"}}}
	if err := inst.buildRuntime(t.Context(), map[string]any{}, classified, "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	if inst.rt == nil {
		t.Fatal("expected inst.rt to be set")
	}
	if inst.rt.Services().Sessions() == nil {
		t.Fatal("expected in-memory session store to be wired; runtime-deployed agents nil-deref in HandleTask without it")
	}
}

func TestStartSharedBroker_DevMode(t *testing.T) {
	inst := newTestInstance(Config{})
	classified := EmbeddedApps{}

	if err := inst.startSharedBroker(t.Context(), classified); err != nil {
		t.Fatalf("startSharedBroker: %v", err)
	}
	if !inst.usingDevBroker {
		t.Fatal("expected usingDevBroker=true in dev mode")
	}
	if inst.sharedBroker == nil {
		t.Fatal("expected sharedBroker to be set")
	}
}

func TestRegisterGatewayPlatformProxy_NoGateway(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	proxy, err := inst.registerGatewayPlatformProxy(EmbeddedApps{}, "ns")
	if err != nil {
		t.Fatalf("registerGatewayPlatformProxy: %v", err)
	}
	if proxy != nil {
		t.Fatal("expected nil proxy when no gateway configs")
	}
}

func TestStartHealthServer_Disabled(t *testing.T) {
	inst := newTestInstance(Config{HealthAddr: "disabled"})
	setupMinimalRuntime(t, inst)

	inst.startHealthServer()
	if inst.healthServer != nil {
		t.Fatal("expected nil healthServer when disabled")
	}
}

func TestStartHealthServer_Auto(t *testing.T) {
	inst := newTestInstance(Config{HealthAddr: "127.0.0.1:0"})
	setupMinimalRuntime(t, inst)

	inst.startHealthServer()
	if inst.healthServer == nil {
		t.Fatal("expected healthServer to be set")
	}
	t.Cleanup(func() { _ = inst.healthServer.Stop(t.Context()) })
}

func TestStartHealthServer_BadAddr(t *testing.T) {
	inst := newTestInstance(Config{HealthAddr: "nope:not-a-port"})
	setupMinimalRuntime(t, inst)

	inst.startHealthServer()
	if inst.healthServer != nil {
		t.Fatal("expected nil healthServer on bind error")
	}
}

func TestNew_DefaultLogger(t *testing.T) {
	inst := New(Config{})
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.logger == nil {
		t.Fatal("expected default logger to be set")
	}
}

func TestInitAndStartRuntime_Happy(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	if err := inst.initAndStartRuntime(ctx, cancel); err != nil {
		t.Fatalf("initAndStartRuntime: %v", err)
	}
	t.Cleanup(func() { _ = inst.rt.Stop(t.Context()) })
}

func TestRegisterGatewayExecutor_AutoWires(t *testing.T) {
	inst := newTestInstance(Config{GatewayPort: 9000})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Gateway: []samapp.AppEntry{
			{
				Name: "gw",
				AppConfig: map[string]any{
					"gateway_id": "gw-1",
				},
			},
		},
	}
	_, _ = inst.registerGatewayExecutor(classified)
	if got, _ := classified.Gateway[0].AppConfig["fastapi_port"].(int); got != 9000 {
		t.Fatalf("expected fastapi_port auto-wire to 9000, got %v", got)
	}
	if got, _ := classified.Gateway[0].AppConfig["volumes_allow_host_paths"].(bool); !got {
		t.Fatalf("expected volumes_allow_host_paths=true in embedded mode")
	}
}

func TestRegisterGatewayExecutor_HostPathVolumesRespectsExplicit(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Gateway: []samapp.AppEntry{
			{
				Name: "gw",
				AppConfig: map[string]any{
					"gateway_id":               "gw-1",
					"volumes_allow_host_paths": false,
				},
			},
		},
	}
	_, _ = inst.registerGatewayExecutor(classified)
	if got, _ := classified.Gateway[0].AppConfig["volumes_allow_host_paths"].(bool); got {
		t.Fatal("explicit volumes_allow_host_paths=false should be preserved")
	}
}

func TestRegisterGatewayExecutor_HostPathVolumesOffForNonLoopbackBind(t *testing.T) {
	inst := newTestInstance(Config{GatewayBindHost: "0.0.0.0"})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Gateway: []samapp.AppEntry{
			{
				Name:      "gw",
				AppConfig: map[string]any{"gateway_id": "gw-1"},
			},
		},
	}
	_, _ = inst.registerGatewayExecutor(classified)
	if got, _ := classified.Gateway[0].AppConfig["volumes_allow_host_paths"].(bool); got {
		t.Fatal("expected volumes_allow_host_paths=false on a non-loopback (0.0.0.0) bind")
	}
}

type stubIdentityResolver struct{}

func (stubIdentityResolver) ResolveIdentity(_ context.Context, _ string) (*auth.Subject, error) {
	return auth.NewTestSubject(auth.SubjectFields{UserID: "stub"}), nil
}

type hooksProvider struct {
	noopExtensionProvider
	hooks *entrypoint.GatewayHooks
}

func (h *hooksProvider) GatewayHooks() *entrypoint.GatewayHooks { return h.hooks }

type noopExtensionProvider struct{}

func (noopExtensionProvider) Bootstrap(_ context.Context, _ map[string]any, _ session.Store, _ broker.Broker, _ *slog.Logger) ([]runtime.Option, runtime.Component, error) {
	return nil, nil, nil
}
func (noopExtensionProvider) ConfigureGateway(_ *entrypoint.GatewayCore, _ map[string]any, _ *slog.Logger) error {
	return nil
}
func (noopExtensionProvider) GatewayHooks() *entrypoint.GatewayHooks { return nil }
func (noopExtensionProvider) Stop(_ context.Context) error           { return nil }

func TestRegisterGatewayExecutor_SetsGatewayHooks(t *testing.T) {
	hp := &hooksProvider{
		hooks: &entrypoint.GatewayHooks{
			IdentityResolver: stubIdentityResolver{},
		},
	}
	inst := newTestInstance(Config{Extensions: hp})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Gateway: []samapp.AppEntry{{
			Name:      "gw",
			AppConfig: map[string]any{"gateway_id": "gw-1"},
		}},
	}
	gweExe, err := inst.registerGatewayExecutor(classified)
	if err != nil {
		t.Fatalf("registerGatewayExecutor: %v", err)
	}
	if gweExe == nil {
		t.Fatal("expected non-nil executor")
	}

	gws := gweExe.Gateways()
	if len(gws) != 1 {
		t.Fatalf("Gateways() = %d, want 1", len(gws))
	}
	if gws[0].IdentityResolver() == nil {
		t.Fatal("IdentityResolver is nil after registerGatewayExecutor — " +
			"SetGatewayHooks was not called for GatewayHooksProvider")
	}
}

func TestEmbeddedPlatformPackagesDir(t *testing.T) {
	t.Setenv("STR_PACKAGES_DIR", "/should-be-ignored")
	t.Setenv("SAM_TOOLS_DIR", "/should-also-be-ignored")
	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got := embeddedPlatformPackagesDir("my-ns")
	want := filepath.FromSlash("/tmp/test-data/my-ns/str-runtime/toolsets")
	if got != want {
		t.Fatalf("data-dir path: got %q, want %q", got, want)
	}

	t.Setenv("STR_PACKAGES_DIR", "")
	t.Setenv("SAM_TOOLS_DIR", "")
	t.Setenv("SAM_DATA_DIR", "")
	abs, err := filepath.Abs("./sam-data")
	if err != nil {
		t.Fatal(err)
	}
	got = embeddedPlatformPackagesDir("my-ns")
	want = filepath.Join(abs, "my-ns", "str-runtime", "toolsets")
	if got != want {
		t.Fatalf("default fallback: got %q, want %q", got, want)
	}

	t.Setenv("SAM_DATA_DIR", "/tmp/test-data")
	got = embeddedPlatformPackagesDir("")
	want = filepath.FromSlash("/tmp/test-data/str-runtime/toolsets")
	if got != want {
		t.Fatalf("empty-namespace: got %q, want %q", got, want)
	}
}

func TestRegisterGatewayExecutor_SkipsHooksWithoutProvider(t *testing.T) {
	inst := newTestInstance(Config{Extensions: &noopExtensionProvider{}})
	setupMinimalRuntime(t, inst)

	classified := EmbeddedApps{
		Gateway: []samapp.AppEntry{{
			Name:      "gw",
			AppConfig: map[string]any{"gateway_id": "gw-1"},
		}},
	}
	gweExe, err := inst.registerGatewayExecutor(classified)
	if err != nil {
		t.Fatalf("registerGatewayExecutor: %v", err)
	}
	if gweExe == nil {
		t.Fatal("expected non-nil executor")
	}

	gws := gweExe.Gateways()
	if len(gws) != 1 {
		t.Fatalf("Gateways() = %d, want 1", len(gws))
	}
	if gws[0].IdentityResolver() != nil {
		t.Fatal("IdentityResolver should be nil when provider lacks GatewayHooksProvider")
	}
}
