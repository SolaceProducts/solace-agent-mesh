// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestNew_InitializesRunnerWithDefaults(t *testing.T) {
	t.Parallel()
	r := New(Options{ConfigFiles: []string{"a.yaml"}, SystemEnv: true})
	if r == nil {
		t.Fatal("New returned nil")
	}
	if r.logger == nil {
		t.Error("logger unset; expected slog.Default() fallback")
	}
	if r.UnexpectedExit() == nil {
		t.Error("UnexpectedExit() returned nil channel")
	}
	select {
	case <-r.UnexpectedExit():
		t.Error("unexpectedExitCh is closed on construction")
	default:
	}
}

func TestWatchProcess_NonExitErrorReportsMinusOne(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mp := &ManagedProcess{
		Label:   "synthetic",
		logger:  logger,
		doneCh:  make(chan struct{}),
		waitErr: errors.New("synthetic wait error (not exec.ExitError)"),
	}
	close(mp.doneCh)

	r := &SAMRunner{logger: logger, unexpectedExitCh: make(chan struct{})}

	var wg sync.WaitGroup
	wg.Go(func() { r.watchProcess(mp) })
	wg.Wait()

	select {
	case <-r.UnexpectedExit():
	case <-time.After(time.Second):
		t.Fatal("unexpectedExitCh was not closed")
	}

	if !strings.Contains(buf.String(), "exitCode=-1") {
		t.Errorf("expected 'exitCode=-1' for non-*exec.ExitError, got: %s", buf.String())
	}
}

func TestCfgToMap_ForwardsProvidersCatalog(t *testing.T) {
	t.Parallel()

	cfg := config.FromMap(map[string]any{
		"apps": []any{map[string]any{"name": "gateway"}},
		"providers": map[string]any{
			"keycloak": map[string]any{
				"issuer":        "https://idp.example/realms/sam",
				"client_id":     "sam",
				"client_secret": "shh",
			},
		},
	})

	got := cfgToMap(cfg)
	if got["providers"] == nil {
		t.Fatal("providers catalog was dropped by cfgToMap; subprocess would never see it")
	}
	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers: want map[string]any, got %T", got["providers"])
	}
	if _, ok := providers["keycloak"]; !ok {
		t.Error("keycloak entry missing from forwarded providers catalog")
	}
}

func TestConfigPipeline_PreservesProvidersCatalog(t *testing.T) {
	t.Parallel()

	yamlBody := []byte(`providers:
  keycloak:
    issuer: https://idp.example/realms/sam
    client_id: sam
    client_secret: shh

apps:
  - name: gateway
    app_exec: ./bin/sam-gateway-enterprise
`)
	tmp, err := os.CreateTemp(t.TempDir(), "runner-providers-*.yaml")
	if err != nil {
		t.Fatalf("create temp yaml: %v", err)
	}
	if _, writeErr := tmp.Write(yamlBody); writeErr != nil {
		t.Fatalf("write temp yaml: %v", writeErr)
	}
	_ = tmp.Close()

	loader := &config.YAMLLoader{}
	cfg, err := loader.Load(t.Context(), tmp.Name())
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	raw := cfg.GetRaw("")
	if raw == nil {
		raw = cfgToMap(cfg)
	}
	fullConfig, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw config: want map[string]any, got %T", raw)
	}

	extra := extractExtraSections(fullConfig)
	if extra == nil {
		t.Fatal("extractExtraSections returned nil; providers catalog would never reach subprocess")
	}
	if _, ok := extra["providers"]; !ok {
		t.Fatalf("providers catalog dropped somewhere in the load pipeline; extra keys=%v", mapKeys(extra))
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestSAMRunner_StartWithCrashingExecClosesUnexpectedExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not available: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	t.Setenv("DEV_BROKER_PORT", strconv.Itoa(port))

	dir := t.TempDir()
	script := filepath.Join(dir, "crashing-stub.sh")
	body := "#!/bin/sh\nprintf 'crashing stub\\n' 1>&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfgPath := filepath.Join(dir, "agent.yaml")
	cfg := "apps:\n  - name: crash-app\n    app_exec: " + script + "\n    app_config:\n      noop: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := New(Options{
		ConfigFiles: []string{cfgPath},
		SystemEnv:   true,
		Logger:      logger,
	})

	if err := r.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v\nlogs:\n%s", err, logBuf.String())
	}
	t.Cleanup(r.Stop)

	select {
	case <-r.UnexpectedExit():
	case <-time.After(10 * time.Second):
		t.Fatalf("UnexpectedExit() did not fire within 10s; logs:\n%s", logBuf.String())
	}

	logs := logBuf.String()
	for _, want := range []string{
		"subprocess exited unexpectedly",
		"label=crashing-stub.sh",
		"exitCode=1",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %q in logs:\n%s", want, logs)
		}
	}
}

func TestResolveHealthPort(t *testing.T) {
	tests := []struct {
		name     string
		label    string
		envVar   string
		envValue string
		wantPort string
		wantOK   bool
	}{
		{
			name:     "default for awe-enterprise",
			label:    "sam-awe-enterprise",
			wantPort: ":8090",
			wantOK:   true,
		},
		{
			name:     "default for str-enterprise (different from awe to avoid collision)",
			label:    "sam-str-enterprise",
			wantPort: ":8092",
			wantOK:   true,
		},
		{
			name:     "default for gateway",
			label:    "sam-gateway",
			wantPort: ":9090",
			wantOK:   true,
		},
		{
			name:     "default for platform-enterprise",
			label:    "sam-platform-enterprise",
			wantPort: ":9091",
			wantOK:   true,
		},
		{
			name:     "env override wins for str-enterprise",
			label:    "sam-str-enterprise",
			envVar:   "SAM_STR_ENTERPRISE_HEALTH_ADDR",
			envValue: ":18092",
			wantPort: ":18092",
			wantOK:   true,
		},
		{
			name:     "env override for community awe",
			label:    "sam-awe",
			envVar:   "SAM_AWE_HEALTH_ADDR",
			envValue: ":18090",
			wantPort: ":18090",
			wantOK:   true,
		},
		{
			name:     "env override targeting different role does not leak",
			label:    "sam-awe-enterprise",
			envVar:   "SAM_STR_ENTERPRISE_HEALTH_ADDR",
			envValue: ":18092",
			wantPort: ":8090",
			wantOK:   true,
		},
		{
			name:   "unknown binary returns false (no flag injected)",
			label:  "sam-unknown-tool",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envValue)
			}
			got, ok := resolveHealthPort(tt.label)
			if ok != tt.wantOK {
				t.Fatalf("resolveHealthPort(%q) ok = %v, want %v", tt.label, ok, tt.wantOK)
			}
			if ok && got != tt.wantPort {
				t.Errorf("resolveHealthPort(%q) = %q, want %q", tt.label, got, tt.wantPort)
			}
		})
	}
}

func TestBuildChildEnv_StripsHealthAddr(t *testing.T) {
	t.Setenv("HEALTH_ADDR", ":9999")
	t.Setenv("SAM_STR_ENTERPRISE_HEALTH_ADDR", ":18092")
	t.Setenv("SAM_AWE_HEALTH_ADDR", ":18090")
	t.Setenv("KEEP_THIS_VAR", "kept")
	t.Setenv("SAM_DATA_DIR", "/tmp/sam-data")

	env := BuildChildEnv("127.0.0.1", 55554)

	for _, prefix := range []string{
		"HEALTH_ADDR=",
		"SAM_STR_ENTERPRISE_HEALTH_ADDR=",
		"SAM_AWE_HEALTH_ADDR=",
	} {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				t.Errorf("%s leaked into child env: %q", prefix, e)
			}
		}
	}

	kept := map[string]bool{
		"KEEP_THIS_VAR=kept":         false,
		"SAM_DATA_DIR=/tmp/sam-data": false,
	}
	for _, e := range env {
		if _, want := kept[e]; want {
			kept[e] = true
		}
	}
	for k, ok := range kept {
		if !ok {
			t.Errorf("BuildChildEnv stripped an unrelated env var: %q", k)
		}
	}
}

func TestBuildChildEnv_PreservesSolaceBrokerVars(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcps://broker.example.com:55443")
	t.Setenv("SOLACE_BROKER_USERNAME", "alice")
	t.Setenv("SOLACE_BROKER_PASSWORD", "secret")
	t.Setenv("SOLACE_BROKER_VPN", "myvpn")

	env := BuildChildEnv("127.0.0.1", 55554)

	want := map[string]bool{
		"SOLACE_BROKER_URL=tcps://broker.example.com:55443": false,
		"SOLACE_BROKER_USERNAME=alice":                      false,
		"SOLACE_BROKER_PASSWORD=secret":                     false,
		"SOLACE_BROKER_VPN=myvpn":                           false,
	}
	for _, e := range env {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("BuildChildEnv must preserve %q for the platform runtime-add Default broker path", k)
		}
	}
}

func TestInjectAppModuleIntoGoADKApps(t *testing.T) {
	brokerCfg := map[string]any{
		"broker_url": "tcp://localhost:55555",
		"broker_vpn": "default",
	}

	apps := []any{
		"not-a-map",
		map[string]any{
			"name":       "agent-1",
			"app_config": map[string]any{"agent_name": "Agent1"},
		},
		map[string]any{
			"name":       "python-app",
			"app_module": "solace_agent_mesh.x",
			"app_config": map[string]any{"k": "v"},
		},
		map[string]any{
			"name":  "flow-app",
			"flows": []any{},
		},
	}

	injectAppModuleIntoGoADKApps(apps, brokerCfg)

	goApp := apps[1].(map[string]any)
	if goApp["app_module"] != "solace_agent_mesh.agent.sac.app" {
		t.Errorf("Go ADK app missing app_module: %v", goApp["app_module"])
	}
	gotBroker, _ := goApp["broker"].(map[string]any)
	if gotBroker == nil || gotBroker["broker_url"] != "tcp://localhost:55555" {
		t.Errorf("Go ADK app broker = %v, want injected", goApp["broker"])
	}

	pyApp := apps[2].(map[string]any)
	if pyApp["app_module"] != "solace_agent_mesh.x" {
		t.Errorf("Python app's app_module was overwritten: %v", pyApp["app_module"])
	}

	flowApp := apps[3].(map[string]any)
	if _, has := flowApp["app_module"]; has {
		t.Error("flow-only app must not have app_module injected")
	}
}

func TestInjectAppModuleIntoGoADKApps_NilBrokerSkipsBrokerInject(t *testing.T) {
	apps := []any{
		map[string]any{
			"name":       "agent-only",
			"app_config": map[string]any{"k": "v"},
		},
	}
	injectAppModuleIntoGoADKApps(apps, nil)

	got := apps[0].(map[string]any)
	if got["app_module"] != "solace_agent_mesh.agent.sac.app" {
		t.Errorf("app_module = %v, want injected", got["app_module"])
	}
	if _, has := got["broker"]; has {
		t.Error("broker must NOT be set when brokerCfg is nil")
	}
}

func TestExtractBrokerConfig_FromSharedConfigAnchor(t *testing.T) {
	full := map[string]any{
		"shared_config": []any{
			"not-a-map",
			map[string]any{"other": "value"},
			map[string]any{
				"broker_connection": map[string]any{
					"broker_url": "tcps://anchor.example.com:55443",
					"broker_vpn": "anchor-vpn",
				},
			},
		},
	}
	got := extractBrokerConfig(full)
	if got == nil {
		t.Fatal("extractBrokerConfig returned nil; want anchor-derived map")
	}
	if got["broker_url"] != "tcps://anchor.example.com:55443" {
		t.Errorf("broker_url = %v, want anchor value", got["broker_url"])
	}
}

func TestExtractBrokerConfig_FallbackFromEnv(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcp://env.example.com:55555")
	t.Setenv("SOLACE_BROKER_VPN", "env-vpn")
	t.Setenv("SOLACE_BROKER_USERNAME", "env-user")
	t.Setenv("SOLACE_BROKER_PASSWORD", "env-pass")
	t.Setenv("SOLACE_DEV_MODE", "true")

	got := extractBrokerConfig(map[string]any{})
	if got == nil {
		t.Fatal("extractBrokerConfig returned nil; want env-derived map")
	}
	if got["broker_url"] != "tcp://env.example.com:55555" {
		t.Errorf("broker_url = %v", got["broker_url"])
	}
	if got["broker_vpn"] != "env-vpn" {
		t.Errorf("broker_vpn = %v", got["broker_vpn"])
	}
	if got["broker_username"] != "env-user" || got["broker_password"] != "env-pass" {
		t.Errorf("user/pass = %v/%v", got["broker_username"], got["broker_password"])
	}
	if got["dev_mode"] != true {
		t.Errorf("dev_mode = %v, want true", got["dev_mode"])
	}
}

func TestExtractBrokerConfig_NoEnvAndNoAnchorReturnsNil(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "")
	if got := extractBrokerConfig(map[string]any{}); got != nil {
		t.Errorf("extractBrokerConfig = %v, want nil", got)
	}
}
