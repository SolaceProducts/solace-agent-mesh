// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"gopkg.in/yaml.v3"
)

const skillDiscoveryTimeout = 5 * time.Second

func waitForSkillResourceDiscovery(ctx context.Context, ag *agent.Agent, skills []TestSkillConfig) error {
	wantResources := slices.ContainsFunc(skills, func(sc TestSkillConfig) bool {
		return !sc.NotConfigured && len(sc.References)+len(sc.Assets)+len(sc.Templates) > 0
	})
	wantAssets := slices.ContainsFunc(skills, func(sc TestSkillConfig) bool {
		return !sc.NotConfigured && len(sc.Assets)+len(sc.Templates) > 0
	})
	mgr := ag.SkillManager()
	if !wantResources || mgr == nil {
		return nil
	}
	discovered := func() bool { return mgr.HasAnyResources() && (!wantAssets || mgr.HasAnyAssets()) }
	deadline := time.Now().Add(skillDiscoveryTimeout)
	for !discovered() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for skill inits: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("skill inits not handled within %s (want assets: %t; agent has resources: %t, assets: %t)",
				skillDiscoveryTimeout, wantAssets, mgr.HasAnyResources(), mgr.HasAnyAssets())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (h *TestHarness) setupSkills(
	ctx context.Context,
	ag *agent.Agent,
	agentCfg *agent.AgentConfig,
	svc runtime.Services,
) (*str.Worker, error) {
	skillConfigs := h.scenario.ConfigOverrides.AgentConfig.Skills
	realSkillNames := h.scenario.ConfigOverrides.AgentConfig.UseRealSkills

	baseDir, err := os.MkdirTemp("", "sam-test-skills-*")
	if err != nil {
		return nil, fmt.Errorf("create skill temp dir: %w", err)
	}
	h.t.Cleanup(func() { _ = os.RemoveAll(baseDir) })

	if len(realSkillNames) > 0 {
		if wsErr := settings.WriteSkills(baseDir); wsErr != nil {
			return nil, fmt.Errorf("write real skills: %w", wsErr)
		}
		for _, name := range realSkillNames {
			skillDir := filepath.Join(baseDir, name)
			if _, statErr := os.Stat(skillDir); statErr != nil {
				return nil, fmt.Errorf("real skill %q not found after WriteSkills: %w", name, statErr)
			}
			agentCfg.SkillRefs = append(agentCfg.SkillRefs, agent.SkillRef{
				Name: name,
				Path: skillDir,
			})
		}
	}

	for _, sc := range skillConfigs {
		skillDir := filepath.Join(baseDir, sc.Name)
		if mkErr := os.MkdirAll(skillDir, 0o755); mkErr != nil {
			return nil, fmt.Errorf("create skill dir %q: %w", sc.Name, mkErr)
		}

		if mdErr := writeSkillMD(skillDir, sc); mdErr != nil {
			return nil, fmt.Errorf("write SKILL.md for %q: %w", sc.Name, mdErr)
		}

		for relPath, content := range sc.References {
			absPath := filepath.Join(skillDir, "references", relPath)
			if dirErr := os.MkdirAll(filepath.Dir(absPath), 0o755); dirErr != nil {
				return nil, dirErr
			}
			if wfErr := os.WriteFile(absPath, []byte(content), 0o644); wfErr != nil {
				return nil, fmt.Errorf("write reference %q: %w", relPath, wfErr)
			}
		}

		for relPath, content := range sc.Assets {
			absPath := filepath.Join(skillDir, "assets", relPath)
			if adErr := os.MkdirAll(filepath.Dir(absPath), 0o755); adErr != nil {
				return nil, adErr
			}
			if wfErr := os.WriteFile(absPath, []byte(content), 0o644); wfErr != nil {
				return nil, fmt.Errorf("write asset %q: %w", relPath, wfErr)
			}
		}

		for file, tpl := range sc.Templates {
			zipBytes, zErr := skillruntime.BuildTemplateZip(file, tpl.Body, tpl.Sidecar)
			if zErr != nil {
				return nil, fmt.Errorf("package template %q for skill %q: %w", file, sc.Name, zErr)
			}
			assetsDir := filepath.Join(skillDir, "assets")
			if adErr := os.MkdirAll(assetsDir, 0o755); adErr != nil {
				return nil, adErr
			}
			if wfErr := os.WriteFile(filepath.Join(assetsDir, file+skillruntime.TemplateArtifactExt), zipBytes, 0o644); wfErr != nil {
				return nil, fmt.Errorf("write template %q: %w", file, wfErr)
			}
		}

		if sc.BundledGoTool || sc.BundledPythonTool || sc.BundledPythonWheelTool {
			if btErr := h.writeBundledToolManifest(skillDir, sc); btErr != nil {
				return nil, fmt.Errorf("write bundled tool manifest for %q: %w", sc.Name, btErr)
			}
		} else {
			for i, toolCfg := range sc.Tools {
				toolsDir := filepath.Join(skillDir, "tools")
				if tdErr := os.MkdirAll(toolsDir, 0o755); tdErr != nil {
					return nil, tdErr
				}
				toolName, _ := toolCfg["tool_name"].(string)
				if toolName == "" {
					toolName = fmt.Sprintf("tool_%d", i)
				}
				toolData, yErr := yaml.Marshal(toolCfg)
				if yErr != nil {
					return nil, fmt.Errorf("marshal tool config %q: %w", toolName, yErr)
				}
				if wfErr := os.WriteFile(filepath.Join(toolsDir, toolName+".yaml"), toolData, 0o644); wfErr != nil {
					return nil, fmt.Errorf("write tool %q: %w", toolName, wfErr)
				}
			}
		}

		if sc.NotConfigured {
			continue
		}
		agentCfg.SkillRefs = append(agentCfg.SkillRefs, agent.SkillRef{
			Name: sc.Name,
			Path: skillDir,
		})
	}

	manifestDir, err := os.MkdirTemp("", "sam-test-skill-manifest-*")
	if err != nil {
		return nil, fmt.Errorf("create manifest temp dir: %w", err)
	}
	h.t.Cleanup(func() { _ = os.RemoveAll(manifestDir) })

	manifestPath := filepath.Join(manifestDir, "manifest.yaml")
	if wErr := os.WriteFile(manifestPath, []byte("version: 1\ntools: {}\n"), 0o644); wErr != nil {
		return nil, fmt.Errorf("write empty manifest: %w", wErr)
	}

	workerID := fmt.Sprintf("str-skills-%s", h.scenario.TestCaseID)
	workerCfg := str.WorkerConfig{
		WorkerID:      workerID,
		Namespace:     "test-ns",
		ManifestPaths: []string{manifestPath},
		SkillsDir:     baseDir,
		Sandbox: str.SandboxConfig{
			Mode:                    "direct",
			MaxConcurrentExecutions: 4,
		},
	}

	worker := str.NewWorker(svc, workerCfg)
	worker.SetEmbedResolveCheck(func(ctx context.Context, candidate string, arts *artifact.ScopedService, agentName, userID, sessionID string) (string, error) {
		rctx := &embed.ResolveContext{
			Artifacts: arts,
			AgentName: agentName,
			UserID:    userID,
			SessionID: sessionID,
			Config: embed.ResolveConfig{
				MaxDepth:             12,
				MaxArtifactSizeBytes: embed.DefaultMaxArtifactSizeBytes,
				MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
			},
			Logger: h.logger,
		}
		resolved, _, rErr := embed.ResolveEmbedsInString(ctx, candidate, embed.ModeA2AMessageToUser, rctx)
		return resolved, rErr
	})
	if err := worker.Init(ctx, nil); err != nil {
		return nil, fmt.Errorf("skill str worker init: %w", err)
	}
	if err := worker.Start(ctx); err != nil {
		return nil, fmt.Errorf("skill str worker start: %w", err)
	}

	if h.strHarness == nil {
		h.strHarness = &strTestHarness{
			worker:      worker,
			manifestDir: manifestDir,
			logger:      h.logger,
		}
	} else {
		h.skillSTRWorker = worker
	}

	return worker, awe.LoadAndRegisterSkills(ag, agentCfg, svc, nil)
}

func (h *TestHarness) writeBundledToolManifest(skillDir string, sc TestSkillConfig) error {
	toolsDir := filepath.Join(skillDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return err
	}

	var executable string

	if sc.BundledGoTool {
		sourceDir := findGoTestToolSourceDir()
		var err error
		executable, err = buildGoTool(sourceDir)
		if err != nil {
			return fmt.Errorf("build go test tool: %w", err)
		}
	} else if sc.BundledPythonTool {
		scriptPath := findPythonTestToolScript()
		var err error
		executable, err = buildPythonToolWrapper(scriptPath)
		if err != nil {
			return fmt.Errorf("build python test tool wrapper: %w", err)
		}
	} else if sc.BundledPythonWheelTool {
		return writePythonWheelToolManifest(toolsDir)
	}

	tools, err := discoverGoToolsForHarness(executable)
	if err != nil {
		return fmt.Errorf("discover tools from %s: %w", executable, err)
	}

	toolsMap := make(map[string]any, len(tools))
	for _, t := range tools {
		toolsMap[t.Name] = map[string]any{
			"executable": executable,
		}
	}

	manifest := map[string]any{
		"version": 1,
		"tools":   toolsMap,
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal tools manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), data, 0o644)
}

var (
	pyWheelBuildMu    sync.Mutex
	pyWheelBuildCache = map[string]string{}
)

func writePythonWheelToolManifest(toolsDir string) error {
	wheelTestDir := findPythonWheelTestToolDir()
	sdkDir := findSamToolSDKDir()

	wheelDir, err := buildPythonWheels(wheelTestDir, sdkDir)
	if err != nil {
		return fmt.Errorf("build python wheels: %w", err)
	}

	entries, err := os.ReadDir(wheelDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".whl") {
			continue
		}
		src := filepath.Join(wheelDir, e.Name())
		dst := filepath.Join(toolsDir, e.Name())
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			return fmt.Errorf("read wheel %s: %w", src, readErr)
		}
		if writeErr := os.WriteFile(dst, data, 0o644); writeErr != nil {
			return fmt.Errorf("write wheel %s: %w", dst, writeErr)
		}
	}

	manifest := map[string]any{
		"version": 1,
		"tools": map[string]any{
			"wheel_echo": map[string]any{
				"executable": "wheel_echo",
			},
		},
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal wheel tool manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), data, 0o644)
}

func buildPythonWheels(toolSourceDir, sdkSourceDir string) (string, error) {
	pyWheelBuildMu.Lock()
	defer pyWheelBuildMu.Unlock()

	if cached, ok := pyWheelBuildCache[toolSourceDir]; ok {
		return cached, nil
	}

	destDir, err := os.MkdirTemp("", "sam-test-wheels-*")
	if err != nil {
		return "", err
	}

	tmpSDK := "/tmp/sam-tool-sdk"
	if _, err := os.Stat(filepath.Join(tmpSDK, "pyproject.toml")); err != nil {
		_ = os.RemoveAll(tmpSDK)
		if err := copyDir(sdkSourceDir, tmpSDK); err != nil {
			return "", fmt.Errorf("copy sam-tool-sdk to %s: %w", tmpSDK, err)
		}
	}

	if err := buildSingleWheel(toolSourceDir, destDir); err != nil {
		return "", fmt.Errorf("build test tool wheel: %w", err)
	}

	pyWheelBuildCache[toolSourceDir] = destDir
	return destDir, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func buildSingleWheel(sourceDir, destDir string) error {
	uvPath, err := exec.LookPath("uv")
	if err != nil {
		return fmt.Errorf("uv not found (required for wheel builds): %w", err)
	}

	cmd := exec.Command(uvPath, "build", "--wheel", "--out-dir", destDir)
	cmd.Dir = sourceDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("uv build in %s: %w (output: %s)", sourceDir, err, string(out))
	}
	return nil
}

func findPythonWheelTestToolDir() string {
	return filepath.Join(findModuleRoot(), "internal", "str", "testdata", "python-wheel-test-tool")
}

func findSamToolSDKDir() string {
	modRoot := findModuleRoot()
	return filepath.Join(modRoot, "python", "sam-tool-sdk")
}

func writeSkillMD(skillDir string, sc TestSkillConfig) error {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", sc.Name)
	if sc.Description != "" {
		fmt.Fprintf(&sb, "description: %s\n", sc.Description)
	}
	if len(sc.Tags) > 0 {
		sb.WriteString("tags:\n")
		for _, tag := range sc.Tags {
			fmt.Fprintf(&sb, "  - %s\n", tag)
		}
	}

	var requiredBuiltins []string
	for _, toolCfg := range sc.Tools {
		if toolType, _ := toolCfg["tool_type"].(string); toolType == "builtin" {
			if toolName, _ := toolCfg["tool_name"].(string); toolName != "" {
				requiredBuiltins = append(requiredBuiltins, toolName)
			}
		}
	}
	if len(requiredBuiltins) > 0 {
		sb.WriteString("required_builtins:\n")
		for _, name := range requiredBuiltins {
			fmt.Fprintf(&sb, "  - %s\n", name)
		}
	}
	if len(sc.Toolsets) > 0 {
		sb.WriteString("toolsets:\n")
		for _, name := range sc.Toolsets {
			fmt.Fprintf(&sb, "  - %s\n", name)
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString(sc.InstructionContent)
	sb.WriteString("\n")

	return os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(sb.String()), 0o644)
}
