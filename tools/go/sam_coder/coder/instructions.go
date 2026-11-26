// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const repoInstructionsMaxBytes = 32 << 10

var repoInstructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

const repoInstructionsHeader = "Repository instructions, from the instruction files between the workspace root and your working directory. Follow them unless the task says otherwise; files deeper in the tree win over shallower ones.\n"

func repoInstructions(base, root string) string {
	var b strings.Builder
	for _, dir := range dirsFromBase(base, root) {
		name, content, ok := readInstructionFile(dir)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(base, filepath.Join(dir, name))
		if err != nil {
			rel = name
		}
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", filepath.ToSlash(rel), strings.TrimSpace(content))
	}
	if b.Len() == 0 {
		return ""
	}
	text := b.String()
	if len(text) > repoInstructionsMaxBytes {
		text = strings.ToValidUTF8(text[:repoInstructionsMaxBytes], "") + "\n(instructions truncated)\n"
	}
	return repoInstructionsHeader + text
}

func dirsFromBase(base, root string) []string {
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return []string{base}
	}
	dirs := []string{base}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dirs = append(dirs, cur)
	}
	return dirs
}

func readInstructionFile(dir string) (string, string, bool) {
	for _, name := range repoInstructionFiles {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && strings.TrimSpace(string(content)) != "" {
			return name, string(content), true
		}
	}
	return "", "", false
}
