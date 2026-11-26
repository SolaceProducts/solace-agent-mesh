// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var toolReferencePattern = regexp.MustCompile("(?:`[A-Za-z][A-Za-z0-9_]*`(?:, | or | and ))*`[A-Za-z][A-Za-z0-9_]*` tools?\\b")

var toolReferenceName = regexp.MustCompile("`([A-Za-z][A-Za-z0-9_]*)`")

func toolReferences(text string) []string {
	var names []string
	for _, phrase := range toolReferencePattern.FindAllString(text, -1) {
		for _, m := range toolReferenceName.FindAllStringSubmatch(phrase, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

var skillRuntimeToolNames = func() map[string]bool {
	names := map[string]bool{
		"load_skill":   true,
		"unload_skill": true,
	}
	for _, name := range SkillResourceToolNames() {
		names[name] = true
	}
	return names
}()

func TestPromptToolReferencesResolveToRealTools(t *testing.T) {
	prompt := renderFullPrompt(t)

	names := toolReferences(prompt)
	if len(names) == 0 {
		t.Fatal("no `name` tool references found in internal/agent's string literals; the pattern no longer " +
			"matches how the prompt names tools, so this guard is checking nothing")
	}

	for _, name := range names {
		if !toolNameResolves(t, name) {
			t.Errorf("the prompt tells the model to use the %q tool, which no registry provides. "+
				"Use the real tool name, or drop the reference.", name)
		}
	}
}

func TestPromptToolReferencesCoverTheLargeTableRule(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range toolReferences(renderFullPrompt(t)) {
		seen[name] = true
	}
	for _, name := range []string{"query_data_with_sql", "execute_python_code"} {
		if !seen[name] {
			t.Errorf("the guard no longer sees the %q tool named in the large-table rule", name)
		}
	}
}

func TestToolNameResolvesAcceptsRealToolsAndRejectsPhantoms(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		why  string
	}{
		{"artifact_grep", true, "in-process factory"},
		{"transform_data_with_jmespath", true, "in-process factory"},
		{"sub_task", true, "registered factory returning nil; the agent registers it at runtime"},
		{"execute_python_code", true, "STR-backed"},
		{"pdf_extract_text", true, "STR-backed"},
		{"peer_SomeAgent", true, "peer tools are registered per-task from discovery"},
		{"load_skill", true, "shipping tool; tools/go/skill is package main with no manifest"},
		{"grep_skill_resources", true, "same family — invisible to both halves of strToolNames"},
		{"unload_skill", true, "registered agent-side in internal/awe/skills.go"},
		{"Grep", false, "Claude Code tool name; the bug this guard exists for"},
		{"search_artifacts", false, "never existed; meant to be artifact_grep"},
		{"Read", false, "Claude Code tool name; the real one is load_artifact"},
		{"edit_artifact", false, "never existed; meant to be artifact_search_and_replace_regex"},
		{"save_artifact", false, "a fenced block directive, not a callable tool"},
		{"create_artifact", false, "never existed; artifacts come from the fenced block"},
	} {
		if got := toolNameResolves(t, tc.name); got != tc.want {
			t.Errorf("toolNameResolves(%q) = %v, want %v (%s)", tc.name, got, tc.want, tc.why)
		}
	}
}

func renderFullPrompt(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob("../agent/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if text, err := strconv.Unquote(lit.Value); err == nil {
					b.WriteString(text)
					b.WriteString("\n")
				}
			}
			return true
		})
	}
	return b.String()
}

func toolNameResolves(t *testing.T, name string) bool {
	t.Helper()

	if strings.HasPrefix(name, "peer_") {
		return true
	}
	if skillRuntimeToolNames[name] {
		return true
	}
	if skill, bare, ok := strings.Cut(name, "__"); ok {
		assertSkillToolDeclared(t, skill, bare)
		return true
	}
	if _, ok := builtinToolFactories[name]; ok {
		return true
	}
	return strToolNames(t)[name]
}
