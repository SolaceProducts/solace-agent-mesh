// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func buildFixtureSkill(t *testing.T, skillsDir, skillName string) string {
	t.Helper()
	skillDir := filepath.Join(skillsDir, skillName)

	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: `+skillName+`
description: Test fixture skill
tags:
  - test
  - fixture
examples:
  - "load and read"
required_builtins:
  - read_skill_resource
---

# `+skillName+`

This is a test skill.
Line two of instructions.
`)

	writeFile(t, filepath.Join(skillDir, "references", "auth.md"), `# Auth Reference
JWT bearer tokens are required.
Algorithm: HS256.
Header: Authorization: Bearer <token>.
`)
	writeFile(t, filepath.Join(skillDir, "references", "errors.md"), `# Error Codes
401 unauthorized
403 forbidden
500 server error
`)
	writeFile(t, filepath.Join(skillDir, "assets", "logo.txt"), "logo placeholder\n")
	writeFile(t, filepath.Join(skillDir, "tools", "manifest.yaml"), `
version: 1
tools:
  search_docs:
    description: Search docs
    parameters:
      type: object
      properties:
        q:
          type: string
    instructions: Search the docs by query.
`)
	return skillDir
}
