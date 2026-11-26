// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestAgentExtraConfigSchema_CoversAllWalkerKeys(t *testing.T) {
	t.Parallel()

	walkerPath := walkerSourcePath(t)
	paths := extractWalkerKeyPaths(t, walkerPath)
	if len(paths) == 0 {
		t.Fatalf("no keys extracted from %s — the AST scanner likely fell out of sync with the walker", walkerPath)
	}

	var missing []string
	for _, p := range paths {
		leaf := p
		root := p
		if i := strings.LastIndex(p, "."); i >= 0 {
			leaf = p[i+1:]
		}
		if i := strings.Index(p, "."); i >= 0 {
			root = p[:i]
		}
		if isForbiddenWalkerKey(leaf) || isForbiddenWalkerKey(root) {
			continue
		}
		if extraConfigSchemaPathExists(p) {
			continue
		}
		missing = append(missing, p)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf(
			"key paths consumed by internal/awe/appconfig.go but missing from agentExtraConfigSchema: %v\n"+
				"Add an entry to agentExtraConfigSchema in agent_extra_config_schema.go, or add the key to the\n"+
				"forbidden lists in agent_extra_config_validation.go if it is computed by the platform / structured DTO.",
			missing,
		)
	}
}

func extraConfigSchemaPathExists(path string) bool {
	if path == "" {
		return false
	}
	parts := strings.Split(path, ".")
	cur := agentExtraConfigSchema
	for i, segment := range parts {
		var matched agentExtraConfigKey
		var found bool
		for canonical, entry := range cur {
			if runtimeKeyOf(canonical, entry) == segment {
				matched = entry
				found = true
				break
			}
		}
		if !found {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		if matched.Nested == nil {
			return true
		}
		cur = matched.Nested
	}
	return false
}

func walkerSourcePath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get the package directory: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(dir, "..", "..", ".."))
	return filepath.Join(repoRoot, "internal", "awe", "appconfig.go")
}

func extractWalkerKeyPaths(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	sectionByVar := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		var lhs []ast.Expr
		var rhs []ast.Expr
		switch s := n.(type) {
		case *ast.AssignStmt:
			lhs, rhs = s.Lhs, s.Rhs
		default:
			return true
		}
		if len(lhs) != 1 || len(rhs) != 1 {
			return true
		}
		ident, ok := lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		call, ok := rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "GetSection" || len(call.Args) == 0 {
			return true
		}
		section, ok := stringLiteral(call.Args[0])
		if !ok {
			return true
		}
		recv := ""
		if recvIdent, ok := sel.X.(*ast.Ident); ok {
			recv = recvIdent.Name
		}
		switch recv {
		case "", "cfg":
			sectionByVar[ident.Name] = section
		default:
			if parent, ok := sectionByVar[recv]; ok {
				sectionByVar[ident.Name] = parent + "." + section
			} else {
				sectionByVar[ident.Name] = section
			}
		}
		return true
	})

	pathSet := make(map[string]struct{})
	emit := func(section, key string) {
		if key == "" {
			return
		}
		full := key
		if section != "" {
			full = section + "." + key
		}
		pathSet[full] = struct{}{}
	}

	keyMethods := map[string]bool{
		"GetString":      true,
		"GetInt":         true,
		"GetBool":        true,
		"GetStringSlice": true,
		"GetSection":     true,
		"GetRaw":         true,
		"Has":            true,
	}
	keyHelpersArg1 := map[string]bool{
		"intDefault":    true,
		"stringDefault": true,
		"boolPtr":       true,
		"boolDefault":   true,
	}
	keyHelpersFromArg2 := map[string]bool{
		"intWithAliases": true,
	}

	resolveReceiver := func(arg ast.Expr) string {
		ident, ok := arg.(*ast.Ident)
		if !ok {
			return ""
		}
		if ident.Name == "cfg" {
			return ""
		}
		return sectionByVar[ident.Name]
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if !keyMethods[fn.Sel.Name] || len(call.Args) == 0 {
				return true
			}
			lit, ok := stringLiteral(call.Args[0])
			if !ok {
				return true
			}
			recv := ""
			if recvIdent, ok := fn.X.(*ast.Ident); ok && recvIdent.Name != "cfg" {
				recv = sectionByVar[recvIdent.Name]
			}
			emit(recv, lit)
		case *ast.Ident:
			switch {
			case keyHelpersArg1[fn.Name]:
				if len(call.Args) >= 2 {
					if lit, ok := stringLiteral(call.Args[1]); ok {
						emit(resolveReceiver(call.Args[0]), lit)
					}
				}
			case keyHelpersFromArg2[fn.Name]:
				if len(call.Args) >= 3 {
					recv := resolveReceiver(call.Args[0])
					for _, arg := range call.Args[2:] {
						if lit, ok := stringLiteral(arg); ok {
							emit(recv, lit)
						}
					}
				}
			}
		}
		return true
	})

	out := make([]string, 0, len(pathSet))
	for k := range pathSet {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v := lit.Value
	if len(v) >= 2 && (v[0] == '"' || v[0] == '`') {
		return v[1 : len(v)-1], true
	}
	return v, true
}

func TestExtraConfigSchemaPathExists_RuntimeKeyLookup(t *testing.T) {
	t.Parallel()

	if !extraConfigSchemaPathExists("agent_init_function") {
		t.Errorf("expected identity-mapped 'agent_init_function' to resolve")
	}
	if !extraConfigSchemaPathExists("supports_streaming") {
		t.Errorf("expected snake_case walker key 'supports_streaming' to resolve via RuntimeKey")
	}
	if extraConfigSchemaPathExists("supportsStreaming") {
		t.Errorf("camelCase 'supportsStreaming' must not resolve — the walker only reads the snake_case form")
	}
	if !extraConfigSchemaPathExists("agent_card.documentationUrl") {
		t.Errorf("expected 'agent_card.documentationUrl' to resolve — agentCard block flips, child is identity")
	}
	if !extraConfigSchemaPathExists("agent_card_publishing.interval_seconds") {
		t.Errorf("expected nested snake_case path to resolve via parent RuntimeKey + child RuntimeKey")
	}
	if extraConfigSchemaPathExists("agent_card_publishing.intervalSeconds") {
		t.Errorf("camelCase nested key 'intervalSeconds' must not resolve — the walker reads 'interval_seconds'")
	}
}

var typedListItemWalkerKeys = []struct {
	SchemaPath string
	WalkerKey  string
}{
	{"agentCard.welcome.suggestions[*].label", "label"},
	{"agentCard.welcome.suggestions[*].prompt", "prompt"},
	{"agentCard.welcome.suggestions[*].autoSend", "auto_send"},
}

func resolveTypedListItemRuntimeKey(schema map[string]agentExtraConfigKey, path string) (string, bool) {
	parts := strings.Split(path, ".")
	cur := schema
	for i, segment := range parts {
		bare := strings.TrimSuffix(segment, "[*]")
		entry, ok := cur[bare]
		if !ok {
			return "", false
		}
		if i == len(parts)-1 {
			return runtimeKeyOf(bare, entry), true
		}
		if entry.Nested == nil {
			return "", false
		}
		cur = entry.Nested
	}
	return "", false
}

func TestAgentExtraConfigSchema_TypedListItemKeysMatchWalker(t *testing.T) {
	t.Parallel()

	for _, entry := range typedListItemWalkerKeys {
		runtimeKey, ok := resolveTypedListItemRuntimeKey(agentExtraConfigSchema, entry.SchemaPath)
		if !ok {
			t.Errorf("schema path %q does not resolve — schema and walker have drifted", entry.SchemaPath)
			continue
		}
		if runtimeKey != entry.WalkerKey {
			t.Errorf("schema path %q yields runtime key %q but the walker reads %q — schema and walker have drifted",
				entry.SchemaPath, runtimeKey, entry.WalkerKey)
		}
	}
}

func TestAgentExtraConfigSchema_TypedListItemDriftFails(t *testing.T) {
	t.Parallel()

	broken := map[string]agentExtraConfigKey{
		"agentCard": {
			Type:       extraTypeObject,
			RuntimeKey: "agent_card",
			Nested: map[string]agentExtraConfigKey{
				"welcome": {
					Type: extraTypeObject,
					Nested: map[string]agentExtraConfigKey{
						"message": {Type: extraTypeString},
						"suggestions": {
							Type: extraTypeList,
							Nested: map[string]agentExtraConfigKey{
								"label":     {Type: extraTypeString},
								"prompt":    {Type: extraTypeString},
								"autoSendX": {Type: extraTypeBool, RuntimeKey: "auto_send"},
							},
						},
					},
				},
			},
		},
	}
	if _, ok := resolveTypedListItemRuntimeKey(broken, "agentCard.welcome.suggestions[*].autoSend"); ok {
		t.Errorf("expected broken schema (autoSend renamed to autoSendX) to fail resolution for autoSend, but it succeeded")
	}
}

func TestRuntimeKeyOf(t *testing.T) {
	t.Parallel()

	if got := runtimeKeyOf("supportsStreaming", agentExtraConfigKey{RuntimeKey: "supports_streaming"}); got != "supports_streaming" {
		t.Errorf("expected explicit RuntimeKey, got %q", got)
	}
	if got := runtimeKeyOf("model", agentExtraConfigKey{}); got != "model" {
		t.Errorf("expected identity → 'model', got %q", got)
	}
	if got := runtimeKeyOf("agent_init_function", agentExtraConfigKey{}); got != "agent_init_function" {
		t.Errorf("expected identity Python knob → 'agent_init_function', got %q", got)
	}
}

func isForbiddenWalkerKey(key string) bool {
	if _, ok := forbiddenComputedRuntimeKeys[key]; ok {
		return true
	}
	if _, ok := forbiddenAdditionalConfigStructuredKeys[key]; ok {
		return true
	}
	if _, ok := forbiddenAdditionalConfigComputedKeys[key]; ok {
		return true
	}
	if _, ok := strippedUnsupportedKeys[key]; ok {
		return true
	}
	return strings.HasPrefix(key, "_")
}
