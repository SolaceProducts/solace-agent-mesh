// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
	skillbuild "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/skillinit"
)

type fixedTargetResolver struct{ os, arch, python string }

func (r fixedTargetResolver) Resolve(context.Context) (string, string, string, error) {
	return r.os, r.arch, r.python, nil
}

func newSkillPackageCmd() *cobra.Command {
	var (
		url    string
		target string
		output string
	)
	cmd := &cobra.Command{
		Use:   "package NAME [PATH]",
		Short: "Build a skill into a zip you can upload in the web UI",
		Long: `Build a skill and zip it into a bundle you can upload through the web UI.

NAME is the skill directory name (e.g. "summarizer"). PATH (optional)
locates the skill:
  - a declarative-config repo root            → skills/<name>/ is used
  - a skills/ directory                       → <path>/<name>/ is used
  - a full path ending in skills/<name>       → used verbatim
PATH defaults to the current working directory.

The command runs the same build pipeline "sam config apply" uses: it
cross-compiles bundled Go tools and builds native wheels for bundled
Python tools (both declared in tools/manifest.yaml) for the deployment
target, then zips the skill directory (SKILL.md + references + compiled
tools) into the bundle the platform expects. Use it when you deploy a
skill by uploading the bundle in the WebUI instead of running
"sam config apply".

Build target resolution matches "sam toolset build-target" and affects
bundled tools — OS/arch for both, plus the STR's Python version for
Python tools (so native wheels match its interpreter ABI):
  1. SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH (and SAM_TOOL_PYTHON_VERSION)
     env vars (explicit wins).
  2. The platform's toolsets/buildTarget endpoint, when --url (or --target) is given.
  3. The baked-in linux/arm64 default, used ONLY without --url (offline).
     With --url, a platform that reports no STR runtime yet is a hard
     error rather than a guessed arch.

Examples:
  sam skill package summarizer
  sam skill package summarizer ./my-repo --url https://platform.example.com
  sam skill package summarizer --target dev
  sam skill package summarizer -o /tmp/summarizer.zip`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			return runSkillPackage(cmd.Context(), cmd, args[0], path, url, target, output)
		},
	}
	addPackageFlags(cmd, &url, &target, &output)
	return cmd
}

func runSkillPackage(ctx context.Context, cmd *cobra.Command, name, path, url, target, output string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	targetURL, err := platformURLFromFlags(url, target)
	if err != nil {
		return err
	}
	recordAddressedPlatformURL(ctx, targetURL)

	skillDir, err := skillinit.ResolveSkillDir(name, path)
	if err != nil {
		return err
	}
	if st, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr != nil || st.IsDir() {
		return fmt.Errorf("skill not found: %s has no SKILL.md", skillDir)
	}

	return writePackagedBundle(ctx, cmd, packageRequest{
		kind:       "skill",
		name:       name,
		targetURL:  targetURL,
		output:     output,
		uploadHint: "Skills → Upload skill",
	},
		func(targetOS, targetArch, pyVer string) ([]byte, error) {
			buildRes, bErr := skillbuild.BuildSkillTools(ctx, name, skillDir, skillbuild.SkillBuildOptions{
				Stdout:         cmd.ErrOrStderr(),
				Stderr:         cmd.ErrOrStderr(),
				TargetResolver: fixedTargetResolver{os: targetOS, arch: targetArch, python: pyVer},
			})
			if bErr != nil {
				return nil, fmt.Errorf("build skill %s: %w", name, bErr)
			}
			data, _, bundleErr := bundle.BundleDirWithOverlay(skillDir, buildRes.Overlay)
			if bundleErr != nil {
				return nil, fmt.Errorf("bundle %s: %w", name, bundleErr)
			}
			return data, nil
		})
}
