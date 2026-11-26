// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth_test

import (
	"go/ast"
	"go/types"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/tools/go/packages"
)

const getterScanChild = "SAM_SUBJECT_GETTER_SCAN_CHILD"

func TestNoSubjectGetterIsUsedUncalled(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the whole repository")
	}
	if raceEnabled && os.Getenv(getterScanChild) == "" {
		rerunGetterScanWithoutRaceBecauseGoTypesTripsTheDetector(t)
		return
	}
	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests:      true,
		Dir:        "../../..",
		BuildFlags: []string{"-tags=product_analytics,integration,platform_crosstest,gateway_crosstest"},
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Errorf("load %s: %v", p.PkgPath, e)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		called := map[*ast.SelectorExpr]bool{}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if s, ok := c.Fun.(*ast.SelectorExpr); ok {
						called[s] = true
					}
				}
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				s, ok := n.(*ast.SelectorExpr)
				if !ok || called[s] || !isSubjectMethodValue(p.TypesInfo.Selections[s]) {
					return true
				}
				if pos := p.Fset.Position(s.Pos()).String(); !seen[pos] {
					seen[pos] = true
					t.Errorf("%s: Subject.%s is referenced but not called", pos, s.Sel.Name)
				}
				return true
			})
		}
	}
}

func rerunGetterScanWithoutRaceBecauseGoTypesTripsTheDetector(t *testing.T) {
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestNoSubjectGetterIsUsedUncalled$", ".")
	cmd.Env = append(os.Environ(), getterScanChild+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("getter scan without -race: %v\n%s", err, out)
	}
}

func isSubjectMethodValue(sel *types.Selection) bool {
	if sel == nil || (sel.Kind() != types.MethodVal && sel.Kind() != types.MethodExpr) {
		return false
	}
	recv := sel.Recv()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	return recv.String() == gwAuthPkg+".Subject"
}
