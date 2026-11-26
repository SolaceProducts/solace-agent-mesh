// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osteele/liquid"
)

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestInclude_RelativePath_Blocked(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "TOP-SECRET-SENTINEL-DO-NOT-LEAK"
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine()
	out, err := engine.ParseAndRenderString(`before {% include "secret.txt" %} after`, nil)
	if strings.Contains(out, sentinel) {
		t.Fatalf("rendered output leaked file content: %q", out)
	}
	if err == nil {
		t.Fatalf("expected an error from {%% include %%} with deny-store, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), IncludeDisabledMessage) {
		t.Fatalf("error %q does not contain IncludeDisabledMessage marker %q", err.Error(), IncludeDisabledMessage)
	}
}

func TestInclude_RelativeTraversal_Blocked(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "TRAVERSAL-SENTINEL"
	parent := filepath.Dir(dir)
	target := filepath.Join(parent, "outer-secret.txt")
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(target) })

	engine := NewEngine()
	out, _ := engine.ParseAndRenderString(`before {% include "../outer-secret.txt" %} after`, nil)
	if strings.Contains(out, sentinel) {
		t.Fatalf("rendered output leaked file content: %q", out)
	}
}

func TestInclude_DefaultEngine_LeaksFileContent(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "DEFAULT-ENGINE-LEAK-CHECK"
	if err := os.WriteFile(filepath.Join(dir, "sentinel.txt"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	engine := liquid.NewEngine()
	out, err := engine.ParseAndRenderString(`{% include "sentinel.txt" %}`, nil)
	if err != nil || !strings.Contains(out, sentinel) {
		t.Skipf("upstream behavior changed: default liquid.NewEngine() did not read the file. wrapper may no longer be needed. out=%q err=%v", out, err)
	}
}

func TestNonIncludeTemplates_StillRender(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(`hello {{ name }}`, map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("unexpected output: %q", out)
	}

	out, err = engine.ParseAndRenderString(`{% for n in (1..3) %}{{ n }}{% endfor %}`, nil)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "123" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestNewStrictEngineRejectsUndefinedVariablesAndStillBlocksInclude(t *testing.T) {
	engine := NewStrictEngine()

	if _, err := engine.ParseAndRenderString(`{{ missing }}`, map[string]any{"present": "x"}); err == nil {
		t.Error("an undefined variable must fail a strict render")
	}
	if out, err := engine.ParseAndRenderString(`{{ present }}`, map[string]any{"present": "x"}); err != nil || out != "x" {
		t.Errorf("a defined variable rendered %q, %v; want x", out, err)
	}
	if _, err := engine.ParseAndRenderString(`{% include "anything" %}`, nil); err == nil || !strings.Contains(err.Error(), IncludeDisabledMessage) {
		t.Errorf("include through the strict engine: %v, want the include-disabled error", err)
	}
}
