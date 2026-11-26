// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Options struct {
	DryRun bool
	Force  bool
}

func Run(legacyPath, outputPath string, opts Options) (*Report, error) {
	report := &Report{}

	info, err := os.Stat(legacyPath)
	if err != nil {
		return nil, fmt.Errorf("legacy path: %w", err)
	}

	if !opts.DryRun {
		if err := prepareOutputDir(outputPath, opts.Force); err != nil {
			return nil, err
		}
	}

	if !info.IsDir() {
		if err := migrateOneFile(legacyPath, outputPath, opts, report); err != nil {
			report.AddError(legacyPath, err)
		}
		return report, nil
	}

	walkErr := filepath.WalkDir(legacyPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			report.AddError(path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		if mErr := migrateOneFile(path, outputPath, opts, report); mErr != nil {
			report.AddError(path, mErr)
		}
		return nil
	})
	if walkErr != nil {
		return report, walkErr
	}

	return report, nil
}

func prepareOutputDir(outputPath string, force bool) error {
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		return fmt.Errorf("output path: %w", err)
	}
	if force {
		return nil
	}
	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return fmt.Errorf("output path: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("output path %q is not empty; pass --force to write anyway", outputPath)
	}
	return nil
}

func migrateOneFile(srcPath, outputRoot string, opts Options, report *Report) error {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	expanded, missingIncludes, err := tolerantProcessIncludes(string(raw), filepath.Dir(srcPath))
	if err != nil {
		return fmt.Errorf("resolve !include: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	preWarnings := includeWarnings(missingIncludes)
	if doc == nil {
		reason := "empty document"
		if len(preWarnings) > 0 {
			reason = fmt.Sprintf("empty after stripping %d missing !include target(s); none of the file's content survived",
				len(missingIncludes))
		}
		report.AddSkipped(srcPath, "", reason)
		return nil
	}

	kind := classifyFile(srcPath, doc)
	r := kindRun{srcPath: srcPath, outputRoot: outputRoot, opts: opts, report: report, extraWarnings: preWarnings}
	switch kind {
	case "agent":
		return runOneKind(r, "agents", MigrateAgent, doc)
	case "entrypoint":
		return runOneKind(r, "entrypoints", MigrateGateway, doc)
	case "workflow":
		return runOneKind(r, "workflows", MigrateWorkflow, doc)
	case "shared":
		return runSharedConfig(srcPath, outputRoot, opts, report, doc, preWarnings)
	case "overlay":
		report.AddSkipped(srcPath, "overlay",
			"per-environment overlay file — no equivalent in clean spec; skipped")
		return nil
	default:
		report.AddSkipped(srcPath, kind,
			"could not classify by content or directory; skipped")
		return nil
	}
}

type kindRun struct {
	srcPath, outputRoot string
	opts                Options
	report              *Report
	extraWarnings       []string
}

func runOneKind(
	r kindRun,
	kindDir string,
	migrateFn func(map[string]any) (CleanSpec, []string, error),
	doc map[string]any,
) error {
	clean, warnings, err := migrateFn(doc)
	if err != nil {
		return err
	}
	target, err := writeClean(r.outputRoot, kindDir, clean, r.opts.DryRun)
	if err != nil {
		return err
	}
	allWarnings := append([]string(nil), r.extraWarnings...)
	allWarnings = append(allWarnings, warnings...)
	if len(allWarnings) > 0 {
		r.report.AddWarning(r.srcPath, target, clean.Kind, clean.Name, allWarnings)
	} else {
		r.report.AddMigrated(r.srcPath, target, clean.Kind, clean.Name)
	}
	return nil
}

func runSharedConfig(
	srcPath, outputRoot string,
	opts Options,
	report *Report,
	doc map[string]any,
	extraWarnings []string,
) error {
	models, perModelWarnings, perModelSkips := ExtractModelsFromSharedConfig(doc)
	if len(models) == 0 && len(perModelSkips) == 0 {
		report.AddSkipped(srcPath, "shared",
			"no models extracted from shared_config; nothing to write")
		return nil
	}
	for _, m := range models {
		target, err := writeClean(outputRoot, "models", m, opts.DryRun)
		if err != nil {
			report.AddError(srcPath, fmt.Errorf("write model %s: %w", m.Name, err))
			continue
		}
		merged := append([]string(nil), extraWarnings...)
		if w, ok := perModelWarnings[m.Name]; ok {
			merged = append(merged, w...)
		}
		if len(merged) > 0 {
			report.AddWarning(srcPath, target, m.Kind, m.Name, merged)
		} else {
			report.AddMigrated(srcPath, target, m.Kind, m.Name)
		}
	}
	for name, reason := range perModelSkips {
		report.AddSkipped(srcPath, "model",
			fmt.Sprintf("model %q: %s", name, reason))
	}
	return nil
}

func writeClean(outputRoot, kindDir string, doc CleanSpec, dryRun bool) (string, error) {
	if doc.Name == "" {
		return "", errors.New("clean spec has empty name; cannot pick a filename")
	}
	safeName := filepath.Base(doc.Name)
	if safeName == "." || safeName == ".." || safeName == string(filepath.Separator) {
		return "", fmt.Errorf("clean spec name %q resolves to an invalid filename", doc.Name)
	}
	dir := filepath.Join(outputRoot, kindDir)
	if !dryRun {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	target := filepath.Join(dir, safeName+".yaml")
	if dryRun {
		return target, nil
	}
	body, err := renderCleanSpec(doc)
	if err != nil {
		return target, err
	}
	if err := os.WriteFile(target, body, 0o644); err != nil {
		return target, fmt.Errorf("write %s: %w", target, err)
	}
	return target, nil
}

func classifyFile(path string, doc map[string]any) string {
	base := strings.ToLower(filepath.Base(path))
	if base == "shared_config.yaml" || base == "shared_config.yml" {
		return "shared"
	}
	if strings.Contains(strings.ToLower(path), "/overlay") || strings.Contains(base, "overlay") {
		return "overlay"
	}

	module := appModuleString(doc)
	switch {
	case strings.Contains(module, "agent.sac.app") || strings.Contains(module, ".agent.app"):
		return "agent"
	case strings.Contains(module, "gateway."):
		return "entrypoint"
	case strings.Contains(module, ".workflow.app"):
		return "workflow"
	}

	dir := filepath.Base(filepath.Dir(path))
	switch dir {
	case "agents":
		return "agent"
	case "gateways":
		return "entrypoint"
	case "workflows":
		return "workflow"
	case "models":
		return "model"
	}
	return "unknown"
}

func appModuleString(doc map[string]any) string {
	apps, ok := doc["apps"].([]any)
	if !ok || len(apps) == 0 {
		return ""
	}
	first, ok := apps[0].(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := first["app_module"].(string); ok {
		return s
	}
	if s, ok := first["app_exec"].(string); ok {
		return s
	}
	return ""
}

var includePattern = regexp.MustCompile(`(?m)^([ \t]*)!include\s+["']?([^"'\s]+)["']?`)

func tolerantProcessIncludes(content, baseDir string) (string, []string, error) {
	const maxDepth = 50
	var missing []string
	resolved, err := tolerantIncludeRecursive(content, baseDir, nil, 0, maxDepth, &missing)
	return resolved, missing, err
}

func tolerantIncludeRecursive(
	content, baseDir string,
	visited []string,
	depth, maxDepth int,
	missing *[]string,
) (string, error) {
	if depth > maxDepth {
		return "", fmt.Errorf("include depth limit exceeded (%d): %s", maxDepth,
			strings.Join(visited, " -> "))
	}
	var firstErr error
	result := includePattern.ReplaceAllStringFunc(content, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := includePattern.FindStringSubmatch(match)
		indent := groups[1]
		includePath := groups[2]

		fullPath := includePath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(baseDir, includePath)
		}
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			*missing = append(*missing, includePath)
			return ""
		}
		if slices.Contains(visited, absPath) {
			firstErr = fmt.Errorf("circular include detected: %s -> %s",
				strings.Join(visited, " -> "), absPath)
			return match
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			*missing = append(*missing, includePath)
			return ""
		}
		includeDir := filepath.Dir(absPath)
		newVisited := append(append([]string{}, visited...), absPath)
		included, recErr := tolerantIncludeRecursive(string(data), includeDir, newVisited, depth+1, maxDepth, missing)
		if recErr != nil {
			firstErr = recErr
			return match
		}
		lines := strings.Split(included, "\n")
		for i, line := range lines {
			if line != "" {
				lines[i] = indent + line
			}
		}
		return strings.Join(lines, "\n")
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

func includeWarnings(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, fmt.Sprintf(
			"missing !include target %q stripped before migration; restore manually if its content matters",
			p,
		))
	}
	return out
}
