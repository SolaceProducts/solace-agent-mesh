// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var pythonCompatIncludePattern = regexp.MustCompile(`(?m)^([ \t]*)!include\s+["']?([^"'\s]+)["']?`)

const maxIncludeDepth = 50

func ProcessIncludes(content, baseDir string) (string, error) {
	return processIncludesRecursive(content, baseDir, nil, 0)
}

func processIncludesRecursive(content, baseDir string, visited []string, depth int) (string, error) {
	if depth > maxIncludeDepth {
		return "", fmt.Errorf("include depth limit exceeded (%d levels): %s", maxIncludeDepth, strings.Join(visited, " -> "))
	}

	var processErr error

	result := pythonCompatIncludePattern.ReplaceAllStringFunc(content, func(match string) string {
		if processErr != nil {
			return match
		}

		groups := pythonCompatIncludePattern.FindStringSubmatch(match)
		indent := groups[1]
		includePath := groups[2]

		fullPath := filepath.Join(baseDir, includePath)
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			processErr = fmt.Errorf("resolving include path %s: %w", fullPath, err)
			return match
		}

		for _, v := range visited {
			if v == absPath {
				chain := append(append([]string{}, visited...), absPath)
				processErr = fmt.Errorf("circular include detected: %s", strings.Join(chain, " -> "))
				return match
			}
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			processErr = fmt.Errorf("reading included file %s: %w", fullPath, err)
			return match
		}

		includeDir := filepath.Dir(absPath)
		newVisited := append(append([]string{}, visited...), absPath)
		included, err := processIncludesRecursive(string(data), includeDir, newVisited, depth+1)
		if err != nil {
			processErr = err
			return match
		}

		lines := strings.Split(included, "\n")
		for i, line := range lines {
			if line != "" {
				lines[i] = indent + line
			}
		}
		return strings.Join(lines, "\n")
	})

	if processErr != nil {
		return "", processErr
	}
	return result, nil
}
