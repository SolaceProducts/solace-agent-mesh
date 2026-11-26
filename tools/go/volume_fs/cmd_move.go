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

type volumeMoveParams struct {
	From          string `json:"from" jsonschema:"description=Source path under the workspace volume. Must exist."`
	To            string `json:"to" jsonschema:"description=Destination path under the workspace volume. Must NOT exist unless overwrite is true."`
	CreateParents bool   `json:"create_parents,omitempty" jsonschema:"description=Create missing parent directories of the destination. Defaults to false."`
	Overwrite     bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite an existing destination. Defaults to false (move fails if destination exists)."`
}

const volumeMoveInstructions = `## volume_move — Rename or move within the workspace volume

Renames ` + "`from`" + ` to ` + "`to`" + `. Both paths must be under the same workspace
volume (cross-volume moves aren't supported in v1 — agent dispatches
a copy + delete instead).

Behaviour:
- ` + "`overwrite`" + ` defaults to false. If destination exists, the call
  fails. Use ` + "`overwrite: true`" + ` only when you mean to replace.
- ` + "`create_parents`" + ` recursively mkdir-p the destination's parent.
- Moves a directory tree wholesale via rename(2); cheap, atomic on
  the same filesystem.

Refuses to move the workspace root or to use the root as the
destination.`

func volumeMove(_ context.Context, params volumeMoveParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.From == "" || params.From == "." || params.From == "/" {
		return sdk.Error("from path is required and must not be the workspace root"), nil
	}
	if params.To == "" || params.To == "." || params.To == "/" {
		return sdk.Error("to path is required and must not be the workspace root"), nil
	}
	if params.From == params.To {
		return sdk.Error("from and to are the same path"), nil
	}

	absFrom, err := resolveUnderWorkspace(tc, params.From)
	if err != nil {
		return sdk.Error(fmt.Sprintf("from: %v", err)), nil
	}
	absTo, err := resolveUnderWorkspace(tc, params.To)
	if err != nil {
		return sdk.Error(fmt.Sprintf("to: %v", err)), nil
	}
	root, _ := volumeRoot(tc)
	if absFrom == root || absTo == root {
		return sdk.Error("refuse to use the workspace root as source or destination"), nil
	}

	if _, err := os.Lstat(absFrom); errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("from path not found: %s", params.From)), nil
	} else if err != nil {
		return sdk.Error(fmt.Sprintf("stat from %s: %v", params.From, err)), nil
	}

	if symErr := refuseSymlinkedParent(root, absFrom); symErr != nil {
		return sdk.Error(symErr.Error()), nil
	}
	if symErr := refuseSymlinkedParent(root, absTo); symErr != nil {
		return sdk.Error(symErr.Error()), nil
	}

	if toInfo, err := os.Lstat(absTo); err == nil {
		if toInfo.Mode()&os.ModeSymlink != 0 {
			return sdk.Error(fmt.Sprintf("destination %s is a symlink; refuse to move onto a symlink", params.To)), nil
		}
		if !params.Overwrite {
			return sdk.Error(fmt.Sprintf("destination %s exists; pass overwrite: true to replace", params.To)), nil
		}
		if rmErr := os.RemoveAll(absTo); rmErr != nil {
			return sdk.Error(fmt.Sprintf("clear destination %s for overwrite: %v", params.To, rmErr)), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("stat to %s: %v", params.To, err)), nil
	}

	if params.CreateParents {
		if err := os.MkdirAll(filepath.Dir(absTo), 0o755); err != nil {
			return sdk.Error(fmt.Sprintf("mkdir parents for %s: %v", params.To, err)), nil
		}
	}

	if err := os.Rename(absFrom, absTo); err != nil {
		return sdk.Error(fmt.Sprintf("move %s -> %s: %v", params.From, params.To, err)), nil
	}
	return sdk.OK(fmt.Sprintf("moved %s -> %s\n", params.From, params.To)), nil
}
