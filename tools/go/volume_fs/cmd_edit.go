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

type volumeEditParams struct {
	Path       string `json:"path" jsonschema:"description=Path under the workspace volume to edit. Required."`
	OldString  string `json:"old_string" jsonschema:"description=Exact text to find. Must match verbatim — no regex, no whitespace normalisation."`
	NewString  string `json:"new_string" jsonschema:"description=Replacement text. Empty string deletes the matched region."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"description=Replace every occurrence. Defaults to false (replace the single occurrence; error if old_string appears more than once)."`
}

const volumeEditInstructions = `## volume_edit — In-place find and replace

Replaces ` + "`old_string`" + ` with ` + "`new_string`" + ` in the file at ` + "`path`" + `.

Exact-string match. No regex, no whitespace normalisation, no
case folding. Include enough surrounding context in ` + "`old_string`" + ` to
uniquely identify the target — Claude Code's Edit shape.

Behaviour:
- If ` + "`replace_all`" + ` is false (default) and ` + "`old_string`" + ` appears
  more than once, the call fails with the count. Add context to
  disambiguate, or pass ` + "`replace_all: true`" + ` if you really mean every
  occurrence.
- If ` + "`old_string`" + ` doesn't appear at all, the call fails with
  "no match" — the file is left untouched.
- ` + "`new_string == \"\"`" + ` is a deletion; the matched region is removed.
- File mode + ownership are preserved.

For wholesale file replacement, use ` + "`volume_write`" + ` with
overwrite: true. For appending, use ` + "`volume_append`" + `.`

func volumeEdit(_ context.Context, params volumeEditParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Path == "" {
		return sdk.Error("path is required"), nil
	}
	if params.OldString == "" {
		return sdk.Error("old_string is required; use volume_append to add content without an anchor"), nil
	}
	if params.OldString == params.NewString {
		return sdk.Error("old_string and new_string are identical; nothing to do"), nil
	}

	absPath, err := resolveUnderWorkspace(tc, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	info, err := os.Lstat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("file not found: %s", params.Path)), nil
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

	data, err := os.ReadFile(absPath)
	if err != nil {
		return sdk.Error(fmt.Sprintf("read %s: %v", params.Path, err)), nil
	}

	content := string(data)
	count := strings.Count(content, params.OldString)
	if count == 0 {
		return sdk.Error(fmt.Sprintf("no match in %s for old_string (add surrounding context if the string appears in similar shapes elsewhere)", params.Path)), nil
	}
	if count > 1 && !params.ReplaceAll {
		return sdk.Error(fmt.Sprintf(
			"old_string appears %d times in %s; pass replace_all: true to replace every occurrence, "+
				"or include more surrounding context to make the match unique",
			count, params.Path)), nil
	}

	var updated string
	if params.ReplaceAll {
		updated = strings.ReplaceAll(content, params.OldString, params.NewString)
	} else {
		updated = strings.Replace(content, params.OldString, params.NewString, 1)
	}

	f, openErr := os.OpenFile(absPath, os.O_WRONLY|os.O_TRUNC|syscallNoFollow(), info.Mode().Perm())
	if openErr != nil {
		return sdk.Error(fmt.Sprintf("write %s: %v", params.Path, openErr)), nil
	}
	if _, writeErr := f.WriteString(updated); writeErr != nil {
		_ = f.Close()
		return sdk.Error(fmt.Sprintf("write %s: %v", params.Path, writeErr)), nil
	}
	if closeErr := f.Close(); closeErr != nil {
		return sdk.Error(fmt.Sprintf("close %s: %v", params.Path, closeErr)), nil
	}

	var verb string
	if params.ReplaceAll && count > 1 {
		verb = fmt.Sprintf("replaced %d occurrences in", count)
	} else {
		verb = "replaced 1 occurrence in"
	}
	return sdk.OK(fmt.Sprintf("%s %s (was %d bytes, now %d)\n", verb, params.Path, len(content), len(updated))), nil
}
