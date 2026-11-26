// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

type Lang string

const (
	LangGo     Lang = "go"
	LangPython Lang = "python"
)

type Options struct {
	Name       string
	Path       string
	Force      bool
	WithTool   bool
	Lang       Lang
	CLIVersion string
}

type Result struct {
	SkillDir      string
	SkillMDPath   string
	ReferencesDir string
	ToolDir       string
	SDKInjected   bool
}

func Init(opts Options) (*Result, error) {
	if err := validateName(opts.Name); err != nil {
		return nil, err
	}

	skillDir, err := ResolveSkillDir(opts.Name, opts.Path)
	if err != nil {
		return nil, err
	}

	if existsAndNonEmpty(skillDir) && !opts.Force {
		return nil, fmt.Errorf("destination %s exists and is not empty (use --force to overwrite)", skillDir)
	}
	if opts.Force {
		if err := os.RemoveAll(skillDir); err != nil {
			return nil, fmt.Errorf("clear destination: %w", err)
		}
	}

	version := opts.CLIVersion
	if version == "" {
		version = "dev"
	}

	referencesDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(referencesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create references dir: %w", err)
	}

	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMDPath, []byte(renderSkillMD(opts.Name)), 0o644); err != nil {
		return nil, fmt.Errorf("write SKILL.md: %w", err)
	}

	res := &Result{
		SkillDir:      skillDir,
		SkillMDPath:   skillMDPath,
		ReferencesDir: referencesDir,
	}

	if opts.WithTool {
		toolDir := filepath.Join(skillDir, "tools", opts.Name)
		switch lang := opts.Lang; lang {
		case "", LangGo:
			if err := writeSampleGoTool(skillDir, toolDir, opts.Name, version); err != nil {
				return nil, err
			}
			injected, err := sdkvendor.EnsureGoSDK(toolDir)
			if err != nil {
				return nil, fmt.Errorf("vendor Go SDK: %w", err)
			}
			res.SDKInjected = injected
		case LangPython:
			if err := writeSamplePyTool(skillDir, toolDir, opts.Name, version); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("--lang must be %q or %q, got %q", LangGo, LangPython, lang)
		}
		res.ToolDir = toolDir
	}

	return res, nil
}

func ResolveSkillDir(name, path string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", path, err)
	}
	if filepath.Base(abs) == name && filepath.Base(filepath.Dir(abs)) == "skills" {
		return abs, nil
	}
	if filepath.Base(abs) == "skills" {
		return filepath.Join(abs, name), nil
	}
	return filepath.Join(abs, "skills", name), nil
}

func validateName(name string) error {
	if name == "" {
		return errors.New("skill name is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("skill name %q contains a path separator", name)
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("skill name %q is not allowed", name)
	}
	return nil
}

func existsAndNonEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}
