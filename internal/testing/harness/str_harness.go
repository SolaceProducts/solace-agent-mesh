// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"gopkg.in/yaml.v3"
)

type PythonToolDef struct {
	Module     string
	ClassName  string
	ToolConfig map[string]any
}

type GoToolDef struct {
	SourceDir      string
	Executable     string
	ToolConfig     map[string]any
	RequiredScopes map[string][]string
}

type DynamicAgentConfig struct {
	Tools         []PythonToolDef
	GoTools       []GoToolDef
	BuiltinGroups []string
}

type discoveredTool struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Parameters     map[string]any `json:"parameters"`
	ClassName      string         `json:"class_name"`
	FunctionName   string         `json:"function_name"`
	RawStringArgs  []string       `json:"raw_string_args"`
	ResolutionType string         `json:"resolution_type"`
	AuthConfig     map[string]any `json:"auth_config"`
	ArtifactParams map[string]any `json:"-"`
	defIndex       int
}

type discoveryResult struct {
	Tools []discoveredTool `json:"tools"`
}

type strTestHarness struct {
	worker      *str.Worker
	manifestDir string
	logger      *slog.Logger
}

func (h *TestHarness) setupSTRTools(
	ctx context.Context,
	ag *agent.Agent,
	svc runtime.Services,
	dynCfg *DynamicAgentConfig,
) error {
	if dynCfg == nil || (len(dynCfg.Tools) == 0 && len(dynCfg.GoTools) == 0) {
		return nil
	}

	modRoot := findModuleRoot()

	var allDiscovered []discoveredTool
	if len(dynCfg.Tools) > 0 {
		discoverScript := filepath.Join(modRoot, "test", "integration", "discover_tools.py")
		for defIdx, toolDef := range dynCfg.Tools {
			tools, err := discoverPythonTools(modRoot, discoverScript, toolDef)
			if err != nil {
				return fmt.Errorf("discover tools from %s: %w", toolDef.Module, err)
			}
			for _, t := range tools {
				t.defIndex = defIdx
				allDiscovered = append(allDiscovered, t)
			}
		}
	}

	type nativeToolInfo struct {
		discovered     []discoveredTool
		executable     string
		toolConfig     map[string]any
		requiredScopes map[string][]string
	}
	var nativeInfos []nativeToolInfo

	for _, goDef := range dynCfg.GoTools {
		var executable string
		if goDef.Executable != "" {
			executable = goDef.Executable
		} else {
			var err error
			executable, err = buildGoTool(goDef.SourceDir)
			if err != nil {
				return fmt.Errorf("build go tool %s: %w", goDef.SourceDir, err)
			}
		}
		tools, err := discoverGoToolsForHarness(executable)
		if err != nil {
			return fmt.Errorf("discover go tools from %s: %w", executable, err)
		}
		nativeInfos = append(nativeInfos, nativeToolInfo{
			discovered:     tools,
			executable:     executable,
			toolConfig:     goDef.ToolConfig,
			requiredScopes: goDef.RequiredScopes,
		})
	}

	if len(allDiscovered) == 0 && len(nativeInfos) == 0 {
		return nil
	}

	totalNativeTools := 0
	for _, ni := range nativeInfos {
		totalNativeTools += len(ni.discovered)
	}

	if len(allDiscovered) == 0 && totalNativeTools == 0 {
		return nil
	}

	manifestDir, err := os.MkdirTemp("", "sam-str-test-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")

	manifest := map[string]any{
		"version": 1,
		"tools":   map[string]any{},
	}
	toolsMap := manifest["tools"].(map[string]any)

	if len(allDiscovered) > 0 {
		wrapperByDef := make(map[int]string)
		for _, t := range allDiscovered {
			if _, ok := wrapperByDef[t.defIndex]; ok {
				continue
			}
			toolDef := dynCfg.Tools[t.defIndex]
			wrapper, wrapErr := buildPythonDynamicToolWrapper(modRoot, manifestDir, toolDef)
			if wrapErr != nil {
				return fmt.Errorf("build python wrapper for %s: %w", toolDef.Module, wrapErr)
			}
			wrapperByDef[t.defIndex] = wrapper
		}

		seen := make(map[int]bool)
		for _, t := range allDiscovered {
			if seen[t.defIndex] {
				continue
			}
			seen[t.defIndex] = true
			entryName := fmt.Sprintf("_py_discovery_%d", t.defIndex)
			toolsMap[entryName] = map[string]any{
				"executable": wrapperByDef[t.defIndex],
			}
		}
	}

	for i, gi := range nativeInfos {
		entryName := fmt.Sprintf("_discovery_%d", i)
		toolsMap[entryName] = map[string]any{
			"executable": gi.executable,
		}
	}

	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	h.logger.Info("STR manifest generated", "path", manifestPath,
		"pythonTools", len(allDiscovered), "nativeTools", totalNativeTools)

	workerID := fmt.Sprintf("str-%s", h.scenario.TestCaseID)

	workerCfg := str.WorkerConfig{
		WorkerID:      workerID,
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 8,
		},
	}

	worker := str.NewWorker(svc, workerCfg)
	if err := worker.Init(ctx, nil); err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("str worker init: %w", err)
	}
	if err := worker.Start(ctx); err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("str worker start: %w", err)
	}

	var executor *remote.Executor
	ensureExecutor := func() {
		if executor == nil {
			executor = remote.NewExecutor(svc.Broker(), "test-ns", ag.Name(), remote.WithLogger(svc.Logger()))
			ag.AddSubComponent(executor)
		}
	}

	for _, t := range allDiscovered {
		ensureExecutor()
		cfg := &remote.ToolConfig{
			ToolName:       t.Name,
			Description:    t.Description,
			TimeoutSeconds: 120,
			SandboxProfile: "standard",
			Parameters:     buildParamConfig(t.Parameters),
		}
		srcDef := dynCfg.Tools[t.defIndex]
		if len(srcDef.ToolConfig) > 0 {
			cfg.ToolPassthroughConfig = srcDef.ToolConfig
		}
		if len(t.AuthConfig) > 0 {
			cfg.Auth = toolauth.ParseAuthConfig(t.AuthConfig, t.Name)
		}
		rt := remote.NewToolWithOptions(cfg, executor, t.RawStringArgs)
		if err := ag.Tools().Register(rt); err != nil {
			return fmt.Errorf("register tool %q: %w", t.Name, err)
		}
		if ah := rt.AuthHandler(); ah != nil {
			ag.RegisterAuthHandler(ah)
		}
	}

	for _, gi := range nativeInfos {
		for _, t := range gi.discovered {
			ensureExecutor()
			cfg := &remote.ToolConfig{
				ToolName:       t.Name,
				Description:    t.Description,
				TimeoutSeconds: 120,
				SandboxProfile: "standard",
				Parameters:     buildParamConfigWithArtifacts(t.Parameters, t.ArtifactParams),
			}
			if len(gi.toolConfig) > 0 {
				cfg.ToolPassthroughConfig = gi.toolConfig
			}
			if sc := gi.requiredScopes[t.Name]; len(sc) > 0 {
				cfg.RequiredScopes = sc
			}
			if len(t.AuthConfig) > 0 {
				cfg.Auth = toolauth.ParseAuthConfig(t.AuthConfig, t.Name)
			}
			rt := remote.NewToolWithOptions(cfg, executor, nil)
			if err := ag.Tools().Register(rt); err != nil {
				return fmt.Errorf("register go tool %q: %w", t.Name, err)
			}
			if ah := rt.AuthHandler(); ah != nil {
				ag.RegisterAuthHandler(ah)
			}
		}
	}

	for _, group := range dynCfg.BuiltinGroups {
		h.logger.Debug("builtin group requested", "group", group)
	}

	h.strHarness = &strTestHarness{
		worker:      worker,
		manifestDir: manifestDir,
		logger:      h.logger,
	}

	return nil
}

func buildParamConfigWithArtifacts(params map[string]any, artifactParams map[string]any) map[string]remote.ParamConfig {
	result := buildParamConfig(params)
	if result == nil || len(artifactParams) == 0 {
		return result
	}
	for name, info := range artifactParams {
		pc, ok := result[name]
		if !ok {
			pc = remote.ParamConfig{}
		}
		if infoMap, ok := info.(map[string]any); ok {
			if isList, _ := infoMap["is_list"].(bool); isList {
				pc.Type = "artifact_list"
			} else {
				pc.Type = "artifact"
			}
			if isOpt, _ := infoMap["is_optional"].(bool); isOpt {
				pc.Required = false
			}
		} else {
			pc.Type = "artifact"
		}
		result[name] = pc
	}
	return result
}

func buildParamConfig(params map[string]any) map[string]remote.ParamConfig {
	props, _ := params["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}

	reqSet := make(map[string]bool)
	if reqList, ok := params["required"].([]any); ok {
		for _, r := range reqList {
			if rs, ok := r.(string); ok {
				reqSet[rs] = true
			}
		}
	}

	result := make(map[string]remote.ParamConfig, len(props))
	for pname, pschema := range props {
		pmap, ok := pschema.(map[string]any)
		if !ok {
			continue
		}
		pc := remote.ParamConfig{
			Required: reqSet[pname],
		}
		if t, ok := pmap["type"].(string); ok {
			pc.Type = t
		}
		if d, ok := pmap["description"].(string); ok {
			pc.Description = d
		}
		if def, ok := pmap["default"]; ok {
			pc.Default = def
			if !reqSet[pname] {
				pc.Required = false
			}
		}
		result[pname] = pc
	}
	return result
}

type productionPythonToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

var productionPythonTools = map[string]productionPythonToolSchema{
	"create_chart_from_plotly_config": {
		Name:        "create_chart_from_plotly_config",
		Description: "Generates a static chart image from a Plotly configuration provided as a string.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config_content": map[string]any{
					"type":        "string",
					"description": "The Plotly configuration as a string (JSON or YAML format).",
				},
				"config_format": map[string]any{
					"type":        "string",
					"description": "The format of config_content: 'json' or 'yaml'.",
				},
				"output_filename": map[string]any{
					"type":        "string",
					"description": "Desired filename for the generated chart image.",
				},
				"output_format": map[string]any{
					"type":        "string",
					"description": "Image format: 'png', 'jpeg', 'svg', or 'pdf'.",
					"default":     "png",
				},
			},
			"required": []any{"config_content", "config_format", "output_filename"},
		},
	},
}

func (h *TestHarness) setupProductionPythonTools(
	ctx context.Context,
	ag *agent.Agent,
	svc runtime.Services,
	toolNames []string,
) error {
	if len(toolNames) == 0 {
		return nil
	}

	modRoot := findModuleRoot()
	toolsDir := filepath.Join(modRoot, "tools", "python")

	manifestDir, err := os.MkdirTemp("", "sam-str-prodtools-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	toolsMap := make(map[string]any, len(toolNames))
	for _, name := range toolNames {
		prodManifest, readErr := os.ReadFile(filepath.Join(toolsDir, "manifest.yaml"))
		if readErr != nil {
			_ = os.RemoveAll(manifestDir)
			return fmt.Errorf("read production manifest: %w", readErr)
		}
		var fullManifest struct {
			Version int                       `yaml:"version"`
			Tools   map[string]map[string]any `yaml:"tools"`
		}
		if yamlErr := yaml.Unmarshal(prodManifest, &fullManifest); yamlErr != nil {
			_ = os.RemoveAll(manifestDir)
			return fmt.Errorf("parse production manifest: %w", yamlErr)
		}
		entry, ok := fullManifest.Tools[name]
		if !ok {
			_ = os.RemoveAll(manifestDir)
			return fmt.Errorf("tool %q not found in production manifest", name)
		}
		toolsMap[name] = entry
	}

	manifest := map[string]any{
		"version": 1,
		"tools":   toolsMap,
	}
	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("write manifest: %w", err)
	}

	h.logger.Info("production Python tools manifest generated",
		"path", manifestPath, "tools", toolNames)

	workerID := fmt.Sprintf("str-prod-%s", h.scenario.TestCaseID)
	workerCfg := str.WorkerConfig{
		WorkerID:      workerID,
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 8,
		},
	}

	worker := str.NewWorker(svc, workerCfg)
	if err := worker.Init(ctx, nil); err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("str worker init: %w", err)
	}
	if err := worker.Start(ctx); err != nil {
		_ = os.RemoveAll(manifestDir)
		return fmt.Errorf("str worker start: %w", err)
	}

	executor := remote.NewExecutor(svc.Broker(), "test-ns", ag.Name(), remote.WithLogger(svc.Logger()))
	ag.AddSubComponent(executor)

	for _, name := range toolNames {
		schema, ok := productionPythonTools[name]
		if !ok {
			continue
		}
		cfg := &remote.ToolConfig{
			ToolName:       schema.Name,
			Description:    schema.Description,
			TimeoutSeconds: 120,
			SandboxProfile: "standard",
			Parameters:     buildParamConfig(schema.Parameters),
		}
		rt := remote.NewToolWithOptions(cfg, executor, nil)
		if err := ag.Tools().Register(rt); err != nil {
			return fmt.Errorf("register production tool %q: %w", name, err)
		}
	}

	h.strHarness = &strTestHarness{
		worker:      worker,
		manifestDir: manifestDir,
		logger:      h.logger,
	}

	return nil
}

func detectProductionPythonTools(scenario *TestScenario) []string {
	var needed []string
	seen := make(map[string]bool)

	for _, interaction := range scenario.LLMInteractions {
		raw, err := interaction.StaticResponseJSON()
		if err != nil {
			continue
		}
		var resp struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		for _, choice := range resp.Choices {
			for _, tc := range choice.Message.ToolCalls {
				name := tc.Function.Name
				if _, ok := productionPythonTools[name]; ok && !seen[name] {
					seen[name] = true
					needed = append(needed, name)
				}
			}
		}
	}
	return needed
}

func (h *TestHarness) cleanupSTR(ctx context.Context) {
	if h.strHarness == nil {
		return
	}
	if h.strHarness.worker != nil {
		if err := h.strHarness.worker.Stop(ctx); err != nil {
			h.logger.Warn("str worker stop failed", "error", err)
		}
	}
	if h.strHarness.manifestDir != "" {
		_ = os.RemoveAll(h.strHarness.manifestDir)
	}
}

const (
	transientProcRetries    = 3
	transientProcRetryDelay = 50 * time.Millisecond
)

func runCmdOutputWithRetry(newCmd func() *exec.Cmd) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= transientProcRetries; attempt++ {
		out, err := newCmd().Output()
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, syscall.ECHILD) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		lastErr = err
		if attempt < transientProcRetries {
			time.Sleep(transientProcRetryDelay)
		}
	}
	return nil, lastErr
}

var (
	goToolBuildMu    sync.Mutex
	goToolBuildCache = map[string]string{}
)

func buildGoTool(sourceDir string) (string, error) {
	modRoot := findModuleRoot()
	absDir := filepath.Join(modRoot, sourceDir)

	goToolBuildMu.Lock()
	defer goToolBuildMu.Unlock()

	if cached, ok := goToolBuildCache[absDir]; ok {
		return cached, nil
	}

	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("sam-go-tool-%s", filepath.Base(sourceDir)))
	if out, err := runCmdOutputWithRetry(func() *exec.Cmd {
		cmd := exec.Command("go", "build", "-o", outPath, ".")
		cmd.Dir = absDir
		cmd.Stderr = os.Stderr
		return cmd
	}); err != nil {
		return "", fmt.Errorf("go build %s: %w (output: %s)", absDir, err, string(out))
	}

	goToolBuildCache[absDir] = outPath
	return outPath, nil
}

type goSchemaOutput struct {
	Tools map[string]goSchemaEntry `json:"tools"`
}

type goSchemaEntry struct {
	Description    string         `json:"description"`
	Parameters     map[string]any `json:"parameters"`
	ArtifactParams map[string]any `json:"artifact_params,omitempty"`
	Instructions   string         `json:"instructions,omitempty"`
	Auth           map[string]any `json:"auth,omitempty"`
}

func discoverGoToolsForHarness(executable string) ([]discoveredTool, error) {
	output, err := runCmdOutputWithRetry(func() *exec.Cmd {
		return exec.Command(executable, "--schema")
	})
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("schema failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("run schema: %w", err)
	}

	var schema goSchemaOutput
	if err := json.Unmarshal(output, &schema); err != nil {
		return nil, fmt.Errorf("parse schema output: %w (output: %s)", err, string(output))
	}

	var tools []discoveredTool
	for name, entry := range schema.Tools {
		params := entry.Parameters
		if params != nil && len(entry.ArtifactParams) > 0 {
			params = filterArtifactParams(params, entry.ArtifactParams)
		}
		tools = append(tools, discoveredTool{
			Name:           name,
			Description:    entry.Description,
			Parameters:     params,
			ArtifactParams: entry.ArtifactParams,
			AuthConfig:     entry.Auth,
		})
	}
	return tools, nil
}

func filterArtifactParams(params map[string]any, artifactParams map[string]any) map[string]any {
	result := make(map[string]any, len(params))
	for k, v := range params {
		result[k] = v
	}
	props, ok := result["properties"].(map[string]any)
	if !ok {
		return result
	}
	newProps := make(map[string]any, len(props))
	for k, v := range props {
		if _, isArtifact := artifactParams[k]; isArtifact {
			pmap, ok := v.(map[string]any)
			if ok {
				newPmap := map[string]any{"type": "string"}
				if desc, ok := pmap["description"].(string); ok {
					newPmap["description"] = desc
				}
				newProps[k] = newPmap
				continue
			}
		}
		newProps[k] = v
	}
	result["properties"] = newProps
	return result
}

func findGoTestToolSourceDir() string {
	return moduleRelativeTestdataToolDir("go-test-tool")
}

func findGoSleepToolSourceDir() string {
	return moduleRelativeTestdataToolDir("go-sleep-tool")
}

func moduleRelativeTestdataToolDir(name string) string {
	return filepath.Join("internal", "str", "testdata", name)
}

var (
	pyWrapperMu    sync.Mutex
	pyWrapperCache = map[string]string{}
)

func buildPythonToolWrapper(scriptPath string) (string, error) {
	pyWrapperMu.Lock()
	defer pyWrapperMu.Unlock()

	if cached, ok := pyWrapperCache[scriptPath]; ok {
		return cached, nil
	}

	pythonBin, err := findPython()
	if err != nil {
		return "", fmt.Errorf("find python: %w", err)
	}

	wrapper, err := os.CreateTemp("", "sam-py-tool-wrapper-*.sh")
	if err != nil {
		return "", fmt.Errorf("create wrapper: %w", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n", pythonBin, scriptPath)
	if _, err := wrapper.WriteString(script); err != nil {
		_ = wrapper.Close()
		return "", fmt.Errorf("write wrapper: %w", err)
	}
	_ = wrapper.Close()
	if err := os.Chmod(wrapper.Name(), 0o755); err != nil {
		return "", fmt.Errorf("chmod wrapper: %w", err)
	}

	pyWrapperCache[scriptPath] = wrapper.Name()
	return wrapper.Name(), nil
}

func buildPythonDynamicToolWrapper(repoRoot, manifestDir string, toolDef PythonToolDef) (string, error) {
	pythonBin, err := findPython()
	if err != nil {
		return "", fmt.Errorf("find python: %w", err)
	}

	runnerScript := filepath.Join(repoRoot, "test", "integration", "tool_test_runner.py")

	cfg := map[string]any{
		"module": toolDef.Module,
	}
	if toolDef.ClassName != "" {
		cfg["class_name"] = toolDef.ClassName
	}
	if len(toolDef.ToolConfig) > 0 {
		cfg["tool_config"] = toolDef.ToolConfig
	}

	cfgData, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	cfgFile := filepath.Join(manifestDir, fmt.Sprintf("runner_config_%s.json", toolDef.Module))
	if wErr := os.WriteFile(cfgFile, cfgData, 0o644); wErr != nil {
		return "", fmt.Errorf("write config: %w", wErr)
	}

	wrapper, err := os.CreateTemp(manifestDir, "sam-py-dyn-wrapper-*.sh")
	if err != nil {
		return "", fmt.Errorf("create wrapper: %w", err)
	}
	sdkPath := filepath.Join(repoRoot, "python", "sam-tool-sdk")
	script := fmt.Sprintf(
		"#!/bin/sh\nexport PYTHONPATH=%q:%q:$PYTHONPATH\nexport SAM_TOOL_RUNNER_CONFIG=%q\nexec %s %s \"$@\"\n",
		repoRoot, sdkPath, cfgFile, pythonBin, runnerScript,
	)
	if _, err := wrapper.WriteString(script); err != nil {
		_ = wrapper.Close()
		return "", fmt.Errorf("write wrapper: %w", err)
	}
	_ = wrapper.Close()
	if err := os.Chmod(wrapper.Name(), 0o755); err != nil {
		return "", fmt.Errorf("chmod wrapper: %w", err)
	}

	return wrapper.Name(), nil
}

func findPythonTestToolScript() string {
	return filepath.Join(findModuleRoot(), "internal", "str", "testdata", "python-test-tool", "tool.py")
}

func discoverPythonTools(repoRoot, scriptPath string, toolDef PythonToolDef) ([]discoveredTool, error) {
	input := map[string]any{
		"module": toolDef.Module,
	}
	if toolDef.ClassName != "" {
		input["class_name"] = toolDef.ClassName
	}
	if len(toolDef.ToolConfig) > 0 {
		input["tool_config"] = toolDef.ToolConfig
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	pythonBin, err := findPython()
	if err != nil {
		return nil, fmt.Errorf("find python: %w", err)
	}

	output, err := runCmdOutputWithRetry(func() *exec.Cmd {
		c := exec.Command(pythonBin, scriptPath, string(inputJSON))
		c.Dir = repoRoot
		c.Env = append(os.Environ(), fmt.Sprintf("PYTHONPATH=%s", repoRoot))
		return c
	})
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("discovery failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("run discovery: %w", err)
	}

	var result discoveryResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("parse discovery output: %w (output: %s)", err, string(output))
	}

	return result.Tools, nil
}

func (h *TestHarness) knownDynamicToolAgent(agentName string) *DynamicAgentConfig {
	switch agentName {
	case "CombinedDynamicAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{
					Module:     "test.integration.python_tools.single_tool",
					ToolConfig: map[string]any{"greeting_prefix": "Hi there"},
				},
				{Module: "test.integration.python_tools.provider_tool"},
			},
		}
	case "ComplexSignaturesAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{Module: "test.integration.python_tools.complex_signatures"},
			},
		}
	case "ConfigContextAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{
					Module:     "test.integration.python_tools.config_and_context",
					ToolConfig: map[string]any{"provider_level": "value1", "tool_specific": "value2"},
				},
			},
		}
	case "ArtifactContentAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{Module: "test.integration.python_tools.artifact_content_tools"},
			},
			BuiltinGroups: []string{"artifact_management"},
		}
	case "EmptyProviderAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{
					Module:    "test.integration.python_tools.error_cases",
					ClassName: "EmptyToolProvider",
				},
			},
		}
	case "DocstringlessAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{
					Module:    "test.integration.python_tools.error_cases",
					ClassName: "ProviderWithDocstringlessTool",
				},
			},
		}
	case "MixedDiscoveryAgent":
		return &DynamicAgentConfig{
			Tools: []PythonToolDef{
				{Module: "test.integration.python_tools.mixed_discovery"},
			},
		}
	case "SleepToolAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{SourceDir: findGoSleepToolSourceDir()},
			},
		}
	case "CitingToolAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{SourceDir: moduleRelativeTestdataToolDir("go-cite-tool")},
			},
		}
	case "GoToolEchoAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{
					SourceDir:  findGoTestToolSourceDir(),
					ToolConfig: map[string]any{"default_prefix": "TestPrefix"},
				},
			},
		}
	case "GoToolAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{SourceDir: findGoTestToolSourceDir()},
			},
		}
	case "ScopedGoToolAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{
					SourceDir:      findGoTestToolSourceDir(),
					ToolConfig:     map[string]any{"default_prefix": "TestPrefix"},
					RequiredScopes: map[string][]string{"echo": {"tool:echo:invoke"}},
				},
			},
		}
	case "ConvertMarkdownAgent":
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{SourceDir: "tools/go/convert_to_markdown"},
			},
		}
	case "PythonToolEchoAgent":
		scriptPath := findPythonTestToolScript()
		wrapper, err := buildPythonToolWrapper(scriptPath)
		if err != nil {
			return nil
		}
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{
					Executable: wrapper,
					ToolConfig: map[string]any{"default_prefix": "TestPrefix"},
				},
			},
		}
	case "PythonToolAgent":
		scriptPath := findPythonTestToolScript()
		wrapper, err := buildPythonToolWrapper(scriptPath)
		if err != nil {
			return nil
		}
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{
				{Executable: wrapper},
			},
		}
	case "SlackToolAgent":
		mockSlack := NewMockSlackServer()
		h.slackToolMockServer = mockSlack
		return &DynamicAgentConfig{
			GoTools: []GoToolDef{{
				SourceDir: "tools/go/slack",
				ToolConfig: map[string]any{
					"slack_bot_token": "xoxb-test-token",
					"api_url":         mockSlack.URL(),
				},
			}},
		}
	default:
		return nil
	}
}
