// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	readMaxLines     = 2000
	readMaxBytes     = 50 << 10
	readMaxLineChars = 2000
	grepMaxMatches   = 200
	globMaxResults   = 500
)

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

var innerToolSpecs = []sdk.ChatTool{
	{
		Name:        "read",
		Description: "Read a file as numbered lines (`N: text`), up to 2000 lines or 50 KiB. Use offset and limit for large files. Reading a directory lists its entries.",
		Parameters: obj(map[string]any{
			"path":   str("Path relative to the workspace."),
			"offset": integer("1-based line to start from."),
			"limit":  integer("Number of lines to read."),
		}, "path"),
	},
	{
		Name:        "write",
		Description: "Create or overwrite a file with the given content. Parent directories are created.",
		Parameters: obj(map[string]any{
			"path":    str("Path relative to the workspace."),
			"content": str("Full file content."),
		}, "path", "content"),
	},
	{
		Name:        "edit",
		Description: "Replace old_string with new_string in a file. old_string must match the file (copy it from a fresh read without the line-number prefix) and be unique unless replace_all is set. To make several changes to one file in one call, pass edits instead; they apply in order and the file is written only if every one matches.",
		Parameters: obj(map[string]any{
			"path":        str("Path relative to the workspace."),
			"old_string":  str("Text to replace."),
			"new_string":  str("Replacement text."),
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every match."},
			"edits": map[string]any{
				"type":        "array",
				"description": "Several replacements in this file, applied in order. Use instead of old_string and new_string.",
				"items": obj(map[string]any{
					"old_string":  str("Text to replace."),
					"new_string":  str("Replacement text."),
					"replace_all": map[string]any{"type": "boolean", "description": "Replace every match."},
				}, "old_string", "new_string"),
			},
		}, "path"),
	},
	{
		Name:        "grep",
		Description: "Search file contents with a Go regular expression. Returns `path:line: text`, at most 200 matches. Skips .git, node_modules, vendor and similar.",
		Parameters: obj(map[string]any{
			"pattern": str("Regular expression."),
			"path":    str("Directory or file to search; defaults to the workspace root."),
			"include": str("Only search files whose name matches this glob, e.g. *.go."),
		}, "pattern"),
	},
	{
		Name:        "glob",
		Description: "List files matching a glob such as **/*_test.go or cmd/*/main.go, at most 500, sorted.",
		Parameters: obj(map[string]any{
			"pattern": str("Glob pattern; ** matches any number of directories."),
		}, "pattern"),
	},
	{
		Name:        "changes",
		Description: "Show every change made to the workspace since this task started (including ones made through bash), as a file list and unified diff. Use it to review your work before you report.",
		Parameters:  obj(map[string]any{}),
	},
	{
		Name:        "bash",
		Description: "Run a shell command in the workspace root and return its exit code and output (long output keeps the head and tail). Default timeout 120s, max 600s. Anything the command leaves running in the background is killed when it returns.",
		Parameters: obj(map[string]any{
			"command":         str("Command to run with bash -c."),
			"timeout_seconds": integer("Timeout in seconds."),
		}, "command"),
	},
}

var editingTools = map[string]bool{"write": true, "edit": true}

var planWithheld = map[string]bool{"write": true, "edit": true, "changes": true}

func toolSpecsFor(mode string) []sdk.ChatTool {
	if mode != modePlan {
		return innerToolSpecs
	}
	specs := make([]sdk.ChatTool, 0, len(innerToolSpecs))
	for _, t := range innerToolSpecs {
		if !planWithheld[t.Name] {
			specs = append(specs, t)
		}
	}
	return specs
}

func (w *workspace) dispatch(ctx context.Context, call sdk.ChatToolCall) string {
	var out string
	var err error
	if w.readOnly && editingTools[call.Name] {
		return "error: " + call.Name + " is not available while planning; reply with the plan instead"
	}
	switch call.Name {
	case "read":
		var a struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			out, err = w.read(a.Path, a.Offset, a.Limit)
		}
	case "write":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			if out, err = w.write(a.Path, a.Content); err == nil {
				out += w.syntaxWarning(ctx, a.Path)
			}
		}
	case "edit":
		var a struct {
			Path string `json:"path"`
			editOp
			Edits []editOp `json:"edits"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			ops := a.Edits
			if len(ops) == 0 {
				ops = []editOp{a.editOp}
			}
			if out, err = w.edit(a.Path, ops); err == nil {
				out += w.syntaxWarning(ctx, a.Path)
			}
		}
	case "grep":
		var a struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
			Include string `json:"include"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			out, err = w.grep(ctx, a.Pattern, a.Path, a.Include)
		}
	case "glob":
		var a struct {
			Pattern string `json:"pattern"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			out, err = w.glob(ctx, a.Pattern)
		}
	case "changes":
		out, err = w.changes(ctx)
	case "bash":
		var a struct {
			Command        string `json:"command"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			out, err = w.shell.run(ctx, w.root, a.Command, a.TimeoutSeconds, w.env)
		}
	case "fetch":
		var a struct {
			URL    string `json:"url"`
			Offset int    `json:"offset"`
		}
		if err = decodeArgs(call.Arguments, &a); err == nil {
			out, err = w.fetch(ctx, a.URL, a.Offset)
		}
	default:
		if t, ok := w.hostTool(call.Name); ok {
			out, err = w.runHost(ctx, t, call.Arguments)
			break
		}
		available := "read, write, edit, grep, glob, changes, bash"
		if len(w.fetchDomains) > 0 {
			available += ", fetch"
		}
		err = fmt.Errorf("unknown tool %q; available: %s", call.Name, available)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

const changesMaxDiffBytes = 40 << 10

func (w *workspace) changes(ctx context.Context) (string, error) {
	if w.base == nil {
		return "", errors.New("no baseline for this workspace")
	}
	cs, err := w.base.changes(ctx)
	if err != nil {
		return "", err
	}
	if len(cs.Files) == 0 {
		return "no changes yet", nil
	}
	files, folded := fileList(cs.Files)
	diff := headTail(withoutDirs(cs.Diff, folded), changesMaxDiffBytes/2, changesMaxDiffBytes/2)
	return fmt.Sprintf("%s\n%s\n\n%s", cs.Stat, files, diff), nil
}

const changesMaxFiles = 60

func fileList(files []string) (string, map[string]bool) {
	if len(files) <= changesMaxFiles {
		return strings.Join(files, "\n"), nil
	}
	topOf := func(entry string) string {
		path := entry[strings.LastIndex(entry, " ")+1:]
		top, _, found := strings.Cut(path, "/")
		if !found {
			return "."
		}
		return top
	}
	perTop := map[string]int{}
	for _, f := range files {
		perTop[topOf(f)]++
	}
	tops := make([]string, 0, len(perTop))
	for t := range perTop {
		tops = append(tops, t)
	}
	sort.Slice(tops, func(i, j int) bool { return perTop[tops[i]] > perTop[tops[j]] })
	folded := map[string]bool{}
	remaining := len(files)
	for _, t := range tops {
		if remaining <= changesMaxFiles || t == "." {
			break
		}
		folded[t] = true
		remaining -= perTop[t]
	}
	var b strings.Builder
	for _, f := range files {
		if !folded[topOf(f)] {
			b.WriteString(f + "\n")
		}
	}
	for _, t := range tops {
		if folded[t] {
			fmt.Fprintf(&b, "%s/: %d changed files not listed\n", t, perTop[t])
		}
	}
	return strings.TrimSuffix(b.String(), "\n"), folded
}

func withoutDirs(diff string, tops map[string]bool) string {
	if len(tops) == 0 {
		return diff
	}
	const header = "diff --git a/"
	sections := strings.Split(diff, "\n"+header)
	var b strings.Builder
	for i, section := range sections {
		if i == 0 {
			section = strings.TrimPrefix(section, header)
		}
		top, _, _ := strings.Cut(section, "/")
		if tops[top] {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(header + section)
	}
	return b.String()
}

func decodeArgs(raw string, v any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("arguments are not valid JSON: %v", err)
	}
	return nil
}

func (w *workspace) read(path string, offset, limit int) (string, error) {
	abs, err := w.resolveRead(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if info.IsDir() {
		return listDir(abs)
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	offset = max(offset, 1)
	if limit <= 0 || limit > readMaxLines {
		limit = readMaxLines
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var b strings.Builder
	line, shown := 0, 0
	for sc.Scan() {
		line++
		if line < offset {
			continue
		}
		if shown == limit || b.Len() >= readMaxBytes {
			fmt.Fprintf(&b, "\n(file continues past line %d; read again with offset=%d)\n", line-1, line)
			return b.String(), nil
		}
		text := sc.Text()
		if strings.IndexByte(text, 0) >= 0 {
			return "", fmt.Errorf("%s looks like a binary file", path)
		}
		if len(text) > readMaxLineChars {
			text = text[:readMaxLineChars] + "… (line truncated)"
		}
		fmt.Fprintf(&b, "%d: %s\n", line, text)
		shown++
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if shown == 0 {
		return fmt.Sprintf("(%s has %d lines; nothing at offset %d)\n", path, line, offset), nil
	}
	return b.String(), nil
}

func listDir(abs string) (string, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name + "\n")
	}
	return b.String(), nil
}

func (w *workspace) write(path, content string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	if err := w.refuseHostOutput(abs, path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	_, statErr := os.Stat(abs)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	verb := "updated"
	if errors.Is(statErr, fs.ErrNotExist) {
		verb = "created"
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, w.rel(abs), len(content)), nil
}

type editOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (w *workspace) edit(path string, ops []editOp) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	if refuseErr := w.refuseHostOutput(abs, path); refuseErr != nil {
		return "", refuseErr
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	updated, total := string(data), 0
	for i, op := range ops {
		if op.OldString == "" {
			return "", editErr(path, i, len(ops), errors.New("old_string is empty; use write to create a file"))
		}
		var n int
		if updated, n, err = replaceInContent(updated, op.OldString, op.NewString, op.ReplaceAll); err != nil {
			return "", editErr(path, i, len(ops), err)
		}
		total += n
	}
	if err := os.WriteFile(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", w.rel(abs), total), nil
}

func editErr(path string, i, n int, err error) error {
	if n == 1 {
		return fmt.Errorf("%s: %w", path, err)
	}
	return fmt.Errorf("%s: edits[%d]: %w; no edits were applied", path, i, err)
}

func (w *workspace) grep(ctx context.Context, pattern, path, include string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %v", err)
	}
	start, err := w.resolveRead(path)
	if err != nil {
		return "", err
	}
	files := []string{start}
	if info, err := os.Stat(start); err == nil && info.IsDir() {
		rels, err := w.searchFiles(ctx, start)
		if err != nil {
			return "", err
		}
		for i, rel := range rels {
			rels[i] = filepath.Join(start, rel)
		}
		files = rels
	}
	var b strings.Builder
	matches := 0
	for _, p := range files {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if include != "" {
			if ok, _ := filepath.Match(include, filepath.Base(p)); !ok {
				continue
			}
		}
		if err := grepFile(p, w.rel(p), re, &b, &matches); errors.Is(err, errTooManyMatches) {
			fmt.Fprintf(&b, "(stopped at %d matches; narrow the pattern or path)\n", grepMaxMatches)
			break
		}
	}
	if matches == 0 {
		return "no matches", nil
	}
	return b.String(), nil
}

var errTooManyMatches = errors.New("too many matches")

func grepFile(abs, rel string, re *regexp.Regexp, b *strings.Builder, matches *int) error {
	f, err := os.Open(abs)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Bytes()
		if n == 1 && strings.IndexByte(string(line), 0) >= 0 {
			return nil
		}
		if !re.Match(line) {
			continue
		}
		text := string(line)
		if len(text) > 300 {
			text = text[:300] + "…"
		}
		fmt.Fprintf(b, "%s:%d: %s\n", rel, n, text)
		*matches++
		if *matches >= grepMaxMatches {
			return errTooManyMatches
		}
	}
	return nil
}

func (w *workspace) glob(ctx context.Context, pattern string) (string, error) {
	re, err := globToRegexp(pattern)
	if err != nil {
		return "", err
	}
	files, err := w.searchFiles(ctx, w.root)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, rel := range files {
		if re.MatchString(rel) {
			hits = append(hits, rel)
		}
	}
	if len(hits) == 0 {
		return "no files match", nil
	}
	sort.Strings(hits)
	more := ""
	if len(hits) > globMaxResults {
		more = fmt.Sprintf("(%d more not shown)\n", len(hits)-globMaxResults)
		hits = hits[:globMaxResults]
	}
	return strings.Join(hits, "\n") + "\n" + more, nil
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			end := strings.IndexByte(pattern[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed { in %q", pattern)
			}
			alts := strings.Split(pattern[i+1:i+end], ",")
			for k := range alts {
				alts[k] = regexp.QuoteMeta(alts[k])
			}
			b.WriteString("(?:" + strings.Join(alts, "|") + ")")
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
