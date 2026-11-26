// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxReadBytes = 256 * 1024

const MaxReadLines = 500

type ReadResult struct {
	SkillName string `json:"skill_name"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (r ReadResult) AsMap() map[string]any {
	out := map[string]any{
		"skill_name": r.SkillName,
		"path":       r.Path,
		"content":    r.Content,
		"truncated":  r.Truncated,
	}
	if r.StartLine > 0 || r.EndLine > 0 {
		out["start_line"] = r.StartLine
		out["end_line"] = r.EndLine
	}
	return out
}

func ReadResource(ctx SkillContext, relPath string, startLine, endLine int) (ReadResult, error) {
	if relPath == "" {
		return ReadResult{}, fmt.Errorf("%w: path is required. %s %s",
			ErrInvalidParam, ResourceToolUsage(ReadResourceToolName), describeSkillFiles(ctx))
	}
	cleanPath, err := validateResourcePath(relPath)
	if err != nil {
		return ReadResult{}, fmt.Errorf("%w. %s", err, describeSkillFiles(ctx))
	}
	full, err := safeJoin(ctx.LocalPath, cleanPath)
	if err != nil {
		return ReadResult{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return ReadResult{}, fmt.Errorf("%w: %s does not exist. %s", ErrInvalidPath, cleanPath, describeSkillFiles(ctx))
		}
		return ReadResult{}, fmt.Errorf("stat %s: %w", cleanPath, err)
	}
	if info.IsDir() {
		return ReadResult{}, fmt.Errorf("%w: %s. %s", ErrNotAFile, cleanPath, describeSkillFiles(ctx))
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return ReadResult{}, fmt.Errorf("reading %s: %w", cleanPath, err)
	}

	content := string(data)
	truncated := false
	if len(data) > MaxReadBytes {
		content = string(data[:MaxReadBytes])
		truncated = true
	}

	res := ReadResult{
		SkillName: ctx.Name,
		Path:      cleanPath,
	}

	if startLine > 0 || endLine > 0 {
		sliced, slicedTruncated, effStart, effEnd := sliceLines(content, startLine, endLine)
		content = sliced
		if slicedTruncated {
			truncated = true
		}
		res.StartLine = effStart
		res.EndLine = effEnd
	}

	res.Content = content
	res.Truncated = truncated
	return res, nil
}

func validateResourcePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("%w: must be relative and within the skill directory", ErrInvalidPath)
	}
	slash := filepath.ToSlash(cleaned)
	if !strings.HasPrefix(slash, "references/") && !strings.HasPrefix(slash, "assets/") {
		return "", ErrPathNotInResources
	}
	return slash, nil
}

func safeJoin(base, rel string) (string, error) {
	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolving base path: %w", err)
	}
	full := filepath.Join(cleanBase, rel)
	full = filepath.Clean(full)
	if full != cleanBase &&
		!strings.HasPrefix(full, cleanBase+string(filepath.Separator)) {
		return "", ErrPathOutsideSkill
	}
	return full, nil
}

func sliceLines(content string, startLine, endLine int) (string, bool, int, int) {
	lines := strings.Split(content, "\n")
	total := len(lines)

	start := startLine
	if start <= 0 {
		start = 1
	}
	if start > total {
		start = total
	}

	end := endLine
	if end <= 0 || end > total {
		end = total
	}
	if end < start {
		end = start
	}

	truncated := false
	if end-start+1 > MaxReadLines {
		end = start + MaxReadLines - 1
		truncated = true
	}

	if start > total {
		return "", truncated, start, end
	}
	return strings.Join(lines[start-1:end], "\n"), truncated, start, end
}
