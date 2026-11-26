// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func main() {
	sdk.Run(
		sdk.NewTool("volume_read",
			"Read a file from the workspace volume. Returns the file's contents as text. Optional start_line + end_line for a slice; the full file otherwise. Output is capped at 256 KiB with a truncation marker if longer — pass a line range for slices of large files.",
			volumeRead,
			sdk.WithInstructions(volumeReadInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("volume_list",
			"List entries in a directory in the workspace volume. Returns one entry per line in the shape `<kind> <size> <modtime> <name>`. Optional recursive + glob filter + max_entries cap.",
			volumeList,
			sdk.WithInstructions(volumeListInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("volume_glob",
			"Find files matching a pattern under the workspace volume. Supports * (single segment), ** (recursive), ?, and character classes. Returns paths sorted lexicographically.",
			volumeGlob,
			sdk.WithInstructions(volumeGlobInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("volume_grep",
			"Search file contents under the workspace volume with a Go regexp. Output modes: 'content' (matching lines), 'files_with_matches' (paths only), 'count' (per-file totals). Optional file_glob filter, context_lines, head_limit. Skips .git / node_modules / vendor / __pycache__ by default.",
			volumeGrep,
			sdk.WithInstructions(volumeGrepInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("volume_stat",
			"Return metadata (kind, size, mode, modtime) for a single path under the workspace volume. Symlinks report as 'link' and are not followed.",
			volumeStat,
			sdk.WithInstructions(volumeStatInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),

		sdk.NewTool("volume_write",
			"Create or replace a file under the workspace volume. Refuses to overwrite by default — pass overwrite: true explicitly. Optional create_parents recursively makes the destination's parent directories.",
			volumeWrite,
			sdk.WithInstructions(volumeWriteInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("volume_edit",
			"In-place exact-string find/replace in a file. Matches verbatim — no regex, no whitespace normalisation. Defaults to single-occurrence; pass replace_all: true for every occurrence. Fails (without modification) if old_string is ambiguous or absent.",
			volumeEdit,
			sdk.WithInstructions(volumeEditInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("volume_append",
			"Append content to an existing file. Refuses to write through symlinks. Use volume_write to create new files.",
			volumeAppend,
			sdk.WithInstructions(volumeAppendInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("volume_delete",
			"Delete a file, symlink, or directory under the workspace volume. Non-empty directories require recursive: true (the rm -rf shape). Refuses to delete the workspace root.",
			volumeDelete,
			sdk.WithInstructions(volumeDeleteInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("volume_move",
			"Rename or move a path within the workspace volume. Refuses to overwrite the destination by default. Optional create_parents recursively makes the destination's parent directories.",
			volumeMove,
			sdk.WithInstructions(volumeMoveInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("volume_mkdir",
			"Create a directory under the workspace volume. Optional recursive flag (mkdir -p). No-op if the directory already exists; fails if the path exists as a file or symlink.",
			volumeMkdir,
			sdk.WithInstructions(volumeMkdirInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	)
}
