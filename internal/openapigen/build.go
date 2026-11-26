// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"

	kin "github.com/getkin/kin-openapi/openapi3"
	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi3"
)

const (
	openAPIVersion   = "3.0.3"
	requestBodyText  = "The request body."
	rejectedWithText = "%s: %w"
)

type Options struct {
	Title           string
	Description     string
	Version         string
	ContactName     string
	ContactURL      string
	ServerURLs      []string
	Docs            DocSource
	Mapper          TypeMapper
	Unwrap          Unwrapper
	TagDescriptions map[string]string
	TagGroups       map[string]string
	ErrorType       reflect.Type
	AuthErrorType   reflect.Type
}

type Specs struct {
	Full   *kin.T
	Public *kin.T
}

var errorStatuses = []int{
	http.StatusBadRequest,
	http.StatusUnauthorized,
	http.StatusForbidden,
	http.StatusNotFound,
	http.StatusUnprocessableEntity,
	http.StatusInternalServerError,
}

func errorStatusesFor(op Operation) []int {
	statuses := append([]int{}, errorStatuses...)
	if op.Access.Public {
		statuses = slices.DeleteFunc(statuses, isAuthRefusal)
	}
	if op.Method != http.MethodGet {
		statuses = append(statuses, http.StatusConflict)
	}
	for _, extra := range op.ExtraErrors {
		if !slices.Contains(statuses, extra) {
			statuses = append(statuses, extra)
		}
	}
	sort.Ints(statuses)
	return statuses
}

func Build(ops []Operation, opts Options) (*Specs, error) {
	if opts.Docs == nil {
		return nil, errors.New("options: Docs is required")
	}
	var published []Operation
	for _, op := range ops {
		if op.Published {
			published = append(published, op)
		}
	}
	full, fullErr := buildDoc(ops, opts)
	public, publicErr := buildDoc(published, opts)
	if err := errors.Join(fullErr, publicErr); err != nil {
		return nil, dedupe(err)
	}
	if err := errors.Join(Validate(full), Validate(public)); err != nil {
		return nil, err
	}
	return &Specs{Full: full, Public: public}, nil
}

func dedupe(err error) error {
	seen := map[string]bool{}
	var unique []error
	for _, line := range strings.Split(err.Error(), "\n") {
		if line != "" && !seen[line] {
			seen[line] = true
			unique = append(unique, errors.New(line))
		}
	}
	return errors.Join(unique...)
}

type builder struct {
	opts          Options
	reflector     *openapi3.Reflector
	problems      []error
	owners        map[string]reflect.Type
	parents       map[*jsonschema.Schema]reflect.Type
	inlined       map[string]jsonschema.Schema
	synthetic     map[reflect.Type]bool
	nullableTypes map[reflect.Type]bool
	forms         []Operation
	public        []Operation
	notNullable   map[string][]string
}

func newBuilder(opts Options) *builder {
	b := &builder{
		opts:      opts,
		reflector: openapi3.NewReflector(),
		owners:    map[string]reflect.Type{},
		parents:   map[*jsonschema.Schema]reflect.Type{},
		inlined:   map[string]jsonschema.Schema{},
		synthetic: map[reflect.Type]bool{},

		nullableTypes: map[reflect.Type]bool{},
		notNullable:   map[string][]string{},
	}
	b.installHooks()
	b.describeAPI()
	return b
}

func (b *builder) problem(format string, args ...any) {
	b.problems = append(b.problems, fmt.Errorf(format, args...))
}

func (b *builder) describeAPI() {
	spec := b.reflector.SpecEns()
	spec.Openapi = openAPIVersion
	spec.Info.Title = b.opts.Title
	spec.Info.Version = b.opts.Version
	if b.opts.Description != "" {
		spec.Info.Description = &b.opts.Description
	}
	if b.opts.ContactName != "" || b.opts.ContactURL != "" {
		spec.Info.Contact = &openapi3.Contact{Name: &b.opts.ContactName, URL: &b.opts.ContactURL}
	}
	for _, url := range b.opts.ServerURLs {
		spec.Servers = append(spec.Servers, openapi3.Server{URL: url})
	}
	spec.SetHTTPBearerTokenSecurity(bearerSchemeName, "", bearerSchemeDescription)
	spec.Security = []map[string][]string{{bearerSchemeName: {}}}
}

func buildDoc(ops []Operation, opts Options) (*kin.T, error) {
	b := newBuilder(opts)
	ordered := append([]Operation(nil), ops...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Method < ordered[j].Method
	})

	ids := map[string]string{}
	for _, op := range ordered {
		b.addOperation(op)
		if op.OperationID == "" {
			continue
		}
		if other, dup := ids[op.OperationID]; dup {
			b.problem("operation id %q is used by both %s and %s", op.OperationID, other, op.label())
		}
		ids[op.OperationID] = op.label()
	}
	b.declareTags(ordered)
	if len(b.problems) > 0 {
		return nil, errors.Join(b.problems...)
	}
	return b.document()
}

func (b *builder) document() (*kin.T, error) {
	raw, err := b.reflector.Spec.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal generated spec: %w", err)
	}
	raw = b.finish(raw)
	if len(b.problems) > 0 {
		return nil, errors.Join(b.problems...)
	}
	doc, err := kin.NewLoader().LoadFromData(raw)
	if err != nil {
		return nil, fmt.Errorf("load generated spec: %w", err)
	}
	return doc, nil
}

func (b *builder) addOperation(op Operation) {
	if err := checkOperation(op); err != nil {
		b.problems = append(b.problems, err)
	}
	deprecated, err := deprecation(op.label(), op.Description)
	if err != nil {
		b.problems = append(b.problems, err)
	}
	oc, err := b.reflector.NewOperationContext(op.Method, op.documentedPath())
	if err != nil {
		b.problem(rejectedWithText, op.label(), err)
		return
	}
	oc.SetID(op.OperationID)
	oc.SetSummary(op.Summary)
	oc.SetDescription(op.Description + "\n\n" + accessParagraph(op.Access))
	oc.SetTags(op.Tags...)
	oc.SetIsDeprecated(deprecated)
	b.addExtensions(oc, op)
	if op.Access.Public {
		b.public = append(b.public, op)
	}
	b.addRequest(oc, op)
	b.addResponses(oc, op)
	if err := b.reflector.AddOperation(oc); err != nil {
		b.problem(rejectedWithText, op.label(), err)
	}
}

func checkOperation(op Operation) error {
	if err := op.Stability.validate(); err != nil {
		return fmt.Errorf(rejectedWithText, op.label(), err)
	}
	switch {
	case op.Published && op.Stability == StabilityDraft:
		return fmt.Errorf("%s: a published operation cannot be draft", op.label())
	case op.OperationID == "":
		return fmt.Errorf("%s: no OperationID", op.label())
	case op.Summary == "":
		return fmt.Errorf("%s: no Summary", op.label())
	case op.Description == "":
		return fmt.Errorf("%s: no Description", op.label())
	case len(op.Tags) == 0:
		return fmt.Errorf("%s: at least one tag is required", op.label())
	}
	if err := checkBodies(op); err != nil {
		return err
	}
	return checkAccess(op)
}

func checkBodies(op Operation) error {
	for _, t := range []reflect.Type{op.Query, op.Request} {
		if t == nil {
			continue
		}
		for _, f := range reflect.VisibleFields(t) {
			if _, isForm := f.Tag.Lookup("form"); isForm && op.Form == nil {
				return fmt.Errorf("%s: %s.%s is a form part; describe the body with Form", op.label(), t, f.Name)
			}
			if isRawField(f) && op.RawBody == nil {
				return fmt.Errorf("%s: %s.%s is a raw body; describe it with RawBody", op.label(), t, f.Name)
			}
		}
	}
	return nil
}

func isRawField(f reflect.StructField) bool {
	if !f.IsExported() || f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() != reflect.Uint8 {
		return false
	}
	for _, tag := range []string{"query", "path", "header", "form"} {
		if _, tagged := f.Tag.Lookup(tag); tagged {
			return false
		}
	}
	return f.Tag.Get("json") == "-"
}

type operationAccess interface {
	Operation() *openapi3.Operation
}

func (b *builder) addExtensions(oc openapi.OperationContext, op Operation) {
	access, ok := oc.(operationAccess)
	if !ok {
		b.problem("%s: the library no longer exposes the operation for extensions", op.label())
		return
	}
	built := access.Operation()
	built.WithMapOfAnythingItem(stabilityExtension, string(op.Stability))
}

func (b *builder) addRequest(oc openapi.OperationContext, op Operation) {
	if params := b.pathParams(op.Path, declaredPathParams(op)); params != nil {
		oc.AddReqStructure(params)
	}
	var body []openapi.ContentOption
	if op.Request != nil {
		body = []openapi.ContentOption{openapi.WithCustomize(requireBody)}
	}
	if op.Query != nil {
		b.checkQueryFields(op.Query)
		options := []openapi.ContentOption(nil)
		if op.Query == op.Request {
			options = body
		}
		oc.AddReqStructure(valueOf(op.Query), options...)
	}
	if op.Page != nil {
		oc.AddReqStructure(b.pageParams(*op.Page))
	}
	if len(op.SortFields) > 0 {
		oc.AddReqStructure(b.sortParams(op.SortFields))
	}
	if op.Request != nil && op.Request != op.Query {
		oc.AddReqStructure(valueOf(op.Request), body...)
	}
	if op.Form != nil {
		b.forms = append(b.forms, op)
	}
	if op.RawBody != nil {
		oc.AddReqStructure(nil, rawContent(op.RawBody.ContentType, binaryFormat), openapi.WithCustomize(describeBody(op.RawBody.Description)))
	}
}

const binaryFormat = "binary"

func rawContent(contentType, format string) openapi.ContentOption {
	return func(cu *openapi.ContentUnit) {
		cu.ContentType = contentType
		cu.Format = format
	}
}

func describeBody(text string) func(openapi.ContentOrReference) {
	return func(content openapi.ContentOrReference) {
		if body, ok := content.(*openapi3.RequestBodyOrRef); ok && body.RequestBody != nil {
			body.RequestBody.WithRequired(true).WithDescription(text)
		}
	}
}

func requireBody(content openapi.ContentOrReference) {
	body, ok := content.(*openapi3.RequestBodyOrRef)
	if !ok || body.RequestBody == nil {
		return
	}
	body.RequestBody.WithRequired(true).WithDescription(requestBodyText)
}

func (b *builder) addResponses(oc openapi.OperationContext, op Operation) {
	status := op.SuccessStatus
	switch {
	case status == 0 && op.Response == nil:
		status = http.StatusNoContent
	case status == 0:
		status = http.StatusOK
	}
	var success any
	if op.Response != nil {
		success = valueOf(op.Response)
	}
	if op.File != nil {
		b.addFileResponse(oc, op)
	} else {
		oc.AddRespStructure(success, responseStatus(status))
	}
	if op.LocationHeader {
		oc.AddRespStructure(b.locationHeader(status), responseStatus(status))
	}
	if op.NoContentToo {
		oc.AddRespStructure(nil, responseStatus(http.StatusNoContent))
	}
	if op.CreatedToo {
		oc.AddRespStructure(success, responseStatus(http.StatusCreated))
	}
	if b.opts.ErrorType == nil {
		return
	}
	for _, code := range errorStatusesFor(op) {
		oc.AddRespStructure(valueOf(b.errorTypeFor(code)), responseStatus(code))
	}
}

func (b *builder) errorTypeFor(status int) reflect.Type {
	if b.opts.AuthErrorType != nil && isAuthRefusal(status) {
		return b.opts.AuthErrorType
	}
	return b.opts.ErrorType
}

func isAuthRefusal(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

func (b *builder) addFileResponse(oc openapi.OperationContext, op Operation) {
	for _, contentType := range op.File.ContentTypes {
		oc.AddRespStructure(nil, responseStatus(http.StatusOK), rawContent(contentType, op.File.Format))
	}
	if op.File.Attachment {
		oc.AddRespStructure(b.dispositionHeader(), responseStatus(http.StatusOK))
	}
}

func responseStatus(status int) openapi.ContentOption {
	return func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = status
		cu.Description = http.StatusText(status)
	}
}

func valueOf(t reflect.Type) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return reflect.New(t).Interface()
}

func (b *builder) declareTags(ops []Operation) {
	used := map[string]bool{}
	for _, op := range ops {
		for _, tag := range op.Tags {
			used[tag] = true
		}
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	spec := b.reflector.SpecEns()
	for _, name := range names {
		description := b.opts.TagDescriptions[name]
		if description == "" {
			b.problem("tag %q has no description", name)
		}
		tag := openapi3.Tag{Name: name}
		if description != "" {
			tag.Description = &description
		}
		spec.Tags = append(spec.Tags, tag)
	}
	if groups := tagGroupsOf(names, b.opts.TagGroups); len(groups) > 0 {
		spec.WithMapOfAnythingItem(tagGroupsExtension, groups)
	}
}

func tagGroupsOf(names []string, groupOf map[string]string) []map[string]any {
	var order []string
	members := map[string][]string{}
	for _, name := range names {
		group := groupOf[name]
		if group == "" {
			continue
		}
		if _, seen := members[group]; !seen {
			order = append(order, group)
		}
		members[group] = append(members[group], name)
	}
	sort.Strings(order)
	groups := make([]map[string]any, 0, len(order))
	for _, group := range order {
		groups = append(groups, map[string]any{"name": group, "tags": members[group]})
	}
	return groups
}

func (b *builder) dispositionHeader() any {
	return b.syntheticValue([]reflect.StructField{{
		Name: "ContentDisposition",
		Type: reflect.TypeFor[string](),
		Tag:  reflect.StructTag(`header:"Content-Disposition" description:"Names the file: attachment, or inline for a file meant to be shown, with filename*=UTF-8''<name>."`),
	}})
}

func (b *builder) locationHeader(status int) any {
	description := "The URL of the created resource."
	if status == http.StatusAccepted {
		description = "The URL of the operation to poll."
	}
	return b.syntheticValue([]reflect.StructField{{
		Name: "Location",
		Type: reflect.TypeFor[string](),
		Tag:  reflect.StructTag(`header:"Location" description:"` + description + `"`),
	}})
}
