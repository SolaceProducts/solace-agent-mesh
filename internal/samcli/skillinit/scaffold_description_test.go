// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"regexp"
	"testing"
)

var newToolDescRe = regexp.MustCompile(`sdk\.NewTool\([^,]+,\s*"([^"]*)"`)

var pyDocstringRe = regexp.MustCompile(`def greet\([^)]*\)[^:]*:\s*"""(.+)`)

func TestGoSkillScaffold_HasNonEmptyDescription(t *testing.T) {
	m := newToolDescRe.FindStringSubmatch(renderGoMain("demo"))
	if m == nil {
		t.Fatalf("could not find sdk.NewTool call in Go skill scaffold:\n%s", renderGoMain("demo"))
	}
	if m[1] == "" {
		t.Error("Go skill scaffold sdk.NewTool description must be non-empty")
	}
}

func TestPySkillScaffold_HasDocstring(t *testing.T) {
	m := pyDocstringRe.FindStringSubmatch(renderPyMain())
	if m == nil {
		t.Fatalf("could not find greet() docstring in Python skill scaffold:\n%s", renderPyMain())
	}
	if m[1] == "" {
		t.Error("Python skill scaffold greet() docstring first line must be non-empty")
	}
}
