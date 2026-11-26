// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	internalPrefixLiteral = "/api/internal"
	internalPrefixName    = "InternalPrefix"
	repoRoot              = "../.."
)

var mayNameInternalPrefix = map[string]string{
	"internal/platform/api_prefix.go":          "defines the prefix and HandleWebUIRoute",
	"internal/platform/builderapply/routes.go": "exports the two route paths tests and callers address",
	"internal/bootstrap/platform.go":           "mounts the prefix on the gateway proxy",
}

var skippedDirs = map[string]bool{
	".git":         true,
	".claude":      true,
	"node_modules": true,
	"webui":        true,
	"python":       true,
	"third_party":  true,
}

func TestInternalPrefix_IsOnlyReachedThroughHandleWebUIRoute(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() && skippedDirs[d.Name()] {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if _, allowed := mayNameInternalPrefix[filepath.ToSlash(rel)]; allowed {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BasicLit:
				if value, unquoteErr := strconv.Unquote(node.Value); unquoteErr == nil && strings.Contains(value, internalPrefixLiteral) {
					t.Errorf("%s names %q: register web UI routes with HandleWebUIRoute so every one is mounted on both prefixes", fset.Position(node.Pos()), internalPrefixLiteral)
				}
			case *ast.Ident:
				if node.Name == internalPrefixName {
					t.Errorf("%s uses %s: register web UI routes with HandleWebUIRoute so every one is mounted on both prefixes", fset.Position(node.Pos()), internalPrefixName)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan sources: %v", err)
	}
}
