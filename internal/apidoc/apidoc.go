// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apidoc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
)

const (
	MethodName = "APIDoc"
	FileSuffix = "_apidoc.go"
)

type Field struct {
	Doc     string
	Comment string
}

type Doc struct {
	Type   string
	Fields map[string]Field
}

func (d Doc) Field(name string) Field {
	return d.Fields[name]
}

func (f Field) Text() string {
	if f.Doc != "" {
		return f.Doc
	}
	return f.Comment
}

func Read(f *ast.File) (map[string]Doc, error) {
	docs := map[string]Doc{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != MethodName || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		typeName := receiverTypeName(fn.Recv.List[0].Type)
		if typeName == "" {
			continue
		}
		doc, err := readBody(fn.Body)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", typeName, MethodName, err)
		}
		docs[typeName] = doc
	}
	return docs, nil
}

func SiblingPath(goFile string) string {
	return strings.TrimSuffix(goFile, ".go") + FileSuffix
}

func ReadSibling(goFile string) (map[string]Doc, error) {
	path := SiblingPath(goFile)
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Doc{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return Read(f)
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

func readBody(body *ast.BlockStmt) (Doc, error) {
	if body == nil || len(body.List) != 1 {
		return Doc{}, fmt.Errorf("body must be a single return statement")
	}
	ret, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return Doc{}, fmt.Errorf("body must be a single return statement")
	}
	lit, ok := ret.Results[0].(*ast.CompositeLit)
	if !ok {
		return Doc{}, fmt.Errorf("must return a Doc literal")
	}
	var doc Doc
	for _, elt := range lit.Elts {
		key, value, err := keyed(elt)
		if err != nil {
			return Doc{}, err
		}
		switch key {
		case "Type":
			if doc.Type, err = stringValue(value); err != nil {
				return Doc{}, fmt.Errorf("Type: %w", err)
			}
		case "Fields":
			if doc.Fields, err = readFields(value); err != nil {
				return Doc{}, fmt.Errorf("Fields: %w", err)
			}
		default:
			return Doc{}, fmt.Errorf("unknown Doc key %q", key)
		}
	}
	return doc, nil
}

func readFields(expr ast.Expr) (map[string]Field, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("must be a map literal")
	}
	fields := make(map[string]Field, len(lit.Elts))
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("map entries must be key: value")
		}
		name, err := stringValue(kv.Key)
		if err != nil {
			return nil, fmt.Errorf("field name: %w", err)
		}
		field, err := readField(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fields[name] = field
	}
	return fields, nil
}

func readField(expr ast.Expr) (Field, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return Field{}, fmt.Errorf("must be a Field literal")
	}
	var field Field
	for _, elt := range lit.Elts {
		key, value, err := keyed(elt)
		if err != nil {
			return Field{}, err
		}
		text, err := stringValue(value)
		if err != nil {
			return Field{}, fmt.Errorf("%s: %w", key, err)
		}
		switch key {
		case "Doc":
			field.Doc = text
		case "Comment":
			field.Comment = text
		default:
			return Field{}, fmt.Errorf("unknown Field key %q", key)
		}
	}
	return field, nil
}

func keyed(elt ast.Expr) (string, ast.Expr, error) {
	kv, ok := elt.(*ast.KeyValueExpr)
	if !ok {
		return "", nil, fmt.Errorf("literal entries must be keyed")
	}
	ident, ok := kv.Key.(*ast.Ident)
	if !ok {
		return "", nil, fmt.Errorf("literal keys must be field names")
	}
	return ident.Name, kv.Value, nil
}

func stringValue(expr ast.Expr) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", fmt.Errorf("must be a string literal")
		}
		return strconv.Unquote(e.Value)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", fmt.Errorf("only + may join string literals")
		}
		left, err := stringValue(e.X)
		if err != nil {
			return "", err
		}
		right, err := stringValue(e.Y)
		if err != nil {
			return "", err
		}
		return left + right, nil
	case *ast.ParenExpr:
		return stringValue(e.X)
	}
	return "", fmt.Errorf("must be a string literal")
}

func Lines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
