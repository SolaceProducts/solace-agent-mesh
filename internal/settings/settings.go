// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Settings struct {
	Onboarded bool            `yaml:"onboarded,omitempty" json:"onboarded,omitempty"`
	LLM       LLMSettings     `yaml:"llm" json:"llm"`
	Sandbox   SandboxSettings `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
}

type SandboxMode string

const (
	SandboxModeBuiltin   SandboxMode = "builtin"
	SandboxModeContainer SandboxMode = "container"
)

type SandboxSettings struct {
	Mode SandboxMode `yaml:"mode,omitempty" json:"mode,omitempty"`

	ContainerRuntime string `yaml:"container_runtime,omitempty" json:"container_runtime,omitempty"`
}

type LLMSettings struct {
	Provider     string `yaml:"provider" json:"provider"`
	APIKeyEnv    string `yaml:"api_key_env,omitempty" json:"api_key_env,omitempty"`
	APIKey       string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BaseURL      string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	DefaultModel string `yaml:"default_model" json:"default_model"`
}

var skipGlobalDir bool

const (
	settingsFileYAML = "settings.yaml"
	settingsFileJSON = "settings.json"
)

var HomeOverride string

func ResolveHomeDir() (string, error) {
	if HomeOverride != "" {
		return HomeOverride, nil
	}
	if v := os.Getenv("SAM_HOME"); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("get user config directory: %w", err)
	}
	return filepath.Join(dir, "sam"), nil
}

func Load() (*Settings, string, error) {
	candidates := []string{
		filepath.Join(".", ".sam", settingsFileYAML),
	}

	if !skipGlobalDir {
		homeDir, err := ResolveHomeDir()
		if err != nil {
			return nil, "", err
		}
		candidates = append(candidates, filepath.Join(homeDir, settingsFileYAML))
		candidates = append(candidates,
			filepath.Join(".", ".sam", settingsFileJSON),
			filepath.Join(homeDir, settingsFileJSON),
		)
	} else {
		candidates = append(candidates, filepath.Join(".", ".sam", settingsFileJSON))
	}

	for _, path := range candidates {
		s, err := loadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("loading %s: %w", path, err)
		}
		if s != nil {
			absPath, _ := filepath.Abs(path)
			return s, absPath, nil
		}
	}

	return nil, "", nil
}

func LoadGlobal() (*Settings, string, error) {
	homeDir, err := ResolveHomeDir()
	if err != nil {
		return nil, "", err
	}
	candidates := []string{
		filepath.Join(homeDir, settingsFileYAML),
		filepath.Join(homeDir, settingsFileJSON),
	}
	for _, path := range candidates {
		s, err := loadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("loading %s: %w", path, err)
		}
		if s != nil {
			absPath, _ := filepath.Abs(path)
			return s, absPath, nil
		}
	}
	return nil, "", nil
}

func loadFile(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	s := &Settings{}
	if strings.HasSuffix(path, ".json") {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
	}
	if !s.Onboarded && (s.LLM.Provider != "" || s.LLM.DefaultModel != "") {
		s.Onboarded = true
	}
	return s, nil
}

func Save(s *Settings, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	header := "# SAM settings\n# See https://solace.dev/sam/settings for options\n"
	content := []byte(header + string(data))

	path := filepath.Join(dir, settingsFileYAML)
	if err := os.WriteFile(path, content, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	ensureGitignore(dir)

	return nil
}

func DefaultGlobalDir() (string, error) {
	return ResolveHomeDir()
}

func DefaultProjectDir() string {
	return filepath.Join(".", ".sam")
}

func ResolveAPIKey(s *Settings) string {
	if s.LLM.APIKeyEnv != "" {
		if v := os.Getenv(s.LLM.APIKeyEnv); v != "" {
			return v
		}
	}
	return s.LLM.APIKey
}

func InjectIntoEnv(s *Settings, settingsDir string) {
	key := ResolveAPIKey(s)
	if key != "" {
		_ = os.Setenv("LLM_SERVICE_API_KEY", key)
	}

	if s.LLM.BaseURL != "" {
		_ = os.Setenv("LLM_SERVICE_ENDPOINT", s.LLM.BaseURL)
	}

	if strings.TrimSpace(s.LLM.DefaultModel) != "" {
		modelStr := s.LLM.DefaultModel
		if s.LLM.Provider != "" && !strings.Contains(modelStr, "/") {
			modelStr = s.LLM.Provider + "/" + modelStr
		}
		_ = os.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", modelStr)
		_ = os.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", modelStr)
	}

	dataDir := filepath.Join(settingsDir, "data")
	absDataDir, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = absDataDir
	}
	_ = os.Setenv("SAM_DATA_DIR", filepath.ToSlash(dataDir))
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(filepath.Join(dataDir, "artifacts"), 0755)

	skillsDir, skillsFromBundle := resolveResourceDir("SAM_SKILLS_DIR", "skills", settingsDir)
	_ = os.Setenv("SAM_SKILLS_DIR", filepath.ToSlash(skillsDir))
	if !skillsFromBundle {
		_ = os.MkdirAll(skillsDir, 0755)
	}

	skillsLinuxDir, skillsLinuxFromBundle := resolveResourceDir("SAM_SKILLS_LINUX_DIR", "skills-linux", settingsDir)
	_ = os.Setenv("SAM_SKILLS_LINUX_DIR", filepath.ToSlash(skillsLinuxDir))
	if !skillsLinuxFromBundle {
		_ = os.MkdirAll(skillsLinuxDir, 0755)
	}

	if !skillsFromBundle {
		if writeErr := WriteSkills(skillsDir); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write skills: %v\n", writeErr)
		}
	}
	if !skillsLinuxFromBundle {
		if writeErr := WriteSkills(skillsLinuxDir); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write skills to linux dir: %v\n", writeErr)
		}
	}

	toolsDir, toolsFromBundle := resolveResourceDir("SAM_TOOLS_DIR", "tools", settingsDir)
	_ = os.Setenv("SAM_TOOLS_DIR", filepath.ToSlash(toolsDir))
	if !toolsFromBundle {
		_ = os.MkdirAll(toolsDir, 0755)
	}

	toolsLinuxDir, toolsLinuxFromBundle := resolveResourceDir("SAM_TOOLS_LINUX_DIR", "tools-linux", settingsDir)
	_ = os.Setenv("SAM_TOOLS_LINUX_DIR", filepath.ToSlash(toolsLinuxDir))
	if !toolsLinuxFromBundle {
		_ = os.MkdirAll(toolsLinuxDir, 0755)
	}

	if os.Getenv("SAM_SESSION_SECRET") == "" {
		secret := generateSecret(32)
		_ = os.Setenv("SAM_SESSION_SECRET", secret)
	}

	if os.Getenv("SOLACE_DEV_MODE") == "" {
		_ = os.Setenv("SOLACE_DEV_MODE", "true")
	}
}

func ShouldShowOnboardWizard(s *Settings) bool {
	return s == nil || !s.Onboarded
}

func (s *Settings) EffectiveSandboxMode() SandboxMode {
	if s.Sandbox.Mode == "" {
		return SandboxModeBuiltin
	}
	return s.Sandbox.Mode
}

func SettingsDir(settingsPath string) string {
	return filepath.Dir(settingsPath)
}

func ensureGitignore(dir string) {
	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); err == nil {
		return
	}

	content := "# Protect API keys from accidental commits\nsettings.yaml\nsettings.json\n"
	_ = os.WriteFile(gitignorePath, []byte(content), 0644)
}

func resolveResourceDir(envVar, name, settingsDir string) (string, bool) {
	if v := os.Getenv(envVar); v != "" {
		return v, false
	}
	if bundled := resolveBundledResourceDir(name); bundled != "" {
		return bundled, true
	}
	p := filepath.Join(settingsDir, name)
	if abs, err := filepath.Abs(p); err == nil {
		return abs, false
	}
	return p, false
}

func resolveBundledResourceDir(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return bundledResourceDirFromBinary(filepath.Dir(exe), name)
}

func BundledResourceDir(name string) string {
	return resolveBundledResourceDir(name)
}

func bundledResourceDirFromBinary(binDir, name string) string {
	for _, c := range []string{
		filepath.Join(binDir, "..", "Resources", name),
		filepath.Join(binDir, name),
	} {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		if !hasBundleShape(abs, name) {
			continue
		}
		return abs
	}
	return ""
}

func hasBundleShape(dir, name string) bool {
	var marker string
	switch name {
	case "tools", "tools-linux":
		marker = "manifest.yaml"
	case "skills", "skills-linux":
		marker = "SKILL.md"
	default:
		entries, err := os.ReadDir(dir)
		return err == nil && len(entries) > 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), marker)); err == nil {
			return true
		}
	}
	return false
}

func generateSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "sam-default-secret-key"
	}
	return hex.EncodeToString(b)
}
