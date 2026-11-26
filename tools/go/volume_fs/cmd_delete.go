// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeDeleteParams struct {
	Path      string `json:"path" jsonschema:"description=Path under the workspace volume to delete. Required."`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=Delete a non-empty directory and everything inside. Required for directories with contents."`
}

const volumeDeleteInstructions = `## volume_delete — Remove a file or directory

Deletes the path under the workspace volume.

Safety:
- A non-empty directory requires ` + "`recursive: true`" + ` — without it, the
  call fails. This is the equivalent of ` + "`rm`" + ` vs ` + "`rm -rf`" + `; the
  default refuses the dangerous form.
- A symlink is removed itself, not its target.
- The workspace root cannot be deleted (` + "`path: \"\"`" + ` or ` + "`path: \".\"`" + `
  is rejected).
- Cannot delete outside the volume — safejoin in the helper layer
  catches ` + "`../`" + ` traversal and symlinked subtrees pointing outside.

Output:
- ` + "`deleted file: <path>`" + ` for a regular file or symlink.
- ` + "`deleted directory tree: <path>`" + ` for ` + "`recursive: true`" + ` on a
  directory.`

func volumeDelete(_ context.Context, params volumeDeleteParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" || params.Path == "." || params.Path == "/" {
		return sdk.Error("refuse to delete the workspace root"), nil
	}
	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	root, _ := volumeRoot(tc)
	if absPath == root {
		return sdk.Error("refuse to delete the workspace root"), nil
	}

	info, err := os.Lstat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("path not found: %s", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}

	if symErr := refuseSymlinkedParent(root, absPath); symErr != nil {
		return sdk.Error(symErr.Error()), nil
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		if err := os.Remove(absPath); err != nil {
			return sdk.Error(fmt.Sprintf("delete %s: %v", params.Path, err)), nil
		}
		return sdk.OK(fmt.Sprintf("deleted file: %s\n", params.Path)), nil
	}

	if !params.Recursive {
		if err := os.Remove(absPath); err != nil {
			return sdk.Error(fmt.Sprintf(
				"refuse to delete non-empty directory %s without recursive: true (%v)",
				params.Path, err)), nil
		}
		return sdk.OK(fmt.Sprintf("deleted empty directory: %s\n", params.Path)), nil
	}
	if err := os.RemoveAll(absPath); err != nil {
		return sdk.Error(fmt.Sprintf("delete tree %s: %v", params.Path, err)), nil
	}
	return sdk.OK(fmt.Sprintf("deleted directory tree: %s\n", params.Path)), nil
}
