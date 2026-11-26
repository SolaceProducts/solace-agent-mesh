// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeListParams struct {
	Path       string `json:"path,omitempty" jsonschema:"description=Directory to list (relative to the workspace volume). Defaults to the volume root."`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"description=Walk subdirectories. Defaults to false."`
	Glob       string `json:"glob,omitempty" jsonschema:"description=Optional filename glob (e.g. '*.go'). Matched on the basename only."`
	MaxEntries int    `json:"max_entries,omitempty" jsonschema:"description=Cap on entries returned. Defaults to 1000."`
}

const volumeListInstructions = `## volume_list — List entries in a directory

Returns one line per entry in the shape:

    <kind> <size> <modtime>     <relative-path>

where ` + "`kind`" + ` is one of ` + "`file`" + ` / ` + "`dir`" + ` / ` + "`link`" + ` / ` + "`other`" + `, size is in bytes
(- for non-regular entries), and modtime is RFC 3339.

- ` + "`path`" + ` is the directory to list (relative to the volume root). Omit to
  list the root itself.
- ` + "`recursive: true`" + ` walks all subdirectories.
- ` + "`glob`" + ` filters by filename (basename match only — e.g. ` + "`*.go`" + ` matches
  ` + "`main.go`" + ` but not ` + "`src/main.go`" + `).
- ` + "`max_entries`" + ` caps the result. Default 1000; when the cap is hit the
  output ends with a marker line so you know more entries exist (use a
  tighter ` + "`path`" + ` or ` + "`glob`" + ` to find them).

For a single-path query (existence + metadata, no enumeration),
prefer ` + "`volume_stat`" + `.`

const defaultMaxEntries = 1000

type listEntry struct {
	relPath string
	info    fs.FileInfo
}

func volumeList(_ context.Context, params volumeListParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	maxEntries := params.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}

	if params.Glob != "" {
		if _, err := filepath.Match(params.Glob, ""); err != nil {
			return sdk.Error(fmt.Sprintf("invalid glob %q: %v", params.Glob, err)), nil
		}
	}

	root, err := volumeRoot(tc)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	absDir, err := safeJoin(root, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	info, err := os.Stat(absDir)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("directory not found: %s", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat %s: %v", params.Path, err)), nil
	}
	if !info.IsDir() {
		return sdk.Error(fmt.Sprintf("%s is not a directory; use volume_stat or volume_read for files", params.Path)), nil
	}

	var entries []listEntry
	var capped bool
	if params.Recursive {
		err = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrPermission) && d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return walkErr
			}
			if path == absDir {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			i, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if !matchesGlob(params.Glob, i.Name()) {
				return nil
			}
			entries = append(entries, listEntry{relPath: rel, info: i})
			if len(entries) >= maxEntries {
				capped = true
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return sdk.Error(fmt.Sprintf("walk %s: %v", params.Path, err)), nil
		}
	} else {
		dirEntries, err := os.ReadDir(absDir)
		if err != nil {
			return sdk.Error(fmt.Sprintf("read directory %s: %v", params.Path, err)), nil
		}
		for _, d := range dirEntries {
			if !matchesGlob(params.Glob, d.Name()) {
				continue
			}
			i, err := d.Info()
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(root, filepath.Join(absDir, d.Name()))
			if err != nil {
				continue
			}
			entries = append(entries, listEntry{relPath: rel, info: i})
			if len(entries) >= maxEntries {
				capped = true
				break
			}
		}
	}

	slices.SortFunc(entries, func(a, b listEntry) int {
		return cmp.Compare(a.relPath, b.relPath)
	})

	out := formatListEntries(entries)
	if capped {
		out += fmt.Sprintf("\n[volume_list: result capped at %d entries; more exist — narrow the path or glob to see them]\n", maxEntries)
	}
	return sdk.OK(string(truncateForOutput([]byte(out)))), nil
}

func matchesGlob(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	ok, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return ok
}

func formatListEntries(entries []listEntry) string {
	if len(entries) == 0 {
		return "(empty directory)\n"
	}
	var b strings.Builder
	for _, e := range entries {
		kind := classifyEntry(e.info)
		sizeStr := "-"
		if e.info.Mode().IsRegular() {
			sizeStr = fmt.Sprintf("%d", e.info.Size())
		}
		fmt.Fprintf(&b, "%-5s %12s  %s  %s\n",
			kind,
			sizeStr,
			e.info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
			e.relPath,
		)
	}
	return b.String()
}

func classifyEntry(info fs.FileInfo) string {
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return "link"
	case mode.IsDir():
		return "dir"
	case mode.IsRegular():
		return "file"
	default:
		return "other"
	}
}
