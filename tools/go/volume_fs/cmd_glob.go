// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
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

type volumeGlobParams struct {
	Pattern    string `json:"pattern" jsonschema:"description=Glob pattern matched against the relative path from the search root. Supports * (any segment) and ** (any path including separators)."`
	Root       string `json:"root,omitempty" jsonschema:"description=Subdirectory under the workspace volume to search in. Defaults to the volume root."`
	MaxEntries int    `json:"max_entries,omitempty" jsonschema:"description=Cap on entries returned. Defaults to 1000."`
}

const volumeGlobInstructions = `## volume_glob — Find files by pattern

Returns paths matching the glob, one per line, sorted lexicographically.

Supported wildcards:
- ` + "`*`" + ` matches any sequence of non-separator characters within a single
  path segment.
- ` + "`**`" + ` matches any number of segments (zero or more), including
  separators. ` + "`**/*.go`" + ` finds every Go file anywhere under the root.
- ` + "`?`" + ` matches a single character.
- ` + "`[abc]`" + ` character classes.

Examples:
- ` + "`*.go`" + ` — Go files directly in the root.
- ` + "`**/*.go`" + ` — Go files anywhere.
- ` + "`src/**/test_*.py`" + ` — Test files under src.

The match is run against the path **relative** to root, so a pattern
like ` + "`*.go`" + ` matches ` + "`main.go`" + ` but NOT ` + "`pkg/main.go`" + ` (use ` + "`**/*.go`" + `
for that). ` + "`root`" + ` defaults to the volume root; pass a subdirectory
to narrow the search up front.

For content search inside files, use ` + "`volume_grep`" + `.`

const defaultGlobMaxEntries = 1000

func volumeGlob(_ context.Context, params volumeGlobParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Pattern == "" {
		return sdk.Error("pattern is required"), nil
	}
	maxEntries := params.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultGlobMaxEntries
	}

	root, err := volumeRoot(tc)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	absRoot, err := safeJoin(root, params.Root)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	info, err := os.Stat(absRoot)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("search root not found: %s", params.Root)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat search root %s: %v", params.Root, err)), nil
	}
	if !info.IsDir() {
		return sdk.Error(fmt.Sprintf("search root %s is not a directory", params.Root)), nil
	}

	if _, err := matchGlob(params.Pattern, ""); err != nil {
		return sdk.Error(fmt.Sprintf("invalid pattern %q: %v", params.Pattern, err)), nil
	}

	var matches []string
	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return walkErr
		}
		if path == absRoot {
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return relErr
		}
		ok, matchErr := matchGlob(params.Pattern, rel)
		if matchErr != nil {
			return matchErr
		}
		if ok {
			matches = append(matches, rel)
			if len(matches) >= maxEntries {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return sdk.Error(fmt.Sprintf("walk %s: %v", params.Root, walkErr)), nil
	}

	slices.Sort(matches)
	if len(matches) == 0 {
		return sdk.OK(fmt.Sprintf("(no matches for %q under %s)\n", params.Pattern, params.Root)), nil
	}
	return sdk.OK(string(truncateForOutput([]byte(strings.Join(matches, "\n") + "\n")))), nil
}

func matchGlob(pattern, name string) (bool, error) {
	patSegs := strings.Split(pattern, "/")
	nameSegs := strings.Split(filepath.ToSlash(name), "/")
	return globSegs(patSegs, nameSegs)
}

func globSegs(pat, name []string) (bool, error) {
	switch {
	case len(pat) == 0:
		return len(name) == 0, nil
	case pat[0] == "**":
		for i := 0; i <= len(name); i++ {
			ok, err := globSegs(pat[1:], name[i:])
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case len(name) == 0:
		return false, nil
	default:
		ok, err := filepath.Match(pat[0], name[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		return globSegs(pat[1:], name[1:])
	}
}
