// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ToolConfig struct {
	ToolName string

	Description string

	TimeoutSeconds int

	SandboxProfile string

	Parameters map[string]ParamConfig

	ToolPassthroughConfig map[string]any

	Auth *toolauth.AuthConfig

	VolumeParams []VolumeParamConfig

	RequiredScopes []string

	ArgGuard func(args map[string]any) error

	ArgumentErrorHint func(args map[string]any) string
}

func (cfg *ToolConfig) STRBinary() string {
	if cfg == nil || cfg.ToolPassthroughConfig == nil {
		return ""
	}
	s, _ := cfg.ToolPassthroughConfig["_str_binary"].(string)
	return s
}

type VolumeParamConfig struct {
	Name        string
	Description string
	Mode        string
	MountPath   string
}

type ParamConfig struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`

	FilenameKey string `json:"filename_key,omitempty"`
}

func ParseToolConfig(raw map[string]any) (*ToolConfig, error) {
	cfg := &ToolConfig{
		SandboxProfile: "standard",
	}

	if name, ok := raw["tool_name"].(string); ok && name != "" {
		cfg.ToolName = name
	} else {
		return nil, fmt.Errorf("remote tool config missing required 'tool_name'")
	}

	if desc, ok := raw["tool_description"].(string); ok {
		cfg.Description = desc
	}

	if timeout, ok := raw["timeout_seconds"]; ok {
		switch v := timeout.(type) {
		case int:
			cfg.TimeoutSeconds = v
		case int64:
			cfg.TimeoutSeconds = int(v)
		case float64:
			cfg.TimeoutSeconds = int(v)
		}
	}

	if profile, ok := raw["sandbox_profile"].(string); ok && profile != "" {
		cfg.SandboxProfile = profile
	}

	if tc, ok := raw["tool_config"].(map[string]any); ok {
		cfg.ToolPassthroughConfig = tc
	}

	if authMap, ok := raw["auth"].(map[string]any); ok {
		cfg.Auth = toolauth.ParseAuthConfig(authMap, cfg.ToolName)
		if cfg.Auth != nil && cfg.Auth.Type == "oauth2" {
			if _, explicit := authMap["credential_key"]; !explicit {
				cfg.Auth.CredentialKey = ""
			}
		}
	}

	if params, ok := raw["parameters"].(map[string]any); ok {
		parsed, err := parseParameters(params)
		if err != nil {
			return nil, err
		}
		cfg.Parameters = parsed
	}

	if vpRaw, ok := raw["volume_params"].([]any); ok {
		cfg.VolumeParams = parseVolumeParams(vpRaw)
	}

	cfg.RequiredScopes = parseRequiredScopes(raw["required_scopes"])

	return cfg, nil
}

func parseRequiredScopes(v any) []string {
	switch scopes := v.(type) {
	case []string:
		return slices.Clone(scopes)
	case []any:
		out := make([]string, 0, len(scopes))
		for _, s := range scopes {
			if str, ok := s.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func parseParameters(params map[string]any) (map[string]ParamConfig, error) {
	out := make(map[string]ParamConfig, len(params))
	for name, pRaw := range params {
		pMap, mapOK := pRaw.(map[string]any)
		if !mapOK {
			return nil, fmt.Errorf("parameter %q: expected map, got %T", name, pRaw)
		}
		pc := ParamConfig{Required: true}
		if t, ok := pMap["type"].(string); ok {
			pc.Type = t
		}
		if d, ok := pMap["description"].(string); ok {
			pc.Description = d
		}
		if r, ok := pMap["required"].(bool); ok {
			pc.Required = r
		}
		if def, ok := pMap["default"]; ok {
			pc.Default = def
			if _, explicitRequired := pMap["required"]; !explicitRequired {
				pc.Required = false
			}
		}
		out[name] = pc
	}
	return out, nil
}

func parseVolumeParams(vpRaw []any) []VolumeParamConfig {
	var out []VolumeParamConfig
	for _, item := range vpRaw {
		vpMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		vp := VolumeParamConfig{}
		if v, ok := vpMap["name"].(string); ok {
			vp.Name = v
		}
		if v, ok := vpMap["description"].(string); ok {
			vp.Description = v
		}
		if v, ok := vpMap["mode"].(string); ok {
			vp.Mode = v
		}
		if v, ok := vpMap["mount_path"].(string); ok {
			vp.MountPath = v
		}
		out = append(out, vp)
	}
	return out
}

func (cfg *ToolConfig) BuildJSONSchema() *tool.JSONSchema {
	if len(cfg.Parameters) == 0 {
		return &tool.JSONSchema{
			Type:       "object",
			Properties: map[string]any{},
		}
	}

	properties := make(map[string]any, len(cfg.Parameters))
	var required []string

	for name, pc := range cfg.Parameters {
		prop := map[string]any{
			"description": pc.Description,
		}
		switch pc.Type {
		case "artifact":
			prop["type"] = "string"
		case "artifact_list":
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "string"}
		default:
			prop["type"] = pc.Type
		}
		if pc.Default != nil {
			prop["default"] = pc.Default
		}
		properties[name] = prop
		if pc.Required {
			required = append(required, name)
		}
	}

	slices.Sort(required)
	return &tool.JSONSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}

func (cfg *ToolConfig) ArtifactParamNames() map[string]tool.ArtifactParamInfo {
	result := make(map[string]tool.ArtifactParamInfo)
	for name, pc := range cfg.Parameters {
		switch pc.Type {
		case "artifact":
			result[name] = tool.ArtifactParamInfo{
				IsOptional: !pc.Required,
			}
		case "artifact_list":
			result[name] = tool.ArtifactParamInfo{
				IsList: true,
			}
		case "artifact_struct_list":
			result[name] = tool.ArtifactParamInfo{
				IsList:      true,
				FilenameKey: pc.FilenameKey,
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
