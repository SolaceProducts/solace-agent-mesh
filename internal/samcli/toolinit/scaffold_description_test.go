// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"regexp"
	"testing"
)

var newToolDescRe = regexp.MustCompile(`sdk\.NewTool\([^,]+,\s*"([^"]*)"`)

var pyDocstringRe = regexp.MustCompile(`def greet\([^)]*\)[^:]*:\s*"""(.+)`)

func TestGoScaffold_HasNonEmptyDescription(t *testing.T) {
	m := newToolDescRe.FindStringSubmatch(renderGoMain("demo"))
	if m == nil {
		t.Fatalf("could not find sdk.NewTool call in Go scaffold:\n%s", renderGoMain("demo"))
	}
	if m[1] == "" {
		t.Error("Go scaffold sdk.NewTool description must be non-empty")
	}
}

func TestPyScaffold_HasDocstring(t *testing.T) {
	m := pyDocstringRe.FindStringSubmatch(renderPyMain())
	if m == nil {
		t.Fatalf("could not find greet() docstring in Python scaffold:\n%s", renderPyMain())
	}
	if m[1] == "" {
		t.Error("Python scaffold greet() docstring first line must be non-empty")
	}
}
