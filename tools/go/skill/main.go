// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const mountPath = "/skill"

func main() {
	sdk.Run(
		sdk.NewTool("load_skill",
			"Load a skill by name, returning its instructions and bundled tool configurations",
			loadSkill,
			sdk.WithInstructions(loadSkillInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool(skillruntime.ListResourcesToolName,
			resourceToolDescription(skillruntime.ListResourcesToolName),
			listSkillResources,
			sdk.WithInstructions(listSkillResourcesInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool(skillruntime.ReadResourceToolName,
			resourceToolDescription(skillruntime.ReadResourceToolName),
			readSkillResource,
			sdk.WithInstructions(readSkillResourceInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool(skillruntime.GrepResourcesToolName,
			resourceToolDescription(skillruntime.GrepResourcesToolName),
			grepSkillResources,
			sdk.WithInstructions(grepSkillResourcesInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	)
}

func resourceToolDescription(toolName string) string {
	for _, spec := range skillruntime.ResourceTools() {
		if spec.Name == toolName {
			return spec.Description
		}
	}
	return ""
}

func missingSkillName(toolName string) *sdk.Result {
	return sdk.Error("skill_name is required. " + skillruntime.ResourceToolUsage(toolName))
}

func resolveSkillContext(skillName string, tc *sdk.ToolContext) (skillruntime.SkillContext, error) {
	if info, err := os.Stat(mountPath); err == nil && info.IsDir() {
		return skillruntime.LoadFromMount(mountPath, skillName)
	}
	skillsDir := tc.GetConfigString("skills_dir", "")
	if skillsDir == "" {
		skillsDir = os.Getenv("SAM_SKILLS_DIR")
	}
	if skillsDir == "" {
		return skillruntime.SkillContext{}, fmt.Errorf(
			"skills directory not configured (set skills_dir in tool_config or SAM_SKILLS_DIR env var)",
		)
	}
	ctx, err := skillruntime.LoadFromDisk(skillsDir, skillName)
	if err == nil {
		return ctx, nil
	}
	if !errors.Is(err, skillruntime.ErrSkillNotFound) {
		return ctx, err
	}

	builtinDir := tc.GetConfigString("builtin_skills_dir", "")
	if builtinDir == "" {
		builtinDir = os.Getenv("SAM_BUILTIN_SKILLS_DIR")
	}
	if builtinDir != "" && builtinDir != skillsDir {
		return skillruntime.LoadFromDisk(builtinDir, skillName)
	}
	return ctx, err
}

func errorResult(err error) *sdk.Result {
	if err == nil {
		return sdk.Error("unknown error")
	}
	if errors.Is(err, skillruntime.ErrSkillNotFound) {
		return sdk.Error(err.Error())
	}
	return sdk.Error(err.Error())
}

type LoadSkillParams struct {
	SkillName string `json:"skill_name" desc:"Name of the skill to load" required:"true"`
}

func loadSkill(_ context.Context, p LoadSkillParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.SkillName == "" {
		return sdk.Error("skill_name is required"), nil
	}
	ctx, err := resolveSkillContext(p.SkillName, tc)
	if err != nil {
		return errorResult(err), nil
	}
	res := skillruntime.LoadSkill(ctx)
	return sdk.OK("Skill loaded successfully", sdk.WithData(res.AsMap())), nil
}

type ListSkillResourcesParams struct {
	SkillName string `json:"skill_name" desc:"Name of the loaded skill: the same value you passed to load_skill as name" required:"true"`
}

func listSkillResources(_ context.Context, p ListSkillResourcesParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.SkillName == "" {
		return missingSkillName(skillruntime.ListResourcesToolName), nil
	}
	ctx, err := resolveSkillContext(p.SkillName, tc)
	if err != nil {
		return errorResult(err), nil
	}
	res := skillruntime.ListResources(ctx)
	return sdk.OK(fmt.Sprintf("Found %d resource files", res.Count),
		sdk.WithData(res.AsMap())), nil
}

type ReadSkillResourceParams struct {
	SkillName string `json:"skill_name" desc:"Name of the loaded skill: the same value you passed to load_skill as name" required:"true"`
	Path      string `json:"path" desc:"Path of the file to read, starting with references/ or assets/ (e.g. references/auth.md): a path from the load_skill result, from list_skill_resources, or a grep_skill_resources match's path" required:"true"`
	StartLine *int   `json:"start_line" desc:"Optional first line to read, 1-based"`
	EndLine   *int   `json:"end_line" desc:"Optional last line to read, 1-based and inclusive"`
}

func readSkillResource(_ context.Context, p ReadSkillResourceParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.SkillName == "" {
		return missingSkillName(skillruntime.ReadResourceToolName), nil
	}
	ctx, err := resolveSkillContext(p.SkillName, tc)
	if err != nil {
		return errorResult(err), nil
	}
	res, err := skillruntime.ReadResource(ctx, p.Path, derefInt(p.StartLine), derefInt(p.EndLine))
	if err != nil {
		return errorResult(err), nil
	}
	return sdk.OK("File read successfully", sdk.WithData(res.AsMap())), nil
}

type GrepSkillResourcesParams struct {
	SkillName       string `json:"skill_name" desc:"Name of the loaded skill: the same value you passed to load_skill as name" required:"true"`
	Pattern         string `json:"pattern" desc:"Regular expression pattern to search for" required:"true"`
	CaseInsensitive *bool  `json:"case_insensitive" desc:"Whether to ignore case when matching"`
	ContextLines    *int   `json:"context_lines" desc:"Number of lines to include before and after each match in the returned text (default 0)"`
}

func grepSkillResources(_ context.Context, p GrepSkillResourcesParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.SkillName == "" {
		return missingSkillName(skillruntime.GrepResourcesToolName), nil
	}
	if p.Pattern == "" {
		return sdk.Error("pattern is required. " + skillruntime.ResourceToolUsage(skillruntime.GrepResourcesToolName)), nil
	}
	ctx, err := resolveSkillContext(p.SkillName, tc)
	if err != nil {
		return errorResult(err), nil
	}
	res, err := skillruntime.GrepResources(ctx, skillruntime.GrepOptions{
		Pattern:         p.Pattern,
		CaseInsensitive: derefBool(p.CaseInsensitive),
		ContextLines:    derefInt(p.ContextLines),
	})
	if err != nil {
		return errorResult(err), nil
	}
	return sdk.OK(fmt.Sprintf("Found %d matches", res.MatchCount),
		sdk.WithData(res.AsMap())), nil
}

const loadSkillInstructions = `Loads a skill by name, returning its SKILL.md instructions and any bundled tool configurations.
The returned instructions should be injected into your context. Bundled tools will be registered
and available for use after loading.`

const listSkillResourcesInstructions = `Lists all files in a skill's references/ and assets/ directories.
Use this to discover what reference material and assets are available within a loaded skill.
Each file's path is the value to pass as path to read_skill_resource.`

const readSkillResourceInstructions = `Reads the content of a specific file from a skill's references/ or assets/ directory.
Supports an optional 1-based start_line/end_line range for paginated reading. Capped at 500 lines and 256KB per response.`

const grepSkillResourcesInstructions = `Searches for a regex pattern across all files in a skill's references/ and assets/ directories.
Returns matching lines with their path (also given as file) and 1-based line numbers. Supports case-insensitive
search and context lines around matches. Maximum 100 results returned. Pass a match's path and line on to
read_skill_resource as path and start_line/end_line to read around it.`

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func derefBool(b *bool) bool {
	return b != nil && *b
}
