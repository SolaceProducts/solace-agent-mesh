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

type volumeAppendParams struct {
	Path    string `json:"path" jsonschema:"description=Path under the workspace volume to append to. Must already exist."`
	Content string `json:"content" jsonschema:"description=Bytes to append. No newline is added — include one in content if you want one."`
}

const volumeAppendInstructions = `## volume_append — Append to an existing file

Adds ` + "`content`" + ` to the end of ` + "`path`" + `. File must already exist (use
` + "`volume_write`" + ` to create). No newline is added — include one in
` + "`content`" + ` if needed.

Refuses to write through a symlink (the file at ` + "`path`" + ` must itself
be a regular file, not a symlink pointing elsewhere). For wholesale
replacement, use ` + "`volume_write`" + ` with overwrite: true.`

func volumeAppend(_ context.Context, params volumeAppendParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	info, err := os.Lstat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("file not found: %s (use volume_write to create)", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return sdk.Error(fmt.Sprintf("%s is a symlink; refuse to write through symlinks", params.Path)), nil
	}
	if !info.Mode().IsRegular() {
		return sdk.Error(fmt.Sprintf("%s is not a regular file", params.Path)), nil
	}

	if params.Content == "" {
		return sdk.OK(fmt.Sprintf("appended 0 bytes to %s (no content)\n", params.Path)), nil
	}

	f, err := os.OpenFile(absPath, os.O_APPEND|os.O_WRONLY|syscallNoFollow(), 0)
	if err != nil {
		return sdk.Error(fmt.Sprintf("open %s for append: %v", params.Path, err)), nil
	}

	n, writeErr := f.WriteString(params.Content)
	if writeErr != nil {
		_ = f.Close()
		return sdk.Error(fmt.Sprintf("append %s: %v", params.Path, writeErr)), nil
	}
	if closeErr := f.Close(); closeErr != nil {
		return sdk.Error(fmt.Sprintf("close %s after append: %v", params.Path, closeErr)), nil
	}
	return sdk.OK(fmt.Sprintf("appended %d bytes to %s\n", n, params.Path)), nil
}
