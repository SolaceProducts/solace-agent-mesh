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
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeStatParams struct {
	Path string `json:"path" jsonschema:"description=Path under the workspace volume to stat. Required."`
}

const volumeStatInstructions = `## volume_stat — Metadata for a single path

Returns the kind, size, mode bits, and modtime of one path under the
workspace volume. Cheap; intended for existence checks and metadata
lookup before deciding which other tool to dispatch.

Use ` + "`volume_list`" + ` for directory enumeration and ` + "`volume_glob`" + ` for
pattern search. ` + "`volume_stat`" + ` is the right tool for "does this exist?"
and "is this a file or a directory?".

Output shape:

    kind:    file | dir | link | other
    size:    <bytes> | -
    mode:    <octal>
    modtime: <RFC 3339 UTC>
    path:    <relative-to-volume>

A symlink reports its own kind ` + "`link`" + ` and its size as the length of
the target name; it does NOT follow the symlink. Tools that want to
stat the target should call again with the resolved path.`

func volumeStat(_ context.Context, params volumeStatParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	info, err := os.Lstat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("path not found: %s", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}

	kind := classifyEntry(info)
	sizeStr := "-"
	if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		sizeStr = fmt.Sprintf("%d", info.Size())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "kind:    %s\n", kind)
	fmt.Fprintf(&b, "size:    %s\n", sizeStr)
	fmt.Fprintf(&b, "mode:    %#o\n", info.Mode().Perm())
	fmt.Fprintf(&b, "modtime: %s\n", info.ModTime().UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&b, "path:    %s\n", params.Path)
	return sdk.OK(b.String()), nil
}
