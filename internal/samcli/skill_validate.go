// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	skillbuild "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/skillinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

func newSkillValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate NAME [PATH]",
		Short: "Check your skill's SKILL.md and that its tools are discoverable",
		Long: `Validate a skill locally before you package and upload it.

NAME is the skill directory name; PATH (optional) locates it, defaulting to
the current working directory (same resolution as "sam skill package").

The command:
  1. parses SKILL.md and confirms the frontmatter has a "name" (the platform
     rejects an upload without it);
  2. builds any bundled tools (Go or Python; tools/manifest.yaml) for your
     host and runs the same --schema discovery the Secure Tool Runtime (STR)
     performs, reporting the
     agent-facing "<skill>__<tool>" name each manifest entry yields.

It catches the common reasons a bundled tool fails to load — a wrong manifest
"executable"/"tool_dir", an import error, a bad tool signature, a schema
failure, or a manifest key that matches no registered tool — without the
build → upload → discover round-trip.

It builds for your host, not the STR's deployment target; "sam skill package"
pins the STR's target for the bundle you upload. A skill with no bundled tools
validates its SKILL.md and reports that there is nothing to discover.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			return runSkillValidate(cmd.Context(), cmd, args[0], path)
		},
	}
	return cmd
}

func runSkillValidate(ctx context.Context, cmd *cobra.Command, name, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	skillDir, err := skillinit.ResolveSkillDir(name, path)
	if err != nil {
		return err
	}
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return fmt.Errorf("skill not found: %s has no readable SKILL.md: %w", skillDir, err)
	}
	fm, _, err := skillruntime.ParseSkillMD(string(content))
	if err != nil {
		return fmt.Errorf("parse %s: %w", skillMDPath, err)
	}
	if fm.Name == "" {
		return fmt.Errorf("%s: frontmatter has no 'name' field (the platform rejects an upload without it)", skillMDPath)
	}

	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "validating skill %s (%s)\n", fm.Name, skillDir)
	if fm.Description != "" {
		_, _ = fmt.Fprintf(errOut, "  description: %s\n", fm.Description)
	}
	if len(fm.Toolsets) > 0 {
		_, _ = fmt.Fprintf(errOut, "  references toolsets: %v\n", fm.Toolsets)
	}

	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		_, _ = fmt.Fprintf(out, "%s OK — SKILL.md is valid; no bundled tools to discover.\n", fm.Name)
		return nil
	}

	_, _ = fmt.Fprintf(errOut, "building %s tools for your host (%s/%s)...\n", name, runtime.GOOS, runtime.GOARCH)
	buildRes, err := skillbuild.BuildSkillTools(ctx, name, skillDir, skillbuild.SkillBuildOptions{
		Stdout:         errOut,
		Stderr:         errOut,
		TargetResolver: fixedTargetResolver{os: runtime.GOOS, arch: runtime.GOARCH},
	})
	if err != nil {
		return fmt.Errorf("build skill %s for host: %w", name, err)
	}

	builtDir, err := os.MkdirTemp("", "sam-skill-validate-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(builtDir) }()
	for relPath, data := range buildRes.Overlay {
		dest := filepath.Join(builtDir, filepath.FromSlash(relPath))
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return fmt.Errorf("materialize built tool: %w", mkErr)
		}
		if wErr := os.WriteFile(dest, data, 0o755); wErr != nil {
			return fmt.Errorf("materialize built tool: %w", wErr)
		}
	}

	return validateSkillDiscovery(out, fm.Name, skillDir, builtDir, buildRes.Overlay, manifestPath)
}

type skillManifestEntry struct {
	Runtime    string `yaml:"runtime"`
	Executable string `yaml:"executable"`
	ToolDir    string `yaml:"tool_dir"`
}

func pythonPathFromToolDir(base, dirSeg string) string {
	return str.PythonPathFor(filepath.Join(base, "tools", filepath.FromSlash(dirSeg)))
}

func validateSkillDiscovery(out io.Writer, skillName, skillDir, builtDir string, overlay map[string][]byte, manifestPath string) error {
	entries, err := readSkillManifestEntries(manifestPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no tools declared in %s — nothing to validate", manifestPath)
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	total := 0
	var failed []string
	for _, key := range keys {
		entry := entries[key]
		dirSeg := entry.ToolDir
		if dirSeg == "" {
			dirSeg = key
		}
		relExe := skillToolRelPath(entry.ToolDir, entry.Executable, key)
		base := skillDir
		if _, ok := overlay[relExe]; ok {
			base = builtDir
		}
		exe := filepath.Join(base, filepath.FromSlash(relExe))

		pyPath := ""
		if entry.Runtime == "python" {
			pyPath = pythonPathFromToolDir(base, dirSeg)
		}

		tools, discErr := str.DiscoverFromExecutable(exe, pyPath)
		if discErr != nil {
			_, _ = fmt.Fprintf(out, "  ✗ %s: %v\n", key, discErr)
			failed = append(failed, key)
			continue
		}
		if _, ok := tools[key]; !ok && len(tools) != 1 {
			_, _ = fmt.Fprintf(out, "  ✗ %s: manifest key matches none of the %d registered tool(s) %v — no schema will bind to it (it won't be callable)\n",
				key, len(tools), reportedSkillToolNames(tools))
			failed = append(failed, key)
			continue
		}
		_, _ = fmt.Fprintf(out, "  ✓ %s: discovered %d tool(s), exposed as %s%s%s\n",
			key, len(tools), skillName, str.SkillToolSep, key)
		for _, tn := range reportedSkillToolNames(tools) {
			_, _ = fmt.Fprintf(out, "      - %s: %s\n", tn, tools[tn].Description)
		}
		total += len(tools)
	}

	if len(failed) > 0 {
		return fmt.Errorf("%s: %d of %d manifest entr(ies) failed discovery: %v", skillName, len(failed), len(entries), failed)
	}
	_, _ = fmt.Fprintf(out, "%s OK — %d tool(s) discovered.\n", skillName, total)
	return nil
}

func skillToolRelPath(toolDir, executable, key string) string {
	dir := toolDir
	if dir == "" {
		dir = key
	}
	exe := executable
	if exe == "" {
		exe = key
	}
	return filepath.ToSlash(filepath.Join("tools", dir, exe))
}

func reportedSkillToolNames(tools map[string]str.DiscoveredTool) []string {
	names := make([]string, 0, len(tools))
	for tn := range tools {
		names = append(names, tn)
	}
	slices.Sort(names)
	return names
}

func readSkillManifestEntries(manifestPath string) (map[string]skillManifestEntry, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var doc struct {
		Tools yaml.Node `yaml:"tools"`
	}
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	entries := map[string]skillManifestEntry{}
	if doc.Tools.Kind == yaml.MappingNode {
		if decErr := doc.Tools.Decode(&entries); decErr != nil {
			return nil, fmt.Errorf("parse tools in %s: %w", manifestPath, decErr)
		}
	}
	return entries, nil
}
