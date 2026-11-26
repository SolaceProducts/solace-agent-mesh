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
	"path/filepath"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeWriteParams struct {
	Path          string `json:"path" jsonschema:"description=Path under the workspace volume to write. Required."`
	Content       string `json:"content" jsonschema:"description=File contents. Written verbatim; no trailing newline added."`
	CreateParents bool   `json:"create_parents,omitempty" jsonschema:"description=Create missing parent directories. Defaults to false (write fails if the parent doesn't exist)."`
	Overwrite     bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite an existing file. Defaults to false (write fails if path already exists)."`
}

const volumeWriteInstructions = `## volume_write — Create or replace a file

Writes ` + "`content`" + ` to ` + "`path`" + ` under the workspace volume. The agent
config must bind this tool's workspace slot to a readwrite volume —
bwrap enforces the mount mode at the kernel layer.

Safety defaults:
- ` + "`overwrite`" + ` defaults to false. If the path exists the call fails
  rather than silently clobbering. Pass ` + "`overwrite: true`" + ` only when
  you actually mean to replace.
- ` + "`create_parents`" + ` defaults to false. If the parent directory
  doesn't exist the call fails. Pass ` + "`create_parents: true`" + ` to
  recursively mkdir-p the way to the file.
- File mode is 0o644 (rw for owner, r for everyone else).

For appending to an existing file, use ` + "`volume_append`" + `. For
in-place edits, use ` + "`volume_edit`" + `.`

func volumeWrite(_ context.Context, params volumeWriteParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	if info, lstatErr := os.Lstat(absPath); lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return sdk.Error(fmt.Sprintf("%s is a symlink; refuse to write through symlinks (delete the symlink first if you intend to replace it)", params.Path)), nil
		}
		if !params.Overwrite {
			return sdk.Error(fmt.Sprintf("%s already exists; pass overwrite: true to replace", params.Path)), nil
		}
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, lstatErr)), nil
	}

	if params.CreateParents {
		parent := filepath.Dir(absPath)
		if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
			return sdk.Error(fmt.Sprintf("mkdir parents for %s: %v", params.Path, mkErr)), nil
		}
	}

	root, _ := volumeRoot(tc)
	if symErr := refuseSymlinkedParent(root, absPath); symErr != nil {
		return sdk.Error(symErr.Error()), nil
	}

	f, openErr := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow(), 0o644)
	if openErr != nil {
		return sdk.Error(fmt.Sprintf("write %s: %v", params.Path, openErr)), nil
	}
	if _, writeErr := f.WriteString(params.Content); writeErr != nil {
		_ = f.Close()
		return sdk.Error(fmt.Sprintf("write %s: %v", params.Path, writeErr)), nil
	}
	if closeErr := f.Close(); closeErr != nil {
		return sdk.Error(fmt.Sprintf("close %s: %v", params.Path, closeErr)), nil
	}

	return sdk.OK(fmt.Sprintf("wrote %d bytes to %s\n", len(params.Content), params.Path)), nil
}
