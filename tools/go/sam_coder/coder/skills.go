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

const skillFile = "SKILL.md"

type skill struct {
	name, description, path string
}

func loadSkills(dirs []string) ([]skill, error) {
	skills := make([]skill, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(dir, skillFile)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", dir, err)
		}
		s := skill{name: filepath.Base(dir), path: path}
		for key, value := range frontMatter(string(data)) {
			switch key {
			case "name":
				s.name = value
			case "description":
				s.description = value
			}
		}
		if s.description == "" {
			return nil, fmt.Errorf("skill %s: %s has no description in its front matter", dir, skillFile)
		}
		skills = append(skills, s)
	}
	return skills, nil
}

func frontMatter(text string) map[string]string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return fields
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return nil
}

func skillsNote(skills []skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Skills: procedures written for this kind of work. Before work a skill covers, read its file in full and follow it.\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s (%s)\n", s.name, s.description, s.path)
	}
	return b.String()
}
