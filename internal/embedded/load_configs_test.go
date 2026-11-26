// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func newTestInstance(cfg Config) *Instance {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &Instance{cfg: cfg, logger: cfg.Logger}
}

const minimalAgentYAML = `
apps:
  - name: test-agent
    app_config:
      agent_name: TestAgent
`

const defaultAgentYAML = `
apps:
  - name: default-agent
    app_config:
      agent_name: DefaultAgent
`

const onboardAgentYAML = `
apps:
  - name: onboard-agent
    app_config:
      agent_name: OnboardAgent
`

func TestLoadAllConfigs_FromFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(minimalAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{ConfigPaths: []string{path}})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
}

func TestLoadAllConfigs_FilesAndDefaultsBothLoadDifferentNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(minimalAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{
		ConfigPaths:    []string{path},
		DefaultConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 2 {
		t.Fatalf("expected file + default (2 apps), got %d", len(apps))
	}
	gotNames := map[string]bool{}
	for _, a := range apps {
		m, _ := a.(map[string]any)
		n, _ := m["name"].(string)
		gotNames[n] = true
	}
	if !gotNames["test-agent"] || !gotNames["default-agent"] {
		t.Fatalf("expected both test-agent (from file) and default-agent (from default), got %v", gotNames)
	}
}

func TestLoadAllConfigs_UserAppOverridesBundledByName(t *testing.T) {
	const userAgentSameName = `
apps:
  - name: default-agent
    app_config:
      agent_name: UserOverride
`
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(userAgentSameName), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{
		ConfigPaths:    []string{path},
		DefaultConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("expected dedup to 1 app, got %d", len(apps))
	}
	m, _ := apps[0].(map[string]any)
	ac, _ := m["app_config"].(map[string]any)
	if got, _ := ac["agent_name"].(string); got != "UserOverride" {
		t.Fatalf("expected user app to win, got agent_name=%q", got)
	}
}

func TestLoadAllConfigs_DedupDefaultsAgainstAdditional(t *testing.T) {
	inst := newTestInstance(Config{
		DefaultConfigs:    map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
		AdditionalConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("dedup failed: expected 1 app, got %d (defaults and additionals both merged)", len(apps))
	}
}

func TestLoadAllConfigs_AdditionalMergesWithFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(minimalAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{
		ConfigPaths:       []string{path},
		AdditionalConfigs: map[string][]byte{"onboard.yaml": []byte(onboardAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps (file + additional), got %d", len(apps))
	}
}

func TestLoadAllConfigs_DefaultsOnly(t *testing.T) {
	inst := newTestInstance(Config{
		DefaultConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
}

func TestLoadAllConfigs_FilesOnlySucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(minimalAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{ConfigPaths: []string{path}})
	if _, err := inst.loadAllConfigs(t.Context()); err != nil {
		t.Fatalf("files-only should succeed, got %v", err)
	}
}

func TestLoadAllConfigs_NothingConfigured(t *testing.T) {
	inst := newTestInstance(Config{})
	_, err := inst.loadAllConfigs(t.Context())
	if err == nil {
		t.Fatalf("expected error when no config source is provided")
	}
}

func TestLoadAllConfigs_MalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte("apps: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := newTestInstance(Config{ConfigPaths: []string{path}})
	if _, err := inst.loadAllConfigs(t.Context()); err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestLoadAllConfigs_MalformedDefault(t *testing.T) {
	inst := newTestInstance(Config{
		DefaultConfigs: map[string][]byte{"bad.yaml": []byte("apps: [unclosed")},
	})
	if _, err := inst.loadAllConfigs(t.Context()); err == nil {
		t.Fatal("expected error for malformed default YAML")
	}
}

func TestLoadAllConfigs_PreservesProvidersCatalog(t *testing.T) {
	const gatewayYAML = `
providers:
  keycloak:
    issuer: https://idp.example/realms/sam
    client_id: sam
    client_secret: shh

apps:
  - name: test-gateway
    app_config:
      gateway_id: test-gw
`
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(gatewayYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{ConfigPaths: []string{path}})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}

	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers catalog missing from loaded config; Bootstrap would register no providers. Keys present: %v", mapKeys(got))
	}
	if _, ok := providers["keycloak"]; !ok {
		t.Errorf("keycloak entry missing from providers catalog; got %v", providers)
	}
}

func TestLoadAllConfigs_PreservesTopLevelConfigKeys(t *testing.T) {
	const agentYAML = `
management_server:
  observability:
    enabled: true
session_service:
  type: sql
volume_service:
  type: filesystem

apps:
  - name: test-agent
    app_config:
      agent_name: TestAgent
`
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := newTestInstance(Config{ConfigPaths: []string{path}})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}

	for _, key := range []string{"management_server", "session_service", "volume_service"} {
		if _, ok := got[key].(map[string]any); !ok {
			t.Errorf("%s dropped from merged config; the feature it configures would be silently disabled. Keys present: %v", key, mapKeys(got))
		}
	}

	mgmt, ok := got["management_server"].(map[string]any)
	if !ok {
		t.Fatalf("management_server missing; /metrics would never mount. Keys present: %v", mapKeys(got))
	}
	obs, ok := mgmt["observability"].(map[string]any)
	if !ok {
		t.Fatalf("observability block missing from management_server; got %v", mgmt)
	}
	if enabled, _ := obs["enabled"].(bool); !enabled {
		t.Errorf("observability.enabled not preserved; got %v", obs)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestLoadAllConfigs_MissingPathFallsBackToDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	inst := newTestInstance(Config{
		ConfigPaths:    []string{missing},
		DefaultConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("expected fallback to defaults, got error: %v", err)
	}
	apps, _ := got["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("expected 1 app from defaults, got %d", len(apps))
	}
}

func TestLoadAllConfigs_MissingPathErrorsWithoutDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	inst := newTestInstance(Config{ConfigPaths: []string{missing}})
	if _, err := inst.loadAllConfigs(t.Context()); err == nil {
		t.Fatal("expected error when path is missing and no defaults are set")
	}
}

func TestLoadAllConfigs_NonNotExistErrorPropagates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod 000 cannot block the read")
	}
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "unreadable")
	if err := os.Mkdir(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	inst := newTestInstance(Config{
		ConfigPaths:    []string{filepath.Join(unreadable, "agent.yaml")},
		DefaultConfigs: map[string][]byte{"default_agent.yaml": []byte(defaultAgentYAML)},
	})
	if _, err := inst.loadAllConfigs(t.Context()); err == nil {
		t.Fatal("expected non-fs.ErrNotExist error to propagate, got nil (defaults silently substituted)")
	}
}

func TestLoadAllConfigs_MalformedAdditional(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(minimalAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := newTestInstance(Config{
		ConfigPaths:       []string{path},
		AdditionalConfigs: map[string][]byte{"bad.yaml": []byte("apps: [unclosed")},
	})
	if _, err := inst.loadAllConfigs(t.Context()); err == nil {
		t.Fatal("expected error for malformed additional YAML")
	}
}

func TestLoadAllConfigs_BundledDefaultsClassifyGatewayAndPlatform(t *testing.T) {
	inst := newTestInstance(Config{DefaultConfigs: settings.DefaultConfigYAML()})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}

	apps, ok := got["apps"].([]any)
	if !ok || len(apps) != 2 {
		t.Fatalf("merged apps = %v, want gateway + platform (2)", got["apps"])
	}

	classified := ClassifyForEmbedded(apps)
	if len(classified.Gateway) != 1 {
		t.Errorf("Gateway apps = %d, want 1", len(classified.Gateway))
	}
	if len(classified.Platform) != 1 {
		t.Errorf("Platform apps = %d, want 1 (component_type:platform must classify)", len(classified.Platform))
	}
	if len(classified.Platform) == 1 {
		if _, ok := classified.Platform[0].AppConfig["authorization_service"]; !ok {
			t.Error("platform app_config missing authorization_service after load")
		}
	}
}

func TestLoadAllConfigs_BundledGatewayBatchesEventLogFlushes(t *testing.T) {
	inst := newTestInstance(Config{DefaultConfigs: settings.DefaultConfigYAML()})
	got, err := inst.loadAllConfigs(t.Context())
	if err != nil {
		t.Fatalf("loadAllConfigs: %v", err)
	}
	apps, _ := got["apps"].([]any)
	classified := ClassifyForEmbedded(apps)
	if len(classified.Gateway) != 1 {
		t.Fatalf("Gateway apps = %d, want 1", len(classified.Gateway))
	}

	cfg, err := entrypoint.ParseGatewayConfig(classified.Gateway[0].AppConfig)
	if err != nil {
		t.Fatalf("ParseGatewayConfig: %v", err)
	}
	if cfg.EventLogFlushInterval != time.Second {
		t.Errorf("EventLogFlushInterval = %v, want 1s", cfg.EventLogFlushInterval)
	}
}
