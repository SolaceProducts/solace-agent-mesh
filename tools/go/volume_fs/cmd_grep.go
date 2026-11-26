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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type volumeGrepParams struct {
	Pattern       string `json:"pattern" jsonschema:"description=Regular expression to search for. Go regexp syntax (RE2)."`
	Path          string `json:"path,omitempty" jsonschema:"description=Subdirectory under the workspace volume to search in. Defaults to the volume root."`
	FileGlob      string `json:"file_glob,omitempty" jsonschema:"description=Optional filename glob (basename) to filter which files are searched. Same syntax as volume_glob's basename pattern."`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"description=When true, the regex is case-sensitive. Defaults to false (case-insensitive match via (?i) prefix)."`
	OutputMode    string `json:"output_mode,omitempty" jsonschema:"description=One of 'content' (default; show matching lines), 'files_with_matches' (just paths), or 'count' (per-file match count)."`
	HeadLimit     int    `json:"head_limit,omitempty" jsonschema:"description=Cap on results returned. Default 100 matches in content mode, 100 files otherwise."`
	ContextLines  int    `json:"context_lines,omitempty" jsonschema:"description=Number of surrounding context lines to show with each match in content mode. Defaults to 0."`
}

const volumeGrepInstructions = `## volume_grep — Search file contents by regex

Runs a regex match against the lines of every file under ` + "`path`" + ` (the
workspace root by default) and returns matches.

Pattern is Go's regexp/RE2 syntax — same as ripgrep, GitHub search,
Claude Code's Grep. Common shapes:
- ` + "`TODO`" + ` — literal text
- ` + "`func\\s+\\w+`" + ` — function declarations
- ` + "`^import`" + ` — anchored at start of line
- ` + "`(?:foo|bar)`" + ` — alternation

Output modes:
- ` + "`content`" + ` (default) — each matching line in the shape
  ` + "`<path>:<line>: <text>`" + `, with optional ` + "`context_lines`" + ` of surrounding
  lines (marked ` + "`-`" + ` for context vs ` + "`:`" + ` for the match line).
- ` + "`files_with_matches`" + ` — just one path per file that has any match;
  cheaper for "which files contain X?".
- ` + "`count`" + ` — ` + "`<count> <path>`" + ` per file.

` + "`case_sensitive: false`" + ` (the default) wraps the pattern with ` + "`(?i)`" + ` —
useful for the common "find all uses of foo" search where casing
might vary. Set ` + "`case_sensitive: true`" + ` for code-identifier searches
where casing is meaningful.

` + "`file_glob`" + ` filters which files are searched. ` + "`*.go`" + ` limits to Go
files; useful to avoid grepping through lockfiles and binaries.

` + "`head_limit`" + ` caps the result. Default 100 — large enough that real
code-review searches succeed, small enough to fit in context. Use a
tighter ` + "`path`" + ` or ` + "`file_glob`" + ` if you hit the cap.

Skipped: directories named .git, node_modules, vendor, __pycache__ —
the common "search noise" directories. To search inside them
explicitly, point ` + "`path`" + ` at the directory directly.`

const (
	defaultGrepHeadLimit = 100
	defaultGrepMaxLine   = 4096
)

var skipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	"__pycache__":  {},
	".venv":        {},
}

type grepHit struct {
	path    string
	line    int
	text    string
	context []grepContextLine
}

type grepContextLine struct {
	lineNum int
	text    string
	isMatch bool
}

func volumeGrep(_ context.Context, params volumeGrepParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if params.Pattern == "" {
		return sdk.Error("pattern is required"), nil
	}
	if params.OutputMode == "" {
		params.OutputMode = "content"
	}
	switch params.OutputMode {
	case "content", "files_with_matches", "count":
	default:
		return sdk.Error(fmt.Sprintf("output_mode %q invalid; must be content, files_with_matches, or count", params.OutputMode)), nil
	}
	if params.HeadLimit <= 0 {
		params.HeadLimit = defaultGrepHeadLimit
	}
	if params.ContextLines < 0 {
		return sdk.Error("context_lines must be >= 0"), nil
	}
	if params.FileGlob != "" {
		if _, err := filepath.Match(params.FileGlob, ""); err != nil {
			return sdk.Error(fmt.Sprintf("invalid file_glob %q: %v", params.FileGlob, err)), nil
		}
	}

	pattern := params.Pattern
	if !params.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return sdk.Error(fmt.Sprintf("invalid pattern: %v", err)), nil
	}

	root, err := volumeRoot(tc)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	absRoot, err := safeJoin(root, params.Path)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	info, err := os.Stat(absRoot)
	if errors.Is(err, os.ErrNotExist) {
		return sdk.Error(fmt.Sprintf("search path not found: %s", params.Path)), nil
	}
	if err != nil {
		return sdk.Error(fmt.Sprintf("stat search path %s: %v", params.Path, err)), nil
	}
	if !info.IsDir() {
		return grepSingleFile(re, absRoot, root, params)
	}

	return grepWalk(re, absRoot, root, params)
}

func grepWalk(re *regexp.Regexp, absRoot, volRoot string, params volumeGrepParams) (*sdk.Result, error) {
	var hits []grepHit
	fileMatchCounts := map[string]int{}
	var orderedFiles []string
	var skipped []string
	var capped bool

	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip && path != absRoot {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if params.FileGlob != "" && !matchesGlob(params.FileGlob, d.Name()) {
			return nil
		}

		fileHits, fileCount, err := grepFile(re, path, volRoot, params)
		if err != nil {
			if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
				return nil
			}
			rel, _ := filepath.Rel(volRoot, path)
			skipped = append(skipped, rel)
			return nil
		}
		if fileCount == 0 {
			return nil
		}
		rel, _ := filepath.Rel(volRoot, path)
		fileMatchCounts[rel] = fileCount
		orderedFiles = append(orderedFiles, rel)
		hits = append(hits, fileHits...)

		switch params.OutputMode {
		case "content":
			if len(hits) >= params.HeadLimit {
				capped = true
				return filepath.SkipAll
			}
		case "files_with_matches", "count":
			if len(orderedFiles) >= params.HeadLimit {
				capped = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return sdk.Error(fmt.Sprintf("walk %s: %v", params.Path, walkErr)), nil
	}

	out := formatGrepOutput(hits, orderedFiles, fileMatchCounts, params)
	out += grepFooter(capped, skipped, params.HeadLimit)
	return sdk.OK(string(truncateForOutput([]byte(out)))), nil
}

func grepFooter(capped bool, skipped []string, headLimit int) string {
	var b strings.Builder
	if capped {
		fmt.Fprintf(&b, "\n[volume_grep: results capped at head_limit=%d; more matches may exist — narrow the path, file_glob, or pattern]\n", headLimit)
	}
	if len(skipped) > 0 {
		slices.Sort(skipped)
		const show = 10
		shown := skipped
		if len(shown) > show {
			shown = shown[:show]
		}
		fmt.Fprintf(&b, "\n[volume_grep: %d file(s) skipped due to read errors (results may under-report): %s",
			len(skipped), strings.Join(shown, ", "))
		if len(skipped) > show {
			fmt.Fprintf(&b, ", … and %d more", len(skipped)-show)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

func grepSingleFile(re *regexp.Regexp, absPath, volRoot string, params volumeGrepParams) (*sdk.Result, error) {
	fileHits, fileCount, err := grepFile(re, absPath, volRoot, params)
	if err != nil {
		return sdk.Error(fmt.Sprintf("grep %s: %v", params.Path, err)), nil
	}
	if fileCount == 0 {
		return sdk.OK("(no matches)\n"), nil
	}
	rel, _ := filepath.Rel(volRoot, absPath)
	orderedFiles := []string{rel}
	counts := map[string]int{rel: fileCount}
	capped := params.OutputMode == "content" && len(fileHits) >= params.HeadLimit
	out := formatGrepOutput(fileHits, orderedFiles, counts, params)
	out += grepFooter(capped, nil, params.HeadLimit)
	return sdk.OK(string(truncateForOutput([]byte(out)))), nil
}

func grepFile(re *regexp.Regexp, path, volRoot string, params volumeGrepParams) ([]grepHit, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	binary, err := isBinary(f)
	if err != nil {
		return nil, 0, err
	}
	if binary {
		return nil, 0, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, 0, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	relPath, _ := filepath.Rel(volRoot, path)
	withContext := params.OutputMode == "content" && params.ContextLines > 0

	var lines []string
	if withContext {
		lines = make([]string, 0, 128)
	}

	var hits []grepHit
	var matchLines []int
	matchSet := map[int]bool{}
	count := 0
	lineNum := 0
	stopAt := -1
	for scanner.Scan() {
		lineNum++
		if stopAt >= 0 && lineNum > stopAt {
			break
		}
		text := scanner.Text()
		if withContext {
			lines = append(lines, text)
		}
		if !re.MatchString(text) {
			continue
		}
		count++
		if params.OutputMode == "files_with_matches" {
			return hits, count, nil
		}
		if params.OutputMode == "count" {
			continue
		}
		if !withContext {
			if len(hits) < params.HeadLimit {
				hits = append(hits, grepHit{path: relPath, line: lineNum, text: truncateLine(text)})
			}
			if len(hits) >= params.HeadLimit {
				break
			}
			continue
		}
		if len(matchLines) < params.HeadLimit {
			matchLines = append(matchLines, lineNum)
			matchSet[lineNum] = true
		}
		if len(matchLines) >= params.HeadLimit && stopAt < 0 {
			stopAt = lineNum + params.ContextLines
		}
	}
	if err := scanner.Err(); err != nil {
		return hits, count, err
	}

	for _, m := range matchLines {
		start := m - 1 - params.ContextLines
		if start < 0 {
			start = 0
		}
		end := m - 1 + params.ContextLines
		if end > len(lines)-1 {
			end = len(lines) - 1
		}
		var ctx []grepContextLine
		for i := start; i <= end; i++ {
			ctx = append(ctx, grepContextLine{
				lineNum: i + 1,
				text:    truncateLine(lines[i]),
				isMatch: matchSet[i+1],
			})
		}
		hits = append(hits, grepHit{path: relPath, line: m, context: ctx})
	}
	return hits, count, nil
}

func isBinary(f *os.File) (bool, error) {
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true, nil
		}
	}
	return false, nil
}

func truncateLine(s string) string {
	if len(s) <= defaultGrepMaxLine {
		return s
	}
	return s[:defaultGrepMaxLine] + "...[line truncated]"
}

func formatGrepOutput(hits []grepHit, orderedFiles []string, counts map[string]int, params volumeGrepParams) string {
	switch params.OutputMode {
	case "files_with_matches":
		slices.Sort(orderedFiles)
		if len(orderedFiles) == 0 {
			return "(no matches)\n"
		}
		return strings.Join(orderedFiles, "\n") + "\n"

	case "count":
		slices.Sort(orderedFiles)
		if len(orderedFiles) == 0 {
			return "(no matches)\n"
		}
		var b strings.Builder
		for _, f := range orderedFiles {
			fmt.Fprintf(&b, "%d %s\n", counts[f], f)
		}
		return b.String()

	default:
		if len(hits) == 0 {
			return "(no matches)\n"
		}
		var b strings.Builder
		if params.ContextLines == 0 {
			for _, h := range hits {
				fmt.Fprintf(&b, "%s:%d: %s\n", h.path, h.line, h.text)
			}
			return b.String()
		}
		lastFile := ""
		lastLine := 0
		firstGroup := true
		for _, h := range hits {
			for _, ctx := range h.context {
				if h.path == lastFile && ctx.lineNum <= lastLine {
					continue
				}
				contiguous := h.path == lastFile && ctx.lineNum == lastLine+1
				if !contiguous && !firstGroup {
					b.WriteString("--\n")
				}
				sep := "-"
				if ctx.isMatch {
					sep = ":"
				}
				fmt.Fprintf(&b, "%s%s%d%s %s\n", h.path, sep, ctx.lineNum, sep, ctx.text)
				lastFile = h.path
				lastLine = ctx.lineNum
				firstGroup = false
			}
		}
		return b.String()
	}
}
