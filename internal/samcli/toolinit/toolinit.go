// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	Lang       Lang
	Force      bool
	CLIVersion string
}

type Result struct {
	ToolsetDir   string
	SrcDir       string
	MetadataPath string
	Lang         Lang
	SDKInjected  bool
}

func Init(opts Options) (*Result, error) {
	if err := validateName(opts.Name); err != nil {
		return nil, err
	}
	if opts.Lang != LangGo && opts.Lang != LangPython {
		return nil, fmt.Errorf("--lang must be %q or %q, got %q", LangGo, LangPython, opts.Lang)
	}

	toolsetDir, err := ResolveToolsetDir(opts.Name, opts.Path)
	if err != nil {
		return nil, err
	}
	srcDir := filepath.Join(toolsetDir, "src")
	metadataPath := filepath.Join(filepath.Dir(toolsetDir), opts.Name+".yaml")

	if existsAndNonEmpty(srcDir) && !opts.Force {
		return nil, fmt.Errorf("destination %s exists and is not empty (use --force to overwrite)", srcDir)
	}
	if opts.Force {
		if err := os.RemoveAll(srcDir); err != nil {
			return nil, fmt.Errorf("clear destination: %w", err)
		}
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return nil, fmt.Errorf("create destination: %w", err)
	}

	version := opts.CLIVersion
	if version == "" {
		version = "dev"
	}

	res := &Result{
		ToolsetDir:   toolsetDir,
		SrcDir:       srcDir,
		MetadataPath: metadataPath,
		Lang:         opts.Lang,
	}

	switch opts.Lang {
	case LangGo:
		if err := writeGoSkeleton(srcDir, opts.Name, version); err != nil {
			return nil, err
		}
		injected, err := sdkvendor.EnsureGoSDK(srcDir)
		if err != nil {
			return nil, fmt.Errorf("vendor Go SDK: %w", err)
		}
		res.SDKInjected = injected
	case LangPython:
		if err := writePythonSkeleton(srcDir, opts.Name, version); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported language: %s", opts.Lang)
	}

	if !fileExists(metadataPath) {
		if err := writeMetadataHeader(metadataPath, opts.Name); err != nil {
			return nil, fmt.Errorf("write metadata header: %w", err)
		}
	}

	return res, nil
}

type SyncOptions struct {
	Path string
	Name string
	Lang Lang
}

type SyncResult struct {
	Synced []string
}

func Sync(opts SyncOptions) (*SyncResult, error) {
	targets, err := resolveSyncTargets(opts)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return &SyncResult{}, nil
	}

	res := &SyncResult{}
	for _, srcDir := range targets {
		lang := opts.Lang
		if lang == "" {
			lang = detectLang(srcDir)
		}
		switch lang {
		case LangGo:
			if err := sdkvendor.ForceGoSDK(srcDir); err != nil {
				return nil, fmt.Errorf("sync %s: %w", srcDir, err)
			}
		case LangPython:
		default:
			continue
		}
		res.Synced = append(res.Synced, filepath.Base(filepath.Dir(srcDir)))
	}
	slices.Sort(res.Synced)
	return res, nil
}

func ResolveToolsetDir(name, path string) (string, error) {
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", path, err)
	}

	if filepath.Base(abs) == name && filepath.Base(filepath.Dir(abs)) == "toolsets" {
		return abs, nil
	}
	if filepath.Base(abs) == "toolsets" {
		return filepath.Join(abs, name), nil
	}
	return filepath.Join(abs, "toolsets", name), nil
}

func resolveSyncTargets(opts SyncOptions) ([]string, error) {
	path := opts.Path
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	switch {
	case opts.Name != "":
		toolsetDir, err := ResolveToolsetDir(opts.Name, path)
		if err != nil {
			return nil, err
		}
		src := filepath.Join(toolsetDir, "src")
		if !dirExists(src) {
			return nil, fmt.Errorf("toolset src dir not found: %s", src)
		}
		return []string{src}, nil

	case filepath.Base(abs) == "src" && filepath.Base(filepath.Dir(filepath.Dir(abs))) == "toolsets":
		if !dirExists(abs) {
			return nil, fmt.Errorf("toolset src dir not found: %s", abs)
		}
		return []string{abs}, nil

	case filepath.Base(filepath.Dir(abs)) == "toolsets" && dirExists(filepath.Join(abs, "src")):
		return []string{filepath.Join(abs, "src")}, nil

	case filepath.Base(abs) == "toolsets" && dirExists(abs):
		return listToolsetSrcDirs(abs)

	default:
		toolsetsDir := filepath.Join(abs, "toolsets")
		if !dirExists(toolsetsDir) {
			return nil, fmt.Errorf("no toolsets/ directory found at %s", abs)
		}
		return listToolsetSrcDirs(toolsetsDir)
	}
}

func listToolsetSrcDirs(toolsetsDir string) ([]string, error) {
	entries, err := os.ReadDir(toolsetsDir)
	if err != nil {
		return nil, fmt.Errorf("read toolsets dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(toolsetsDir, e.Name(), "src")
		if dirExists(src) {
			out = append(out, src)
		}
	}
	return out, nil
}

func detectLang(srcDir string) Lang {
	if fileExists(filepath.Join(srcDir, "go.mod")) {
		return LangGo
	}
	if fileExists(filepath.Join(srcDir, "pyproject.toml")) {
		return LangPython
	}
	return ""
}

func DetectLang(dir string) Lang { return detectLang(dir) }

func validateName(name string) error {
	if name == "" {
		return errors.New("tool name is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("tool name %q contains a path separator", name)
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("tool name %q is not allowed", name)
	}
	return nil
}

func writeMetadataHeader(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	body := "kind: toolset\nname: " + name + "\ndescription: \"" + name + " toolset (replace this description).\"\n"
	return os.WriteFile(path, []byte(body), 0o644)
}

func existsAndNonEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
