// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func nlog() *slog.Logger { return slog.Default().With("logger_name", "str-config") }

type WorkerConfig struct {
	WorkerID         string
	Namespace        string
	ManifestPaths    []string
	DefaultTimeout   int
	Sandbox          SandboxConfig
	VolumeService    volume.Service
	SkillsDir        string
	BuiltinSkillsDir string
	ToolsetsDir      string
	BuiltinToolsDir  string

	ToolModelAlias string

	ToolChatModel bool

	EnvModelName string

	RebroadcastInterval time.Duration

	ReconcileInterval time.Duration

	InProcessTools []*samtoolsdk.ToolDef

	ToolOutputLLMReturnMaxBytes int

	MaxMessageRedelivery int

	RespectMessageTTL bool

	ExecutionClassSplit bool

	ExecutionClass string
}

const defaultReconcileInterval = 5 * time.Minute

func parseReconcileInterval(cfgVal string) time.Duration {
	if env := strings.TrimSpace(os.Getenv("STR_RECONCILE_INTERVAL")); env != "" {
		d, err := time.ParseDuration(env)
		if err == nil {
			return d
		}
	}
	if cfgVal != "" {
		d, err := time.ParseDuration(cfgVal)
		if err == nil {
			return d
		}
	}
	return defaultReconcileInterval
}

func seccompBindDenyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("STR_SECCOMP_BIND_DENY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func resolveToolsetsDir(toolsets, legacyTools, legacyPackages string) (string, error) {
	var aliases []string
	if legacyTools != "" {
		aliases = append(aliases, fmt.Sprintf("tools_dir (=%s)", legacyTools))
	}
	if legacyPackages != "" {
		aliases = append(aliases, fmt.Sprintf("packages_dir (=%s)", legacyPackages))
	}

	if toolsets != "" {
		if len(aliases) > 0 {
			return "", fmt.Errorf(
				"str worker config: 'toolsets_dir' is set together with deprecated %s; "+
					"remove the deprecated key(s) and keep only 'toolsets_dir: %s'",
				strings.Join(aliases, " and "), toolsets)
		}
		return toolsets, nil
	}

	switch len(aliases) {
	case 0:
		return "", nil
	case 1:
		key, value := "tools_dir", legacyTools
		if legacyPackages != "" {
			key, value = "packages_dir", legacyPackages
		}
		nlog().Warn("str worker config: config key is deprecated; rename it to 'toolsets_dir'",
			"deprecatedKey", key, "value", value)
		return value, nil
	default:
		return "", fmt.Errorf(
			"str worker config: deprecated keys 'tools_dir' (=%s) and 'packages_dir' (=%s) "+
				"are both set and may disagree, which silently breaks toolset discovery; "+
				"remove both and set a single 'toolsets_dir' pointing at the directory the "+
				"platform writes toolset packages to (<namespace>/str-runtime/toolsets)",
			legacyTools, legacyPackages)
	}
}

func ParseWorkerConfig(cfg config.Config) (*WorkerConfig, error) {
	wc := &WorkerConfig{
		WorkerID:       cfg.GetString(config.KeyWorkerID),
		Namespace:      cfg.GetString(config.KeyNamespace),
		ManifestPaths:  cfg.GetStringSlice("manifest_paths"),
		DefaultTimeout: cfg.GetInt("default_timeout_seconds"),
	}

	if err := a2a.ValidateNamespace(wc.Namespace); err != nil {
		return nil, fmt.Errorf("str worker config: %w", err)
	}

	if cfg.GetSection("tool_sync") != nil {
		return nil, fmt.Errorf("str worker config: 'tool_sync' section was removed; " +
			"move tool_sync.tools_dir to top-level toolsets_dir " +
			"(skill/tools prefixes are now derived from namespace and not configurable)")
	}

	if wc.WorkerID == "" {
		wc.WorkerID = config.DefaultWorkerID
		nlog().Warn("str worker config: worker_id not set; using default pool", "workerID", wc.WorkerID)
	}
	if wc.DefaultTimeout <= 0 {
		wc.DefaultTimeout = config.DefaultRequestTimeoutSeconds
	}
	wc.SkillsDir = cfg.GetString(config.KeySkillsDir)
	wc.BuiltinSkillsDir = cfg.GetString(config.KeyBuiltinSkillsDir)
	wc.BuiltinToolsDir = cfg.GetString(config.KeyBuiltinToolsDir)
	wc.ToolModelAlias = strings.TrimSpace(cfg.GetString(config.KeyToolModelAlias))
	wc.ToolChatModel = toolChatModelEnabled()
	wc.EnvModelName = configuredModelName(cfg.GetRaw("model"))

	toolsetsDir, err := resolveToolsetsDir(
		cfg.GetString(config.KeyToolsetsDir),
		cfg.GetString(config.KeyToolsDir),
		cfg.GetString(config.KeyPackagesDir),
	)
	if err != nil {
		return nil, err
	}
	wc.ToolsetsDir = toolsetsDir

	wc.ToolOutputLLMReturnMaxBytes = cfg.GetInt("tool_output_llm_return_max_bytes")
	if wc.ToolOutputLLMReturnMaxBytes <= 0 {
		wc.ToolOutputLLMReturnMaxBytes = 102400
	}

	if cfg.Has("max_message_redelivery") {
		wc.MaxMessageRedelivery = cfg.GetInt("max_message_redelivery")
	} else {
		wc.MaxMessageRedelivery = 1
	}
	if cfg.Has("respect_message_ttl") {
		wc.RespectMessageTTL = cfg.GetBool("respect_message_ttl")
	} else {
		wc.RespectMessageTTL = true
	}

	wc.ReconcileInterval = parseReconcileInterval(cfg.GetString("reconcile_interval"))

	wc.ExecutionClassSplit = isolatedToolExecutionEnabled()
	wc.ExecutionClass = strings.TrimSpace(cfg.GetString("execution_class"))
	if err := validateClassSTR(wc); err != nil {
		return nil, fmt.Errorf("str worker config: %w", err)
	}
	if wc.ExecutionClass != "" {
		wc.WorkerID = classWorkerID(wc.ExecutionClass)
	}

	if sandboxCfg := cfg.GetSection("sandbox"); sandboxCfg != nil {
		wc.Sandbox = SandboxConfig{
			Mode:                  sandboxCfg.GetString("mode"),
			BwrapBin:              sandboxCfg.GetString("bwrap_bin"),
			PrlimitBin:            sandboxCfg.GetString("prlimit_bin"),
			WorkBaseDir:           sandboxCfg.GetString("work_base_dir"),
			DefaultProfile:        sandboxCfg.GetString("default_profile"),
			DefaultTimeoutSeconds: sandboxCfg.GetInt("default_timeout_seconds"),
			MaxTimeoutSeconds:     sandboxCfg.GetInt("max_timeout_seconds"),
			FreshProc:             sandboxCfg.GetString("fresh_proc"),
		}
		if guard := sandboxCfg.GetSection("memory_guard"); guard != nil {
			wc.Sandbox.MemorySoftPercent = guard.GetInt("soft_percent")
			wc.Sandbox.MemoryHardPercent = guard.GetInt("hard_percent")
			wc.Sandbox.MemoryGuardDisabled = guard.GetBool("disabled")
		}
		if err := validateMemoryGuard(wc.Sandbox); err != nil {
			return nil, err
		}
		if err := validateFreshProc(wc.Sandbox.FreshProc); err != nil {
			return nil, err
		}
		isolated, err := parseIsolatedConfig(sandboxCfg)
		if err != nil {
			return nil, fmt.Errorf("str worker config: sandbox.job_template: %w", err)
		}
		wc.Sandbox.Isolated = isolated
	}
	wc.Sandbox.Isolated.ToolsetsDir = wc.ToolsetsDir
	wc.Sandbox.ToolOutputLLMReturnMaxBytes = wc.ToolOutputLLMReturnMaxBytes
	wc.Sandbox.SeccompBindDeny = seccompBindDenyEnabled()
	if wc.Sandbox.Mode == sandboxModeKubernetesJob && !wc.ExecutionClassSplit {
		return nil, fmt.Errorf("str worker config: sandbox mode %s needs the isolated_tool_execution flag", sandboxModeKubernetesJob)
	}

	return wc, nil
}

func validateClassSTR(wc *WorkerConfig) error {
	if wc.ExecutionClass == "" {
		return nil
	}
	if !wc.ExecutionClassSplit {
		return fmt.Errorf("execution_class is set but the isolated_tool_execution flag is off")
	}
	if wc.ExecutionClass == DefaultExecutionClass {
		return fmt.Errorf("execution_class %q is served by the default STR; leave it empty there", DefaultExecutionClass)
	}
	if !validClassName(wc.ExecutionClass) {
		return fmt.Errorf("execution_class %q must be lowercase letters, digits and hyphens, starting with a letter", wc.ExecutionClass)
	}
	return nil
}

func configuredModelName(raw any) string {
	var model string
	switch v := raw.(type) {
	case string:
		model = v
	case map[string]any:
		model, _ = v["model"].(string)
	}
	_, bare := samapp.ParseModelProvider(model)
	return bare
}

func validateMemoryGuard(c SandboxConfig) error {
	if c.MemoryGuardDisabled {
		return nil
	}
	soft, hard := c.MemorySoftPercent, c.MemoryHardPercent
	if soft == 0 {
		soft = defaultMemorySoftPercent
	}
	if hard == 0 {
		hard = defaultMemoryHardPercent
	}
	if !validMemoryThresholds(soft, hard) {
		return fmt.Errorf("str worker config: sandbox.memory_guard needs 0 < soft_percent <= hard_percent <= 100, got soft_percent %d and hard_percent %d", soft, hard)
	}
	return nil
}
