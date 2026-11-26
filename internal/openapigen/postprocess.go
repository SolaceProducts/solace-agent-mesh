// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	refKey          = "$ref"
	allOfKey        = "allOf"
	schemasKey      = "schemas"
	componentsKey   = "components"
	pathsKey        = "paths"
	parametersKey   = "parameters"
	requiredKey     = "required"
	styleKey        = "style"
	explodeKey      = "explode"
	deprecatedKey   = "deprecated"
	arrayType       = "array"
	formStyle       = "form"
	queryParameter  = "query"
	schemaKey       = "schema"
	typeKey         = "type"
	inKey           = "in"
	mappedTypeError = "mapped type %s cannot be inlined: %v"
)

func (b *builder) finish(raw []byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		b.problem("read generated spec: %v", err)
		return raw
	}
	b.inlineMapped(doc)
	b.dropMappedComponents(doc)
	b.applyForms(doc)
	b.openPublic(doc)
	walk(doc, sortRequired)
	walk(doc, nullableFromTypeArray)
	b.dropPatchNullability(doc)
	walk(doc, singleExampleFromList)
	tidyQueryParameters(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		b.problem("write generated spec: %v", err)
		return raw
	}
	return out
}

func (b *builder) inlineMapped(node any) {
	switch v := node.(type) {
	case map[string]any:
		b.inlineReference(v)
		for _, child := range v {
			b.inlineMapped(child)
		}
	case []any:
		for _, child := range v {
			b.inlineMapped(child)
		}
	}
}

func (b *builder) inlineReference(node map[string]any) {
	if allOf, ok := node[allOfKey].([]any); ok && len(allOf) == 1 {
		if target, ok := allOf[0].(map[string]any); ok && b.isMappedReference(target) {
			delete(node, allOfKey)
			b.mergeMapped(node, target)
			return
		}
	}
	if b.isMappedReference(node) {
		b.mergeMapped(node, node)
	}
}

func (b *builder) isMappedReference(node map[string]any) bool {
	_, mapped := b.mappedName(node)
	return mapped
}

func (b *builder) mappedName(node map[string]any) (string, bool) {
	ref, ok := node[refKey].(string)
	if !ok {
		return "", false
	}
	name := strings.TrimPrefix(ref, componentsPrefix)
	_, mapped := b.inlined[name]
	return name, mapped
}

func (b *builder) mergeMapped(node, reference map[string]any) {
	name, _ := b.mappedName(reference)
	base := map[string]any{}
	raw, err := json.Marshal(b.inlined[name])
	if err == nil {
		err = json.Unmarshal(raw, &base)
	}
	if err != nil {
		b.problem(mappedTypeError, name, err)
		return
	}
	delete(reference, refKey)
	for key, value := range base {
		if _, set := node[key]; !set {
			node[key] = value
		}
	}
}

func (b *builder) dropMappedComponents(doc map[string]any) {
	components, _ := doc[componentsKey].(map[string]any)
	schemas, _ := components[schemasKey].(map[string]any)
	for name := range b.inlined {
		delete(schemas, name)
	}
}

func (b *builder) dropPatchNullability(doc map[string]any) {
	components, _ := doc[componentsKey].(map[string]any)
	schemas, _ := components[schemasKey].(map[string]any)
	for owner, names := range b.notNullable {
		schema, _ := schemas[owner].(map[string]any)
		properties, _ := schema[propertiesKey].(map[string]any)
		for _, name := range names {
			if property, ok := properties[name].(map[string]any); ok {
				delete(property, nullableKey)
			}
		}
	}
}

func nullableFromTypeArray(node map[string]any) {
	types, ok := node[typeKey].([]any)
	if !ok {
		return
	}
	var rest []any
	for _, t := range types {
		if t != "null" {
			rest = append(rest, t)
		}
	}
	if len(rest) == len(types) {
		return
	}
	node[nullableKey] = true
	if len(rest) == 1 {
		node[typeKey] = rest[0]
		return
	}
	delete(node, typeKey)
}

func singleExampleFromList(node map[string]any) {
	examples, ok := node[examplesKey].([]any)
	if !ok || len(examples) != 1 {
		return
	}
	delete(node, examplesKey)
	node[exampleKey] = examples[0]
}

func sortRequired(node map[string]any) {
	required, ok := node[requiredKey].([]any)
	if !ok {
		return
	}
	sort.Slice(required, func(i, j int) bool { return fmt.Sprint(required[i]) < fmt.Sprint(required[j]) })
}

func tidyQueryParameters(doc map[string]any) {
	paths, _ := doc[pathsKey].(map[string]any)
	for _, item := range paths {
		operations, _ := item.(map[string]any)
		for _, op := range operations {
			operation, ok := op.(map[string]any)
			if !ok {
				continue
			}
			params, _ := operation[parametersKey].([]any)
			for _, p := range params {
				if param, ok := p.(map[string]any); ok {
					tidyQueryParameter(param)
				}
			}
		}
	}
}

func tidyQueryParameter(param map[string]any) {
	schema, _ := param[schemaKey].(map[string]any)
	if schema[deprecatedKey] == true {
		param[deprecatedKey] = true
	}
	if param[inKey] != queryParameter || schema[typeKey] != arrayType {
		return
	}
	if _, set := param[styleKey]; set {
		return
	}
	param[styleKey] = formStyle
	param[explodeKey] = false
}

func walk(node any, visit func(map[string]any)) {
	switch v := node.(type) {
	case map[string]any:
		for _, child := range v {
			walk(child, visit)
		}
		visit(v)
	case []any:
		for _, child := range v {
			walk(child, visit)
		}
	}
}

const (
	multipartType    = "multipart/form-data"
	formDataPrefix   = "FormData"
	requestBodyKey   = "requestBody"
	securityKey      = "security"
	contentKey       = "content"
	propertiesKey    = "properties"
	descriptionKey   = "description"
	exampleKey       = "example"
	examplesKey      = "examples"
	formatKey        = "format"
	nameKey          = "name"
	objectType       = "object"
	stringType       = "string"
	requestBodyField = "The request body."
)

func (b *builder) applyForms(doc map[string]any) {
	paths, _ := doc[pathsKey].(map[string]any)
	for _, op := range b.forms {
		item, _ := paths[op.Path].(map[string]any)
		operation, ok := item[strings.ToLower(op.Method)].(map[string]any)
		if !ok {
			b.problem("%s: the form body has no operation to attach to", op.label())
			continue
		}
		operation[requestBodyKey] = b.formBody(op)
		dropFormParameters(operation, op.Form)
	}
	components, _ := doc[componentsKey].(map[string]any)
	schemas, _ := components[schemasKey].(map[string]any)
	for name := range schemas {
		if strings.HasPrefix(name, formDataPrefix) {
			delete(schemas, name)
		}
	}
	for _, op := range b.forms {
		dropFileMarkerComponents(schemas, op.Form)
	}
}

func (b *builder) openPublic(doc map[string]any) {
	paths, _ := doc[pathsKey].(map[string]any)
	for _, op := range b.public {
		item, _ := paths[op.documentedPath()].(map[string]any)
		operation, ok := item[strings.ToLower(op.Method)].(map[string]any)
		if !ok {
			b.problem("%s: the public operation has no operation to open", op.label())
			continue
		}
		operation[securityKey] = []any{}
	}
}

func dropFileMarkerComponents(schemas map[string]any, form *Form) {
	for _, part := range form.Parts {
		if !part.File {
			continue
		}
		if field, ok := form.Type.FieldByName(part.Field); ok {
			delete(schemas, baseTypeName(field.Type))
		}
	}
}

func dropFormParameters(operation map[string]any, form *Form) {
	named := map[string]bool{}
	for _, part := range form.Parts {
		named[part.Name] = true
	}
	params, _ := operation[parametersKey].([]any)
	kept := params[:0]
	for _, p := range params {
		if param, ok := p.(map[string]any); ok && param[inKey] == queryParameter && named[fmt.Sprint(param[nameKey])] {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		delete(operation, parametersKey)
		return
	}
	operation[parametersKey] = kept
}

func (b *builder) formBody(op Operation) map[string]any {
	t := op.Form.Type
	properties := map[string]any{}
	var required []any
	for _, part := range op.Form.Parts {
		field, ok := t.FieldByName(part.Field)
		if !ok {
			b.problem("%s: form part %q names the missing field %s.%s", op.label(), part.Name, t, part.Field)
			continue
		}
		description := b.opts.Docs.Field(t.PkgPath(), baseTypeName(t), part.Field)
		if description == "" {
			b.problem("missing description for field %s.%s", t, part.Field)
		}
		schema := map[string]any{typeKey: stringType, descriptionKey: description}
		if part.File {
			schema[formatKey] = binaryFormat
			required = append(required, part.Name)
		} else if example, tagged := field.Tag.Lookup(exampleKey); tagged {
			schema[exampleKey] = example
		} else {
			b.problem("missing example for field %s.%s", t, part.Field)
		}
		properties[part.Name] = schema
	}
	body := map[string]any{typeKey: objectType, propertiesKey: properties}
	if len(required) > 0 {
		body[requiredKey] = required
	}
	return map[string]any{
		requiredKey:    true,
		descriptionKey: requestBodyField,
		contentKey:     map[string]any{multipartType: map[string]any{schemaKey: body}},
	}
}
