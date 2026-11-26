// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolbind"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type ValidateComponentConfigTool struct {
	tool.ReadOnlyTool
	artifactToolBase
	ToolHasToolset func(tool string) bool
}

func (t *ValidateComponentConfigTool) Name() string { return "validate_component_config" }

const (
	SAMAgentConfigMIMEType     = "application/vnd.sam-agent-config+yaml"
	SAMWorkflowConfigMIMEType  = "application/vnd.sam-workflow-config+yaml"
	SAMGatewayConfigMIMEType   = "application/vnd.sam-gateway-config+yaml"
	SAMConnectorConfigMIMEType = "application/vnd.sam-connector-config+json"
	SAMBuildManifestMIMEType   = "application/vnd.sam-build-manifest+yaml"
	SAMSkillConfigMIMEType     = "application/vnd.sam-skill-config+yaml"
	SAMA2AAgentConfigMIMEType  = "application/vnd.sam-a2a-agent-config+json"
)

var validConfigMIMETypes = map[string]string{
	SAMAgentConfigMIMEType:     "agent",
	SAMWorkflowConfigMIMEType:  "workflow",
	SAMGatewayConfigMIMEType:   "gateway",
	SAMConnectorConfigMIMEType: "connector",
	SAMSkillConfigMIMEType:     "skill",
	SAMA2AAgentConfigMIMEType:  "a2a_agent",
}

func IsValidConfigMIMEType(mime string) bool {
	_, ok := validConfigMIMETypes[mime]
	return ok
}

func MIMETypeForComponentType(compType string) string {
	for mime, ct := range validConfigMIMETypes {
		if ct == compType {
			return mime
		}
	}
	return ""
}

func (t *ValidateComponentConfigTool) Description() string {
	return "Validates a SAM component configuration against its JSON Schema. " +
		"Auto-detects component type (agent, workflow, gateway, connector, skill, or a2a_agent). " +
		"Optionally validates the MIME type is a recognized SAM config type. " +
		"For connectors (application/vnd.sam-connector-config+json) and remote A2A agents " +
		"(application/vnd.sam-a2a-agent-config+json), pass JSON instead of YAML. " +
		"Returns structured validation results with field paths for any errors. " +
		"Use this before saving a config artifact to catch structural issues early."
}

func (t *ValidateComponentConfigTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"config_yaml": map[string]any{
				"type": "string",
				"description": "The configuration to validate. For agents/workflows/entrypoints, " +
					"use YAML with the standard apps[].app_config structure. " +
					"For connectors, use JSON with {name, type, subtype, values} structure.",
			},
			"mime_type": map[string]any{
				"type": "string",
				"description": "Optional MIME type to validate. Must be one of the recognized SAM config MIME types " +
					"(e.g., application/vnd.sam-agent-config+yaml, application/vnd.sam-connector-config+json).",
			},
		},
		Required: []string{"config_yaml"},
	}
}

func (t *ValidateComponentConfigTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "valid", typ: "boolean", description: "Whether the config passed schema validation."},
		resultField{name: "component_type", typ: "string", description: "The detected component kind: agent, workflow, gateway, connector, skill or a2a_agent. Absent when the MIME type was rejected before parsing.", optional: true},
		resultField{name: "errors", typ: "array", description: "Why validation failed. Usually {path, message} entries; the unrecognised-MIME-type path writes plain strings instead. Present only when valid is false.", optional: true},
		resultField{name: "warnings", typ: "array", description: "Non-fatal {path, message} findings. Written only when there is at least one, and only for YAML component configs.", optional: true},
		resultField{name: "mime_type", typ: "string", description: "The rejected MIME type, echoed back. Present only when the MIME type was not a recognised SAM config type.", optional: true},
		resultField{name: "mime_type_valid", typ: "boolean", description: "Always true when present; written whenever a recognised mime_type argument was supplied.", optional: true},
	)
}

func (t *ValidateComponentConfigTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	configStr, _ := args["config_yaml"].(string)
	if configStr == "" {
		return tool.Error("config_yaml is required and must be non-empty"), nil
	}

	if buildManifestDefinitelyAbsent(ctx, toolCtx) {
		return tool.Error(noBuildManifestRefusal), nil
	}

	mimeType, _ := args["mime_type"].(string)
	if mimeType != "" && !IsValidConfigMIMEType(mimeType) {
		return tool.OK(
			"MIME type validation failed",
			tool.WithData(map[string]any{
				"valid":     false,
				"mime_type": mimeType,
				"errors":    []string{"MIME type must be a recognized SAM config type (e.g., application/vnd.sam-agent-config+yaml)"},
			}),
		), nil
	}

	if mimeType == SAMConnectorConfigMIMEType {
		return t.validateConnector(ctx, toolCtx, configStr, mimeType)
	}

	if mimeType == SAMSkillConfigMIMEType {
		return t.validateSkill(configStr, mimeType)
	}

	if mimeType == SAMA2AAgentConfigMIMEType {
		return t.validateA2AAgent(configStr, mimeType)
	}

	result, err := schema.ValidateConfig([]byte(configStr))
	if err != nil {
		return tool.Error("validation failed: " + err.Error()), nil
	}

	reconcileToolBinding(result, []byte(configStr), t.ToolHasToolset)
	refuseUnsavedModelKeys(result, []byte(configStr))

	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	data := map[string]any{
		"valid":          result.Valid,
		"component_type": result.ComponentType,
	}
	if mimeType != "" {
		data["mime_type_valid"] = true
	}
	if len(result.Warnings) > 0 {
		data["warnings"] = result.Warnings
	}

	if result.Valid {
		msg := "Configuration is valid (" + componentDisplayWord(result.ComponentType) + ")"
		if len(result.Warnings) > 0 {
			msg += " with warnings"
		}
		return tool.OK(msg, tool.WithData(data)), nil
	}

	data["errors"] = result.Errors
	return tool.OK(
		string(resultJSON),
		tool.WithData(data),
	), nil
}

const noBuildManifestRefusal = "No build_manifest.yaml exists in this session, so this config cannot be used: " +
	"the plan is what the canvas renders and what Build & Activate iterates, and a component " +
	"missing from it is silently never saved. " +
	"If you are the lead Builder, save build_manifest.yaml (Phase 2) before generating any component config, then validate again. " +
	"If you are running as a sub-task, stop and return this message to the lead — do not write a manifest yourself."

func buildManifestDefinitelyAbsent(ctx context.Context, toolCtx *tool.Context) bool {
	if toolCtx == nil {
		return false
	}
	versions, err := toolCtx.Facade().ListArtifactVersions(ctx, manifestFilename)
	if err != nil {
		slog.Default().With("logger_name", "validate_component_config").Warn(
			"validate_component_config: build manifest lookup failed, allowing the config without the plan precondition",
			"filename", manifestFilename, "error", err)
		return false
	}
	return len(versions) == 0
}

func reconcileToolBinding(result *schema.ValidationResult, yamlBytes []byte, hasToolset func(string) bool) {
	if hasToolset == nil {
		return
	}
	apps, err := schema.ParseAppsConfig(yamlBytes)
	if err != nil {
		return
	}
	judged := map[string]struct{}{}
	for appIdx, app := range apps {
		appConfig, _ := app["app_config"].(map[string]any)
		if schema.DetectComponentType(appConfig) != "agent" {
			continue
		}
		entries, _ := appConfig["tools"].([]any)
		for i, raw := range entries {
			entry, _ := raw.(map[string]any)
			if entry["tool_type"] != "builtin" {
				continue
			}
			name, _ := entry["tool_name"].(string)
			if name == "" {
				continue
			}
			path := fmt.Sprintf("apps[%d].app_config/tools[%d]/tool_name", appIdx, i)
			judged[path] = struct{}{}
			if hasToolset(name) {
				continue
			}
			result.Valid = false
			result.Errors = append(result.Errors, schema.ValidationError{
				Path:    path,
				Message: fmt.Sprintf("tool %q cannot be bound to an agent: %s", name, toolbind.Advice(name)),
			})
		}
	}
	result.Warnings = slices.DeleteFunc(result.Warnings, func(w schema.ValidationError) bool {
		_, judgedHere := judged[w.Path]
		return judgedHere
	})
}

func refuseUnsavedModelKeys(result *schema.ValidationResult, yamlBytes []byte) {
	apps, err := schema.ParseAppsConfig(yamlBytes)
	if err != nil {
		return
	}
	for appIdx, app := range apps {
		appConfig, _ := app["app_config"].(map[string]any)
		if schema.DetectComponentType(appConfig) != "agent" {
			continue
		}
		if problems := schema.AgentModelKeyErrors(app, appConfig, appIdx); len(problems) > 0 {
			result.Valid = false
			result.Errors = append(result.Errors, problems...)
		}
	}
}

func (t *ValidateComponentConfigTool) formatValidationResult(result *schema.ValidationResult, err error, errLabel, mimeType string) (*tool.Result, error) {
	if err != nil {
		return tool.Error(errLabel + " validation failed: " + err.Error()), nil
	}
	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	data := map[string]any{
		"valid":          result.Valid,
		"component_type": result.ComponentType,
	}
	if mimeType != "" {
		data["mime_type_valid"] = true
	}
	if result.Valid {
		return tool.OK("Configuration is valid ("+componentDisplayWord(result.ComponentType)+")", tool.WithData(data)), nil
	}
	data["errors"] = result.Errors
	return tool.OK(string(resultJSON), tool.WithData(data)), nil
}

func (t *ValidateComponentConfigTool) validateConnector(ctx context.Context, toolCtx *tool.Context, configJSON string, mimeType string) (*tool.Result, error) {
	result, err := schema.ValidateConnectorConfig([]byte(configJSON))
	if err == nil && result != nil {
		var cfg map[string]any
		if json.Unmarshal([]byte(configJSON), &cfg) == nil {
			if msg := openAPISpecProblem(ctx, toolCtx, cfg); msg != "" {
				result.Valid = false
				result.Errors = append(result.Errors, schema.ValidationError{
					Path:    "/" + strings.ReplaceAll(specProblemField(cfg), ".", "/"),
					Message: msg,
				})
			}
		}
	}
	return t.formatValidationResult(result, err, "connector", mimeType)
}

func (t *ValidateComponentConfigTool) validateA2AAgent(configJSON string, mimeType string) (*tool.Result, error) {
	result, err := schema.ValidateA2AAgentConfig([]byte(configJSON))
	return t.formatValidationResult(result, err, "a2a agent", mimeType)
}

func (t *ValidateComponentConfigTool) validateSkill(configYAML string, mimeType string) (*tool.Result, error) {
	result, err := schema.ValidateSkillConfig([]byte(configYAML))
	return t.formatValidationResult(result, err, "skill", mimeType)
}
