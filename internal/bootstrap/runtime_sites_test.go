// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

const modulePath = "github.com/SolaceProducts/solace-agent-mesh"

type runtimeSite struct{ file, function string }

type runtimeSiteGate struct {
	mustCall  string
	exemption string
}

var productionRuntimeSites = map[runtimeSite]runtimeSiteGate{
	{"cmd/awe/main.go", "run"}:                                        {mustCall: "Bootstrap"},
	{"cmd/gateway/main.go", "run"}:                                    {mustCall: "Bootstrap"},
	{"cmd/platform/main.go", "run"}:                                   {mustCall: "Bootstrap"},
	{"cmd/str/main.go", "run"}:                                        {mustCall: "Bootstrap"},
	{"internal/embedded/embedded.go", "buildRuntime"}:                 {mustCall: "BudgetEnforcement"},
	{"cmd/notify/main.go", "run"}:                                     {exemption: "makes no LLM calls"},
	{"cmd/bounce/main.go", "run"}:                                     {exemption: "makes no LLM calls"},
	{"internal/testing/harness/platform_harness.go", "setupPlatform"}: {exemption: "test harness; its gate takes no state, so it admits every call"},
	{"test/platform/go_service.go", "Start"}:                          {exemption: "test harness; its gate takes no state, so it admits every call"},
}

func TestOnlyListedSitesBuildAProductionRuntime(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	found := map[runtimeSite]bool{}
	var problems []string
	for _, root := range []string{"cmd", "internal", "pkg", "tools", "test"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".claude", "node_modules", "vendor", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			sites, err := runtimeBuilders(path, filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			for site, builder := range sites {
				found[site] = true
				gate, listed := productionRuntimeSites[site]
				switch {
				case !listed:
					problems = append(problems, fmt.Sprintf("%s %s builds a runtime and is not listed", site.file, site.function))
				case gate.mustCall == "":
				case !builder.calls[gate.mustCall]:
					problems = append(problems, fmt.Sprintf("%s %s builds a runtime without calling bootstrap.%s", site.file, site.function, gate.mustCall))
				case !builder.spreadsOptions:
					problems = append(problems, fmt.Sprintf("%s %s builds a runtime without passing it an options slice", site.file, site.function))
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	internal := sourcetree.IsInternal(t)
	for site := range productionRuntimeSites {
		if _, err := os.Stat(filepath.Join(repoRoot, site.file)); errors.Is(err, fs.ErrNotExist) && !internal {
			continue
		}
		if !found[site] {
			problems = append(problems, fmt.Sprintf("%s %s is listed but builds no runtime", site.file, site.function))
		}
	}
	slices.Sort(problems)
	if len(problems) > 0 {
		t.Errorf("production runtimes:\n  %s\n\n"+
			"A process that books spend needs a budget gate something keeps current: build its runtime "+
			"from bootstrap.Bootstrap's options or add bootstrap.BudgetEnforcement, then list the site "+
			"in productionRuntimeSites.", strings.Join(problems, "\n  "))
	}
}

type runtimeBuilder struct {
	calls          map[string]bool
	spreadsOptions bool
}

func runtimeBuilders(path, rel string) (map[runtimeSite]runtimeBuilder, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	runtimeName, bootstrapName := importName(file, "/internal/runtime"), importName(file, "/internal/bootstrap")
	if runtimeName == "" {
		return nil, nil
	}
	sites := map[runtimeSite]runtimeBuilder{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		buildsRuntime := false
		builder := runtimeBuilder{calls: map[string]bool{}}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			switch {
			case !ok:
			case pkg.Name == runtimeName && sel.Sel.Name == "NewRuntime":
				buildsRuntime = true
				builder.spreadsOptions = builder.spreadsOptions || call.Ellipsis.IsValid()
			case pkg.Name == bootstrapName:
				builder.calls[sel.Sel.Name] = true
			}
			return true
		})
		if buildsRuntime {
			sites[runtimeSite{rel, fn.Name.Name}] = builder
		}
	}
	return sites, nil
}

func importName(file *ast.File, suffix string) string {
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		if importPath != modulePath+suffix {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(importPath)
	}
	return ""
}
