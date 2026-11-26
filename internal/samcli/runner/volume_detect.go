// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type VolumeDetectionResult struct {
	NeedsVolumes bool

	ToolsDir string

	Namespace string
}

func DetectVolumeRequirements(fullConfig map[string]any) VolumeDetectionResult {
	var result VolumeDetectionResult

	appsRaw, ok := fullConfig["apps"].([]any)
	if !ok {
		return result
	}

	for _, appRaw := range appsRaw {
		appMap, ok := appRaw.(map[string]any)
		if !ok {
			continue
		}

		appCfg, ok := appMap["app_config"].(map[string]any)
		if !ok {
			continue
		}

		for _, key := range []string{"toolsets_dir", "packages_dir", "tools_dir"} {
			if td, ok := appCfg[key].(string); ok && td != "" && result.ToolsDir == "" {
				result.ToolsDir = td
			}
		}
		if ns, ok := appCfg["namespace"].(string); ok && ns != "" && result.Namespace == "" {
			result.Namespace = ns
		}

		if volumes, ok := appCfg["volumes"].([]any); ok && len(volumes) > 0 {
			result.NeedsVolumes = true
		}

		if tvb, ok := appCfg["tool_volume_bindings"].(map[string]any); ok && len(tvb) > 0 {
			result.NeedsVolumes = true
		}

		if toolsRaw, ok := appCfg["tools"].([]any); ok {
			for _, toolRaw := range toolsRaw {
				toolMap, ok := toolRaw.(map[string]any)
				if !ok {
					continue
				}
				if vp, ok := toolMap["volume_params"].([]any); ok && len(vp) > 0 {
					result.NeedsVolumes = true
				}
				if vb, ok := toolMap["volume_bindings"].(map[string]any); ok && len(vb) > 0 {
					result.NeedsVolumes = true
				}
			}
		}
	}

	if result.ToolsDir != "" && !result.NeedsVolumes {
		if scanToolsDirForVolumes(result.ToolsDir) {
			result.NeedsVolumes = true
		}
	}

	if result.Namespace == "" {
		result.Namespace = "solace-agent-mesh"
	}

	return result
}

func scanToolsDirForVolumes(toolsDir string) bool {
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		slog.Default().With("logger_name", "samcli-volume").Debug("cannot read tools dir for volume scan", "dir", toolsDir, "error", err)
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(toolsDir, entry.Name(), "manifest.yaml")
		if scanManifestForVolumes(manifestPath) {
			return true
		}
	}
	return false
}

func scanManifestForVolumes(manifestPath string) bool {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		slog.Default().With("logger_name", "samcli-volume").Debug("cannot read manifest for volume scan", "path", manifestPath, "error", err)
		return false
	}

	var raw struct {
		Tools map[string]map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		slog.Default().With("logger_name", "samcli-volume").Debug("cannot parse manifest for volume scan", "path", manifestPath, "error", err)
		return false
	}

	for _, toolCfg := range raw.Tools {
		if vp, ok := toolCfg["volume_params"].([]any); ok && len(vp) > 0 {
			return true
		}
	}
	return false
}
