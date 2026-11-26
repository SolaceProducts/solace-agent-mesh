// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const pythonCodeExecutionFeatureKey = "python_code_execution"

const volumesFeatureKey = "volumes"

type builtinToolsetEntry struct {
	ID          string
	Name        string
	Description string
	Default     bool
	FeatureFlag string
}

var builtinToolsetCatalog = []builtinToolsetEntry{
	{ID: "builtin_artifact_tools", Name: "Artifact Tools", Description: "Create, read, update, and manage artifacts", Default: true},
	{ID: "builtin_web_request_tools", Name: "Web Request Tools", Description: "Make HTTP requests to external services", Default: false},
	{ID: "builtin_research_tools", Name: "Research Tools", Description: "Multi-step web research with planning + single-query Google search", Default: false},
	{ID: "builtin_file_tools", Name: "File Tools", Description: "Read and process files", Default: false},
	{ID: "builtin_image_tools", Name: "Image Tools", Description: "Process and analyze images", Default: false},
	{ID: "builtin_time_tools", Name: "Time Tools", Description: "Get current time and date information", Default: false},
	{ID: "hil_tools", Name: "Human-in-Loop Tools", Description: "Request human input during agent execution", Default: false},
	{ID: "data_analysis", Name: "Data Analysis Tools", Description: "JMESPath transforms, SQL queries, JSON merge, and charting over artifacts", Default: false},
	{ID: "builtin_code_execution", Name: "Code Execution", Description: "Run LLM-generated Python over artifacts in a sandbox (no network)", Default: false, FeatureFlag: pythonCodeExecutionFeatureKey},
	{ID: "builtin_document_tools", Name: "Document Tools", Description: "Create PowerPoint presentations, render styled HTML to high-fidelity PDF, and manipulate PDFs (merge, split, rotate, watermark, encrypt)", Default: false},
	{ID: "builtin_diagram_tools", Name: "Diagram Tools", Description: "Generate diagrams from Mermaid syntax (PNG or SVG)", Default: false},
	{ID: "builtin_media_tools", Name: "Media Tools", Description: "Process audio, video, and images with FFmpeg and ImageMagick", Default: false},
	{ID: "builtin_volume_tools", Name: "Volume Tools", Description: "Read, write, search, and manage files in a mounted SAM volume", Default: false, FeatureFlag: volumesFeatureKey},
}

var builtinToolsetResponses = buildBuiltinToolsetResponses()

func buildBuiltinToolsetResponses() []ToolsetResponse {
	out := make([]ToolsetResponse, len(builtinToolsetCatalog))
	for i, e := range builtinToolsetCatalog {
		out[i] = buildBuiltinToolsetResponse(e.ID, e.Name, e.Description, e.Default)
	}
	return out
}

func visibleBuiltinToolsets(reg *features.Registry) []ToolsetResponse {
	out := make([]ToolsetResponse, 0, len(builtinToolsetCatalog))
	for _, e := range builtinToolsetCatalog {
		if e.FeatureFlag != "" && (reg == nil || !reg.IsEnabled(e.FeatureFlag)) {
			continue
		}
		out = append(out, buildBuiltinToolsetResponse(e.ID, e.Name, e.Description, e.Default))
	}
	return out
}

var builtinToolsetTools = buildBuiltinToolsetTools()

func buildBuiltinToolsetTools() map[string][]string {
	m := make(map[string][]string, len(schema.ValidBuiltinGroups))
	for id := range schema.ValidBuiltinGroups {
		m[id] = toolinit.LookupBuiltinGroup(id, toolinit.Options{})
	}
	return m
}

var builtinToolsetVolumeParams = map[string]map[string]struct{}{
	"builtin_volume_tools": {"workspace": {}},
}

func builtinToolsetAcceptsVolumeBindings(toolsetName string) bool {
	return len(builtinToolsetVolumeParams[toolsetName]) > 0
}

var builtinToolNetworkParams = map[string]map[string]struct{}{}

var (
	googleSearchConfigSchema = []configSchemaField{
		{Key: "google_search_api_key", Type: "string", Secret: true, Description: "Google Custom Search API key"},
		{Key: "google_cse_id", Type: "string", Description: "Google Custom Search engine ID"},
		{Key: "api_base", Type: "string", Description: "Google Custom Search API base URL"},
	}
	datadogConfigSchema = []configSchemaField{
		{Key: "api_key", Type: "string", Secret: true, Description: "Datadog API key"},
		{Key: "app_key", Type: "string", Secret: true, Description: "Datadog application key"},
		{Key: "site", Type: "string", Description: "Datadog site, such as datadoghq.com"},
		{Key: "api_host", Type: "string", Description: "Datadog API host, overriding site"},
		{Key: "cluster_name", Type: "string", Description: "Kubernetes cluster the log queries are scoped to"},
		{Key: "kube_namespace", Type: "string", Description: "Kubernetes namespace the log queries are scoped to"},
	}
	geminiImageConfigSchema = []configSchemaField{
		{Key: "gemini_api_key", Type: "string", Secret: true, Description: "Gemini API key"},
		{Key: "gemini_api_base", Type: "string", Description: "Gemini API base URL"},
		{Key: "model", Type: "string", Description: "Default Gemini image model"},
		{Key: "pro_model", Type: "string", Description: "Gemini image model for pro requests"},
	}
)

var builtinToolConfigSchemas = map[string][]configSchemaField{
	"web_search_google": googleSearchConfigSchema,
	"deep_research":     googleSearchConfigSchema,

	"datadog_query_logs":       datadogConfigSchema,
	"datadog_get_errors":       datadogConfigSchema,
	"correlate_task_with_logs": datadogConfigSchema,

	"create_image_from_description": {
		{Key: "api_key", Type: "string", Secret: true, Description: "Image generation API key"},
		{Key: "api_base", Type: "string", Description: "Image generation API base URL"},
		{Key: "model", Type: "string", Description: "Image generation model"},
	},
	"edit_image_with_gemini":     geminiImageConfigSchema,
	"generate_image_with_gemini": geminiImageConfigSchema,
}

var builtinToolConfigSchemaByTool = indexBuiltinToolConfigSchemas()

func indexBuiltinToolConfigSchemas() map[string]map[string]configSchemaField {
	out := make(map[string]map[string]configSchemaField, len(builtinToolConfigSchemas))
	for tool, fields := range builtinToolConfigSchemas {
		byKey := make(map[string]configSchemaField, len(fields))
		for _, f := range fields {
			byKey[f.Key] = f
		}
		out[tool] = byKey
	}
	return out
}

func builtinToolSecretFields(toolName string) map[string]secretFieldInfo {
	return toolsetSecretFields(builtinToolConfigSchemaByTool[toolName])
}

func builtinToolsetNetworkParamUnion(toolsetName string) map[string]struct{} {
	union := map[string]struct{}{}
	for _, toolName := range toolinit.LookupBuiltinGroup(toolsetName, toolinit.Options{}) {
		for param := range builtinToolNetworkParams[toolName] {
			union[param] = struct{}{}
		}
	}
	return union
}

func builtinToolsetAcceptsNetworkBindings(toolsetName string) bool {
	return len(builtinToolsetNetworkParamUnion(toolsetName)) > 0
}
