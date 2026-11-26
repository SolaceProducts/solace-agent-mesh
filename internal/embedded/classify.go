// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func warnLegacyManifestPath(name, manifestPath string) {
	slog.Default().With("logger_name", "embedded-classify").Warn("app uses deprecated manifest_path; migrate to tools_dir",
		"app", name, "manifestPath", manifestPath)
}

type EmbeddedApps struct {
	AWE      []samapp.AppEntry
	Gateway  []samapp.AppEntry
	STR      []samapp.AppEntry
	Platform []samapp.AppEntry
	Python   []map[string]any
}

func (e EmbeddedApps) firstBrokerConfig() map[string]any {
	for _, entries := range [][]samapp.AppEntry{e.Platform, e.AWE, e.Gateway, e.STR} {
		if cfg := samapp.FirstBrokerConfig(entries); cfg != nil {
			return cfg
		}
	}
	return nil
}

func runtimeNeedsAWEExecutor(classified EmbeddedApps) bool {
	return len(classified.AWE) > 0 || len(classified.Platform) > 0
}

func anyAppHasArtifactService(apps []samapp.AppEntry) bool {
	for _, app := range apps {
		if _, ok := app.AppConfig["artifact_service"].(map[string]any); ok {
			return true
		}
	}
	return false
}

func ClassifyForEmbedded(apps []any) EmbeddedApps {
	var result EmbeddedApps

	for _, raw := range apps {
		appMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if _, hasModule := appMap["app_module"]; hasModule {
			result.Python = append(result.Python, appMap)
			continue
		}

		name, _ := appMap["name"].(string)
		appConfig, _ := appMap["app_config"].(map[string]any)
		if appConfig == nil || name == "" {
			slog.Default().With("logger_name", "embedded-classify").Warn("skipping app entry with missing name or app_config",
				"hasName", name != "", "hasAppConfig", appConfig != nil)
			continue
		}

		brokerCfg, _ := appMap["broker"].(map[string]any)

		entry := samapp.AppEntry{
			Name:         name,
			AppConfig:    appConfig,
			BrokerConfig: brokerCfg,
			ModelRaw:     appConfig["model"],
		}

		switch {
		case appConfig["component_type"] == "platform":
			result.Platform = append(result.Platform, entry)
		case appConfig["gateway_id"] != nil:
			result.Gateway = append(result.Gateway, entry)
		case appConfig["manifest_paths"] != nil || appConfig["tool_modules"] != nil ||
			appConfig["toolsets_dir"] != nil || appConfig["tools_dir"] != nil || appConfig["packages_dir"] != nil:
			result.STR = append(result.STR, entry)
		case appConfig["manifest_path"] != nil:
			if mp, ok := appConfig["manifest_path"].(string); ok && mp != "" {
				warnLegacyManifestPath(name, mp)
			}
			result.STR = append(result.STR, entry)
		case appConfig["worker_id"] != nil || appConfig["sandbox"] != nil:
			result.STR = append(result.STR, entry)
		default:
			result.AWE = append(result.AWE, entry)
		}
	}

	return result
}
