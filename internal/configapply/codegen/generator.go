// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"go/format"
	"sort"
	"text/template"
)

type Spec struct {
	Kind           string
	Request        StructInfo
	Response       StructInfo
	DTOImportPath  string
	DTOImportAlias string
	NullIsAbsent   bool
}

func Generate(packageName string, specs ...Spec) ([]byte, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("at least one spec is required")
	}
	if err := checkDroppedFields(specs); err != nil {
		return nil, err
	}
	if err := checkAuthoringKeys(specs); err != nil {
		return nil, err
	}

	tmpl, err := template.New("file").Funcs(template.FuncMap{
		"forwardField":  forwardFieldStmt,
		"reverseField":  reverseFieldStmt,
		"reverseFields": reverseFields,
		"qualifiedType": qualifiedType,
		"hooksTypeName": hooksTypeName,
		"forwardFnName": func(s Spec) string { return "Forward" + s.Kind },
		"reverseFnName": func(s Spec) string { return "Reverse" + s.Kind },
	}).Parse(fileTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	imports, err := collectImports(specs)
	if err != nil {
		return nil, err
	}
	data := struct {
		Package string
		Imports []importEntry
		Specs   []Spec
	}{
		Package: packageName,
		Imports: imports,
		Specs:   specs,
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %w\n--- source ---\n%s", err, buf.String())
	}
	return formatted, nil
}

type importEntry struct {
	Alias string
	Path  string
}

func collectImports(specs []Spec) ([]importEntry, error) {
	byName := make(map[string]importEntry)
	add := func(entry importEntry) error {
		name := cmp.Or(entry.Alias, defaultImportName(entry.Path))
		if prev, ok := byName[name]; ok {
			if prev.Path != entry.Path {
				return fmt.Errorf("import name %s refers to both %s and %s; give one of them a different name in the DTO file", name, prev.Path, entry.Path)
			}
			return nil
		}
		byName[name] = entry
		return nil
	}
	for _, s := range specs {
		if s.DTOImportPath != "" {
			if err := add(importEntry{Alias: s.DTOImportAlias, Path: s.DTOImportPath}); err != nil {
				return nil, fmt.Errorf("%s: %w", s.Kind, err)
			}
		}
		for _, f := range s.Request.Fields {
			if f.Local {
				continue
			}
			for name, importPath := range f.TypeImports {
				entry := importEntry{Alias: name, Path: importPath}
				if name == defaultImportName(importPath) {
					entry.Alias = ""
				}
				if err := add(entry); err != nil {
					return nil, fmt.Errorf("%s: field %s.%s in %s: %w", s.Kind, s.Request.Name, f.GoName, s.Request.File, err)
				}
			}
		}
	}
	out := make([]importEntry, 0, len(byName))
	for _, entry := range byName {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func checkDroppedFields(specs []Spec) error {
	var errs []error
	for _, s := range specs {
		requestJSON := make(map[string]bool, len(s.Request.Fields)+len(s.Request.Dropped))
		for _, f := range s.Request.Fields {
			requestJSON[f.JSONName] = true
		}
		for _, f := range s.Request.Dropped {
			requestJSON[f.JSONName] = true
			errs = append(errs, droppedFieldError(s.Kind, s.Request, f, "sam config would never send it"))
		}
		responseJSON := make(map[string]bool, len(s.Response.Fields)+len(s.Response.Dropped))
		for _, f := range s.Response.Fields {
			responseJSON[f.JSONName] = true
		}
		for _, f := range s.Response.Dropped {
			responseJSON[f.JSONName] = true
			if requestJSON[f.JSONName] {
				errs = append(errs, droppedFieldError(s.Kind, s.Response, f, "sam config would never read it back, so every plan would show a change"))
			}
		}
		for _, f := range s.Request.Fields {
			if !f.Local && !f.Secret && !f.OmitHash && !responseJSON[f.JSONName] {
				errs = append(errs, fmt.Errorf("%s: field %s.%s (json %q) in %s has no field with that JSON name on %s, so sam config would never read it back and every plan would show a change. Add it to %s, or tag it cfgapply:\"secret\" or cfgapply:\"omit-hash\" if the platform never returns it",
					s.Kind, s.Request.Name, f.GoName, f.JSONName, sourceFile(s.Request), s.Response.Name, s.Response.Name))
			}
		}
	}
	return errors.Join(errs...)
}

func checkAuthoringKeys(specs []Spec) error {
	var errs []error
	for _, s := range specs {
		byKey := map[string]Field{}
		for _, f := range s.Request.Fields {
			key := f.AuthoringKey()
			if prev, ok := byKey[key]; ok {
				errs = append(errs, fmt.Errorf("%s: fields %s.%s (json %q) and %s.%s (json %q) in %s both have the YAML key %q. Give one of them a different cfgapply key= tag",
					s.Kind, s.Request.Name, prev.GoName, prev.JSONName, s.Request.Name, f.GoName, f.JSONName, sourceFile(s.Request), key))
				continue
			}
			byKey[key] = f
		}
	}
	return errors.Join(errs...)
}

func droppedFieldError(kind string, st StructInfo, f Field, consequence string) error {
	return fmt.Errorf("%s: field %s.%s (json %q, type %s) in %s has a type the generator cannot translate, so %s. Change the field to a supported type or extend classifyType in internal/configapply/codegen/parser.go",
		kind, st.Name, f.GoName, f.JSONName, f.GoTypeRaw, sourceFile(st), consequence)
}

func sourceFile(st StructInfo) string {
	return cmp.Or(st.File, "an unknown file")
}

func qualifiedType(s Spec, typeName string) string {
	if s.DTOImportAlias == "" {
		return typeName
	}
	return s.DTOImportAlias + "." + typeName
}

func hooksTypeName(s Spec) string { return s.Kind + "Hooks" }

func reverseFields(s Spec) []reverseField {
	requestByJSON := make(map[string]Field, len(s.Request.Fields))
	for _, f := range s.Request.Fields {
		if f.Local {
			continue
		}
		requestByJSON[f.JSONName] = f
	}
	out := make([]reverseField, 0, len(s.Response.Fields))
	for _, rf := range s.Response.Fields {
		req, ok := requestByJSON[rf.JSONName]
		if !ok {
			continue
		}
		out = append(out, reverseField{
			ResponseField: rf,
			RequestField:  req,
			NullIsAbsent:  s.NullIsAbsent,
		})
	}
	return out
}

type reverseField struct {
	ResponseField Field
	RequestField  Field
	NullIsAbsent  bool
}

func forwardFieldStmt(f Field) string {
	key := f.AuthoringKey()
	switch f.Kind {
	case KindString:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	s, err := coerceString(%q, v)
	if err != nil { return out, err }
	out.%s = s
}`, key, key, f.GoName)
	case KindInt:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	n, err := coerceInt(%q, v)
	if err != nil { return out, err }
	out.%s = n
}`, key, key, f.GoName)
	case KindInt64:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	n, err := coerceInt64(%q, v)
	if err != nil { return out, err }
	out.%s = n
}`, key, key, f.GoName)
	case KindBool:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	b, err := coerceBool(%q, v)
	if err != nil { return out, err }
	out.%s = b
}`, key, key, f.GoName)
	case KindStringPtr:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	s, err := coerceString(%q, v)
	if err != nil { return out, err }
	out.%s = &s
}`, key, key, f.GoName)
	case KindIntPtr:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	n, err := coerceInt(%q, v)
	if err != nil { return out, err }
	out.%s = &n
}`, key, key, f.GoName)
	case KindInt64Ptr:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	n, err := coerceInt64(%q, v)
	if err != nil { return out, err }
	out.%s = &n
}`, key, key, f.GoName)
	case KindFloat64Ptr:
		return fmt.Sprintf(`if v, ok := in[%q]; ok && v != nil {
	n, err := coerceFloat64(%q, v)
	if err != nil { return out, err }
	out.%s = &n
}`, key, key, f.GoName)
	case KindBoolPtr:
		return fmt.Sprintf(`if v, ok := in[%q]; ok && v != nil {
	b, err := coerceBool(%q, v)
	if err != nil { return out, err }
	out.%s = &b
}`, key, key, f.GoName)
	case KindStringSlice:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	xs, err := coerceStringSlice(%q, v)
	if err != nil { return out, err }
	out.%s = xs
}`, key, key, f.GoName)
	case KindMapStringAny:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	m, err := coerceMap(%q, v)
	if err != nil { return out, err }
	out.%s = m
}`, key, key, f.GoName)
	case KindAny:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	out.%s = v
}`, key, f.GoName)
	case KindPassthrough:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
	var typed %s
	if err := coerceJSON(%q, v, &typed); err != nil { return out, err }
	out.%s = typed
}`, key, f.GoTypeQualified, key, f.GoName)
	case KindJSONObject:
		return fmt.Sprintf(`if v, ok := in[%q]; ok {
		raw, err := coerceJSONObject(%q, v)
		if err != nil { return out, err }
		out.%s = raw
	}`, key, key, f.GoName)
	}
	return fmt.Sprintf("// SKIPPED unsupported field %s (%s)", f.GoName, f.Kind)
}

func reverseFieldStmt(rf reverseField) string {
	resp := rf.ResponseField
	omit := rf.RequestField.OmitEmpty || rf.NullIsAbsent
	key := rf.RequestField.AuthoringKey()
	goName := resp.GoName

	switch resp.Kind {
	case KindString:
		if def := cmp.Or(resp.Default, rf.RequestField.Default); def != "" {
			return fmt.Sprintf(`if in.%s != "" && in.%s != %q {
	out[%q] = in.%s
}`, goName, goName, def, key, goName)
		}
		if omit {
			return fmt.Sprintf(`if in.%s != "" {
	out[%q] = in.%s
}`, goName, key, goName)
		}
		return fmt.Sprintf(`out[%q] = in.%s`, key, goName)
	case KindInt, KindInt64:
		if omit {
			return fmt.Sprintf(`if in.%s != 0 {
	out[%q] = in.%s
}`, goName, key, goName)
		}
		return fmt.Sprintf(`out[%q] = in.%s`, key, goName)
	case KindBool:
		if omit {
			return fmt.Sprintf(`if in.%s {
	out[%q] = in.%s
}`, goName, key, goName)
		}
		return fmt.Sprintf(`out[%q] = in.%s`, key, goName)
	case KindStringPtr, KindIntPtr, KindInt64Ptr, KindFloat64Ptr, KindBoolPtr:
		return fmt.Sprintf(`if in.%s != nil {
	out[%q] = *in.%s
}`, goName, key, goName)
	case KindStringSlice, KindMapStringAny:
		if omit {
			return fmt.Sprintf(`if len(in.%s) > 0 {
	out[%q] = in.%s
}`, goName, key, goName)
		}
		return fmt.Sprintf(`out[%q] = in.%s`, key, goName)
	case KindAny:
		if omit {
			return fmt.Sprintf(`if in.%s != nil {
	out[%q] = in.%s
}`, goName, key, goName)
		}
		return fmt.Sprintf(`out[%q] = in.%s`, key, goName)
	case KindPassthrough:
		dropEmpty := ""
		if rf.NullIsAbsent && resp.HoldsStructs {
			dropEmpty = fmt.Sprintf("\n\tany%s = withoutEmptyMembers(any%s)", goName, goName)
		}
		return fmt.Sprintf(`{
	var any%s any
	_ = reverseJSON(in.%s, &any%s)%s
	if !isZeroAny(any%s) {
		out[%q] = any%s
	}
}`, goName, goName, goName, dropEmpty, goName, key, goName)
	case KindJSONObject:
		return fmt.Sprintf(`{
		var any%s any
		_ = reverseJSONObject(in.%s, &any%s)
		if !isZeroAny(any%s) {
			out[%q] = any%s
		}
	}`, goName, goName, goName, goName, key, goName)
	}
	return fmt.Sprintf("// SKIPPED unsupported field %s (%s)", goName, resp.Kind)
}

const fileTemplate = `// Code generated by configapply codegen. DO NOT EDIT.
//
// Regenerate with: go generate ./internal/configapply/...

package {{.Package}}

import (
{{range .Imports}}	{{if .Alias}}{{.Alias}} {{end}}"{{.Path}}"
{{end}})

{{range .Specs}}
// {{forwardFnName .}} translates a clean-spec map (keys = authoring keys: a DTO field's JSON name, or its cfgapply key= tag)
// into a {{qualifiedType . .Request.Name}}. The hooks parameter is reserved
// for cross-resource resolution; for this kind it is currently unused.
func {{forwardFnName .}}(in map[string]any, hooks {{hooksTypeName .}}) ({{qualifiedType . .Request.Name}}, error) {
	_ = hooks
	var out {{qualifiedType . .Request.Name}}
{{range .Request.Fields}}{{if not .Local}}
	{{forwardField .}}
{{end}}{{end}}
	return out, nil
}

// {{reverseFnName .}} produces a clean-spec map from a {{qualifiedType . .Response.Name}}.
// Only fields that exist on the matching request DTO are emitted, so server-
// managed fields (id, createdBy, updatedTime, etc.) do not appear in the
// output. omitempty semantics on the request DTO drive whether zero values
// are included.
func {{reverseFnName .}}(in {{qualifiedType . .Response.Name}}, hooks {{hooksTypeName .}}) (map[string]any, error) {
	_ = hooks
	out := map[string]any{}
{{range reverseFields .}}
	{{reverseField .}}
{{end}}
	return out, nil
}
{{end}}
`
