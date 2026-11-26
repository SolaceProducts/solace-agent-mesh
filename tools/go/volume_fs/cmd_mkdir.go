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

type volumeMkdirParams struct {
	Path      string `json:"path" jsonschema:"description=Directory path to create under the workspace volume. Required."`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=Create missing parent directories along the way (mkdir -p). Defaults to false (fail if parent doesn't exist)."`
}

const volumeMkdirInstructions = `## volume_mkdir — Create a directory

Creates the directory at ` + "`path`" + ` under the workspace volume.

- ` + "`recursive: true`" + ` is the equivalent of ` + "`mkdir -p`" + ` — creates any
  missing parent directories along the way.
- If the path already exists as a directory, the call is a no-op
  (returns success).
- If the path already exists as a file or symlink, the call fails.
- New directories are mode 0o755 (rwx for owner, rx for everyone
  else) — standard mkdir default.`

func volumeMkdir(_ context.Context, params volumeMkdirParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	if info, err := os.Lstat(absPath); err == nil {
		if info.IsDir() {
			return sdk.OK(fmt.Sprintf("directory already exists: %s\n", params.Path)), nil
		}
		return sdk.Error(fmt.Sprintf("%s exists but is not a directory", params.Path)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}

	root, _ := volumeRoot(tc)
	if symErr := refuseSymlinkedParent(root, absPath); symErr != nil {
		return sdk.Error(symErr.Error()), nil
	}

	mkFn := os.Mkdir
	if params.Recursive {
		mkFn = os.MkdirAll
	}
	if err := mkFn(absPath, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("mkdir %s: %v", params.Path, err)), nil
	}
	return sdk.OK(fmt.Sprintf("created directory: %s\n", params.Path)), nil
}
