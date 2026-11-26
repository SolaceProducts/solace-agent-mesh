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

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

func newToolsetValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate NAME [PATH]",
		Short: "Check that your toolset builds and its tools are discoverable",
		Long: `Build a toolset for your host machine and run the same --schema
discovery the Secure Tool Runtime (STR) performs, so you can confirm your
tools load before you package and upload them.

NAME is the toolset directory name; PATH (optional) locates it, defaulting
to the current working directory (same resolution as "sam toolset package").

This is a fast local pre-flight. It catches the common reasons a toolset
fails to load — a wrong manifest "executable" path, an import error in the
tool's code, a bad tool signature, a schema-generation failure, or a missing
dependency — without the build → upload → discover round-trip.

It builds for your host, not the STR's deployment target, so it validates
your tool's code, manifest, and schema. Platform-specific native-wheel
issues are not reproduced here; "sam toolset package" pins the STR's target
for the bundle you upload.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			return runToolsetValidate(cmd.Context(), cmd, args[0], path)
		},
	}
	return cmd
}

func runToolsetValidate(ctx context.Context, cmd *cobra.Command, name, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	toolsetDir, err := toolinit.ResolveToolsetDir(name, path)
	if err != nil {
		return err
	}
	srcDir := filepath.Join(toolsetDir, "src")
	if st, statErr := os.Stat(srcDir); statErr != nil || !st.IsDir() {
		return fmt.Errorf("toolset src dir not found: %s", srcDir)
	}

	out := cmd.OutOrStdout()
	plan, err := build.Resolve(name, srcDir)
	if err != nil {
		return err
	}

	manifestDir := srcDir
	if plan.Kind != build.KindNone {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "building %s for your host (%s/%s)...\n", name, runtime.GOOS, runtime.GOARCH)
		distDir, runErr := build.Run(ctx, plan, build.RunOptions{
			Stdout:     cmd.ErrOrStderr(),
			Stderr:     cmd.ErrOrStderr(),
			TargetOS:   runtime.GOOS,
			TargetArch: runtime.GOARCH,
		})
		if runErr != nil {
			return fmt.Errorf("build %s for host: %w", name, runErr)
		}
		manifestDir = distDir
	}

	return validateDiscovery(out, name, manifestDir)
}

type manifestToolEntry struct {
	Runtime    string `yaml:"runtime"`
	Executable string `yaml:"executable"`
}

func validateDiscovery(out io.Writer, name, dir string) error {
	entries, err := readManifestToolEntries(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no tools declared in %s — nothing to validate", filepath.Join(dir, "manifest.yaml"))
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
		exe := filepath.Join(dir, entry.Executable)
		pyPath := ""
		if entry.Runtime == "python" {
			pyPath = str.PythonPathFor(dir)
		}
		tools, discErr := str.DiscoverFromExecutable(exe, pyPath)
		if discErr != nil {
			_, _ = fmt.Fprintf(out, "  ✗ %s: %v\n", key, discErr)
			failed = append(failed, key)
			continue
		}
		toolNames := make([]string, 0, len(tools))
		for tn := range tools {
			toolNames = append(toolNames, tn)
		}
		slices.Sort(toolNames)
		_, _ = fmt.Fprintf(out, "  ✓ %s: discovered %d tool(s)\n", key, len(tools))
		for _, tn := range toolNames {
			_, _ = fmt.Fprintf(out, "      - %s: %s\n", tn, tools[tn].Description)
		}
		total += len(tools)
	}

	if len(failed) > 0 {
		return fmt.Errorf("%s: %d of %d manifest entr(ies) failed discovery: %v", name, len(failed), len(entries), failed)
	}
	_, _ = fmt.Fprintf(out, "%s OK — %d tool(s) discovered.\n", name, total)
	return nil
}

func readManifestToolEntries(manifestPath string) (map[string]manifestToolEntry, error) {
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
	entries := map[string]manifestToolEntry{}
	if doc.Tools.Kind == yaml.MappingNode {
		if decErr := doc.Tools.Decode(&entries); decErr != nil {
			return nil, fmt.Errorf("parse tools in %s: %w", manifestPath, decErr)
		}
	}
	return entries, nil
}
