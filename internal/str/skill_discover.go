// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type DiscoveredSkills struct {
	SkillsDir string

	ManifestPath string
}

func DiscoverSkills(skillsDir, manifestDir, _ string, logger *slog.Logger) *DiscoveredSkills {
	result := &DiscoveredSkills{
		SkillsDir: skillsDir,
	}

	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		logger.Debug("skills directory does not exist", "dir", skillsDir)
		return result
	}

	registry := NewSkillRegistry(skillsDir)
	if err := registry.Load(); err != nil {
		logger.Warn("skill registry load failed", "error", err)
		return result
	}

	allBundled := registry.AllBundledToolEntries()
	bundledTools := subprocessBundledTools(allBundled, logger)
	if len(bundledTools) > 0 {
		outputDir, dirErr := resolveBundledManifestDir(skillsDir, manifestDir, logger)
		if dirErr != nil {
			logger.Warn("failed to prepare bundled tool manifest dir", "error", dirErr)
		} else {
			manifestPath := filepath.Join(outputDir, bundledManifestFileName(skillsDir))
			if genErr := generateBundledToolManifest(bundledTools, manifestPath, logger); genErr == nil {
				result.ManifestPath = manifestPath
			} else {
				logger.Warn("failed to generate bundled tool manifest", "error", genErr)
			}
		}
	}

	logger.Info("skill discovery complete",
		"dir", skillsDir,
		"skills", len(registry.Skills()),
		"bundledTools", len(allBundled),
		"inProcessTools", len(allBundled)-len(bundledTools))

	return result
}

func resolveBundledManifestDir(skillsDir, manifestDir string, logger *slog.Logger) (string, error) {
	dir := manifestDir
	if dir != "" && isWithin(dir, skillsDir) {
		logger.Warn("skill manifest dir is inside the skills tree; using a writable fallback instead",
			"manifestDir", dir, "skillsDir", skillsDir)
		dir = ""
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache dir for skill manifest: %w", err)
		}
		dir = filepath.Join(cache, "sam", "skill-manifests")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create skill manifest dir %q: %w", dir, err)
	}
	return dir, nil
}

func isWithin(path, root string) bool {
	ap, err1 := filepath.Abs(path)
	ar, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(ar, ap)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func bundledManifestFileName(skillsDir string) string {
	abs, err := filepath.Abs(skillsDir)
	if err != nil {
		abs = skillsDir
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return "skill-bundled-tools-" + hex.EncodeToString(sum[:8]) + ".yaml"
}

func subprocessBundledTools(all map[string]*ManifestEntry, logger *slog.Logger) map[string]*ManifestEntry {
	out := make(map[string]*ManifestEntry, len(all))
	for name, entry := range all {
		if entry.inProcess != nil {
			logger.Debug("skipping in-process skill tool in bundled manifest", "tool", name)
			continue
		}
		out[name] = entry
	}
	return out
}

func generateBundledToolManifest(entries map[string]*ManifestEntry, manifestPath string, logger *slog.Logger) error {
	tools := make(map[string]map[string]any, len(entries))
	for name, entry := range entries {
		toolCfg := map[string]any{
			"executable": entry.Executable,
		}
		if entry.ToolDir != "" {
			toolCfg["tool_dir"] = entry.ToolDir
		}
		if entry.Description != "" {
			toolCfg["description"] = entry.Description
		}
		if entry.TimeoutSeconds > SkillBundledToolMaxUsefulTimeoutSeconds {
			logger.Warn("skill-bundled tool declares a timeout beyond the agent-side wait window; "+
				"the invocation will be abandoned before the tool can finish",
				"tool", name,
				"timeout_s", entry.TimeoutSeconds,
				"agentWaitWindow_s", SkillBundledToolMaxUsefulTimeoutSeconds)
		}
		if entry.TimeoutSeconds > 0 {
			toolCfg["timeout_seconds"] = entry.TimeoutSeconds
		}
		if entry.SandboxProfile != "" {
			toolCfg["sandbox_profile"] = entry.SandboxProfile
		}
		if entry.DeclaredEffect != "" {
			toolCfg["effect"] = entry.DeclaredEffect
		}
		tools[name] = toolCfg
	}

	return writeManifestYAML(tools, manifestPath)
}

func writeManifestYAML(tools map[string]map[string]any, manifestPath string) error {
	combined := map[string]any{
		"version": 1,
		"tools":   tools,
	}

	data, err := yaml.Marshal(combined)
	if err != nil {
		return err
	}

	tmpPath := manifestPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, manifestPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
