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
)

func addPackageFlags(cmd *cobra.Command, url, target, output *string) {
	cmd.Flags().StringVar(url, "url", "",
		"Platform URL to auto-detect the build target. Falls back to SAM_TOOL_TARGET_* env vars, then linux/arm64.")
	cmd.Flags().StringVar(target, "target", "",
		"Named `sam auth login` target to auto-detect the build target from (alternative to --url).")
	cmd.Flags().StringVarP(output, "output", "o", "",
		"Output zip path (default: <name>.zip in the current directory)")
}

type packageRequest struct {
	kind       string
	name       string
	targetURL  string
	output     string
	uploadHint string
	pythonTool bool
}

func writePackagedBundle(ctx context.Context, cmd *cobra.Command, req packageRequest,
	buildZip func(targetOS, targetArch, pythonVersion string) ([]byte, error)) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	targetOS, targetArch, pythonVersion, source, err := resolveToolBuildTarget(ctx, req.targetURL)
	if err != nil {
		return err
	}
	target := fmt.Sprintf("%s/%s", targetOS, targetArch)
	if req.pythonTool && pythonVersion != "" {
		target += ", Python " + pythonVersion
	}
	_, _ = fmt.Fprintf(errOut, "packaging %s %s (build target %s, source: %s)\n", req.kind, req.name, target, source)
	if source == "default" {
		_, _ = fmt.Fprintf(errOut, "note: build target defaulted to %s (no --url, --target, or SAM_TOOL_TARGET_* given). "+
			"If your STR runs a different arch, the bundle fails discovery with \"exec format error\" — "+
			"pass --url/--target or set SAM_TOOL_TARGET_OS/ARCH to match it.\n", target)
	}
	if req.pythonTool && pythonVersion == "" {
		_, _ = fmt.Fprintf(errOut, "note: the STR has not reported its Python version; the build falls back to its default. "+
			"Set SAM_TOOL_PYTHON_VERSION to match your STR so the native wheels load.\n")
	}

	zipData, err := buildZip(targetOS, targetArch, pythonVersion)
	if err != nil {
		return err
	}

	output := req.output
	if output == "" {
		output = req.name + ".zip"
	}
	if writeErr := os.WriteFile(output, zipData, 0o644); writeErr != nil {
		return fmt.Errorf("write %s: %w", output, writeErr)
	}
	absOut, absErr := filepath.Abs(output)
	if absErr != nil {
		absOut = output
	}

	_, _ = fmt.Fprintf(errOut, "wrote %s (%d bytes)\n", absOut, len(zipData))
	_, _ = fmt.Fprintf(errOut, "Next step: upload %s in the WebUI (%s).\n", filepath.Base(absOut), req.uploadHint)
	_, _ = fmt.Fprintln(out, absOut)
	return nil
}
