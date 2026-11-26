// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

func TestEverySignalTypeIsAShapeTheLinkRefuses(t *testing.T) {
	file, err := parser.ParseFile(gotoken.NewFileSet(), "../../a2a/protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || spec.Type == nil {
			return true
		}
		if ident, ok := spec.Type.(*ast.Ident); !ok || ident.Name != "SignalType" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok {
				continue
			}
			kind, _ := strconv.Unquote(lit.Value)
			found++
			if !uiActedTypes[kind] {
				t.Errorf("signal type %q is not refused in an agent's result or tool result", kind)
			}
		}
		return true
	})
	if found < 20 {
		t.Fatalf("found only %d signal types; the const block moved", found)
	}
}

func TestAShapeSAMActsOnIsFoundAtAnyDepth(t *testing.T) {
	for kind := range uiActedTypes {
		for name, v := range map[string]any{
			"top level":        map[string]any{"type": kind},
			"nested":           map[string]any{"type": "report", "x": map[string]any{"y": map[string]any{"type": kind}}},
			"in a list":        []any{map[string]any{"type": "report"}, map[string]any{"type": kind}},
			"in a nested list": map[string]any{"rows": []any{[]any{map[string]any{"type": kind}}}},
		} {
			if !carriesUIShape(v) {
				t.Errorf("%s %s was not found", kind, name)
			}
		}
	}
	for _, key := range taskRecordKeys {
		if got := taskRecordKeyIn(map[string]any{"report": 1, "rows": []any{map[string]any{"metadata": map[string]any{key: "forged"}}}}); got != key {
			t.Errorf("a metadata object naming %s was found as %q", key, got)
		}
	}
	if got := taskRecordKeyIn(map[string]any{"metadata": map[string]any{"source": "policy.pdf", "page": 3}}); got != "" {
		t.Errorf("a document's own metadata was refused for %q", got)
	}
	for name, v := range map[string]any{
		"plain report":      map[string]any{"type": "report", "rows": []any{map[string]any{"type": "row"}}},
		"type not a string": map[string]any{"type": 7},
		"a string":          "redirect",
		"a different case":  map[string]any{"type": "Redirect"},
	} {
		if carriesUIShape(v) {
			t.Errorf("%s was refused", name)
		}
	}
}

func TestTheLinkSDKLeavesOutTheSameShapesTheEntrypointRefuses(t *testing.T) {
	src, err := os.ReadFile("../../../python/sam-link-sdk/src/sam_link_sdk/link/v2.py")
	if err != nil {
		t.Fatal(err)
	}
	quoted := regexp.MustCompile(`"([^"]+)"`)
	listed := func(name string, pattern *regexp.Regexp) []string {
		block := pattern.FindSubmatch(src)
		if block == nil {
			t.Fatalf("%s is not in the Link SDK's link/v2.py", name)
		}
		var out []string
		for _, m := range quoted.FindAllSubmatch(block[1], -1) {
			out = append(out, string(m[1]))
		}
		slices.Sort(out)
		return out
	}
	sdkTypes := listed("UI_ACTED_TYPES", regexp.MustCompile(`(?s)UI_ACTED_TYPES = frozenset\(\s*\{(.*?)\}\s*\)`))
	var types []string
	for kind := range uiActedTypes {
		types = append(types, kind)
	}
	slices.Sort(types)
	if !slices.Equal(sdkTypes, types) {
		t.Errorf("the Link SDK's UI_ACTED_TYPES is %v; the entrypoint refuses %v", sdkTypes, types)
	}
	sdkKeys := listed("TASK_RECORD_KEYS", regexp.MustCompile(`TASK_RECORD_KEYS = \(([^)]*)\)`))
	keys := slices.Sorted(slices.Values(taskRecordKeys))
	if !slices.Equal(sdkKeys, keys) {
		t.Errorf("the Link SDK's TASK_RECORD_KEYS is %v; the entrypoint reads %v", sdkKeys, keys)
	}
}
