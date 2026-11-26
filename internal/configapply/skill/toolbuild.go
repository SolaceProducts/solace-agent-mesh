// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
)

type ToolManifest struct {
	Version int                   `yaml:"version"`
	Tools   map[string]*ToolEntry `yaml:"tools"`
}

type ToolEntry struct {
	Runtime    string `yaml:"runtime"`
	Executable string `yaml:"executable"`
	ToolDir    string `yaml:"tool_dir"`
}

type ToolBuildAction string

const (
	ToolBuildActionNone     ToolBuildAction = "none"
	ToolBuildActionCacheHit ToolBuildAction = "cache-hit"
	ToolBuildActionBuilt    ToolBuildAction = "built"
	ToolBuildActionSkipped  ToolBuildAction = "skipped"
)

type ToolBuildOutcome struct {
	ToolDir    string
	Executable string
	Action     ToolBuildAction
	Duration   time.Duration
	Target     build.Target
}

type SkillBuildOptions struct {
	NoBuild        bool
	Strict         bool
	SDKVersion     string
	Stdout         io.Writer
	Stderr         io.Writer
	TargetResolver SkillBuildTargetResolver
}

type SkillBuildTargetResolver interface {
	Resolve(ctx context.Context) (os, arch, pythonVersion string, err error)
}

func ParseToolManifest(skillDir string) (*ToolManifest, error) {
	path := filepath.Join(skillDir, toolsSubdir, toolManifestFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var tm ToolManifest
	if err := yaml.Unmarshal(data, &tm); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &tm, nil
}

type goToolDir struct {
	toolDir     string
	executable  string
	srcDir      string
	overlayPath string
}

func resolveGoToolDirs(skillDir string, tm *ToolManifest) ([]goToolDir, error) {
	if tm == nil || len(tm.Tools) == 0 {
		return nil, nil
	}
	toolsRoot := filepath.Join(skillDir, toolsSubdir)
	byDir := map[string]*goToolDir{}
	for name, entry := range tm.Tools {
		if entry == nil {
			continue
		}
		if entry.Runtime != "" && entry.Runtime != "go" {
			continue
		}
		dir := entry.ToolDir
		if dir == "" {
			dir = name
		}
		if err := validateRelPath("tool_dir", dir); err != nil {
			return nil, err
		}
		if entry.Runtime == "" && !hasGoMod(filepath.Join(toolsRoot, dir)) {
			continue
		}
		exe := entry.Executable
		if exe == "" {
			exe = name
		}
		if err := validateRelPath("executable", exe); err != nil {
			return nil, err
		}
		if existing, ok := byDir[dir]; ok {
			if existing.executable != exe {
				return nil, fmt.Errorf("tool_dir %q declared with conflicting executables %q and %q — every manifest entry sharing a tool_dir must agree", dir, existing.executable, exe)
			}
			continue
		}
		srcDir := filepath.Join(toolsRoot, dir)
		byDir[dir] = &goToolDir{
			toolDir:     dir,
			executable:  exe,
			srcDir:      srcDir,
			overlayPath: filepath.ToSlash(filepath.Join(toolsSubdir, dir, exe)),
		}
	}
	out := make([]goToolDir, 0, len(byDir))
	for _, g := range byDir {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].toolDir < out[j].toolDir })
	return out, nil
}

type pyToolDir struct {
	toolDir string
	srcDir  string
}

func resolvePythonToolDirs(skillDir string, tm *ToolManifest) ([]pyToolDir, error) {
	if tm == nil || len(tm.Tools) == 0 {
		return nil, nil
	}
	toolsRoot := filepath.Join(skillDir, toolsSubdir)
	byDir := map[string]*pyToolDir{}
	for name, entry := range tm.Tools {
		if entry == nil {
			continue
		}
		if entry.Runtime != "" && entry.Runtime != "python" {
			continue
		}
		dir := entry.ToolDir
		if dir == "" {
			dir = name
		}
		if err := validateRelPath("tool_dir", dir); err != nil {
			return nil, err
		}
		srcDir := filepath.Join(toolsRoot, dir)
		if entry.Runtime == "" && (hasGoMod(srcDir) || !hasPythonProject(srcDir)) {
			continue
		}
		if _, ok := byDir[dir]; ok {
			continue
		}
		byDir[dir] = &pyToolDir{toolDir: dir, srcDir: srcDir}
	}
	out := make([]pyToolDir, 0, len(byDir))
	for _, p := range byDir {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].toolDir < out[j].toolDir })
	return out, nil
}

func hasPythonProject(dir string) bool {
	for _, f := range []string{"pyproject.toml", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func GoToolDirs(skillDir string) ([]string, error) {
	tm, err := ParseToolManifest(skillDir)
	if err != nil {
		return nil, err
	}
	dirs, err := resolveGoToolDirs(skillDir, tm)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(dirs))
	for _, g := range dirs {
		if _, statErr := os.Stat(filepath.Join(g.srcDir, goModFilename)); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("skill tool %q: stat go.mod: %w", g.toolDir, statErr)
		}
		out = append(out, g.srcDir)
	}
	return out, nil
}

func hasGoMod(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, goModFilename))
	return err == nil
}

func validateRelPath(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) {
		return fmt.Errorf("%s %q must be a relative path within the skill's tools/ root", field, value)
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%s %q must not traverse out of the skill's tools/ root", field, value)
	}
	for seg := range strings.SplitSeq(cleaned, "/") {
		if seg == ".." {
			return fmt.Errorf("%s %q must not contain a %q segment", field, value, "..")
		}
	}
	return nil
}

type BuildSkillToolsResult struct {
	Overlay  map[string][]byte
	Outcomes []ToolBuildOutcome
}

func BuildSkillTools(ctx context.Context, skillName, skillDir string, opts SkillBuildOptions) (*BuildSkillToolsResult, error) {
	tm, err := ParseToolManifest(skillDir)
	if err != nil {
		return nil, err
	}
	goDirs, err := resolveGoToolDirs(skillDir, tm)
	if err != nil {
		return nil, err
	}
	pyDirs, err := resolvePythonToolDirs(skillDir, tm)
	if err != nil {
		return nil, err
	}
	if len(goDirs) == 0 && len(pyDirs) == 0 {
		return &BuildSkillToolsResult{}, nil
	}

	target, pyVer, err := resolveSkillTarget(ctx, opts)
	if err != nil {
		return nil, err
	}

	result := &BuildSkillToolsResult{Overlay: map[string][]byte{}}
	cache := build.NewCache(skillDir)

	for _, g := range goDirs {
		if _, statErr := os.Stat(filepath.Join(g.srcDir, goModFilename)); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("skill %q tool %q: stat go.mod: %w", skillName, g.toolDir, statErr)
		}

		plan, err := build.Resolve(g.executable, g.srcDir)
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: resolve build: %w", skillName, g.toolDir, err)
		}

		hash, err := build.Hash(plan, target, g.srcDir)
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: hash: %w", skillName, g.toolDir, err)
		}

		out := ToolBuildOutcome{ToolDir: g.toolDir, Executable: g.executable, Target: target}

		if cached, ok, cacheErr := cache.GetBinary(target, hash); cacheErr != nil {
			return nil, fmt.Errorf("skill %q tool %q: cache get: %w", skillName, g.toolDir, cacheErr)
		} else if ok {
			result.Overlay[g.overlayPath] = cached
			out.Action = ToolBuildActionCacheHit
			result.Outcomes = append(result.Outcomes, out)
			continue
		}

		if opts.NoBuild {
			if opts.Strict {
				return nil, fmt.Errorf("skill %q tool %q is not built and apply was run with --no-build. Re-run without --no-build, or run plan first to populate the cache", skillName, g.toolDir)
			}
			out.Action = ToolBuildActionSkipped
			result.Outcomes = append(result.Outcomes, out)
			continue
		}

		start := time.Now()
		dist, err := build.Run(ctx, plan, build.RunOptions{
			Stdout:     opts.Stdout,
			Stderr:     opts.Stderr,
			SDKVersion: opts.SDKVersion,
			TargetOS:   target.OS,
			TargetArch: target.Arch,
		})
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: build: %w", skillName, g.toolDir, err)
		}
		out.Duration = time.Since(start)

		binPath := filepath.Join(dist, g.executable)
		if target.OS == windowsGOOS {
			binPath += windowsExeExt
		}
		binData, err := os.ReadFile(binPath)
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: read built binary: %w", skillName, g.toolDir, err)
		}
		if err := cache.PutBinary(target, hash, binData); err != nil {
			return nil, fmt.Errorf("skill %q tool %q: cache put: %w", skillName, g.toolDir, err)
		}

		result.Overlay[g.overlayPath] = binData
		out.Action = ToolBuildActionBuilt
		result.Outcomes = append(result.Outcomes, out)
	}

	for _, p := range pyDirs {
		plan, err := build.Resolve(p.toolDir, p.srcDir)
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: resolve build: %w", skillName, p.toolDir, err)
		}
		out := ToolBuildOutcome{ToolDir: p.toolDir, Executable: pythonLayerDir, Target: target}

		if plan.Kind != build.KindPythonConvention {
			out.Action = ToolBuildActionNone
			result.Outcomes = append(result.Outcomes, out)
			continue
		}

		if opts.NoBuild {
			if opts.Strict {
				return nil, fmt.Errorf("skill %q tool %q is not built and apply was run with --no-build. Re-run without --no-build, or run plan first", skillName, p.toolDir)
			}
			out.Action = ToolBuildActionSkipped
			result.Outcomes = append(result.Outcomes, out)
			continue
		}

		start := time.Now()
		dist, err := build.Run(ctx, plan, build.RunOptions{
			Stdout:        opts.Stdout,
			Stderr:        opts.Stderr,
			SDKVersion:    opts.SDKVersion,
			TargetOS:      target.OS,
			TargetArch:    target.Arch,
			PythonVersion: pyVer,
		})
		if err != nil {
			return nil, fmt.Errorf("skill %q tool %q: build: %w", skillName, p.toolDir, err)
		}
		out.Duration = time.Since(start)

		if err := overlayPythonLayer(result.Overlay, dist, p.toolDir); err != nil {
			return nil, fmt.Errorf("skill %q tool %q: collect python layer: %w", skillName, p.toolDir, err)
		}
		out.Action = ToolBuildActionBuilt
		result.Outcomes = append(result.Outcomes, out)
	}

	return result, nil
}

func resolveSkillTarget(ctx context.Context, opts SkillBuildOptions) (build.Target, string, error) {
	var pinnedOS, pinnedArch, pyVer string
	if opts.TargetResolver != nil {
		osS, archS, pv, err := opts.TargetResolver.Resolve(ctx)
		if err != nil {
			return build.Target{}, "", fmt.Errorf("resolve build target: %w", err)
		}
		pinnedOS, pinnedArch, pyVer = osS, archS, pv
	}
	return build.ResolveTarget(build.Target{OS: pinnedOS, Arch: pinnedArch}), pyVer, nil
}

func overlayPythonLayer(overlay map[string][]byte, distDir, toolDir string) error {
	root := filepath.Join(distDir, pythonLayerDir)
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("built bundle has no %s/ layer at %s: %w", pythonLayerDir, root, err)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(distDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		overlay[filepath.ToSlash(filepath.Join(toolsSubdir, toolDir, rel))] = data
		return nil
	})
}

const (
	toolsSubdir          = "tools"
	toolManifestFilename = "manifest.yaml"
	goModFilename        = "go.mod"
	pythonLayerDir       = "python"
	windowsGOOS          = "windows"
	windowsExeExt        = ".exe"
)
