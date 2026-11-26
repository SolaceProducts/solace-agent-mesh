// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"context"
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type AppEntry struct {
	Name         string
	AppConfig    map[string]any
	BrokerConfig map[string]any
	ModelRaw     any
	ModelAliases map[string]any
}

func LoadFullConfig(ctx context.Context, loader *config.YAMLLoader, path string) (config.Config, error) {
	return loader.Load(ctx, path)
}

func ToolDefaultsOf(cfg config.Config) map[string]map[string]any {
	return ExtractToolDefaults(cfg.GetRaw("tool_defaults"))
}

func LoadFileToolDefaults(ctx context.Context, loader *config.YAMLLoader, path string) map[string]map[string]any {
	cfg, err := LoadFullConfig(ctx, loader, path)
	if err != nil {
		return nil
	}
	return ToolDefaultsOf(cfg)
}

func ExtractToolDefaults(toolDefaultsRaw any) map[string]map[string]any {
	m, ok := toolDefaultsRaw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(m))
	for name, v := range m {
		if block, ok := v.(map[string]any); ok && len(block) > 0 {
			out[name] = block
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func LoadAppEntriesFromConfig(cfg config.Config) ([]AppEntry, error) {
	appsRaw := cfg.GetRaw("apps")
	if appsRaw == nil {
		return nil, fmt.Errorf("no 'apps' key found in config")
	}
	appsList, ok := appsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("'apps' is not a list")
	}
	entries, err := ParseAppList(appsList)
	if err != nil {
		return nil, err
	}
	if aliases := ExtractModelAliases(cfg.GetRaw("shared_config")); len(aliases) > 0 {
		for i := range entries {
			entries[i].ModelAliases = aliases
		}
	}
	return entries, nil
}

func ExtractModelAliases(sharedConfigRaw any) map[string]any {
	list, ok := sharedConfigRaw.([]any)
	if !ok {
		return nil
	}
	aliases := make(map[string]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		modelsVal, hasModels := m["models"]
		if !hasModels {
			continue
		}
		if nested, ok := modelsVal.(map[string]any); ok {
			for k, v := range nested {
				if block, ok := v.(map[string]any); ok {
					aliases[k] = block
				}
			}
			continue
		}
		for k, v := range m {
			if k == "models" {
				continue
			}
			if block, ok := v.(map[string]any); ok {
				aliases[k] = block
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func LoadAppEntries(ctx context.Context, loader *config.YAMLLoader, path string) ([]AppEntry, error) {
	cfg, err := loader.Load(ctx, path)
	if err != nil {
		return nil, err
	}

	appsRaw := cfg.GetRaw("apps")
	if appsRaw == nil {
		return loadAppEntriesFromRawFile(path)
	}

	appsList, ok := appsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("'apps' is not a list")
	}

	entries, err := ParseAppList(appsList)
	if err != nil {
		return nil, err
	}
	if aliases := ExtractModelAliases(cfg.GetRaw("shared_config")); len(aliases) > 0 {
		for i := range entries {
			entries[i].ModelAliases = aliases
		}
	}
	return entries, nil
}

func loadAppEntriesFromRawFile(path string) ([]AppEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	text := config.ExpandVars(string(data), os.LookupEnv)

	var raw any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML list: %w", err)
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected YAML list, got %T", raw)
	}

	return ParseAppList(list)
}

func MergeBundledAppEntries(
	ctx context.Context,
	loader *config.YAMLLoader,
	existing []AppEntry,
	bundled map[string][]byte,
) ([]AppEntry, map[string]map[string]any, error) {
	if len(bundled) == 0 {
		return existing, nil, nil
	}

	taken := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		taken[e.Name] = struct{}{}
	}

	names := make([]string, 0, len(bundled))
	for name := range bundled {
		names = append(names, name)
	}
	slices.Sort(names)

	merged := existing
	var toolDefaults map[string]map[string]any
	for _, name := range names {
		cfg, err := loader.LoadBytes(ctx, bundled[name])
		if err != nil {
			return nil, nil, fmt.Errorf("parsing bundled config %s: %w", name, err)
		}
		entries, err := LoadAppEntriesFromConfig(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("loading bundled config %s: %w", name, err)
		}
		for _, e := range entries {
			if _, dup := taken[e.Name]; dup {
				continue
			}
			taken[e.Name] = struct{}{}
			merged = append(merged, e)
		}
		if td := ToolDefaultsOf(cfg); len(td) > 0 {
			if toolDefaults == nil {
				toolDefaults = make(map[string]map[string]any, len(td))
			}
			for k, v := range td {
				toolDefaults[k] = v
			}
		}
	}
	return merged, toolDefaults, nil
}

func ParseAppList(list []any) ([]AppEntry, error) {
	var entries []AppEntry
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		if _, hasModule := m["app_module"]; hasModule {
			continue
		}

		name, _ := m["name"].(string)
		if name == "" {
			continue
		}

		appCfg, _ := m["app_config"].(map[string]any)
		if appCfg == nil {
			continue
		}

		brokerCfg, _ := m["broker"].(map[string]any)

		entry := AppEntry{
			Name:         name,
			AppConfig:    appCfg,
			BrokerConfig: brokerCfg,
			ModelRaw:     appCfg["model"],
		}
		entries = append(entries, entry)
	}

	return entries, nil
}
