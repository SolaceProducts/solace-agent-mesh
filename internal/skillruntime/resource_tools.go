// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"fmt"
	"strings"
)

const (
	ListResourcesToolName = "list_skill_resources"
	ReadResourceToolName  = "read_skill_resource"
	GrepResourcesToolName = "grep_skill_resources"
)

const MaxListedResourcePaths = 50

type ResourceToolSpec struct {
	Name        string
	Description string
	Usage       string
	Parameters  map[string]any
}

const (
	skillNameParamDescription = "Name of the loaded skill: the same value you passed to load_skill as `name`. " +
		"This argument is called skill_name, not name."
	pathParamDescription = "Path of the file to read, relative to the skill and starting with references/ or assets/ " +
		"(e.g. references/api.md). Copy it from the load_skill result, from a list_skill_resources file's `path`, " +
		"or from a grep_skill_resources match's `path`."
)

func ResourceTools() []ResourceToolSpec {
	return []ResourceToolSpec{
		{
			Name: ListResourcesToolName,
			Description: "List every file in a loaded skill's references/ and assets/ directories. " +
				"Arguments: skill_name (required). Each returned file's `path` is the value to pass as `path` to read_skill_resource.",
			Usage: "list_skill_resources takes skill_name (string, required).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill_name": map[string]any{"type": "string", "description": skillNameParamDescription},
				},
				"required": []any{"skill_name"},
			},
		},
		{
			Name: ReadResourceToolName,
			Description: fmt.Sprintf("Read one file from a loaded skill. Arguments: skill_name (required), "+
				"path (required, e.g. references/api.md: a path from the load_skill result, from list_skill_resources, "+
				"or a grep_skill_resources match's `path`), and optional start_line and end_line (1-based integers) "+
				"to read a range, such as a few lines either side of a grep match's `line`. "+
				"Returns at most %d lines and %d KB per call.", MaxReadLines, MaxReadBytes/1024),
			Usage: "read_skill_resource takes skill_name (string, required), path (string, required, e.g. references/api.md) " +
				"and optional start_line and end_line (integers).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill_name": map[string]any{"type": "string", "description": skillNameParamDescription},
					"path":       map[string]any{"type": "string", "description": pathParamDescription},
					"start_line": map[string]any{"type": "integer", "description": "Optional first line to read, 1-based."},
					"end_line":   map[string]any{"type": "integer", "description": "Optional last line to read, 1-based and inclusive."},
				},
				"required": []any{"path", "skill_name"},
			},
		},
		{
			Name: GrepResourcesToolName,
			Description: fmt.Sprintf("Search the files in a loaded skill's references/ and assets/ directories for a regular expression. "+
				"Arguments: skill_name (required), pattern (required), and optional case_insensitive (boolean) and context_lines (integer). "+
				"Returns up to %d matches, each with `path` (also given as `file`), `line` and `text`. "+
				"To read around a match, call read_skill_resource with the same skill_name, path set to the match's `path`, "+
				"and start_line and end_line around its `line`.", MaxGrepResults),
			Usage: "grep_skill_resources takes skill_name (string, required), pattern (string, required) " +
				"and optional case_insensitive (boolean) and context_lines (integer).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill_name": map[string]any{"type": "string", "description": skillNameParamDescription},
					"pattern":    map[string]any{"type": "string", "description": "Regular expression to search for, in Go RE2 syntax."},
					"case_insensitive": map[string]any{
						"type":        "boolean",
						"description": "Whether to ignore case when matching.",
					},
					"context_lines": map[string]any{
						"type":        "integer",
						"description": "Number of lines before and after each match to include in its `text`.",
					},
				},
				"required": []any{"pattern", "skill_name"},
			},
		},
	}
}

func ResourceToolUsage(toolName string) string {
	for _, spec := range ResourceTools() {
		if spec.Name == toolName {
			return spec.Usage
		}
	}
	return ""
}

func DescribeResourcePaths(skillName string, paths []string, total int) string {
	shown := paths[:min(len(paths), MaxListedResourcePaths)]
	described := fmt.Sprintf("Files in skill %q: %s", skillName, strings.Join(shown, ", "))
	if omitted := max(total, len(paths)) - len(shown); omitted > 0 {
		described += fmt.Sprintf(", and %d more (call list_skill_resources with skill_name %q to see them all)", omitted, skillName)
	}
	return described + "."
}

func LoadedResourcesGuidance(skillName string, paths []string, total int) string {
	guidance := fmt.Sprintf("The skill has %d resource files. Read one with read_skill_resource "+
		"(skill_name %q and the file's path); search them with grep_skill_resources (skill_name and pattern), "+
		"whose matches give the `path` and `line` to read; list them with list_skill_resources (skill_name).",
		total, skillName)
	if len(paths) == 0 {
		return guidance + " Call list_skill_resources to see their paths."
	}
	return guidance + " " + DescribeResourcePaths(skillName, paths, total)
}

func listedResourcePaths(ctx SkillContext) ([]string, int) {
	listed := ListResources(ctx)
	paths := make([]string, 0, min(listed.Count, MaxListedResourcePaths))
	for _, f := range listed.Files[:min(listed.Count, MaxListedResourcePaths)] {
		paths = append(paths, f.Path)
	}
	return paths, listed.Count
}

func describeSkillFiles(ctx SkillContext) string {
	paths, total := listedResourcePaths(ctx)
	if total == 0 {
		return fmt.Sprintf("Skill %q has no resource files.", ctx.Name)
	}
	return DescribeResourcePaths(ctx.Name, paths, total)
}
