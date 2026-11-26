// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type LoadResult struct {
	SkillName        string           `json:"skill_name"`
	Instructions     string           `json:"instructions"`
	HasReferences    bool             `json:"has_references"`
	HasAssets        bool             `json:"has_assets"`
	HasTemplates     bool             `json:"has_templates,omitempty"`
	Templates        []TemplateInfo   `json:"templates,omitempty"`
	ResourceCount    int              `json:"resource_count"`
	ResourceFiles    []string         `json:"resource_files,omitempty"`
	Metadata         map[string]any   `json:"metadata,omitempty"`
	BundledTools     []map[string]any `json:"bundled_tools,omitempty"`
	RequiredBuiltins []string         `json:"required_builtins,omitempty"`
	Tools            []map[string]any `json:"tools,omitempty"`
	ContentHash      string           `json:"content_hash,omitempty"`
}

func (r LoadResult) AsMap() map[string]any {
	out := map[string]any{
		"skill_name":     r.SkillName,
		"instructions":   r.Instructions,
		"has_references": r.HasReferences,
		"has_assets":     r.HasAssets,
		"resource_count": r.ResourceCount,
	}
	if len(r.ResourceFiles) > 0 {
		out["resource_files"] = r.ResourceFiles
	}
	if r.HasTemplates {
		out["has_templates"] = true
	}
	if len(r.Templates) > 0 {
		out["templates"] = r.Templates
	}
	if len(r.Metadata) > 0 {
		out["metadata"] = r.Metadata
	}
	if len(r.BundledTools) > 0 {
		out["bundled_tools"] = r.BundledTools
	}
	if len(r.RequiredBuiltins) > 0 {
		out["required_builtins"] = r.RequiredBuiltins
	}
	if len(r.Tools) > 0 {
		out["tools"] = r.Tools
	}
	if r.ContentHash != "" {
		out["content_hash"] = r.ContentHash
	}
	return out
}

func LoadSkill(ctx SkillContext) LoadResult {
	res := LoadResult{
		SkillName:        ctx.Name,
		Instructions:     ctx.InstructionContent,
		HasReferences:    ctx.HasReferences,
		HasAssets:        ctx.HasAssets,
		HasTemplates:     ctx.HasTemplates,
		Templates:        ctx.Templates,
		ResourceCount:    ctx.ResourceCount,
		BundledTools:     ctx.BundledTools,
		RequiredBuiltins: ctx.RequiredBuiltins,
		Tools:            ctx.Tools,
		ContentHash:      ctx.ContentHash,
	}
	res.Metadata = buildMetadata(ctx)
	if ctx.ResourceCount > 0 {
		res.ResourceFiles, _ = listedResourcePaths(ctx)
	}
	return res
}

func buildMetadata(ctx SkillContext) map[string]any {
	if ctx.Description == "" && len(ctx.Tags) == 0 && len(ctx.Examples) == 0 {
		return nil
	}
	m := map[string]any{"name": ctx.Name}
	if ctx.Description != "" {
		m["description"] = ctx.Description
	}
	if len(ctx.Tags) > 0 {
		m["tags"] = ctx.Tags
	}
	if len(ctx.Examples) > 0 {
		m["examples"] = ctx.Examples
	}
	return m
}

func LoadFromDisk(skillsDir, skillName string) (SkillContext, error) {
	if skillsDir == "" {
		return SkillContext{}, fmt.Errorf("%w: skills directory not configured", ErrInvalidParam)
	}
	if skillName == "" {
		return SkillContext{}, fmt.Errorf("%w: skill name is required", ErrInvalidParam)
	}

	cleanSkillsDir := filepath.Clean(skillsDir)
	skillDir := filepath.Clean(filepath.Join(cleanSkillsDir, skillName))
	if skillDir != cleanSkillsDir &&
		!strings.HasPrefix(skillDir, cleanSkillsDir+string(filepath.Separator)) {
		return SkillContext{}, fmt.Errorf("%w: skill name %q", ErrPathOutsideSkill, skillName)
	}

	info, err := os.Stat(skillDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SkillContext{}, fmt.Errorf("%w: %q in %s", ErrSkillNotFound, skillName, cleanSkillsDir)
		}
		return SkillContext{}, fmt.Errorf("stat %s: %w", skillDir, err)
	}
	if !info.IsDir() {
		return SkillContext{}, fmt.Errorf("%w: %q is not a directory", ErrSkillNotFound, skillName)
	}

	return loadFromSkillDir(skillName, skillDir)
}

func LoadFromMount(mountPath, skillName string) (SkillContext, error) {
	if skillName == "" {
		return SkillContext{}, fmt.Errorf("%w: skill name is required", ErrInvalidParam)
	}
	info, err := os.Stat(mountPath)
	if err != nil {
		return SkillContext{}, fmt.Errorf("stat %s: %w", mountPath, err)
	}
	if !info.IsDir() {
		return SkillContext{}, fmt.Errorf("%w: %s is not a directory", ErrSkillNotFound, mountPath)
	}
	return loadFromSkillDir(skillName, filepath.Clean(mountPath))
}

func loadFromSkillDir(skillName, skillDir string) (SkillContext, error) {
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return SkillContext{}, fmt.Errorf("reading SKILL.md: %w", err)
	}
	fm, body, err := ParseSkillMD(string(content))
	if err != nil {
		return SkillContext{}, fmt.Errorf("parsing SKILL.md: %w", err)
	}

	name := fm.Name
	if name == "" {
		name = skillName
	}

	ctx := SkillContext{
		Name:               name,
		LocalPath:          skillDir,
		InstructionContent: body,
		Description:        fm.Description,
		Tags:               fm.Tags,
		Examples:           fm.Examples,
		RequiredBuiltins:   fm.RequiredBuiltins,
		Tools:              MergeRequiredBuiltinsIntoTools(fm.RequiredBuiltins, fm.Tools),
		Toolsets:           fm.Toolsets,
		ContentHash:        BundleHash(skillDir),
	}

	if dirExists(filepath.Join(skillDir, "references")) {
		ctx.HasReferences = true
		ctx.ResourceCount += countFiles(filepath.Join(skillDir, "references"))
	}
	if assetsDir := filepath.Join(skillDir, "assets"); dirExists(assetsDir) {
		ctx.HasAssets = true
		ctx.ResourceCount += countFiles(assetsDir)
		ctx.Templates = ScanTemplates(assetsDir)
		ctx.HasTemplates = len(ctx.Templates) > 0
	}

	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if data, err := os.ReadFile(manifestPath); err == nil {
		if tools, err := ParseBundledToolsManifest(data); err == nil && len(tools) > 0 {
			ctx.BundledTools = tools
		}
	}

	return ctx, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func countFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}
