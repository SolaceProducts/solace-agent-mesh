// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
)

// JSONSchema is a simplified JSON Schema representation for tool parameters.
// Exported so that DynamicSchemaFunc implementations can build and return
// alternate parameter schemas via SchemaOverride.Parameters.
type JSONSchema struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
	Required   []string       `json:"required,omitempty"`
}

// BuildSchema returns the JSON schema that the SDK would auto-generate for
// the parameter struct type T. It is intended for use from DynamicSchemaFunc
// implementations that swap the parameter shape based on per-agent tool_config
// (for example a data-access tool that exposes different fields for MongoDB
// vs DynamoDB backends).
//
// If T is not a struct type the returned schema has Type set to "object" and
// no properties. Artifact-typed fields are rendered in the schema exactly as
// they are by NewTool.
func BuildSchema[T any]() *JSONSchema {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return &JSONSchema{Type: "object"}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	schema, _ := schemaFromStruct(t)
	return schema
}

type artifactParamInfo struct {
	IsList      bool   `json:"is_list"`
	IsOptional  bool   `json:"is_optional"`
	FilenameKey string `json:"filename_key,omitempty"`
}

func schemaFromStruct(t reflect.Type) (*JSONSchema, map[string]artifactParamInfo) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return &JSONSchema{Type: "object"}, nil
	}

	props := make(map[string]any)
	var required []string
	artifactParams := make(map[string]artifactParamInfo)

	for i := range t.NumField() {
		addFieldToSchema(t, t.Field(i), props, &required, artifactParams)
	}

	schema := &JSONSchema{
		Type:       "object",
		Properties: props,
		Required:   required,
	}

	return schema, artifactParams
}

func addFieldToSchema(parent reflect.Type, field reflect.StructField,
	props map[string]any, required *[]string, artifactParams map[string]artifactParamInfo,
) {
	if !field.IsExported() {
		return
	}
	name := jsonFieldName(field)
	if name == "-" {
		return
	}

	desc := field.Tag.Get("desc")
	isPointer := field.Type.Kind() == reflect.Pointer
	hasOmitempty := jsonTagHasOmitempty(field)
	isOptional := isPointer || hasOmitempty
	if !isPointer && hasOmitempty && omitemptyAmbiguous(field.Type) {
		warnSignalMismatch(parent, field)
	}

	elemType := field.Type
	if isPointer {
		elemType = field.Type.Elem()
	}

	switch {
	case isArtifactType(elemType):
		addArtifactProp(name, desc, false, isOptional, "", props, artifactParams)
	case elemType.Kind() == reflect.Slice && isArtifactType(elemType.Elem()):
		addArtifactListProp(name, desc, isOptional, props, artifactParams)
	case isStructuredArtifactList(elemType):
		addStructuredArtifactListProp(name, desc, elemType.Elem(), isOptional, props, artifactParams)
	default:
		addRegularProp(name, desc, elemType, props)
	}

	if !isOptional {
		*required = append(*required, name)
	}
}

const artifactFilenameSchemaType = "string"

func addArtifactProp(name, desc string, isList, isOptional bool, filenameKey string,
	props map[string]any, artifactParams map[string]artifactParamInfo,
) {
	prop := map[string]any{"type": artifactFilenameSchemaType}
	if desc != "" {
		prop["description"] = desc
	}
	props[name] = prop
	artifactParams[name] = artifactParamInfo{
		IsList:      isList,
		IsOptional:  isOptional,
		FilenameKey: filenameKey,
	}
}

func addArtifactListProp(name, desc string, isOptional bool,
	props map[string]any, artifactParams map[string]artifactParamInfo,
) {
	prop := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	if desc != "" {
		prop["description"] = desc
	}
	props[name] = prop
	artifactParams[name] = artifactParamInfo{IsList: true, IsOptional: isOptional}
}

func addStructuredArtifactListProp(name, desc string, elemStruct reflect.Type, isOptional bool,
	props map[string]any, artifactParams map[string]artifactParamInfo,
) {
	key, _ := filenameTagField(elemStruct)
	nested, _ := schemaFromStruct(elemStruct)
	items := map[string]any{"type": "object"}
	if len(nested.Properties) > 0 {
		items["properties"] = nested.Properties
	}
	if len(nested.Required) > 0 {
		items["required"] = nested.Required
	}
	prop := map[string]any{"type": "array", "items": items}
	if desc != "" {
		prop["description"] = desc
	}
	props[name] = prop
	artifactParams[name] = artifactParamInfo{
		IsList:      true,
		IsOptional:  isOptional,
		FilenameKey: key,
	}
}

func addRegularProp(name, desc string, elemType reflect.Type, props map[string]any) {
	prop := goTypeToSchema(elemType)
	if desc != "" {
		prop["description"] = desc
	}
	props[name] = prop
}

func isStructuredArtifactList(t reflect.Type) bool {
	if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Struct {
		return false
	}
	_, ok := filenameTagField(t.Elem())
	return ok
}

func goTypeToSchema(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.Slice:
		items := goTypeToSchema(t.Elem())
		return map[string]any{
			"type":  "array",
			"items": items,
		}

	case reflect.Map:
		return map[string]any{"type": "object"}

	case reflect.Struct:
		nested, _ := schemaFromStruct(t)
		result := map[string]any{"type": "object"}
		if len(nested.Properties) > 0 {
			result["properties"] = nested.Properties
		}
		if len(nested.Required) > 0 {
			result["required"] = nested.Required
		}
		return result

	case reflect.Pointer:
		return goTypeToSchema(t.Elem())

	default:
		return map[string]any{"type": "string"}
	}
}

func isArtifactType(t reflect.Type) bool {
	return t == reflect.TypeOf(Artifact{})
}

func filenameTagField(t reflect.Type) (string, bool) {
	if t.Kind() != reflect.Struct {
		return "", false
	}
	var match string
	found := 0
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Type.Kind() != reflect.String {
			continue
		}
		if f.Tag.Get("artifact") != "filename" {
			continue
		}
		match = jsonFieldName(f)
		found++
	}
	if found == 1 {
		return match, true
	}
	return "", false
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}
	return name
}

func jsonTagHasOmitempty(f reflect.StructField) bool {
	tag := f.Tag.Get("json")
	if tag == "" {
		return false
	}
	_, opts, _ := strings.Cut(tag, ",")
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}

var schemaWarnSink atomic.Pointer[io.Writer]

func init() {
	var w io.Writer = os.Stderr
	schemaWarnSink.Store(&w)
}

func setSchemaWarnWriter(w io.Writer) io.Writer {
	old := schemaWarnSink.Load()
	schemaWarnSink.Store(&w)
	if old == nil {
		return nil
	}
	return *old
}

func omitemptyAmbiguous(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Slice, reflect.Map, reflect.Interface:
		return false
	default:
		return true
	}
}

func warnSignalMismatch(parent reflect.Type, f reflect.StructField) {
	w := schemaWarnSink.Load()
	if w == nil || *w == nil {
		return
	}
	_, _ = fmt.Fprintf(*w,
		"samtoolsdk: %s.%s has json `,omitempty` but is non-pointer (%s); "+
			"treating as optional. Use a pointer type (e.g. *%s) and drop "+
			"`,omitempty` to silence this warning, or remove `,omitempty` "+
			"if the field should be required.\n",
		parent.Name(), f.Name, f.Type.String(), f.Type.String(),
	)
}
