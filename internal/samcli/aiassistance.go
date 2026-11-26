// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/aiassist"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/docsembed"
)

func newAIAssistanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai-assistance",
		Short: "Let your AI coding assistant write Agent Mesh config for you (Early Access)",
		Long: `Skills that teach an AI coding assistant how to build Agent Mesh agents, tools,
entrypoints, connectors and declarative config. They follow the Agent Skills
standard, so assistants such as OpenAI Codex CLI read them as-is; Claude Code
reads them from its own skills directory. "skill install" writes them into
your repository or your home directory; "skill check" tells you when they are
older than this CLI.

Early Access: commands, flags and output may change between releases.

Distinct from "sam skill", which packages skills for deployment to the platform.`,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			slog.Default().With("logger_name", "samcli").
				Warn("sam ai-assistance is an Early Access feature; its commands, flags, and output may change in future releases")
			return nil
		},
	}
	cmd.AddCommand(newAIAssistanceSkillCmd())
	return cmd
}

func newAIAssistanceSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install or check the assistant skill suite",
	}
	cmd.AddCommand(newAIAssistanceSkillInstallCmd())
	cmd.AddCommand(newAIAssistanceSkillCheckCmd())
	return cmd
}

func newAIAssistanceSkillInstallCmd() *cobra.Command {
	var (
		toDir      string
		targetName string
		scopeName  string
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the assistant skill suite for your repository or your user account",
		Long: `Write the assistant skill suite to the directory your AI coding assistant
reads. --target picks the assistant, --scope picks where it is installed.
Existing skills are left alone unless you pass --force.`,
		Example: `  sam ai-assistance skill install                                # Codex CLI, this repository
  sam ai-assistance skill install --target claude                # Claude Code, this repository
  sam ai-assistance skill install --scope user                   # Codex CLI, every repository on this machine
  sam ai-assistance skill install --target claude --scope user   # Claude Code, every repository on this machine`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := resolveInstallRoot(toDir, targetName, scopeName)
			if err != nil {
				return err
			}
			if err := aiassist.InstallSuite(dir, Version, force, embeddedDocs()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote assistant suite (version %s)\n", Version)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&toDir, "to", "", "Directory to write into instead of the --target/--scope one")
	cmd.Flags().StringVar(&targetName, "target", string(aiassist.DefaultTarget),
		"where to write: agents (.agents/skills/) or claude (.claude/skills/)")
	cmd.Flags().StringVar(&scopeName, "scope", string(aiassist.DefaultScope),
		"where to install: project (current directory) or user (home directory, every repository)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing skills")
	cmd.MarkFlagsMutuallyExclusive("to", "target")
	cmd.MarkFlagsMutuallyExclusive("to", "scope")
	return cmd
}

func newAIAssistanceSkillCheckCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify the installed skill suites match this CLI version",
		Long: `Verify the installed skill suites match this CLI version. Without --path, every
well-known directory is checked: .agents/skills/ and .claude/skills/ under the
current directory and under your home directory. Use --path to check an
explicit directory instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			roots, err := suiteRootsToCheck(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			inSync := true
			for _, root := range roots {
				res, err := aiassist.CheckSuite(root.dir, Version, docsembed.Available())
				if err != nil {
					return fmt.Errorf("read suite at %s: %w", root.dir, err)
				}
				if res.OK() {
					_, _ = fmt.Fprintf(out, "%s: suite in sync\n", root.dir)
					continue
				}
				inSync = false
				_, _ = fmt.Fprintf(out, "%s: OUT OF SYNC — refresh with: sam ai-assistance skill install %s --force\n", root.dir, root.installFlag)
				writeSuiteProblems(out, res)
			}
			for _, note := range bothScopeNotes(roots) {
				_, _ = fmt.Fprintln(out, note)
			}
			if !inSync {
				return fmt.Errorf("assistant suite out of sync")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Directory to check instead of the well-known ones")
	return cmd
}

func resolveInstallRoot(explicit, targetName, scopeName string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	target, err := aiassist.ParseTarget(targetName)
	if err != nil {
		return "", fmt.Errorf("--target: %w", err)
	}
	scope, err := aiassist.ParseScope(scopeName)
	if err != nil {
		return "", fmt.Errorf("--scope: %w", err)
	}
	dir, err := aiassist.Root{Target: target, Scope: scope}.Dir()
	if err != nil {
		return "", fmt.Errorf("%w; pass --to <dir> to choose the directory yourself", err)
	}
	return dir, nil
}

type suiteRoot struct {
	dir         string
	installFlag string
	root        aiassist.Root
}

func suiteRootsToCheck(explicit string) ([]suiteRoot, error) {
	if explicit != "" {
		return []suiteRoot{{dir: explicit, installFlag: "--to " + explicit}}, nil
	}
	var roots []suiteRoot
	for _, root := range aiassist.Roots() {
		dir, err := root.Dir()
		if err != nil || !aiassist.SuitePresent(dir) {
			continue
		}
		roots = append(roots, suiteRoot{dir: dir, installFlag: rootFlags(root), root: root})
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no assistant suite under %s — install one with: sam ai-assistance skill install [--target claude] [--scope user]", wellKnownRootList())
	}
	return roots, nil
}

func rootFlags(root aiassist.Root) string {
	flags := "--target " + string(root.Target)
	if root.Scope != aiassist.DefaultScope {
		flags += " --scope " + string(root.Scope)
	}
	return flags
}

func wellKnownRootList() string {
	var dirs []string
	for _, root := range aiassist.Roots() {
		if dir, err := root.Dir(); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return strings.Join(dirs, ", ")
}

func bothScopeNotes(roots []suiteRoot) []string {
	var notes []string
	for _, target := range aiassist.Targets() {
		var dirs []string
		for _, r := range roots {
			if r.root.Target == target {
				dirs = append(dirs, r.dir)
			}
		}
		if len(dirs) > 1 {
			notes = append(notes, fmt.Sprintf("note: suite installed at both %s; keep one", strings.Join(dirs, " and ")))
		}
	}
	return notes
}

func writeSuiteProblems(out io.Writer, res aiassist.SuiteCheckResult) {
	for _, m := range res.MissingSkills {
		_, _ = fmt.Fprintf(out, "  missing skill: %s\n", m)
	}
	if res.DocsCorpusMissing {
		_, _ = fmt.Fprintln(out, "  sam-docs: documentation corpus missing under references/documentation")
	}
	for _, name := range slices.Sorted(maps.Keys(res.VersionMismatch)) {
		_, _ = fmt.Fprintf(out, "  %s: version %q != CLI %q\n", name, res.VersionMismatch[name], Version)
	}
	for _, r := range res.DeclMissingRefs {
		_, _ = fmt.Fprintf(out, "  declarative-config missing reference: %s\n", r)
	}
}

func embeddedDocs() fs.FS {
	if !docsembed.Available() {
		return nil
	}
	return docsembed.FS()
}
