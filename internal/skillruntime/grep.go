// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const MaxGrepResults = 100

type GrepOptions struct {
	Pattern         string
	CaseInsensitive bool
	ContextLines    int
}

type GrepMatch struct {
	File string `json:"file"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type GrepResult struct {
	SkillName  string      `json:"skill_name"`
	Pattern    string      `json:"pattern"`
	Matches    []GrepMatch `json:"matches"`
	MatchCount int         `json:"match_count"`
	Truncated  bool        `json:"truncated"`
	Note       string      `json:"note,omitempty"`
}

func (r GrepResult) AsMap() map[string]any {
	matches := make([]map[string]any, len(r.Matches))
	for i, m := range r.Matches {
		matches[i] = map[string]any{
			"file": m.File,
			"path": m.Path,
			"line": m.Line,
			"text": m.Text,
		}
	}
	out := map[string]any{
		"skill_name":  r.SkillName,
		"pattern":     r.Pattern,
		"matches":     matches,
		"match_count": r.MatchCount,
		"truncated":   r.Truncated,
	}
	if r.Note != "" {
		out["note"] = r.Note
	}
	return out
}

func GrepResources(ctx SkillContext, opts GrepOptions) (GrepResult, error) {
	if opts.Pattern == "" {
		return GrepResult{}, fmt.Errorf("%w: pattern is required", ErrInvalidParam)
	}
	re, err := compileGrepPattern(opts.Pattern, opts.CaseInsensitive)
	if err != nil {
		return GrepResult{}, err
	}
	matches, truncated := grepResources(ctx, re, opts.ContextLines)
	note := ""
	if len(matches) == 0 {
		matches, truncated, note = grepAlternationRetry(ctx, opts, matches, truncated)
	}
	return GrepResult{
		SkillName:  ctx.Name,
		Pattern:    opts.Pattern,
		Matches:    matches,
		MatchCount: len(matches),
		Truncated:  truncated,
		Note:       note,
	}, nil
}

func grepAlternationRetry(ctx SkillContext, opts GrepOptions, matches []GrepMatch, truncated bool) ([]GrepMatch, bool, string) {
	orRe, holds := strutil.GrepAlternation(opts.Pattern, opts.CaseInsensitive)
	if !holds {
		return matches, truncated, ""
	}
	if orRe != nil {
		if orMatches, orTruncated := grepResources(ctx, orRe, opts.ContextLines); len(orMatches) > 0 {
			return orMatches, orTruncated, strutil.GrepAlternationRetriedNote
		}
	}
	return matches, truncated, strutil.GrepLiteralPipeNote
}

func compileGrepPattern(pattern string, caseInsensitive bool) (*regexp.Regexp, error) {
	if caseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPattern, err)
	}
	return re, nil
}

func grepResources(ctx SkillContext, re *regexp.Regexp, contextLines int) ([]GrepMatch, bool) {
	matches := make([]GrepMatch, 0)
	truncated := false

	for _, sub := range []string{"references", "assets"} {
		if truncated {
			break
		}
		dir := filepath.Join(ctx.LocalPath, sub)
		if !dirExists(dir) {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d == nil || d.IsDir() {
				return nil
			}
			if len(matches) >= MaxGrepResults {
				truncated = true
				return filepath.SkipAll
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			rel, relErr := filepath.Rel(ctx.LocalPath, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if !re.MatchString(line) {
					continue
				}
				if len(matches) >= MaxGrepResults {
					truncated = true
					break
				}
				text := line
				if contextLines > 0 {
					start := max(i-contextLines, 0)
					end := min(i+contextLines+1, len(lines))
					text = strings.Join(lines[start:end], "\n")
				}
				matches = append(matches, GrepMatch{
					File: rel,
					Path: rel,
					Line: i + 1,
					Text: text,
				})
			}
			return nil
		})
	}
	return matches, truncated
}
