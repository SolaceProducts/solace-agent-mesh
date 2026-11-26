// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type typeScope struct {
	dir             string
	localStructs    map[string]bool
	imports         map[string]string
	externalStructs map[string]map[string]bool
}

func newTypeScope(filePath string, f *ast.File) (*typeScope, error) {
	dir := filepath.Dir(filePath)
	localStructs, err := dirStructNames(dir, f.Name.Name)
	if err != nil {
		return nil, fmt.Errorf("read package of %s: %w", filePath, err)
	}
	return &typeScope{
		dir:             dir,
		localStructs:    localStructs,
		imports:         fileImports(f),
		externalStructs: map[string]map[string]bool{},
	}, nil
}

var majorVersionSuffix = regexp.MustCompile(`^v[0-9]+$`)

func fileImports(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range f.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := defaultImportName(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = importPath
	}
	return out
}

func defaultImportName(importPath string) string {
	base := path.Base(importPath)
	if majorVersionSuffix.MatchString(base) {
		return path.Base(path.Dir(importPath))
	}
	return base
}

func (s *typeScope) isExternalStruct(pkg, name string) (bool, error) {
	importPath, ok := s.imports[pkg]
	if !ok {
		return false, fmt.Errorf("package %s is not imported", pkg)
	}
	structs, ok := s.externalStructs[importPath]
	if !ok {
		var err error
		structs, err = packageStructNames(importPath, s.dir)
		if err != nil {
			return false, err
		}
		s.externalStructs[importPath] = structs
	}
	return structs[name], nil
}

type passthroughType struct {
	imports      map[string]string
	holdsStructs bool
}

func (s *typeScope) passthroughType(expr ast.Expr) (passthroughType, error) {
	imports, err := s.referencedImports(expr)
	if err != nil {
		return passthroughType{}, err
	}
	holdsStructs, err := s.holdsStructs(expr)
	if err != nil {
		return passthroughType{}, err
	}
	return passthroughType{imports: imports, holdsStructs: holdsStructs}, nil
}

func (s *typeScope) holdsStructs(expr ast.Expr) (bool, error) {
	if slice, ok := expr.(*ast.ArrayType); ok && slice.Len == nil {
		expr = slice.Elt
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch t := expr.(type) {
	case *ast.Ident:
		return s.localStructs[t.Name], nil
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return false, nil
		}
		return s.isExternalStruct(pkg.Name, t.Sel.Name)
	}
	return false, nil
}

func packageStructNames(importPath, fromDir string) (map[string]bool, error) {
	pkg, err := build.Default.Import(importPath, fromDir, build.FindOnly)
	if err != nil {
		return nil, fmt.Errorf("locate package %s: %w", importPath, err)
	}
	names, err := dirStructNames(pkg.Dir, "")
	if err != nil {
		return nil, fmt.Errorf("read package %s: %w", importPath, err)
	}
	return names, nil
}

func dirStructNames(dir, pkgName string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if pkgName != "" && f.Name.Name != pkgName {
			continue
		}
		for name := range structTypeNames(f) {
			names[name] = true
		}
	}
	return names, nil
}

func (s *typeScope) referencedImports(expr ast.Expr) (map[string]string, error) {
	var out map[string]string
	var err error
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || err != nil {
			return err == nil
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath, found := s.imports[pkg.Name]
		if !found {
			err = fmt.Errorf("type %s.%s: package %s is not imported", pkg.Name, sel.Sel.Name, pkg.Name)
			return false
		}
		if out == nil {
			out = map[string]string{}
		}
		out[pkg.Name] = importPath
		return false
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
