// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestParseWorkerConfig_LegacyToolSyncRejected(t *testing.T) {
	legacy := map[string]any{
		"namespace": "test-ns",
		"worker_id": "test-worker",
		"tool_sync": map[string]any{
			"tools_dir": "/opt/sam/tools",
		},
	}
	_, err := ParseWorkerConfig(config.FromMap(legacy))
	if err == nil {
		t.Fatal("expected error on legacy tool_sync block, got nil")
	}
	if !strings.Contains(err.Error(), "tool_sync") || !strings.Contains(err.Error(), "tools_dir") {
		t.Errorf("error message should name both the removed key and the replacement, got: %v", err)
	}
}

func TestParseWorkerConfig_ToolsetsDirAccepted(t *testing.T) {
	flat := map[string]any{
		"namespace":    "test-ns",
		"worker_id":    "test-worker",
		"toolsets_dir": "/opt/sam/toolsets",
	}
	wc, err := ParseWorkerConfig(config.FromMap(flat))
	if err != nil {
		t.Fatalf("ParseWorkerConfig toolsets_dir: %v", err)
	}
	if wc.ToolsetsDir != "/opt/sam/toolsets" {
		t.Fatalf("ToolsetsDir = %q, want /opt/sam/toolsets", wc.ToolsetsDir)
	}
}

func TestParseWorkerConfig_ToolModelAlias(t *testing.T) {
	base := map[string]any{"namespace": "test-ns", "worker_id": "test-worker"}

	tests := []struct {
		name string
		set  any
		want string
	}{
		{name: "unset defers to the worker default", set: nil, want: ""},
		{name: "explicit alias", set: "planning", want: "planning"},
		{name: "trimmed", set: "  planning  ", want: "planning"},
		{name: "blank is unset", set: "   ", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flat := map[string]any{}
			for k, v := range base {
				flat[k] = v
			}
			if tc.set != nil {
				flat["tool_model_alias"] = tc.set
			}
			wc, err := ParseWorkerConfig(config.FromMap(flat))
			if err != nil {
				t.Fatalf("ParseWorkerConfig: %v", err)
			}
			if wc.ToolModelAlias != tc.want {
				t.Errorf("ToolModelAlias = %q, want %q", wc.ToolModelAlias, tc.want)
			}
		})
	}
}

func TestParseWorkerConfig_EnvModelName(t *testing.T) {
	tests := map[string]struct {
		model any
		want  string
	}{
		"unset":                           {nil, ""},
		"string form drops the provider":  {"openai/gpt-4o", "gpt-4o"},
		"map form drops the provider":     {map[string]any{"model": "anthropic/claude", "api_base": "http://x"}, "claude"},
		"map with no model names nothing": {map[string]any{"api_base": "http://x"}, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			flat := map[string]any{"namespace": "test-ns", "worker_id": "test-worker"}
			if tc.model != nil {
				flat["model"] = tc.model
			}
			wc, err := ParseWorkerConfig(config.FromMap(flat))
			if err != nil {
				t.Fatalf("ParseWorkerConfig: %v", err)
			}
			if wc.EnvModelName != tc.want {
				t.Errorf("EnvModelName = %q, want %q", wc.EnvModelName, tc.want)
			}
		})
	}
}

func TestExtractAppConfigMap_ForwardsToolModelAlias(t *testing.T) {
	flat := map[string]any{
		"namespace":        "test-ns",
		"worker_id":        "test-worker",
		"tool_model_alias": "planning",
	}
	ac := ExtractAppConfigMap(config.FromMap(flat))
	wc, err := ParseWorkerConfig(config.FromMap(ac))
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.ToolModelAlias != "planning" {
		t.Errorf("ToolModelAlias = %q, want planning — the flat-config allowlist dropped the key", wc.ToolModelAlias)
	}
}

func TestParseWorkerConfig_ToolOutputCapDefault(t *testing.T) {
	flat := map[string]any{
		"namespace": "test-ns",
		"worker_id": "test-worker",
	}
	wc, err := ParseWorkerConfig(config.FromMap(flat))
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.ToolOutputLLMReturnMaxBytes != 102400 {
		t.Fatalf("ToolOutputLLMReturnMaxBytes default = %d, want 102400", wc.ToolOutputLLMReturnMaxBytes)
	}
}

func TestParseWorkerConfig_RedeliveryAndTTL(t *testing.T) {
	base := map[string]any{"namespace": "test-ns", "worker_id": "test-worker"}

	wc, err := ParseWorkerConfig(config.FromMap(base))
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.MaxMessageRedelivery != 1 {
		t.Errorf("MaxMessageRedelivery default = %d, want 1", wc.MaxMessageRedelivery)
	}
	if !wc.RespectMessageTTL {
		t.Error("RespectMessageTTL default = false, want true")
	}

	wc, err = ParseWorkerConfig(config.FromMap(map[string]any{
		"namespace":              "test-ns",
		"worker_id":              "test-worker",
		"max_message_redelivery": 5,
		"respect_message_ttl":    false,
	}))
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.MaxMessageRedelivery != 5 {
		t.Errorf("MaxMessageRedelivery = %d, want 5", wc.MaxMessageRedelivery)
	}
	if wc.RespectMessageTTL {
		t.Error("RespectMessageTTL = true, want false")
	}

	wc, err = ParseWorkerConfig(config.FromMap(map[string]any{
		"namespace":              "test-ns",
		"worker_id":              "test-worker",
		"max_message_redelivery": 0,
	}))
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.MaxMessageRedelivery != 0 {
		t.Errorf("MaxMessageRedelivery = %d, want 0 (explicit opt-out)", wc.MaxMessageRedelivery)
	}
}

func TestParseWorkerConfig_DeprecatedAliasesMigrate(t *testing.T) {
	for _, key := range []string{"tools_dir", "packages_dir"} {
		t.Run(key, func(t *testing.T) {
			cfg := map[string]any{
				"namespace": "test-ns",
				"worker_id": "test-worker",
				key:         "/opt/sam/toolsets",
			}
			wc, err := ParseWorkerConfig(config.FromMap(cfg))
			if err != nil {
				t.Fatalf("ParseWorkerConfig %s: %v", key, err)
			}
			if wc.ToolsetsDir != "/opt/sam/toolsets" {
				t.Fatalf("%s: ToolsetsDir = %q, want /opt/sam/toolsets", key, wc.ToolsetsDir)
			}
		})
	}
}

func TestParseWorkerConfig_ConflictingDirsRejected(t *testing.T) {
	cases := map[string]map[string]any{
		"tools_dir+packages_dir": {
			"tools_dir":    "/opt/sam/tools",
			"packages_dir": "/opt/sam/toolsets",
		},
		"toolsets_dir+tools_dir": {
			"toolsets_dir": "/opt/sam/toolsets",
			"tools_dir":    "/opt/sam/tools",
		},
		"toolsets_dir+packages_dir": {
			"toolsets_dir": "/opt/sam/toolsets",
			"packages_dir": "/opt/sam/other",
		},
	}
	for name, dirs := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := map[string]any{"namespace": "test-ns", "worker_id": "test-worker"}
			for k, v := range dirs {
				cfg[k] = v
			}
			_, err := ParseWorkerConfig(config.FromMap(cfg))
			if err == nil {
				t.Fatal("expected error on conflicting dir keys, got nil")
			}
			if !strings.Contains(err.Error(), "toolsets_dir") {
				t.Errorf("error should name the canonical 'toolsets_dir' replacement, got: %v", err)
			}
		})
	}
}

func TestHandleToolSyncEvent_NilPackageProcessorLogsError(t *testing.T) {
	buf := &syncBuf{}
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)
	w.logger = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if w.packageProcessor != nil {
		t.Fatalf("precondition: with empty ToolsDir, packageProcessor must be nil")
	}

	mirror := &recordingMirror{}
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"my_pkg"}`)},
	); err != nil {
		t.Fatalf("publish sync notify: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mirror.fetches.Load() > 0 && strings.Contains(buf.String(), "packageProcessor is nil") {
			out := buf.String()
			if !strings.Contains(out, "reason=") {
				t.Fatalf("expected log line to carry payload reason, got:\n%s", out)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected ERROR-level log mentioning 'packageProcessor is nil' after sync notification; got logs:\n%s", buf.String())
}

func TestParseReconcileInterval_EnvVarWins(t *testing.T) {
	t.Setenv("STR_RECONCILE_INTERVAL", "3m")
	got := parseReconcileInterval("10m")
	if got != 3*time.Minute {
		t.Errorf("parseReconcileInterval with env=3m cfgVal=10m = %v, want 3m", got)
	}
}

func TestParseReconcileInterval_CfgValUsedWhenNoEnv(t *testing.T) {
	if err := os.Unsetenv("STR_RECONCILE_INTERVAL"); err != nil {
		t.Fatal(err)
	}
	got := parseReconcileInterval("7m")
	if got != 7*time.Minute {
		t.Errorf("parseReconcileInterval with no env cfgVal=7m = %v, want 7m", got)
	}
}

func TestParseReconcileInterval_DefaultWhenBothEmpty(t *testing.T) {
	if err := os.Unsetenv("STR_RECONCILE_INTERVAL"); err != nil {
		t.Fatal(err)
	}
	got := parseReconcileInterval("")
	if got != defaultReconcileInterval {
		t.Errorf("parseReconcileInterval with no env no cfgVal = %v, want %v", got, defaultReconcileInterval)
	}
}

func TestParseReconcileInterval_InvalidEnvFallsThrough(t *testing.T) {
	t.Setenv("STR_RECONCILE_INTERVAL", "not-a-duration")
	got := parseReconcileInterval("2m")
	if got != 2*time.Minute {
		t.Errorf("parseReconcileInterval with invalid env cfgVal=2m = %v, want 2m", got)
	}
}

func TestParseWorkerConfig_MemoryGuard(t *testing.T) {
	parse := func(guard map[string]any) (*WorkerConfig, error) {
		return ParseWorkerConfig(config.FromMap(map[string]any{
			"namespace": "test-ns",
			"worker_id": "test-worker",
			"sandbox":   map[string]any{"memory_guard": guard},
		}))
	}

	wc, err := parse(map[string]any{"soft_percent": 70, "hard_percent": 90, "disabled": true})
	if err != nil {
		t.Fatalf("ParseWorkerConfig: %v", err)
	}
	if wc.Sandbox.MemorySoftPercent != 70 || wc.Sandbox.MemoryHardPercent != 90 || !wc.Sandbox.MemoryGuardDisabled {
		t.Fatalf("memory guard = %d/%d disabled=%t, want 70/90 disabled=true",
			wc.Sandbox.MemorySoftPercent, wc.Sandbox.MemoryHardPercent, wc.Sandbox.MemoryGuardDisabled)
	}

	for name, guard := range map[string]map[string]any{
		"soft above the default hard": {"soft_percent": 96},
		"soft above hard":             {"soft_percent": 90, "hard_percent": 80},
		"hard above 100":              {"hard_percent": 120},
		"negative soft":               {"soft_percent": -1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(guard); err == nil {
				t.Fatal("thresholds that turn the guard off must be rejected")
			}
		})
	}
	if _, err := parse(map[string]any{"soft_percent": 96, "disabled": true}); err != nil {
		t.Fatalf("a disabled guard must not be checked: %v", err)
	}
}
