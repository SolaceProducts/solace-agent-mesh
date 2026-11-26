// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"
)

type DocSource interface {
	Type(pkgPath, typeName string) string
	Field(pkgPath, typeName, fieldName string) string
}

type astDocs struct {
	types    map[string]string
	fields   map[string]string
	examples map[string]string
}

func NewASTDocs(dirs map[string]string) (DocSource, error) {
	d := &astDocs{types: map[string]string{}, fields: map[string]string{}, examples: map[string]string{}}
	for pkgPath, dir := range dirs {
		if err := d.loadDir(pkgPath, dir); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func NewASTDocsForPackages(importPaths ...string) (DocSource, error) {
	root, modulePath, err := enclosingModule()
	if err != nil {
		return nil, err
	}
	dirs := make(map[string]string, len(importPaths))
	for _, importPath := range importPaths {
		rel, inModule := strings.CutPrefix(importPath, modulePath)
		if !inModule || (rel != "" && !strings.HasPrefix(rel, "/")) {
			return nil, fmt.Errorf("package %q is outside module %q", importPath, modulePath)
		}
		dirs[importPath] = filepath.Join(root, filepath.FromSlash(rel))
	}
	return NewASTDocs(dirs)
}

func enclosingModule() (root, modulePath string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("working directory: %w", err)
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if path, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
					return dir, strings.Trim(strings.TrimSpace(path), `"`), nil
				}
			}
			return "", "", fmt.Errorf("%s has no module line", filepath.Join(dir, "go.mod"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", errors.New("no go.mod found above the working directory")
		}
		dir = parent
	}
}

func (d *astDocs) Type(pkgPath, typeName string) string {
	return withoutGoName(d.types[pkgPath+"."+typeName], typeName, "")
}

func (d *astDocs) Field(pkgPath, typeName, fieldName string) string {
	key := pkgPath + "." + typeName + "." + fieldName
	return withoutGoName(d.fields[key], fieldName, d.examples[key])
}

var linkingVerbs = map[string]bool{"is": true, "are": true}

var jsonLiterals = map[string]bool{"true": true, "false": true, "null": true}

var conditionWords = map[string]bool{"when": true, "if": true, "unless": true, "once": true, "while": true}

func withoutGoName(doc, goName, example string) string {
	rest, named := cutWord(doc, goName)
	switch {
	case named && strings.HasPrefix(rest, "and "):
		return doc
	case named:
		if first, after, found := strings.Cut(rest, " "); found && linkingVerbs[first] {
			rest = strings.TrimLeftFunc(after, unicode.IsSpace)
		}
	default:
		rest, named = strings.CutPrefix(doc, goName+", ")
		if first, _, _ := strings.Cut(rest, " "); !named || !conditionWords[first] {
			return doc
		}
	}
	if opensWithWireValue(rest, example) {
		return rest
	}
	return upperFirst(rest)
}

func cutWord(s, word string) (string, bool) {
	rest, found := strings.CutPrefix(s, word)
	if !found || (rest != "" && !unicode.IsSpace(rune(rest[0]))) {
		return s, false
	}
	return strings.TrimLeftFunc(rest, unicode.IsSpace), true
}

func opensWithWireValue(text, example string) bool {
	first, after, _ := strings.Cut(text, " ")
	word := strings.TrimRight(first, ",.;:")
	switch {
	case word == "" || !startsLower(word), jsonLiterals[word]:
		return false
	case strings.ContainsAny(word, "_:"), word == example:
		return true
	case strings.IndexFunc(word, unicode.IsUpper) > 0:
		return true
	}
	return strings.HasPrefix(after, "or ")
}

func startsLower(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLower(r)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func (d *astDocs) loadDir(pkgPath, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no Go files in %s for package %s", dir, pkgPath)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		docs, err := apidoc.Read(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for typeName, doc := range docs {
			d.types[pkgPath+"."+typeName] = strings.TrimSpace(doc.Type)
			for fieldName, field := range doc.Fields {
				d.fields[pkgPath+"."+typeName+"."+fieldName] = strings.TrimSpace(field.Text())
			}
		}
		d.collectExamples(pkgPath, f)
	}
	return nil
}

func (d *astDocs) collectExamples(pkgPath string, f *ast.File) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				example := exampleTag(field)
				for _, name := range field.Names {
					d.examples[pkgPath+"."+ts.Name.Name+"."+name.Name] = example
				}
			}
		}
	}
}

func exampleTag(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	return reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("example")
}
