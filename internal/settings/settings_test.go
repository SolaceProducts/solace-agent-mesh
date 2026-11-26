// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadResolutionOrder(t *testing.T) {
	skipGlobalDir = true
	defer func() { skipGlobalDir = false }()

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	s, path, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s != nil {
		t.Fatal("expected nil settings when no files exist")
	}
	if path != "" {
		t.Fatalf("expected empty path, got %q", path)
	}

	projectDir := filepath.Join(tmpDir, ".sam")
	if mkErr := os.MkdirAll(projectDir, 0755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	if wErr := os.WriteFile(filepath.Join(projectDir, "settings.yaml"), []byte(`
llm:
  provider: openai
  api_key: sk-test123
  default_model: gpt-4o
`), 0600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}

	s, _, err = Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings from project .sam/settings.yaml")
	}
	if s.LLM.Provider != "openai" {
		t.Errorf("expected provider openai, got %q", s.LLM.Provider)
	}
	if s.LLM.APIKey != "sk-test123" {
		t.Errorf("expected api_key sk-test123, got %q", s.LLM.APIKey)
	}
	if s.LLM.DefaultModel != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %q", s.LLM.DefaultModel)
	}
}

func TestLoadGlobal_IgnoresProjectDotSam(t *testing.T) {
	home := t.TempDir()
	origHome := HomeOverride
	HomeOverride = home
	defer func() { HomeOverride = origHome }()
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), []byte(`
llm:
  provider: globalprovider
  default_model: gpt-4o
`), 0600); err != nil {
		t.Fatalf("write home settings: %v", err)
	}

	project := t.TempDir()
	t.Chdir(project)
	if err := os.MkdirAll(filepath.Join(project, ".sam"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".sam", "settings.yaml"), []byte(`
llm:
  provider: projectprovider
  default_model: gpt-4o
`), 0600); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	if s, _, err := Load(); err != nil || s == nil || s.LLM.Provider != "projectprovider" {
		t.Fatalf("Load() should read project ./.sam; got %+v err=%v", s, err)
	}

	s, path, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal() error: %v", err)
	}
	if s == nil || s.LLM.Provider != "globalprovider" {
		t.Fatalf("LoadGlobal() should read <SAM home>; got %+v", s)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("LoadGlobal() path must be absolute, got %q", path)
	}
	if want := filepath.Join(home, "settings.yaml"); path != want {
		t.Errorf("LoadGlobal() path = %q, want %q", path, want)
	}
}

func TestLoadJSON(t *testing.T) {
	skipGlobalDir = true
	defer func() { skipGlobalDir = false }()

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	projectDir := filepath.Join(tmpDir, ".sam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.json"), []byte(`{
		"llm": {
			"provider": "anthropic",
			"api_key_env": "ANTHROPIC_API_KEY",
			"default_model": "claude-sonnet-4-20250514"
		}
	}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings from JSON")
	}
	if s.LLM.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %q", s.LLM.Provider)
	}
	if s.LLM.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("expected api_key_env ANTHROPIC_API_KEY, got %q", s.LLM.APIKeyEnv)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, ".sam")

	s := &Settings{
		LLM: LLMSettings{
			Provider:     "openai",
			APIKeyEnv:    "OPENAI_API_KEY",
			BaseURL:      "https://my-proxy.example.com",
			DefaultModel: "gpt-4o",
		},
	}

	if err := Save(s, saveDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	info, err := os.Stat(filepath.Join(saveDir, "settings.yaml"))
	if err != nil {
		t.Fatalf("settings.yaml not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected permissions 0600, got %o", perm)
	}

	if _, statErr := os.Stat(filepath.Join(saveDir, ".gitignore")); statErr != nil {
		t.Error("expected .gitignore to be created")
	}

	loaded, err := loadFile(filepath.Join(saveDir, "settings.yaml"))
	if err != nil {
		t.Fatalf("loadFile() error: %v", err)
	}
	if loaded.LLM.Provider != "openai" {
		t.Errorf("roundtrip: expected provider openai, got %q", loaded.LLM.Provider)
	}
	if loaded.LLM.BaseURL != "https://my-proxy.example.com" {
		t.Errorf("roundtrip: expected base URL, got %q", loaded.LLM.BaseURL)
	}
}

func TestResolveAPIKey(t *testing.T) {
	s := &Settings{LLM: LLMSettings{APIKeyEnv: "TEST_SAM_KEY_12345"}}

	if got := ResolveAPIKey(s); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	t.Setenv("TEST_SAM_KEY_12345", "my-secret")

	if got := ResolveAPIKey(s); got != "my-secret" {
		t.Errorf("expected my-secret, got %q", got)
	}

	s2 := &Settings{LLM: LLMSettings{APIKey: "literal-key"}}
	if got := ResolveAPIKey(s2); got != "literal-key" {
		t.Errorf("expected literal-key, got %q", got)
	}
}

func TestInjectIntoEnv(t *testing.T) {
	s := &Settings{
		LLM: LLMSettings{
			Provider:     "anthropic",
			APIKey:       "sk-ant-test",
			BaseURL:      "https://custom.api.com",
			DefaultModel: "claude-sonnet-4-20250514",
		},
	}

	tmpDir := t.TempDir()
	settingsDir := filepath.Join(tmpDir, ".sam")

	t.Setenv("LLM_SERVICE_API_KEY", "")
	t.Setenv("LLM_SERVICE_ENDPOINT", "")
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", "")
	t.Setenv("SAM_DATA_DIR", "")
	t.Setenv("SAM_SESSION_SECRET", "")

	InjectIntoEnv(s, settingsDir)

	if got := os.Getenv("LLM_SERVICE_API_KEY"); got != "sk-ant-test" {
		t.Errorf("expected sk-ant-test, got %q", got)
	}
	if got := os.Getenv("LLM_SERVICE_ENDPOINT"); got != "https://custom.api.com" {
		t.Errorf("expected https://custom.api.com, got %q", got)
	}
	if got := os.Getenv("LLM_SERVICE_PLANNING_MODEL_NAME"); got != "anthropic/claude-sonnet-4-20250514" {
		t.Errorf("expected anthropic/claude-sonnet-4-20250514, got %q", got)
	}
	if got := os.Getenv("SAM_DATA_DIR"); got == "" {
		t.Error("expected SAM_DATA_DIR to be set")
	}
	if got := os.Getenv("SAM_SESSION_SECRET"); got == "" {
		t.Error("expected SAM_SESSION_SECRET to be generated")
	}

	dataDir := os.Getenv("SAM_DATA_DIR")
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "artifacts")); err != nil {
		t.Errorf("artifacts dir not created: %v", err)
	}
}

func TestLoadBackwardsCompat_LegacyLLMFile_TreatedAsOnboarded(t *testing.T) {
	skipGlobalDir = true
	defer func() { skipGlobalDir = false }()

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	projectDir := filepath.Join(tmpDir, ".sam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"), []byte(`
llm:
  provider: openai
  api_key: sk-test
  default_model: gpt-4o
`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings to load")
	}
	if !s.Onboarded {
		t.Error("legacy settings with populated LLM should load as Onboarded=true")
	}
}

func TestLoadBackwardsCompat_LegacyEmptyLLM_NotOnboarded(t *testing.T) {
	skipGlobalDir = true
	defer func() { skipGlobalDir = false }()

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	projectDir := filepath.Join(tmpDir, ".sam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"), []byte("llm: {}\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings to load")
	}
	if s.Onboarded {
		t.Error("legacy settings with empty LLM should load as Onboarded=false (retrigger wizard)")
	}
}

func TestLoadSkipPath_EmptyLLMOnboardedTrue(t *testing.T) {
	skipGlobalDir = true
	defer func() { skipGlobalDir = false }()

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	projectDir := filepath.Join(tmpDir, ".sam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.yaml"), []byte("onboarded: true\nllm: {}\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings to load")
	}
	if !s.Onboarded {
		t.Error("skip-path file should load as Onboarded=true")
	}
	if s.LLM.Provider != "" || s.LLM.DefaultModel != "" {
		t.Errorf("expected empty LLM, got %+v", s.LLM)
	}
}

func TestOnboardedRoundtripYAML(t *testing.T) {
	in := &Settings{Onboarded: true, LLM: LLMSettings{Provider: "openai", DefaultModel: "gpt-4o"}}
	b, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(b), "onboarded: true") {
		t.Errorf("expected `onboarded: true` in YAML, got:\n%s", string(b))
	}
	var out Settings
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if !out.Onboarded {
		t.Error("Onboarded did not round-trip through YAML")
	}
}

func TestOnboardedRoundtripJSON(t *testing.T) {
	in := &Settings{Onboarded: true, LLM: LLMSettings{Provider: "openai", DefaultModel: "gpt-4o"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"onboarded":true`) {
		t.Errorf("expected `\"onboarded\":true` in JSON, got: %s", string(b))
	}
	var out Settings
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !out.Onboarded {
		t.Error("Onboarded did not round-trip through JSON")
	}
}

func TestShouldShowOnboardWizard(t *testing.T) {
	cases := []struct {
		name string
		in   *Settings
		want bool
	}{
		{"nil settings → wizard", nil, true},
		{"loaded but not onboarded → wizard", &Settings{}, true},
		{"explicit not onboarded with empty LLM → wizard", &Settings{Onboarded: false, LLM: LLMSettings{}}, true},
		{"onboarded with empty LLM (skip path) → main app", &Settings{Onboarded: true}, false},
		{"onboarded with model → main app", &Settings{Onboarded: true, LLM: LLMSettings{Provider: "openai", DefaultModel: "gpt-4o"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldShowOnboardWizard(tc.in); got != tc.want {
				t.Errorf("ShouldShowOnboardWizard = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInjectIntoEnv_EmptyLLM_LeavesModelEnvUnset(t *testing.T) {
	s := &Settings{Onboarded: true, LLM: LLMSettings{}}

	const sentinel = "openai/sentinel"
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", sentinel)
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", sentinel)

	InjectIntoEnv(s, t.TempDir())

	if got := os.Getenv("LLM_SERVICE_PLANNING_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_PLANNING_MODEL_NAME = %q, want %q (must not be overwritten when LLM is empty)", got, sentinel)
	}
	if got := os.Getenv("LLM_SERVICE_GENERAL_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_GENERAL_MODEL_NAME = %q, want %q (must not be overwritten when LLM is empty)", got, sentinel)
	}
}

func TestInjectIntoEnv_ProviderWithoutModel_LeavesModelEnvUnset(t *testing.T) {
	s := &Settings{Onboarded: true, LLM: LLMSettings{Provider: "anthropic"}}

	const sentinel = "openai/sentinel"
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", sentinel)
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", sentinel)

	InjectIntoEnv(s, t.TempDir())

	if got := os.Getenv("LLM_SERVICE_PLANNING_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_PLANNING_MODEL_NAME = %q, want %q", got, sentinel)
	}
	if got := os.Getenv("LLM_SERVICE_GENERAL_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_GENERAL_MODEL_NAME = %q, want %q", got, sentinel)
	}
}

func TestInjectIntoEnv_BlankModel_LeavesModelEnvUnset(t *testing.T) {
	s := &Settings{Onboarded: true, LLM: LLMSettings{Provider: "anthropic", DefaultModel: " "}}

	const sentinel = "openai/sentinel"
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", sentinel)
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", sentinel)

	InjectIntoEnv(s, t.TempDir())

	if got := os.Getenv("LLM_SERVICE_PLANNING_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_PLANNING_MODEL_NAME = %q, want %q (a blank default_model must not emit %q)", got, sentinel, "anthropic/ ")
	}
	if got := os.Getenv("LLM_SERVICE_GENERAL_MODEL_NAME"); got != sentinel {
		t.Errorf("LLM_SERVICE_GENERAL_MODEL_NAME = %q, want %q (a blank default_model must not emit %q)", got, sentinel, "anthropic/ ")
	}
}

func TestResolveHomeDir(t *testing.T) {
	orig := HomeOverride
	t.Cleanup(func() { HomeOverride = orig })

	t.Run("default_is_xdg_config_sam", func(t *testing.T) {
		HomeOverride = ""
		t.Setenv("SAM_HOME", "")
		got, err := ResolveHomeDir()
		if err != nil {
			t.Fatalf("ResolveHomeDir: %v", err)
		}
		configDir, _ := os.UserConfigDir()
		want := filepath.Join(configDir, "sam")
		if got != want {
			t.Errorf("ResolveHomeDir = %q, want %q", got, want)
		}
	})

	t.Run("env_var_overrides_default", func(t *testing.T) {
		HomeOverride = ""
		t.Setenv("SAM_HOME", "/tmp/sam-env")
		got, err := ResolveHomeDir()
		if err != nil {
			t.Fatalf("ResolveHomeDir: %v", err)
		}
		if got != "/tmp/sam-env" {
			t.Errorf("ResolveHomeDir = %q, want /tmp/sam-env", got)
		}
	})

	t.Run("flag_overrides_env_and_default", func(t *testing.T) {
		HomeOverride = "/tmp/sam-flag"
		t.Setenv("SAM_HOME", "/tmp/sam-env")
		got, err := ResolveHomeDir()
		if err != nil {
			t.Fatalf("ResolveHomeDir: %v", err)
		}
		if got != "/tmp/sam-flag" {
			t.Errorf("ResolveHomeDir = %q, want /tmp/sam-flag", got)
		}
	})

	t.Run("default_global_dir_matches_resolve", func(t *testing.T) {
		HomeOverride = "/tmp/sam-flag2"
		t.Setenv("SAM_HOME", "")
		dg, err := DefaultGlobalDir()
		if err != nil {
			t.Fatalf("DefaultGlobalDir: %v", err)
		}
		if dg != "/tmp/sam-flag2" {
			t.Errorf("DefaultGlobalDir = %q, want /tmp/sam-flag2", dg)
		}
	})
}

func TestLoadHonoursHomeOverride(t *testing.T) {
	orig := HomeOverride
	t.Cleanup(func() { HomeOverride = orig })

	tmpHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpHome, "settings.yaml"), []byte(`
onboarded: true
llm:
  provider: openai
  default_model: gpt-4o
`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)

	HomeOverride = tmpHome
	t.Setenv("SAM_HOME", "")
	s, path, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s == nil {
		t.Fatal("expected settings to load from SAM_HOME override")
	}
	if !s.Onboarded {
		t.Error("expected Onboarded=true")
	}
	wantPath := filepath.Join(tmpHome, "settings.yaml")
	if path != wantPath {
		t.Errorf("Load path = %q, want %q", path, wantPath)
	}
}

func TestEffectiveSandboxMode(t *testing.T) {
	tests := []struct {
		name string
		mode SandboxMode
		want SandboxMode
	}{
		{"default empty", "", SandboxModeBuiltin},
		{"explicit builtin", SandboxModeBuiltin, SandboxModeBuiltin},
		{"container", SandboxModeContainer, SandboxModeContainer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Settings{Sandbox: SandboxSettings{Mode: tt.mode}}
			if got := s.EffectiveSandboxMode(); got != tt.want {
				t.Errorf("EffectiveSandboxMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSandboxSettingsRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	saveDir := filepath.Join(tmpDir, ".sam")

	s := &Settings{
		LLM: LLMSettings{
			Provider:     "openai",
			APIKey:       "test",
			DefaultModel: "gpt-4o",
		},
		Sandbox: SandboxSettings{
			Mode:             SandboxModeContainer,
			ContainerRuntime: "podman",
		},
	}

	if err := Save(s, saveDir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := loadFile(filepath.Join(saveDir, "settings.yaml"))
	if err != nil {
		t.Fatalf("loadFile() error: %v", err)
	}
	if loaded.Sandbox.Mode != SandboxModeContainer {
		t.Errorf("expected sandbox mode 'container', got %q", loaded.Sandbox.Mode)
	}
	if loaded.Sandbox.ContainerRuntime != "podman" {
		t.Errorf("expected container_runtime 'podman', got %q", loaded.Sandbox.ContainerRuntime)
	}
}

func TestInjectIntoEnv_NoBaseURL_LeavesEndpointUnset(t *testing.T) {
	s := &Settings{
		LLM: LLMSettings{
			Provider:     "openai",
			APIKey:       "test",
			DefaultModel: "gpt-4o",
		},
	}

	t.Run("unset_env_stays_unset", func(t *testing.T) {
		t.Setenv("LLM_SERVICE_ENDPOINT", "")
		InjectIntoEnv(s, t.TempDir())
		if got := os.Getenv("LLM_SERVICE_ENDPOINT"); got != "" {
			t.Errorf("expected LLM_SERVICE_ENDPOINT to remain unset, got %q", got)
		}
	})

	t.Run("preset_env_is_preserved", func(t *testing.T) {
		const preset = "https://prior.example/v1"
		t.Setenv("LLM_SERVICE_ENDPOINT", preset)
		InjectIntoEnv(s, t.TempDir())
		if got := os.Getenv("LLM_SERVICE_ENDPOINT"); got != preset {
			t.Errorf("expected pre-set env to be preserved as %q, got %q", preset, got)
		}
	})
}

func TestInjectIntoEnv_BaseURLPropagated(t *testing.T) {
	const endpoint = "https://llm-proxy.example.com/v1"
	s := &Settings{
		LLM: LLMSettings{
			Provider:     "openai",
			APIKey:       "test",
			BaseURL:      endpoint,
			DefaultModel: "gpt-4o",
		},
	}

	t.Setenv("LLM_SERVICE_ENDPOINT", "")
	InjectIntoEnv(s, t.TempDir())

	if got := os.Getenv("LLM_SERVICE_ENDPOINT"); got != endpoint {
		t.Errorf("expected LLM_SERVICE_ENDPOINT=%q, got %q", endpoint, got)
	}
}

func TestInjectIntoEnv_PathsUseForwardSlashes(t *testing.T) {
	s := &Settings{LLM: LLMSettings{Provider: "openai", APIKey: "x", DefaultModel: "gpt-4o"}}

	t.Setenv("SAM_TOOLS_DIR", "")
	InjectIntoEnv(s, t.TempDir())

	for _, name := range []string{
		"SAM_DATA_DIR",
		"SAM_SKILLS_DIR",
		"SAM_SKILLS_LINUX_DIR",
		"SAM_TOOLS_DIR",
		"SAM_TOOLS_LINUX_DIR",
	} {
		v := os.Getenv(name)
		if v == "" {
			t.Errorf("%s not set", name)
			continue
		}
		if strings.Contains(v, `\`) {
			t.Errorf("%s = %q contains backslashes; must use forward slashes for YAML-safe substitution", name, v)
		}
	}
}

func TestInjectIntoEnv_HonoursExistingToolsDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: filepath.ToSlash normalises backslashes only on Windows")
	}
	s := &Settings{LLM: LLMSettings{Provider: "openai", APIKey: "x", DefaultModel: "gpt-4o"}}

	const installDir = `C:\Program Files\Solace\SAM Desktop\tools`
	t.Setenv("SAM_TOOLS_DIR", installDir)

	InjectIntoEnv(s, t.TempDir())

	got := os.Getenv("SAM_TOOLS_DIR")
	want := "C:/Program Files/Solace/SAM Desktop/tools"
	if got != want {
		t.Errorf("SAM_TOOLS_DIR = %q, want %q (existing value preserved, slashes normalised)", got, want)
	}
}

func TestInjectIntoEnv_DefaultsSolaceDevMode(t *testing.T) {
	s := &Settings{LLM: LLMSettings{Provider: "openai", APIKey: "x", DefaultModel: "gpt-4o"}}

	t.Run("defaults_to_true_when_unset", func(t *testing.T) {
		t.Setenv("SOLACE_DEV_MODE", "")
		InjectIntoEnv(s, t.TempDir())
		if got := os.Getenv("SOLACE_DEV_MODE"); got != "true" {
			t.Errorf("SOLACE_DEV_MODE = %q, want %q", got, "true")
		}
	})

	t.Run("respects_caller_set_value", func(t *testing.T) {
		t.Setenv("SOLACE_DEV_MODE", "false")
		InjectIntoEnv(s, t.TempDir())
		if got := os.Getenv("SOLACE_DEV_MODE"); got != "false" {
			t.Errorf("SOLACE_DEV_MODE = %q, want caller's %q to be preserved", got, "false")
		}
	})
}

func TestBundledResourceDirFromBinary(t *testing.T) {
	t.Run("macos_resources_layout", func(t *testing.T) {
		root := t.TempDir()
		binDir := filepath.Join(root, "Contents", "MacOS")
		toolsDir := filepath.Join(root, "Contents", "Resources", "tools")
		mustWriteFile(t, filepath.Join(binDir, ".keep"), "")
		mustWriteFile(t, filepath.Join(toolsDir, "pptx", "manifest.yaml"), "tools: {}")

		got := bundledResourceDirFromBinary(binDir, "tools")
		want, _ := filepath.Abs(toolsDir)
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("sibling_layout_skills", func(t *testing.T) {
		binDir := t.TempDir()
		skillsDir := filepath.Join(binDir, "skills")
		mustWriteFile(t, filepath.Join(skillsDir, "doc-convert", "SKILL.md"), "# doc-convert")

		got := bundledResourceDirFromBinary(binDir, "skills")
		want, _ := filepath.Abs(skillsDir)
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		binDir := t.TempDir()
		if got := bundledResourceDirFromBinary(binDir, "tools"); got != "" {
			t.Errorf("expected no match, got %q", got)
		}
	})

	t.Run("empty_dir_is_ignored", func(t *testing.T) {
		binDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(binDir, "tools"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := bundledResourceDirFromBinary(binDir, "tools"); got != "" {
			t.Errorf("expected empty dir to be ignored, got %q", got)
		}
	})

	t.Run("anemic_bundle_is_ignored", func(t *testing.T) {
		binDir := t.TempDir()
		mustWriteFile(t, filepath.Join(binDir, "skills", ".bin", "skill-tools"), "fake")
		if got := bundledResourceDirFromBinary(binDir, "skills"); got != "" {
			t.Errorf("expected anemic bundle (only .bin) to be ignored, got %q", got)
		}
	})

	t.Run("ds_store_does_not_qualify", func(t *testing.T) {
		binDir := t.TempDir()
		mustWriteFile(t, filepath.Join(binDir, "tools", ".DS_Store"), "")
		if got := bundledResourceDirFromBinary(binDir, "tools"); got != "" {
			t.Errorf("expected .DS_Store-only dir to be ignored, got %q", got)
		}
	})
}

func TestResolveResourceDir(t *testing.T) {
	t.Run("env_var_wins_over_bundle", func(t *testing.T) {
		t.Setenv("SAM_TOOLS_DIR", "/tmp/operator-override")
		got, fromBundle := resolveResourceDir("SAM_TOOLS_DIR", "tools", t.TempDir())
		if got != "/tmp/operator-override" {
			t.Errorf("got %q, want %q", got, "/tmp/operator-override")
		}
		if fromBundle {
			t.Error("fromBundle = true; want false for env-var hit")
		}
	})

	t.Run("falls_back_to_settings_dir_when_unset", func(t *testing.T) {
		t.Setenv("SAM_TOOLS_DIR", "")
		settingsDir := t.TempDir()
		got, fromBundle := resolveResourceDir("SAM_TOOLS_DIR", "tools", settingsDir)
		wantAbs, _ := filepath.Abs(filepath.Join(settingsDir, "tools"))
		if got != wantAbs {
			t.Errorf("got %q, want %q", got, wantAbs)
		}
		if fromBundle {
			t.Error("fromBundle = true; want false for settings-dir fallback")
		}
	})
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
