// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/skillinit"
)

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Package skills (instructions + tools) your agents can reuse",
		Long: `Scaffold and package deployable skill bundles.

"sam skill init" creates a new skills/<name>/ bundle with a SKILL.md and
references/ directory (add --with-tool for a sample bundled Go tool).
"sam skill sync" re-vendors the embedded Go SDK into bundled skill tools.
"sam skill validate" builds a skill's bundled tools for your host and runs
the same --schema discovery the Secure Tool Runtime (STR) performs, so you can confirm they load
before packaging. "sam skill package" builds a skill bundle (compiling any
bundled Go tools) and zips it into an artifact you can upload through the
web UI without running "sam config apply". "sam skill build-target" prints
the architecture the deployed STR expects for those bundled tools.

To install or check the sam-declarative-config authoring skill (the
CLI's generated Claude Code guidance), use "sam ai-assistance skill".`,
	}
	cmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core:"},
		&cobra.Group{ID: "advanced", Title: "Advanced:"},
	)
	addSub := func(group string, c *cobra.Command) {
		c.GroupID = group
		cmd.AddCommand(c)
	}
	addSub("core", newSkillInitCmd())
	addSub("core", newSkillValidateCmd())
	addSub("core", newSkillPackageCmd())
	addSub("advanced", newSkillSyncCmd())
	addSub("advanced", newSkillBuildTargetCmd())
	return cmd
}

func newSkillInitCmd() *cobra.Command {
	var (
		force    bool
		withTool bool
		lang     string
	)
	cmd := &cobra.Command{
		Use:   "init NAME [PATH]",
		Short: "Create a new skill with the required SKILL.md structure",
		Long: `Scaffold a new deployable skill under skills/<name>/.

NAME is the skill directory name (e.g. "summarizer"). PATH (optional) is
one of:
  - a declarative-config repo root            → skills/<name>/ is created
  - a skills/ directory                       → <path>/<name>/ is created
  - a full path ending in skills/<name>       → used verbatim
PATH defaults to the current working directory.

By default it writes SKILL.md (frontmatter + body) and an empty
references/ directory. Pass --with-tool to also scaffold a sample bundled
tool under tools/<name>/ (plus tools/manifest.yaml). Choose the tool's
language with --lang (default go): a Go tool (go.mod + main.go using
samtoolsdk) or a Python tool (pyproject.toml + src/<pkg>/ using
sam-tool-sdk). Passing --lang implies --with-tool.`,
		Example: `  sam skill init summarizer
  sam skill init summarizer ./my-repo
  sam skill init summarizer --with-tool
  sam skill init summarizer --with-tool --lang python
  sam skill init summarizer ./my-repo/skills/summarizer --force`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			res, err := skillinit.Init(skillinit.Options{
				Name:       args[0],
				Path:       path,
				Force:      force,
				WithTool:   withTool || cmd.Flags().Changed("lang"),
				Lang:       skillinit.Lang(lang),
				CLIVersion: Version,
			})
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errOut, "scaffolded skill at %s\n", res.SkillDir)
			_, _ = fmt.Fprintf(errOut, "  SKILL.md:    %s\n", res.SkillMDPath)
			_, _ = fmt.Fprintf(errOut, "  references:  %s\n", res.ReferencesDir)
			if res.ToolDir != "" {
				_, _ = fmt.Fprintf(errOut, "  sample tool: %s\n", res.ToolDir)
				if res.SDKInjected {
					_, _ = fmt.Fprintln(errOut, "  injected SDK vendor copy from CLI binary")
				}
			}
			_, _ = fmt.Fprintf(errOut, "\nNext steps:\n  edit %s\n  sam skill package %s\n", res.SkillMDPath, args[0])
			_, _ = fmt.Fprintln(errOut, "\nTip: run 'sam ai-assistance skill install' at your repo root to let your AI coding assistant author the skill and any bundled tools (add --target claude for Claude Code).")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.SkillDir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing non-empty skills/<name>/ directory")
	cmd.Flags().BoolVar(&withTool, "with-tool", false, "Also scaffold a sample bundled tool under tools/<name>/")
	cmd.Flags().StringVar(&lang, "lang", "go", "Bundled sample tool language: go or python (implies --with-tool)")
	return cmd
}

func newSkillSyncCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "sync [PATH]",
		Short: "Refresh the SDK in your skills' bundled tools after a CLI upgrade",
		Long: `Re-vendor the embedded Go SDK into a skill's bundled tools.

PATH defaults to "." and accepts the following forms:
  - declarative-config repo root      → sync every skills/<name>/
  - a skills/ directory               → sync every skill inside it
  - a skills/<name>/ directory        → sync that one skill

Use --name to restrict a repo-wide invocation to a single skill. Only
bundled runtime:go tools (skills/<name>/tools/<dir>/ with a go.mod) are
re-vendored; skills without Go tools are skipped.`,
		Example: `  sam skill sync
  sam skill sync --name summarizer
  sam skill sync ./my-repo/skills/summarizer`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			res, err := skillinit.Sync(skillinit.SyncOptions{Path: path, Name: name})
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			if len(res.Synced) == 0 {
				_, _ = fmt.Fprintln(errOut, "no skills with bundled Go tools matched — nothing to sync")
				return nil
			}
			_, _ = fmt.Fprintf(errOut, "re-vendored SDK in %d tool dir(s) across %d skill(s): %s\n",
				res.ToolDirs, len(res.Synced), strings.Join(res.Synced, ", "))
			out := cmd.OutOrStdout()
			for _, name := range res.Synced {
				_, _ = fmt.Fprintln(out, name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Sync only this skill by name")
	return cmd
}

func newSkillBuildTargetCmd() *cobra.Command {
	cmd := newToolsetBuildTargetCmd()
	cmd.Short = "Show the OS and CPU your platform expects for tool binaries"
	cmd.Long = `Show the OS and CPU architecture the deployed Secure Tool Runtime (STR) expects skill tool binaries to use.

Bundled skill tools are cross-compiled to the architecture the STR runs.
"sam skill package" resolves this automatically; use this command when you
build a bundled tool by hand. The value, flags, and output formats are
identical to "sam toolset build-target" (the STR build target is
platform-wide) — see that command for the resolution rules and --format
options.`
	return cmd
}
