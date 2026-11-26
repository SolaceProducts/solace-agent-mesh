// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeReadParams struct {
	Path      string `json:"path" jsonschema:"description=Path under the workspace volume (relative or absolute under the mount). Required."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"description=Optional first line to include (1-indexed). 0 means start at the beginning."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"description=Optional last line to include (1-indexed, inclusive). 0 means read to EOF."`
}

const volumeReadInstructions = `## volume_read — Read a file from the workspace volume

Reads the file at the given path and returns its contents as text.
- ` + "`path`" + ` is relative to the workspace volume mount.
- Optional ` + "`start_line`" + ` / ` + "`end_line`" + ` slice the file. Lines are 1-indexed; both ends inclusive.
- The full file is returned when both are 0 (or omitted).
- Output is capped at 256 KiB. Anything larger is truncated with a marker;
  re-run with a narrower line range.

Paths that escape the volume (` + "`../`" + `, absolute paths outside the mount,
symlinks pointing outside the mount) are rejected before any read.`

func volumeRead(_ context.Context, params volumeReadParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	if params.StartLine < 0 || params.EndLine < 0 {
		return sdk.Error("start_line and end_line must be >= 0 (0 means unconstrained)"), nil
	}
	if params.StartLine > 0 && params.EndLine > 0 && params.EndLine < params.StartLine {
		return sdk.Error(fmt.Sprintf("end_line (%d) must be >= start_line (%d)", params.EndLine, params.StartLine)), nil
	}

	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	f, err := os.Open(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("file not found: %s", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("open %s: %v", params.Path, err)), nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}
	if info.IsDir() {
		return sdk.Error(fmt.Sprintf("%s is a directory; use volume_list to enumerate its contents", params.Path)), nil
	}

	if params.StartLine == 0 && params.EndLine == 0 {
		data, err := io.ReadAll(io.LimitReader(f, maxOutputBytes+1))
		if err != nil {
			return sdk.Error(fmt.Sprintf("read %s: %v", params.Path, err)), nil
		}
		return sdk.OK(string(truncateForOutputTotal(data, info.Size()))), nil
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out strings.Builder
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if params.StartLine > 0 && lineNum < params.StartLine {
			continue
		}
		if params.EndLine > 0 && lineNum > params.EndLine {
			break
		}
		out.WriteString(scanner.Text())
		out.WriteByte('\n')
		if out.Len() > maxOutputBytes*2 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return sdk.Error(fmt.Sprintf("read %s: %v", params.Path, err)), nil
	}
	return sdk.OK(string(truncateForOutput([]byte(out.String())))), nil
}
