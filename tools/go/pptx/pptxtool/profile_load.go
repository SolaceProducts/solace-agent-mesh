// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

//go:embed profiles/*.json
var profilesFS embed.FS

const defaultProfileName = "neutral"

func profileDisabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "none", "off", "plain", "false":
		return true
	}
	return false
}

func loadProfileFromConfig(tc *sdk.ToolContext) (*Profile, error) {
	logger := slog.Default().With("logger_name", "pptx")
	if path := tc.GetConfigString("template_profile_path", ""); path != "" {
		p, err := LoadProfileFromFile(path)
		if err != nil {
			logger.Warn("failed to load template profile from path", "path", path, "error", err)
			return nil, fmt.Errorf("template_profile_path %q could not be loaded (%w): fix the path, or remove it to use template_profile",
				path, err)
		}
		return p, nil
	}

	name := tc.GetConfigString("template_profile", "")
	if strings.TrimSpace(name) == "" {
		name = defaultProfileName
	}
	if profileDisabled(name) {
		return nil, nil
	}
	p, err := loadBuiltinProfile(name)
	if err != nil {
		logger.Warn("unknown built-in template profile", "name", name, "error", err)
		return nil, fmt.Errorf("template_profile %q is not a built-in profile: use one of %s, or none for plain slides",
			name, strings.Join(builtinProfileNames(), ", "))
	}
	return p, nil
}

func builtinProfileNames() []string {
	entries, err := profilesFS.ReadDir("profiles")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names
}

func loadBuiltinProfile(name string) (*Profile, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")

	data, err := profilesFS.ReadFile("profiles/" + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("built-in profile %q not found: %w", name, err)
	}
	return LoadProfile(data)
}

func loadProfileFromConfigMap(config map[string]any) *Profile {
	if path, ok := config["template_profile_path"]; ok {
		if s, ok := path.(string); ok && s != "" {
			p, err := LoadProfileFromFile(s)
			if err != nil {
				return nil
			}
			return p
		}
	}
	name := defaultProfileName
	if v, ok := config["template_profile"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			name = s
		}
	}
	if profileDisabled(name) {
		return nil
	}
	p, err := loadBuiltinProfile(name)
	if err != nil {
		return nil
	}
	return p
}
