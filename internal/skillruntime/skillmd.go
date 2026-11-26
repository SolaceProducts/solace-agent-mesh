// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"bufio"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Frontmatter struct {
	Name             string           `yaml:"name"`
	Description      string           `yaml:"description"`
	Tags             []string         `yaml:"tags,omitempty"`
	Examples         []string         `yaml:"examples,omitempty"`
	RequiredBuiltins []string         `yaml:"required_builtins,omitempty"`
	Tools            []map[string]any `yaml:"tools,omitempty"`
	Toolsets         []string         `yaml:"toolsets,omitempty"`
}

func ParseSkillMD(content string) (Frontmatter, string, error) {
	var fm Frontmatter

	if !strings.HasPrefix(content, "---") {
		return fm, content, nil
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Scan()

	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			break
		}
		fmLines = append(fmLines, line)
	}

	if err := yaml.Unmarshal([]byte(strings.Join(fmLines, "\n")), &fm); err != nil {
		return fm, "", fmt.Errorf("parsing frontmatter: %w", err)
	}

	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))

	return fm, body, nil
}

func MergeRequiredBuiltinsIntoTools(requiredBuiltins []string, tools []map[string]any) []map[string]any {
	if len(requiredBuiltins) == 0 {
		return tools
	}
	existing := make(map[string]bool, len(tools))
	for _, t := range tools {
		tt, _ := t["tool_type"].(string)
		tn, _ := t["tool_name"].(string)
		if tt == "builtin" && tn != "" {
			existing[tn] = true
		}
	}
	merged := make([]map[string]any, 0, len(requiredBuiltins)+len(tools))
	for _, name := range requiredBuiltins {
		if existing[name] {
			continue
		}
		merged = append(merged, map[string]any{
			"tool_type": "builtin",
			"tool_name": name,
		})
	}
	merged = append(merged, tools...)
	return merged
}

func ParseBundledToolsManifest(data []byte) ([]map[string]any, error) {
	var raw struct {
		Tools map[string]map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing bundled tools manifest: %w", err)
	}

	tools := make([]map[string]any, 0, len(raw.Tools))
	for name, cfg := range raw.Tools {
		entry := map[string]any{"tool_name": name}
		if desc, ok := cfg["description"].(string); ok {
			entry["description"] = desc
		}
		if params, ok := cfg["parameters"]; ok {
			entry["parameters"] = params
		}
		if instr, ok := cfg["instructions"].(string); ok {
			entry["instructions"] = instr
		}
		tools = append(tools, entry)
	}
	return tools, nil
}
