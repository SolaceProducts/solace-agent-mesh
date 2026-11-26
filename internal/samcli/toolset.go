// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/toolinit"
)

func newToolsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "toolset",
		Short: "Build the custom tools your agents can call",
		Long: `Scaffold and maintain toolset directories.

"sam toolset init" creates a new toolsets/<name>/ directory with the
kind:toolset metadata YAML and a src/ subdirectory pre-populated with
the Agent Mesh tool SDK. "sam toolset sync" re-vendors the SDK in one or
every toolset under toolsets/*/src/. "sam toolset package" builds one
toolset and zips it into a bundle you can upload through the web UI
without running "sam config apply". "sam toolset validate" builds a
toolset for your host and runs the Secure Tool Runtime (STR)'s --schema discovery against it,
so you can confirm your tools load before packaging and uploading.
"sam toolset build-target" reports the OS and CPU architecture the
deployed STR expects toolset binaries to use.

The injected SDK is embedded in the CLI binary, so init and sync require
no network access; package and build-target consult the platform only
when you pass --url (or --target).`,
	}
	cmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core:"},
		&cobra.Group{ID: "advanced", Title: "Advanced:"},
	)
	addSub := func(group string, c *cobra.Command) {
		c.GroupID = group
		cmd.AddCommand(c)
	}
	addSub("core", newToolsetInitCmd())
	addSub("core", newToolsetValidateCmd())
	addSub("core", newToolsetPackageCmd())
	addSub("advanced", newToolsetSyncCmd())
	addSub("advanced", newToolsetBuildTargetCmd())
	return cmd
}

type targetOutputFormat string

const (
	targetFormatText  targetOutputFormat = "text"
	targetFormatJSON  targetOutputFormat = "json"
	targetFormatShell targetOutputFormat = "shell"
)

const targetSourceEnv = "env"

const targetSourceFallback = "fallback"

func newToolsetBuildTargetCmd() *cobra.Command {
	var (
		url    string
		target string
		format string
	)
	cmd := &cobra.Command{
		Use:   "build-target",
		Short: "Show the OS and CPU your platform expects for tool binaries",
		Long: `Show the OS and CPU architecture the deployed Secure Tool Runtime (STR) expects toolset binaries to use.

The CLI consults the platform's toolsets/buildTarget endpoint so the
answer reflects what the STR is actually running. Use this when you
build a toolset by hand (` + "`go build`" + ` outside ` + "`sam config apply`" + `)
and want to cross-compile to the right architecture without guessing.

Point at the platform with --url (a raw URL) or --target (a named
` + "`sam auth login`" + ` entry). When SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH
are set, those win and the platform is not consulted (source: env). On a
mixed-fleet response the command exits non-zero and lists every observed
target so you can pick one explicitly via the env vars.

Output formats:
  --format text   "linux/arm64" (default; for humans)
  --format json   {"os":"linux","arch":"arm64","source":"str-init"}
  --format shell  GOOS=linux GOARCH=arm64
                  (suitable for ` + "`eval \"$(sam toolset build-target --format shell --url URL)\"`" + `)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmtMode := targetOutputFormat(strings.ToLower(format))
			switch fmtMode {
			case targetFormatText, targetFormatJSON, targetFormatShell:
			default:
				return fmt.Errorf("--format must be one of text|json|shell, got %q", format)
			}
			resolvedURL, err := platformURLFromFlags(url, target)
			if err != nil {
				return err
			}
			return runToolsetBuildTarget(cmd.Context(), cmd, resolvedURL, fmtMode)
		},
	}
	cmd.Flags().StringVar(&url, "url", "",
		"Platform URL (e.g. https://platform.example.com). Required when SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH are not both set.")
	cmd.Flags().StringVar(&target, "target", "",
		"Named `sam auth login` target to resolve the platform URL from (alternative to --url).")
	cmd.Flags().StringVar(&format, "format", string(targetFormatText),
		"Output format: text | json | shell")
	return cmd
}

func runToolsetBuildTarget(ctx context.Context, cmd *cobra.Command, targetURL string, format targetOutputFormat) error {
	if ctx == nil {
		ctx = context.Background()
	}

	envOS := os.Getenv("SAM_TOOL_TARGET_OS")
	envArch := os.Getenv("SAM_TOOL_TARGET_ARCH")
	if envOS != "" && envArch != "" {
		return printToolBuildTarget(cmd, format, envOS, envArch, targetSourceEnv)
	}

	if targetURL == "" {
		return errors.New("--url (or --target) is required unless SAM_TOOL_TARGET_OS and SAM_TOOL_TARGET_ARCH are both set")
	}

	resp, err := queryPlatformBuildTarget(ctx, targetURL)
	if err != nil {
		return err
	}
	if resp.Source == targetSourceFallback {
		return &reconcile.UndiscoverableTargetError{}
	}

	osS := resp.OS
	archS := resp.Arch
	source := resp.Source
	if envOS != "" {
		osS = envOS
		source = targetSourceEnv
	}
	if envArch != "" {
		archS = envArch
		source = targetSourceEnv
	}
	return printToolBuildTarget(cmd, format, osS, archS, source)
}

func printToolBuildTarget(cmd *cobra.Command, format targetOutputFormat, osS, arch, source string) error {
	out := cmd.OutOrStdout()
	switch format {
	case targetFormatJSON:
		body := map[string]string{"os": osS, "arch": arch, "source": source}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(body)
	case targetFormatShell:
		_, err := fmt.Fprintf(out, "GOOS=%s GOARCH=%s\n", osS, arch)
		return err
	default:
		_, err := fmt.Fprintf(out, "%s/%s\n", osS, arch)
		return err
	}
}

func newToolsetInitCmd() *cobra.Command {
	var (
		lang  string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "init NAME [PATH]",
		Short: "Create a new toolset with starter code and the SDK",
		Long: `Scaffold a new toolset under toolsets/<name>/.

NAME is the toolset directory name (e.g. "pptx"). PATH (optional) is
one of:
  - a declarative-config repo root            → toolsets/<name>/ is created
  - a toolsets/ directory                     → <path>/<name>/ is created
  - a full path ending in toolsets/<name>     → used verbatim
PATH defaults to the current working directory.

Two artifacts are written:
  - toolsets/<name>.yaml         kind:toolset metadata header
  - toolsets/<name>/src/...      skeleton + embedded SDK

Examples:
  sam toolset init pptx --lang go
  sam toolset init pptx ./my-repo --lang go
  sam toolset init pptx ./my-repo/toolsets --lang python
  sam toolset init pptx ./my-repo/toolsets/pptx --lang go --force`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			if lang == "" {
				return errors.New("--lang is required (one of: go, python)")
			}
			res, err := toolinit.Init(toolinit.Options{
				Name:       args[0],
				Path:       path,
				Lang:       toolinit.Lang(lang),
				Force:      force,
				CLIVersion: Version,
			})
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(errOut, "scaffolded %s toolset at %s\n", res.Lang, res.SrcDir)
			_, _ = fmt.Fprintf(errOut, "  metadata: %s\n", res.MetadataPath)
			if res.SDKInjected {
				_, _ = fmt.Fprintf(errOut, "  injected SDK vendor copy from CLI binary\n")
			}
			_, _ = fmt.Fprintf(errOut, "\nNext steps:\n  cd %s\n  ./build.sh\n", res.SrcDir)
			_, _ = fmt.Fprintln(errOut, "\nTip: run 'sam ai-assistance skill install' at your repo root to let your AI coding assistant write the tool (add --target claude for Claude Code).")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.SrcDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&lang, "lang", "", "Scaffolding language: go | python (required)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing non-empty toolsets/<name>/src/ directory")
	_ = cmd.MarkFlagRequired("lang")
	return cmd
}

func newToolsetSyncCmd() *cobra.Command {
	var (
		name string
		lang string
	)
	cmd := &cobra.Command{
		Use:   "sync [PATH]",
		Short: "Refresh the SDK in your toolsets after a CLI upgrade",
		Long: `Re-vendor the embedded SDK into existing toolset source dirs.

PATH defaults to "." and accepts the following forms:
  - declarative-config repo root              → sync every toolsets/*/src/
  - a toolsets/ directory                     → sync every toolset inside it
  - a toolsets/<name>/ directory              → sync that one toolset
  - a toolsets/<name>/src/ directory          → sync that one toolset

Use --name to restrict a repo-wide invocation to a single toolset.
--lang overrides per-toolset language detection (defaults to inferring
from go.mod or pyproject.toml under src/).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			res, err := toolinit.Sync(toolinit.SyncOptions{
				Path: path,
				Name: name,
				Lang: toolinit.Lang(lang),
			})
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			if len(res.Synced) == 0 {
				_, _ = fmt.Fprintln(errOut, "no toolsets matched — nothing to sync")
				return nil
			}
			_, _ = fmt.Fprintf(errOut, "synced %d toolset(s): %s\n", len(res.Synced), strings.Join(res.Synced, ", "))
			out := cmd.OutOrStdout()
			for _, name := range res.Synced {
				_, _ = fmt.Fprintln(out, name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Sync only this toolset by name")
	cmd.Flags().StringVar(&lang, "lang", "", "Override the inferred language: go | python")
	return cmd
}

func newToolsetPackageCmd() *cobra.Command {
	var (
		url    string
		target string
		output string
	)
	cmd := &cobra.Command{
		Use:   "package NAME [PATH]",
		Short: "Build a toolset into a zip you can upload in the web UI",
		Long: `Build a toolset and zip it into a bundle you can upload through the web UI.

NAME is the toolset directory name (e.g. "pptx"). PATH (optional) locates
the toolset the same way "sam toolset init" does:
  - a declarative-config repo root            → toolsets/<name>/ is used
  - a toolsets/ directory                     → <path>/<name>/ is used
  - a full path ending in toolsets/<name>     → used verbatim
PATH defaults to the current working directory.

The command runs the same build pipeline "sam config apply" uses (the Go
or Python convention build, plus the Python --schema discoverability
check) and zips the result into the AWS-Lambda-Layer shape the platform
expects (manifest.yaml + python/ layer, or the Go binary, at the zip
root). Use it when you deploy a toolset by uploading the bundle in the
WebUI instead of running "sam config apply".

Build target (GOOS/GOARCH) resolution matches "sam toolset build-target":
  1. SAM_TOOL_TARGET_OS / SAM_TOOL_TARGET_ARCH env vars (explicit wins).
  2. The platform's toolsets/buildTarget endpoint, when --url (or --target) is given.
  3. The baked-in linux/arm64 fallback.

Pass --url (or --target) so the bundle matches the architecture your Secure Tool Runtime (STR) runs
on; a mismatch is rejected at discovery time with "exec format error".

Examples:
  sam toolset package pptx
  sam toolset package pptx ./my-repo --url https://platform.example.com
  sam toolset package pptx --target dev
  sam toolset package pptx -o /tmp/pptx.zip`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			return runToolsetPackage(cmd.Context(), cmd, args[0], path, url, target, output)
		},
	}
	addPackageFlags(cmd, &url, &target, &output)
	return cmd
}

func runToolsetPackage(ctx context.Context, cmd *cobra.Command, name, path, url, target, output string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	targetURL, err := platformURLFromFlags(url, target)
	if err != nil {
		return err
	}
	recordAddressedPlatformURL(ctx, targetURL)

	toolsetDir, err := toolinit.ResolveToolsetDir(name, path)
	if err != nil {
		return err
	}
	srcDir := filepath.Join(toolsetDir, "src")
	if st, statErr := os.Stat(srcDir); statErr != nil || !st.IsDir() {
		return fmt.Errorf("toolset src dir not found: %s", srcDir)
	}

	return writePackagedBundle(ctx, cmd, packageRequest{
		kind:       "toolset",
		name:       name,
		targetURL:  targetURL,
		output:     output,
		uploadHint: "Toolsets → Upload bundle",
		pythonTool: toolsetUsesPython(srcDir),
	},
		func(targetOS, targetArch, pythonVersion string) ([]byte, error) {
			plan, err := build.Resolve(name, srcDir)
			if err != nil {
				return nil, err
			}
			bundleSrc := srcDir
			if plan.Kind != build.KindNone {
				distDir, runErr := build.Run(ctx, plan, build.RunOptions{
					Stdout:        cmd.ErrOrStderr(),
					Stderr:        cmd.ErrOrStderr(),
					TargetOS:      targetOS,
					TargetArch:    targetArch,
					PythonVersion: pythonVersion,
				})
				if runErr != nil {
					return nil, fmt.Errorf("build %s: %w", name, runErr)
				}
				bundleSrc = distDir
			}
			data, _, bErr := bundle.BundleDir(bundleSrc)
			if bErr != nil {
				return nil, fmt.Errorf("bundle %s: %w", name, bErr)
			}
			return data, nil
		})
}

func toolsetUsesPython(srcDir string) bool {
	for _, marker := range []string{"pyproject.toml", "requirements.txt"} {
		if st, err := os.Stat(filepath.Join(srcDir, marker)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func queryPlatformBuildTarget(ctx context.Context, targetURL string) (*reconcile.BuildTargetResponse, error) {
	tg := target.Target{URL: targetURL, Auth: target.AuthSpec{Type: target.AuthOAuth}}
	authResolver, err := defaultOAuthResolver()
	if err != nil {
		return nil, err
	}
	resolved, err := tg.Resolve(ctx, authResolver)
	if err != nil {
		if !errors.Is(err, target.ErrNotLoggedIn) {
			return nil, fmt.Errorf("resolve target: %w", err)
		}
		resolved = target.Resolved{URL: strings.TrimRight(targetURL, "/")}
	}
	resp, err := reconcile.NewClient(resolved).GetToolBuildTarget(ctx)
	if err != nil {
		return nil, hintLoginIfUnauthorized(err)
	}
	return resp, nil
}

func hintLoginIfUnauthorized(err error) error {
	if he, ok := errors.AsType[*reconcile.HTTPError](err); ok && he.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("the platform requires authentication; run `sam auth login` first (%w)", err)
	}
	return err
}

func resolveToolBuildTarget(ctx context.Context, targetURL string) (targetOS, targetArch, pythonVersion, source string, err error) {
	envOS := os.Getenv("SAM_TOOL_TARGET_OS")
	envArch := os.Getenv("SAM_TOOL_TARGET_ARCH")
	envPy := os.Getenv("SAM_TOOL_PYTHON_VERSION")
	if envOS != "" && envArch != "" {
		return envOS, envArch, envPy, targetSourceEnv, nil
	}
	if targetURL == "" {
		t := build.ResolveTarget(build.Target{OS: envOS, Arch: envArch})
		return t.OS, t.Arch, envPy, "default", nil
	}

	resp, err := queryPlatformBuildTarget(ctx, targetURL)
	if err != nil {
		return "", "", "", "", err
	}
	if resp.Source == targetSourceFallback {
		return "", "", "", "", &reconcile.UndiscoverableTargetError{}
	}
	targetOS, targetArch, pythonVersion, source = resp.OS, resp.Arch, resp.PythonVersion, resp.Source
	if envOS != "" {
		targetOS, source = envOS, targetSourceEnv
	}
	if envArch != "" {
		targetArch, source = envArch, targetSourceEnv
	}
	if envPy != "" {
		pythonVersion = envPy
	}
	return targetOS, targetArch, pythonVersion, source, nil
}
