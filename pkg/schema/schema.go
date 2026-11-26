// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed schemas/*.yaml
var schemasFS embed.FS

const a2aAgentSchemaName = "a2a-agent"

var SchemaNames = []string{"agent", "workflow", "tool", "gateway", "connector", "skill", a2aAgentSchemaName}

func SchemaFiles() map[string][]byte {
	result := make(map[string][]byte)
	_ = fs.WalkDir(schemasFS, "schemas", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := schemasFS.ReadFile(path)
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		result[name] = data
		return nil
	})
	return result
}

func SchemaContent(name string) ([]byte, error) {
	filename := name + ".schema.yaml"
	data, err := schemasFS.ReadFile("schemas/" + filename)
	if err != nil {
		return nil, fmt.Errorf("schema %q not found: %w", name, err)
	}
	return data, nil
}

func SkillSchemaMapping() map[string]string {
	return map[string]string{
		"agent.schema.yaml":     filepath.Join("sam-agent-schema", "references", "agent-config-schema.yaml"),
		"workflow.schema.yaml":  filepath.Join("sam-workflow-schema", "references", "workflow-config-schema.yaml"),
		"tool.schema.yaml":      filepath.Join("sam-tool-schema", "references", "tool-config-schema.yaml"),
		"gateway.schema.yaml":   filepath.Join("sam-gateway-schema", "references", "gateway-config-schema.yaml"),
		"connector.schema.yaml": filepath.Join("sam-connector-schema", "references", "connector-config-schema.yaml"),
		"skill.schema.yaml":     filepath.Join("sam-skill-schema", "references", "skill-config-schema.yaml"),
		"a2a-agent.schema.yaml": filepath.Join("sam-a2a-agent-schema", "references", "a2a-agent-config-schema.yaml"),
	}
}

func SkillSchemaInline() map[string]string {
	return map[string]string{
		"{{SCHEMA:agent}}":     "agent",
		"{{SCHEMA:tool}}":      "tool",
		"{{SCHEMA:workflow}}":  "workflow",
		"{{SCHEMA:gateway}}":   "gateway",
		"{{SCHEMA:connector}}": "connector",
		"{{SCHEMA:skill}}":     "skill",
		"{{SCHEMA:a2a-agent}}": a2aAgentSchemaName,
	}
}

func SchemaContentFiltered(name string) ([]byte, error) {
	raw, err := SchemaContent(name)
	if err != nil {
		return nil, err
	}
	if name == "connector" || name == "skill" || name == a2aAgentSchemaName {
		return raw, nil
	}
	filtered, err := FilterSystemFields(raw)
	if err != nil {
		return nil, err
	}
	if name == "tool" {
		return injectToolEnums(filtered)
	}
	return filtered, nil
}

func SchemaFilesFiltered() map[string][]byte {
	files := SchemaFiles()
	result := make(map[string][]byte, len(files))
	for name, data := range files {
		if name == "connector.schema.yaml" || name == "skill.schema.yaml" || name == "a2a-agent.schema.yaml" {
			result[name] = data
			continue
		}
		filtered, err := FilterSystemFields(data)
		if err != nil {
			result[name] = data
			continue
		}
		if name == "tool.schema.yaml" {
			if injected, err := injectToolEnums(filtered); err == nil {
				filtered = injected
			}
		}
		result[name] = filtered
	}
	return result
}

func injectToolEnums(schemaYAML []byte) ([]byte, error) {
	var schema map[string]any
	if err := yaml.Unmarshal(schemaYAML, &schema); err != nil {
		return nil, fmt.Errorf("inject tool enums: unmarshal: %w", err)
	}

	defs := yamlMapGet(schema, "$defs")
	if defs == nil {
		return schemaYAML, nil
	}

	if bg := yamlMapGet(defs, "builtin-group"); bg != nil {
		if props := yamlMapGet(bg, "properties"); props != nil {
			if gn := yamlMapGet(props, "group_name"); gn != nil {
				groups := make([]string, 0, len(ValidBuiltinGroups))
				for name := range ValidBuiltinGroups {
					groups = append(groups, name)
				}
				slices.Sort(groups)

				enumList := make([]any, len(groups))
				for i, g := range groups {
					enumList[i] = g
				}
				gn["enum"] = enumList

				var desc strings.Builder
				desc.WriteString("Groups:\n")
				for _, g := range groups {
					fmt.Fprintf(&desc, "  %s — %s\n", g, ValidBuiltinGroups[g].Tools)
				}
				gn["description"] = desc.String()
			}
		}
	}

	if bi := yamlMapGet(defs, "builtin"); bi != nil {
		if props := yamlMapGet(bi, "properties"); props != nil {
			if tn := yamlMapGet(props, "tool_name"); tn != nil {
				names := make([]string, 0, len(ValidBuiltinToolNames))
				for name := range ValidBuiltinToolNames {
					if !UngroupedBuiltinToolNames[name] {
						names = append(names, name)
					}
				}
				slices.Sort(names)
				tn["description"] = fmt.Sprintf(
					"Available built-in tools — use these exact snake_case names, do not change the casing.\n"+
						"The Builder can bind a tool only when a built-in group registers it, and that includes the Secure "+
						"Tool Runtime (STR) tools a group lists (create_chart_from_plotly_config, ffmpeg, mermaid_diagram_generator). "+
						"For the Builder, a tool no group registers is reached through a skill (the skill's tools come with it under skills:) "+
						"or through a connector (execute_sql_query is the runtime behind SQL connectors), not by naming it here. "+
						"For ordinary capabilities (web, artifacts, time, data, images, research) pick a name below "+
						"or a builtin-group instead:\n  %s",
					strings.Join(names, ", "),
				)
			}
		}
	}

	return yaml.Marshal(schema)
}

func yamlMapGet(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return sub
}

func DetectComponentType(appConfig map[string]any) string {
	if at, ok := appConfig["agent_type"]; ok {
		if s, ok := at.(string); ok && strings.EqualFold(s, "workflow") {
			return "workflow"
		}
	}
	if _, hasHandlers := appConfig["event_handlers"]; hasHandlers {
		return "gateway"
	}
	if _, hasPort := appConfig["fastapi_port"]; hasPort {
		if _, hasAgentName := appConfig["agent_name"]; !hasAgentName {
			return "gateway"
		}
	}
	if _, hasAgentName := appConfig["agent_name"]; !hasAgentName {
		return "gateway"
	}
	return "agent"
}
