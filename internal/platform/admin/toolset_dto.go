// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	schemapkg "github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type CreateToolsetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type UpdateToolsetRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ToolsetResponse struct {
	ID              string            `json:"id" cfgapply:"read-only"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Type            string            `json:"type" cfgapply:"read-only"`
	Runtime         *string           `json:"runtime"`
	DiscoveryStatus string            `json:"discoveryStatus,omitempty" cfgapply:"read-only"`
	DiscoveryErrors []string          `json:"discoveryErrors" cfgapply:"read-only"`
	ZipFilename     *string           `json:"zipFilename" cfgapply:"read-only"`
	ZipFileSize     *int64            `json:"zipFileSize" cfgapply:"read-only"`
	ContentHash     string            `json:"contentHash" cfgapply:"read-only"`
	Files           []ToolsetFileResp `json:"files" cfgapply:"read-only"`
	Tools           []ToolsetToolResp `json:"tools" cfgapply:"read-only"`
	Agents          []ToolsetAgentRef `json:"agents" cfgapply:"read-only"`
	ConfigSummary   *ConfigSummary    `json:"configSummary,omitempty" cfgapply:"read-only"`
	Capabilities    map[string]any    `json:"capabilities" cfgapply:"read-only"`
	Default         bool              `json:"default" cfgapply:"read-only"`
	Aliases         []string          `json:"aliases,omitempty" cfgapply:"read-only"`
	CreatedBy       string            `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy       string            `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime     Timestamp         `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime     Timestamp         `json:"updatedAt" cfgapply:"read-only"`
}

type ToolsetFileResp struct {
	ID              string    `json:"id"`
	Filename        *string   `json:"filename"`
	ToolName        string    `json:"toolName"`
	ToolFunction    *string   `json:"toolFunction"`
	ClassName       *string   `json:"className"`
	ToolDescription string    `json:"toolDescription"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	SandboxProfile  string    `json:"sandboxProfile"`
	ModulePath      *string   `json:"modulePath"`
	FileSize        *int64    `json:"fileSize"`
	ConfigSchema    any       `json:"configSchema"`
	Parameters      any       `json:"parameters"`
	CreatedTime     Timestamp `json:"createdAt"`
}

type ToolsetToolResp struct {
	ToolName        string `json:"toolName"`
	ToolDescription string `json:"toolDescription"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	SandboxProfile  string `json:"sandboxProfile"`
	ConfigSchema    any    `json:"configSchema"`
	Parameters      any    `json:"parameters"`
}

type ToolsetAgentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func buildToolsetResponse(p *store.ToolPackage, files []store.ToolPackageFile, agents []store.AgentToolsetRef) *ToolsetResponse {
	resp := &ToolsetResponse{
		ID:              p.ID,
		Name:            p.Name,
		Description:     derefStr(p.Description),
		Type:            "custom",
		Runtime:         p.Runtime,
		DiscoveryStatus: p.DiscoveryStatus,
		ZipFilename:     p.ZipFilename,
		ZipFileSize:     p.ZipFileSize,
		ContentHash:     derefStr(p.ContentHash),
		Agents:          []ToolsetAgentRef{},
		CreatedBy:       p.CreatedBy,
		UpdatedBy:       p.UpdatedBy,
		CreatedTime:     Timestamp(p.CreatedTime),
		UpdatedTime:     Timestamp(p.UpdatedTime),
	}

	if p.DiscoveryErrors != nil && *p.DiscoveryErrors != "" {
		var errs []string
		if json.Unmarshal([]byte(*p.DiscoveryErrors), &errs) == nil {
			resp.DiscoveryErrors = errs
		} else {
			resp.DiscoveryErrors = []string{*p.DiscoveryErrors}
		}
	}
	if resp.DiscoveryErrors == nil {
		resp.DiscoveryErrors = []string{}
	}

	resp.Capabilities = map[string]any{}
	if p.Capabilities != "" {
		var caps map[string]any
		if err := json.Unmarshal([]byte(p.Capabilities), &caps); err == nil && caps != nil {
			resp.Capabilities = caps
		}
	}

	resp.Files = make([]ToolsetFileResp, len(files))
	resp.Tools = make([]ToolsetToolResp, len(files))
	for i, f := range files {
		resp.Files[i] = buildToolsetFileResp(&f)
		resp.Tools[i] = buildToolsetToolResp(&f)
	}

	if len(agents) > 0 {
		resp.Agents = make([]ToolsetAgentRef, len(agents))
		for i, a := range agents {
			resp.Agents[i] = ToolsetAgentRef{
				ID:          a.ID,
				Name:        a.Name,
				Description: a.Description,
			}
		}
	}

	return resp
}

func buildToolsetToolResp(f *store.ToolPackageFile) ToolsetToolResp {
	resp := ToolsetToolResp{
		ToolName:        f.ToolName,
		ToolDescription: derefStr(f.ToolDescription),
		TimeoutSeconds:  f.TimeoutSeconds,
		SandboxProfile:  f.SandboxProfile,
	}
	if f.Parameters != nil && *f.Parameters != "" {
		var params any
		if json.Unmarshal([]byte(*f.Parameters), &params) == nil {
			resp.Parameters = params
		}
	}
	if f.ConfigSchema != nil && *f.ConfigSchema != "" {
		var schema any
		if json.Unmarshal([]byte(*f.ConfigSchema), &schema) == nil {
			resp.ConfigSchema = schema
		}
	}
	return resp
}

func buildToolsetFileResp(f *store.ToolPackageFile) ToolsetFileResp {
	resp := ToolsetFileResp{
		ID:              f.ID,
		ToolName:        f.ToolName,
		ToolDescription: derefStr(f.ToolDescription),
		TimeoutSeconds:  f.TimeoutSeconds,
		SandboxProfile:  f.SandboxProfile,
		CreatedTime:     Timestamp(f.CreatedTime),
	}

	if f.Parameters != nil && *f.Parameters != "" {
		var params any
		if json.Unmarshal([]byte(*f.Parameters), &params) == nil {
			resp.Parameters = params
		}
	}
	if f.ConfigSchema != nil && *f.ConfigSchema != "" {
		var schema any
		if json.Unmarshal([]byte(*f.ConfigSchema), &schema) == nil {
			resp.ConfigSchema = schema
		}
	}

	return resp
}

type ToolsetConfigResponse struct {
	Values    map[string]any `json:"values"`
	UpdatedBy string         `json:"updatedBy"`
	UpdatedAt Timestamp      `json:"updatedAt"`
}

type ToolsetConfigPatchRequest struct {
	Values map[string]any `json:"values"`
}

type ConfigSummary struct {
	RequiredFields            []string `json:"requiredFields"`
	RequiredCoveredByDefaults []string `json:"requiredCoveredByDefaults"`
	RequiresAgentConfig       bool     `json:"requiresAgentConfig"`
}

func buildBuiltinToolsetResponse(id, name, description string, isDefault bool) ToolsetResponse {
	toolNames := builtinToolsetTools[id]
	tools := make([]ToolsetToolResp, 0, len(toolNames))
	for _, toolName := range toolNames {
		tools = append(tools, ToolsetToolResp{ToolName: toolName})
	}
	return ToolsetResponse{
		ID:              id,
		Name:            name,
		Description:     description,
		Type:            "builtin",
		Default:         isDefault,
		Aliases:         schemapkg.BuiltinGroupAliasesFor(id),
		DiscoveryErrors: []string{},
		Files:           []ToolsetFileResp{},
		Tools:           tools,
		Agents:          []ToolsetAgentRef{},
		Capabilities:    map[string]any{},
	}
}
