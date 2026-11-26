// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
)

type TypeMapper func(t reflect.Type) (*jsonschema.Schema, bool)

type Unwrapper func(t reflect.Type) (inner reflect.Type, ok bool)

const (
	componentsPrefix  = "#/components/schemas/"
	envelopeDataField = "data"
	nullableKey       = "nullable"
	clearableTag      = "clearable"
	nullableTag       = "nullable"
	requiredTag       = "required"
)

func (b *builder) installHooks() {
	reflector := b.reflector.JSONSchemaReflector()
	reflector.DefaultOptions = append(reflector.DefaultOptions,
		jsonschema.InterceptDefName(func(t reflect.Type, _ string) string { return b.claimName(t) }),
		jsonschema.InterceptNullability(b.nullability),
		jsonschema.InterceptSchema(func(p jsonschema.InterceptSchemaParams) (bool, error) {
			return b.interceptSchema(p), nil
		}),
		jsonschema.InterceptProp(func(p jsonschema.InterceptPropParams) error {
			b.interceptProp(p)
			return nil
		}),
	)
}

func (b *builder) claimName(t reflect.Type) string {
	name := componentName(t)
	if owner, seen := b.owners[name]; seen && owner != t {
		b.problem("schema name %q is produced by both %s and %s", name, owner, t)
	}
	b.owners[name] = t
	return name
}

func (b *builder) interceptSchema(p jsonschema.InterceptSchemaParams) (stop bool) {
	if !p.Value.IsValid() {
		return false
	}
	t := p.Value.Type()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if !p.Processed {
		b.parents[p.Schema] = t
		if mapped := b.mapped(t, p.Context); mapped != nil {
			b.inlined[componentName(t)] = *mapped
			*p.Schema = *mapped
			return true
		}
		switch {
		case t.Kind() == reflect.Map && t.Key().Kind() != reflect.String:
			b.problem("map key of %s must be a string", t)
		case t.Kind() == reflect.Struct && t.Name() == "" && !b.synthetic[t]:
			b.problem("anonymous struct type %s cannot be a component", t)
		}
		return false
	}
	switch t.Kind() {
	case reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		p.Schema.WithFormat("int32")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int64, reflect.Uint, reflect.Uint64:
		p.Schema.WithFormat("int64")
	case reflect.Struct:
		if p.Schema.HasType(jsonschema.Object) && t.Name() != "" {
			b.describeType(p.Schema, t)
		}
	}
	return false
}

func (b *builder) mapped(t reflect.Type, rc *jsonschema.ReflectContext) *jsonschema.Schema {
	if b.opts.Mapper != nil {
		if schema, ok := b.opts.Mapper(t); ok {
			b.nullableTypes[t] = schema.ExtraProperties[nullableKey] == true
			return schema
		}
	}
	if b.opts.Unwrap == nil {
		return nil
	}
	inner, ok := b.opts.Unwrap(t)
	if !ok {
		return nil
	}
	b.nullableTypes[t] = true
	return b.nullableSchemaOf(t, inner, rc)
}

func (b *builder) nullableSchemaOf(wrapper, inner reflect.Type, rc *jsonschema.ReflectContext) *jsonschema.Schema {
	inOperation := func(nested *jsonschema.ReflectContext) { nested.Context = rc.Context }
	schema, err := b.reflector.JSONSchemaReflector().Reflect(reflect.New(inner).Interface(), jsonschema.InlineRefs, inOperation)
	if err != nil {
		b.problem("%s: %v", wrapper, err)
		return new(jsonschema.Schema)
	}
	schema.WithExtraPropertiesItem(nullableKey, true)
	return &schema
}

func (b *builder) mappedByMapper(t reflect.Type) *jsonschema.Schema {
	if b.opts.Mapper == nil {
		return nil
	}
	schema, ok := b.opts.Mapper(t)
	if !ok {
		return nil
	}
	return schema
}

func (b *builder) describeType(schema *jsonschema.Schema, t reflect.Type) {
	description := b.opts.Docs.Type(t.PkgPath(), baseTypeName(t))
	if description == "" {
		b.problem("missing description for type %s", t)
	}
	schema.WithDescription(description)
}

func (b *builder) interceptProp(p jsonschema.InterceptPropParams) {
	if !p.Processed {
		return
	}
	parent, ok := b.parents[p.ParentSchema]
	if !ok {
		return
	}
	state := operationState(p.Context)
	if b.isRequired(p.Field, state) {
		p.ParentSchema.Required = appendUnique(p.ParentSchema.Required, p.Name)
	}
	if b.synthetic[parent] {
		return
	}
	if p.Name == envelopeDataField && p.PropertySchema.Ref != nil {
		delete(p.PropertySchema.ExtraProperties, nullableKey)
		return
	}
	owner := declaringStruct(parent, p.Field.Name)
	description := b.opts.Docs.Field(owner.PkgPath(), baseTypeName(owner), p.Field.Name)
	if description == "" {
		b.problem("missing description for field %s.%s", owner, p.Field.Name)
	}
	_, isPathParam := p.Field.Tag.Lookup("path")
	if !isPathParam && !declaresExample(p) {
		b.problem("missing example for field %s.%s", owner, p.Field.Name)
	}
	deprecated, err := deprecation(fmt.Sprintf("field %s.%s", owner, p.Field.Name), description)
	if err != nil {
		b.problems = append(b.problems, err)
	}
	b.decodeMappedExample(p)
	b.allowDocumentedNull(p, owner)
	b.narrowPatchNullability(p)
	rejectNullWhereRequestRequiresValue(p, state)
	p.PropertySchema.WithDescription(description)
	if isPathParam {
		narrowPathParam(p.PropertySchema, p.Field)
	}
	if deprecated {
		p.PropertySchema.WithDeprecated(true)
	}
	wrapReference(p.PropertySchema)
}

func rejectNullWhereRequestRequiresValue(p jsonschema.InterceptPropParams, state reflectState) {
	requestRequires := validateRequires(p.Field) || p.Field.Tag.Get(requiredTag) == "true"
	clearable := p.Field.Tag.Get(clearableTag) == "true"
	if !state.known || state.isResponse || state.in != openapi.InBody || !requestRequires || clearable {
		return
	}
	p.PropertySchema.RemoveType(jsonschema.Null)
	delete(p.PropertySchema.ExtraProperties, nullableKey)
}

func (b *builder) allowDocumentedNull(p jsonschema.InterceptPropParams, owner reflect.Type) {
	if p.Field.Tag.Get(nullableTag) != "true" {
		return
	}
	switch {
	case p.PropertySchema.Ref != nil:
		p.PropertySchema.WithExtraPropertiesItem(nullableKey, true)
	case p.PropertySchema.Type == nil:
		b.problem("field %s.%s: an open value already admits null; drop the nullable tag", owner, p.Field.Name)
	default:
		p.PropertySchema.AddType(jsonschema.Null)
	}
}

func (b *builder) narrowPatchNullability(p jsonschema.InterceptPropParams) {
	if b.opts.Unwrap == nil || p.Field.Tag.Get(clearableTag) == "true" {
		return
	}
	t := p.Field.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if _, patch := b.opts.Unwrap(t); patch {
		owner := componentName(b.parents[p.ParentSchema])
		b.notNullable[owner] = append(b.notNullable[owner], p.Name)
	}
}

func (b *builder) decodeMappedExample(p jsonschema.InterceptPropParams) {
	t := p.Field.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if b.mappedByMapper(t) == nil {
		return
	}
	tag, ok := p.Field.Tag.Lookup("example")
	if !ok {
		return
	}
	var example any
	if err := json.Unmarshal([]byte(tag), &example); err != nil {
		b.problem("example for field %s is not JSON: %v", p.Field.Name, err)
		return
	}
	p.PropertySchema.Examples = []any{example}
}

func declaresExample(p jsonschema.InterceptPropParams) bool {
	schema := p.PropertySchema
	if _, tagged := p.Field.Tag.Lookup("example"); tagged {
		return true
	}
	if len(schema.Examples) > 0 || schema.ExtraProperties["example"] != nil {
		return true
	}
	return pointsAtSchema(schema) || (schema.Items != nil && schema.Items.SchemaOrBool != nil &&
		schema.Items.SchemaOrBool.TypeObject != nil && pointsAtSchema(schema.Items.SchemaOrBool.TypeObject))
}

func pointsAtSchema(s *jsonschema.Schema) bool {
	return s.Ref != nil || len(s.AllOf) > 0
}

func wrapReference(s *jsonschema.Schema) {
	if s.Ref == nil {
		return
	}
	ref := *s.Ref
	s.Ref = nil
	s.AllOf = []jsonschema.SchemaOrBool{new(jsonschema.Schema).WithRef(ref).ToSchemaOrBool()}
	if s.ExtraProperties[nullableKey] == true {
		delete(s.ExtraProperties, nullableKey)
		s.AddType(jsonschema.Null)
	}
}

type reflectState struct {
	known      bool
	isResponse bool
	in         openapi.In
}

func operationState(rc *jsonschema.ReflectContext) reflectState {
	oc, ok := openapi.OperationCtx(rc)
	if !ok {
		return reflectState{}
	}
	return reflectState{known: true, isResponse: oc.IsProcessingResponse(), in: oc.ProcessingIn()}
}

func (b *builder) isRequired(field reflect.StructField, state reflectState) bool {
	switch {
	case state.known && !state.isResponse && state.in != openapi.InBody:
		return false
	case state.known && !state.isResponse:
		return validateRequires(field)
	}
	_, omitEmpty := jsonTag(field)
	return !omitEmpty
}

func (b *builder) nullability(p jsonschema.InterceptNullabilityParams) {
	*p.Schema = p.OrigSchema
	if p.Type.Kind() != reflect.Pointer {
		if !b.nullableTypes[p.Type] {
			p.Schema.RemoveType(jsonschema.Null)
		}
		return
	}
	if p.Schema.Ref != nil {
		p.Schema.WithExtraPropertiesItem(nullableKey, true)
		return
	}
	p.Schema.AddType(jsonschema.Null)
}

func validateRequires(field reflect.StructField) bool {
	for _, rule := range strings.Split(field.Tag.Get("validate"), ",") {
		if rule == "required" {
			return true
		}
	}
	return false
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

func declaringStruct(t reflect.Type, fieldName string) reflect.Type {
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.Name == fieldName && !f.Anonymous {
			return t
		}
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous || f.Type.Kind() != reflect.Struct {
			continue
		}
		if found := declaringStruct(f.Type, fieldName); found != nil {
			return found
		}
	}
	return nil
}

func jsonTag(f reflect.StructField) (name string, omitEmpty bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		if f.Anonymous {
			return "", false
		}
		return f.Name, false
	}
	if tag == "-" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" && !f.Anonymous {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" || opt == "omitzero" {
			omitEmpty = true
		}
	}
	return name, omitEmpty
}

func baseTypeName(t reflect.Type) string {
	name := t.Name()
	if i := strings.IndexByte(name, '['); i >= 0 {
		return name[:i]
	}
	return name
}

func componentName(t reflect.Type) string {
	return sanitizeName(rawComponentName(t))
}

func sanitizeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '.' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, name)
}

func rawComponentName(t reflect.Type) string {
	name := t.Name()
	open := strings.IndexByte(name, '[')
	if open < 0 {
		return name
	}
	args := strings.TrimSuffix(name[open+1:], "]")
	var b strings.Builder
	b.WriteString(name[:open])
	for _, arg := range strings.Split(args, ",") {
		arg = arg[strings.LastIndexAny(arg, "./")+1:]
		b.WriteString(strings.ToUpper(arg[:1]) + arg[1:])
	}
	return b.String()
}

const (
	keyOption       = "key"
	foldedKeyOption = "foldedkey"
	builtinOption   = "builtin"
	patternOption   = "pattern"
	textOption      = "text"
	keyPattern      = "^[a-z0-9]+(_[a-z0-9]+)*$"
	foldedPattern   = "^[A-Za-z0-9]+(_[A-Za-z0-9]+)*$"
	builtinPattern  = "^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9]+([_-][a-z0-9]+)*)$"
	uuidFormat      = "uuid"
)

func narrowPathParam(schema *jsonschema.Schema, field reflect.StructField) {
	if field.Type.Kind() != reflect.String {
		return
	}
	_, option, _ := strings.Cut(field.Tag.Get("path"), ",")
	switch option {
	case keyOption:
		schema.WithPattern(keyPattern)
	case foldedKeyOption:
		schema.WithPattern(foldedPattern)
	case builtinOption:
		schema.WithPattern(builtinPattern)
	case patternOption:
		schema.WithPattern(field.Tag.Get("pattern"))
	case textOption:
	default:
		schema.WithFormat(uuidFormat)
	}
}
