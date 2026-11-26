// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"
)

type FieldKind int

const (
	KindUnsupported FieldKind = iota
	KindString
	KindInt
	KindInt64
	KindBool
	KindStringPtr
	KindIntPtr
	KindInt64Ptr
	KindStringSlice
	KindMapStringAny
	KindAny
	KindPassthrough
	KindJSONObject
	KindFloat64Ptr
	KindBoolPtr
)

func (k FieldKind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "int"
	case KindInt64:
		return "int64"
	case KindBool:
		return "bool"
	case KindStringPtr:
		return "*string"
	case KindIntPtr:
		return "*int"
	case KindInt64Ptr:
		return "*int64"
	case KindFloat64Ptr:
		return "*float64"
	case KindBoolPtr:
		return "*bool"
	case KindStringSlice:
		return "[]string"
	case KindMapStringAny:
		return "map[string]any"
	case KindAny:
		return "any"
	case KindPassthrough:
		return "passthrough"
	case KindJSONObject:
		return "json-object"
	default:
		return "unsupported"
	}
}

type Field struct {
	GoName          string
	JSONName        string
	OmitEmpty       bool
	Kind            FieldKind
	GoTypeQualified string
	Description     string
	Validate        string
	DocRequired     bool
	DocHidden       bool
	DocLimits       string
	GoTypeRaw       string
	Local           bool
	Default         string
	Key             string
	TypeImports     map[string]string
	HoldsStructs    bool
	Secret          bool
	OmitHash        bool
}

func (f Field) AuthoringKey() string {
	return cmp.Or(f.Key, f.JSONName)
}

type StructInfo struct {
	Name    string
	Doc     string
	Fields  []Field
	File    string
	Dropped []Field
}

func ParseStructs(filePath string, structNames ...string) ([]StructInfo, error) {
	return ParseStructsWithAlias(filePath, "", structNames...)
}

func ParseStructsWithAlias(filePath, alias string, structNames ...string) ([]StructInfo, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	wanted := make(map[string]bool, len(structNames))
	for _, n := range structNames {
		wanted[n] = true
	}

	scope, err := newTypeScope(filePath, f)
	if err != nil {
		return nil, err
	}
	docs, err := apidoc.ReadSibling(filePath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}

	found := make(map[string]StructInfo, len(structNames))
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if !wanted[ts.Name.Name] {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil, fmt.Errorf("%s: %s is not a struct type", filePath, ts.Name.Name)
			}
			info, err := extractStruct(ts.Name.Name, st, alias, scope, docs[ts.Name.Name])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filePath, err)
			}
			info.File = filePath
			info.Doc = joinDocLines(docs[ts.Name.Name].Type)
			found[info.Name] = info
		}
	}

	out := make([]StructInfo, 0, len(structNames))
	for _, n := range structNames {
		info, ok := found[n]
		if !ok {
			return nil, fmt.Errorf("struct %s not found in %s", n, filePath)
		}
		out = append(out, info)
	}
	return out, nil
}

func structTypeNames(f *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, isStruct := ts.Type.(*ast.StructType); isStruct {
				names[ts.Name.Name] = true
			}
		}
	}
	return names
}

func extractStruct(name string, st *ast.StructType, alias string, scope *typeScope, doc apidoc.Doc) (StructInfo, error) {
	info := StructInfo{Name: name}
	if st.Fields == nil {
		return info, nil
	}
	for _, f := range st.Fields.List {
		jsonName, omitempty, skip := parseJSONTag(f.Tag)
		if len(f.Names) == 0 {
			if !skip {
				info.Dropped = append(info.Dropped, embeddedField(f, jsonName))
			}
			continue
		}
		cfgTag := parseCfgApplyTag(f.Tag)
		docTag := parseDocTag(f.Tag)
		local := false
		if skip {
			if cfgTag.has("local") {
				name := cfgTag.get("local")
				if name == "" {
					return info, fmt.Errorf("field %s: cfgapply:\"local\" requires a key, e.g. local=examples_file", f.Names[0].Name)
				}
				jsonName = name
				omitempty = true
				local = true
			} else {
				continue
			}
		}
		kind, classErr := classifyType(f.Type, scope)
		if cfgTag.has("json-object") && isJSONRawMessage(f.Type) {
			kind = KindJSONObject
			classErr = nil
		}
		if classErr != nil {
			return info, fmt.Errorf("field %s: %w", f.Names[0].Name, classErr)
		}
		goTypeRaw := exprString(f.Type)
		if kind == KindUnsupported {
			info.Dropped = append(info.Dropped, unsupportedFields(f, jsonName, goTypeRaw)...)
			continue
		}
		var passthrough passthroughType
		if kind == KindPassthrough {
			if passthrough, classErr = scope.passthroughType(f.Type); classErr != nil {
				return info, fmt.Errorf("field %s: %w", f.Names[0].Name, classErr)
			}
		}
		validateTag := parseValidateTag(f.Tag)
		desc := joinDocLines(doc.Field(f.Names[0].Name).Text())
		for _, ident := range f.Names {
			if !ident.IsExported() {
				continue
			}
			fld := Field{
				GoName:      ident.Name,
				JSONName:    jsonName,
				OmitEmpty:   omitempty,
				Kind:        kind,
				Description: desc,
				Validate:    validateTag,
				DocRequired: docTag.has("required"),
				DocHidden:   docTag.has("hidden"),
				DocLimits:   docTag.limits(),
				GoTypeRaw:   goTypeRaw,
				Local:       local,
				Default:     cfgTag.get("default"),
				Key:         cfgTag.get("key"),
				Secret:      cfgTag.has("secret"),
				OmitHash:    cfgTag.has("omit-hash") || cfgTag.has("read-only"),
			}
			if fld.JSONName == "" {
				fld.JSONName = ident.Name
			}
			if kind == KindPassthrough {
				fld.GoTypeQualified = qualifyType(f.Type, alias)
				fld.TypeImports = passthrough.imports
				fld.HoldsStructs = passthrough.holdsStructs
			}
			info.Fields = append(info.Fields, fld)
		}
	}
	return info, nil
}

type cfgApplyTag struct {
	values map[string]string
}

func (c cfgApplyTag) has(key string) bool {
	if c.values == nil {
		return false
	}
	_, ok := c.values[key]
	return ok
}

func (c cfgApplyTag) get(key string) string {
	if c.values == nil {
		return ""
	}
	return c.values[key]
}

func (c cfgApplyTag) limits() string {
	var out []string
	for _, key := range []string{"oneof", "min", "max"} {
		if v, ok := c.values[key]; ok {
			out = append(out, key+"="+v)
		}
	}
	return strings.Join(out, ",")
}

func parseCfgApplyTag(tag *ast.BasicLit) cfgApplyTag {
	return parseListTag(tag, "cfgapply")
}

func parseDocTag(tag *ast.BasicLit) cfgApplyTag {
	return parseListTag(tag, "doc")
}

func parseListTag(tag *ast.BasicLit, name string) cfgApplyTag {
	out := cfgApplyTag{values: map[string]string{}}
	if tag == nil {
		return out
	}
	raw := tag.Value
	if len(raw) >= 2 && (raw[0] == '`' || raw[0] == '"') {
		raw = raw[1 : len(raw)-1]
	}
	st := reflect.StructTag(raw)
	v, ok := st.Lookup(name)
	if !ok {
		return out
	}
	for part := range strings.SplitSeq(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, val, found := strings.Cut(part, "="); found {
			out.values[k] = val
		} else {
			out.values[part] = ""
		}
	}
	return out
}

func isJSONRawMessage(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "json" && sel.Sel.Name == "RawMessage"
}

func parseValidateTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	raw := tag.Value
	if len(raw) >= 2 && (raw[0] == '`' || raw[0] == '"') {
		raw = raw[1 : len(raw)-1]
	}
	st := reflect.StructTag(raw)
	v, ok := st.Lookup("validate")
	if !ok {
		return ""
	}
	return v
}

func joinDocLines(text string) string {
	return strings.Join(apidoc.Lines(text), " ")
}

func parseJSONTag(tag *ast.BasicLit) (name string, omitempty bool, skip bool) {
	if tag == nil {
		return "", false, false
	}
	raw := tag.Value
	if len(raw) >= 2 && (raw[0] == '`' || raw[0] == '"') {
		raw = raw[1 : len(raw)-1]
	}
	st := reflect.StructTag(raw)
	jsonTag, ok := st.Lookup("json")
	if !ok {
		return "", false, false
	}
	parts := strings.Split(jsonTag, ",")
	name = parts[0]
	if name == "-" {
		return "", false, true
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

func classifyType(expr ast.Expr, scope *typeScope) (FieldKind, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			return KindString, nil
		case "int":
			return KindInt, nil
		case "int64":
			return KindInt64, nil
		case "bool":
			return KindBool, nil
		case "any":
			return KindAny, nil
		}
		return KindUnsupported, nil
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return KindAny, nil
		}
		return KindUnsupported, nil
	case *ast.SelectorExpr:
		return KindUnsupported, nil
	case *ast.StarExpr:
		if sel, ok := t.X.(*ast.SelectorExpr); ok {
			return classifyExternalPointer(sel, scope)
		}
		ident, ok := t.X.(*ast.Ident)
		if !ok {
			return KindUnsupported, nil
		}
		switch ident.Name {
		case "string":
			return KindStringPtr, nil
		case "int":
			return KindIntPtr, nil
		case "int64":
			return KindInt64Ptr, nil
		case "float64":
			return KindFloat64Ptr, nil
		case "bool":
			return KindBoolPtr, nil
		}
		if scope.localStructs[ident.Name] {
			return KindPassthrough, nil
		}
		return KindUnsupported, nil
	case *ast.ArrayType:
		if t.Len != nil {
			return KindUnsupported, nil
		}
		if ident, ok := t.Elt.(*ast.Ident); ok && ident.Name == "string" {
			return KindStringSlice, nil
		}
		return KindPassthrough, nil
	case *ast.MapType:
		keyIdent, ok := t.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "string" {
			return KindUnsupported, nil
		}
		switch v := t.Value.(type) {
		case *ast.Ident:
			if v.Name == "any" {
				return KindMapStringAny, nil
			}
		case *ast.InterfaceType:
			if v.Methods == nil || len(v.Methods.List) == 0 {
				return KindMapStringAny, nil
			}
		}
		return KindPassthrough, nil
	}
	return KindUnsupported, nil
}

func classifyExternalPointer(sel *ast.SelectorExpr, scope *typeScope) (FieldKind, error) {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return KindUnsupported, nil
	}
	isStruct, err := scope.isExternalStruct(pkg.Name, sel.Sel.Name)
	if err != nil {
		return KindUnsupported, fmt.Errorf("type *%s.%s: %w", pkg.Name, sel.Sel.Name, err)
	}
	if isStruct {
		return KindPassthrough, nil
	}
	return KindUnsupported, nil
}

func unsupportedFields(f *ast.Field, jsonName, goTypeRaw string) []Field {
	var out []Field
	for _, ident := range f.Names {
		if ident.IsExported() {
			out = append(out, Field{GoName: ident.Name, JSONName: cmp.Or(jsonName, ident.Name), GoTypeRaw: goTypeRaw})
		}
	}
	return out
}

func embeddedField(f *ast.Field, jsonName string) Field {
	typeName := exprString(f.Type)
	name := typeName[strings.LastIndexAny(typeName, "*.")+1:]
	return Field{GoName: name, JSONName: cmp.Or(jsonName, name), GoTypeRaw: typeName}
}

func qualifyType(expr ast.Expr, alias string) string {
	switch t := expr.(type) {
	case *ast.Ident:
		if alias == "" || isBuiltinIdent(t.Name) {
			return t.Name
		}
		return alias + "." + t.Name
	case *ast.StarExpr:
		return "*" + qualifyType(t.X, alias)
	case *ast.ArrayType:
		return "[]" + qualifyType(t.Elt, alias)
	case *ast.MapType:
		return "map[" + qualifyType(t.Key, alias) + "]" + qualifyType(t.Value, alias)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "any"
	}
	return exprString(expr)
}

func isBuiltinIdent(name string) bool {
	switch name {
	case "string", "int", "int64", "bool", "any",
		"byte", "rune", "uintptr",
		"int8", "int16", "int32",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64",
		"complex64", "complex128",
		"error":
		return true
	}
	return false
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	}
	return fmt.Sprintf("%T", e)
}
