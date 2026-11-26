// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type Provenance struct {
	Category          string
	ConnectorCategory string
	BuiltinCategory   string
}

func SourceConnectorCategory(toolMap map[string]any) (category string, isConnector bool) {
	if c, _ := toolMap["source_connector_category"].(string); c != "" {
		if slices.Contains(taxonomy.ConnectorCategories, c) {
			return c, true
		}
		return "", true
	}
	if SourceConnectorID(toolMap) != "" {
		return "", true
	}
	return "", false
}

func ProvenanceOf(toolMap map[string]any, toolName string) Provenance {
	if category, isConnector := SourceConnectorCategory(toolMap); isConnector {
		return Provenance{
			Category:          taxonomy.ToolCategoryConnector,
			ConnectorCategory: category,
		}
	}
	if strings.Contains(toolName, str.SkillToolSep) {
		return Provenance{Category: taxonomy.ToolCategoryCustom}
	}
	switch toolMap["tool_type"] {
	case ToolTypeBuiltinGroup:
		return Provenance{
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: builtinCategoryOfGroup(toolMap),
		}
	case ToolTypeBuiltin:
		return Provenance{
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: builtinCategoryByToolName[toolName],
		}
	default:
		return Provenance{Category: taxonomy.ToolCategoryCustom}
	}
}

func SkillProvenance() Provenance {
	return Provenance{Category: taxonomy.ToolCategorySkill}
}

func builtinCategoryOfGroup(toolMap map[string]any) string {
	group, _ := toolMap["group_name"].(string)
	category := schema.CanonicalBuiltinGroup(group)
	if slices.Contains(builtinGroupCategories, category) {
		return category
	}
	return ""
}

var builtinGroupCategories = groupCategories()

func groupCategories() []string {
	out := make([]string, 0, len(taxonomy.BuiltinToolCategories))
	for _, category := range taxonomy.BuiltinToolCategories {
		if category != taxonomy.BuiltinCategoryUngrouped {
			out = append(out, category)
		}
	}
	return out
}

var builtinCategoryOverrides = map[string]string{
	"ask_user_question":             "hil_tools",
	"convert_file_to_markdown":      "builtin_file_tools",
	"convert_pdf_to_markdown":       "builtin_file_tools",
	"pdf_extract_text":              "builtin_file_tools",
	"get_current_time":              "builtin_time_tools",
	"platform_list_components":      "observability_platform_read",
	"platform_get_component_config": "observability_platform_read",
	"web_search_google":             "builtin_web_request_tools",
	"transform_data_with_jmespath":  "data_analysis",

	"index_search":            taxonomy.BuiltinCategoryUngrouped,
	"sub_task":                taxonomy.BuiltinCategoryUngrouped,
	"select_voice":            taxonomy.BuiltinCategoryUngrouped,
	"transcribe_audio":        taxonomy.BuiltinCategoryUngrouped,
	"web_search":              taxonomy.BuiltinCategoryUngrouped,
	"redirect_user":           taxonomy.BuiltinCategoryUngrouped,
	"RedirectUser":            taxonomy.BuiltinCategoryUngrouped,
	"scheduled_task_redirect": taxonomy.BuiltinCategoryUngrouped,
}

var builtinCategoryByToolName = indexBuiltinCategories()

func indexBuiltinCategories() map[string]string {
	containing := map[string][]string{}
	for _, category := range builtinGroupCategories {
		for _, name := range LookupBuiltinGroup(category, Options{}) {
			containing[name] = append(containing[name], category)
		}
	}
	index := make(map[string]string, len(containing)+len(builtinCategoryOverrides))
	for name, categories := range containing {
		if len(categories) == 1 {
			index[name] = categories[0]
		}
	}
	for name, category := range builtinCategoryOverrides {
		index[name] = category
	}
	return index
}
