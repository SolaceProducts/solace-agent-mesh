// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

const (
	toolLoadSkill          = "load_skill"
	toolListSkillResources = skillruntime.ListResourcesToolName
	toolReadSkillResource  = skillruntime.ReadResourceToolName
	toolGrepSkillResources = skillruntime.GrepResourcesToolName

	toolInstantiateTemplate     = "instantiate_template"
	toolReadTemplate            = "read_template"
	toolVerifyTemplateCandidate = "verify_template_candidate"
	toolPackageTemplate         = "package_template"
)

func (w *Worker) executeBuiltinSkillTool(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	if w.skillRegistry == nil {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrToolNotAvailable,
			Message: "skill registry not configured (no skills_dir)",
		}
	}

	start := time.Now()

	var result map[string]any
	var toolErr *remotetool.InvokeError

	switch params.ToolName {
	case toolLoadSkill:
		result, toolErr = w.executeLoadSkill(params)
	case toolListSkillResources:
		result, toolErr = w.executeListSkillResources(params)
	case toolReadSkillResource:
		result, toolErr = w.executeReadSkillResource(params)
	case toolGrepSkillResources:
		result, toolErr = w.executeGrepSkillResources(params)
	case toolInstantiateTemplate:
		result, toolErr = w.executeInstantiateTemplate(ctx, params, artifactSvc)
	case toolReadTemplate:
		result, toolErr = w.executeReadTemplate(ctx, params, artifactSvc)
	case toolVerifyTemplateCandidate:
		result, toolErr = w.executeVerifyTemplateCandidate(ctx, params, artifactSvc)
	case toolPackageTemplate:
		result, toolErr = w.executePackageTemplate(ctx, params, artifactSvc)
	default:
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrToolNotAvailable,
			Message: fmt.Sprintf("unknown built-in skill tool: %s", params.ToolName),
		}
	}

	if toolErr != nil {
		return nil, toolErr
	}

	return &remotetool.InvokeResult{
		ToolResult:      result,
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}, nil
}

func (w *Worker) skillContextFor(skillName string) (skillruntime.SkillContext, *remotetool.InvokeError) {
	entry := w.skillRegistry.GetSkill(skillName)
	if entry == nil {
		return skillruntime.SkillContext{}, &remotetool.InvokeError{
			Code:    remotetool.ErrInvalidRequest,
			Message: fmt.Sprintf("skill %q not found", skillName),
		}
	}
	return skillruntime.SkillContext{
		Name:               entry.Name,
		LocalPath:          entry.LocalPath,
		InstructionContent: entry.InstructionContent,
		Description:        entry.Description,
		Tags:               entry.Tags,
		Examples:           entry.Examples,
		RequiredBuiltins:   entry.RequiredBuiltins,
		Tools:              w.expandSkillToolsets(entry),
		Toolsets:           entry.Toolsets,
		BundledTools:       projectBundledTools(entry),
		HasReferences:      entry.HasReferences,
		HasAssets:          entry.HasAssets,
		HasTemplates:       entry.HasTemplates,
		Templates:          entry.Templates,
		ResourceCount:      entry.ResourceCount,
		ContentHash:        entry.ContentHash,
	}, nil
}

func projectBundledTools(entry *SkillEntry) []map[string]any {
	if len(entry.BundledToolManifest) == 0 {
		return nil
	}
	tools := make([]map[string]any, 0, len(entry.BundledToolManifest))
	for name, me := range entry.BundledToolManifest {
		bt := map[string]any{
			"tool_name":   entry.Name + SkillToolSep + name,
			"description": me.Description,
		}
		if me.Parameters != nil {
			bt["parameters"] = me.Parameters
		}
		if me.Instructions != "" {
			bt["instructions"] = me.Instructions
		}
		tools = append(tools, bt)
	}
	return tools
}

func (w *Worker) executeLoadSkill(params *remotetool.InvokeParams) (map[string]any, *remotetool.InvokeError) {
	skillName, _ := params.Args["skill_name"].(string)
	if skillName == "" {
		skillName, _ = params.Args["name"].(string)
	}
	if skillName == "" {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrInvalidRequest,
			Message: "skill_name parameter is required",
		}
	}
	ctx, err := w.skillContextFor(skillName)
	if err != nil {
		return nil, err
	}
	return skillruntime.LoadSkill(ctx).AsMap(), nil
}

func (w *Worker) executeListSkillResources(params *remotetool.InvokeParams) (map[string]any, *remotetool.InvokeError) {
	skillName, _ := params.Args["skill_name"].(string)
	if skillName == "" {
		return nil, missingSkillName(params.ToolName)
	}
	ctx, err := w.skillContextFor(skillName)
	if err != nil {
		return nil, err
	}
	return skillruntime.ListResources(ctx).AsMap(), nil
}

func (w *Worker) executeReadSkillResource(params *remotetool.InvokeParams) (map[string]any, *remotetool.InvokeError) {
	skillName, _ := params.Args["skill_name"].(string)
	if skillName == "" {
		return nil, missingSkillName(params.ToolName)
	}
	ctx, ierr := w.skillContextFor(skillName)
	if ierr != nil {
		return nil, ierr
	}

	path, _ := params.Args["path"].(string)
	startLine := intArg(params.Args, "start_line")
	endLine := intArg(params.Args, "end_line")

	res, err := skillruntime.ReadResource(ctx, path, startLine, endLine)
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	return res.AsMap(), nil
}

func (w *Worker) executeGrepSkillResources(params *remotetool.InvokeParams) (map[string]any, *remotetool.InvokeError) {
	skillName, _ := params.Args["skill_name"].(string)
	if skillName == "" {
		return nil, missingSkillName(params.ToolName)
	}
	pattern, _ := params.Args["pattern"].(string)
	if pattern == "" {
		return nil, &remotetool.InvokeError{
			Code:    remotetool.ErrInvalidRequest,
			Message: "pattern parameter is required. " + skillruntime.ResourceToolUsage(params.ToolName),
		}
	}
	ctx, ierr := w.skillContextFor(skillName)
	if ierr != nil {
		return nil, ierr
	}

	caseInsensitive, _ := params.Args["case_insensitive"].(bool)
	contextLines := intArg(params.Args, "context_lines")

	res, err := skillruntime.GrepResources(ctx, skillruntime.GrepOptions{
		Pattern:         pattern,
		CaseInsensitive: caseInsensitive,
		ContextLines:    contextLines,
	})
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	return res.AsMap(), nil
}

func missingSkillName(toolName string) *remotetool.InvokeError {
	return &remotetool.InvokeError{
		Code:    remotetool.ErrInvalidRequest,
		Message: "skill_name parameter is required. " + skillruntime.ResourceToolUsage(toolName),
	}
}

func intArg(args map[string]any, key string) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(int); ok {
		return v
	}
	return 0
}

func mapSkillRuntimeError(err error) *remotetool.InvokeError {
	switch {
	case errors.Is(err, skillruntime.ErrSkillNotFound),
		errors.Is(err, skillruntime.ErrInvalidPath),
		errors.Is(err, skillruntime.ErrPathOutsideSkill),
		errors.Is(err, skillruntime.ErrPathNotInResources),
		errors.Is(err, skillruntime.ErrNotAFile),
		errors.Is(err, skillruntime.ErrInvalidPattern),
		errors.Is(err, skillruntime.ErrInvalidParam):
		return &remotetool.InvokeError{
			Code:    remotetool.ErrInvalidRequest,
			Message: err.Error(),
		}
	default:
		return &remotetool.InvokeError{
			Code:    remotetool.ErrToolNotAvailable,
			Message: err.Error(),
		}
	}
}

func (w *Worker) registerBuiltinSkillTools() {
	if w.skillRegistry == nil {
		return
	}

	skillTools := []*ManifestEntry{
		{
			ToolName:    toolLoadSkill,
			Description: "Load a skill to retrieve its instructions, metadata, and bundled tools.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill_name": map[string]any{
						"type":        "string",
						"description": "The name of the skill to load.",
					},
				},
				"required": []any{"skill_name"},
			},
			IsBuiltin: true,
		},
	}

	for _, spec := range skillruntime.ResourceTools() {
		skillTools = append(skillTools, &ManifestEntry{
			ToolName:    spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			IsBuiltin:   true,
		})
	}

	skillTools = append(skillTools,
		instantiateTemplateManifest(),
		readTemplateToolManifest(),
		verifyTemplateToolManifest(),
		packageTemplateToolManifest(),
	)

	for _, st := range skillTools {
		w.manifest.RegisterBuiltin(st)
	}
}
