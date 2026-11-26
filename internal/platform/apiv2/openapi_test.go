// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/openapispec"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type docStub struct{}

func (docStub) Type(_, _ string) string     { return "A documented type." }
func (docStub) Field(_, _, _ string) string { return "A documented field." }

type specItem struct {
	ID          string  `json:"id" example:"019f9b46-77bc-76ba-a494-017487b5f5c4"`
	Type        string  `json:"type" example:"item"`
	Name        string  `json:"name" example:"first"`
	CreatedTime Instant `json:"createdTime" example:"\"2022-03-14T15:01:34.000Z\""`
}

type specListRequest struct {
	Page Page      `json:"-"`
	Sort SortOrder `sort:"name,createdTime" json:"-"`
}

type specCreateRequest struct {
	Name string `json:"name" validate:"required" example:"first"`
}

type specGetRequest struct {
	ID string `path:"id" json:"-"`
}

type specGetByKeyRequest struct {
	Key string `path:"type,key" json:"-"`
}

type specUpdateRequest struct {
	ID      string                `path:"id" json:"-"`
	Name    Patch[string]         `json:"name" example:"first"`
	Note    Patch[string]         `json:"note" clearable:"true" example:"first"`
	Expires Patch[Instant]        `json:"expires" clearable:"true" example:"2022-03-14T15:01:34.000Z"`
	Values  Patch[map[string]any] `json:"values" example:"{\"region\":\"eu-west-1\"}"`
}

type specPatchOfObjectRequest struct {
	Item Patch[specCreateRequest] `json:"item" example:"{\"name\":\"first\"}"`
}

const specItemID = "019f9b46-77bc-76ba-a494-017487b5f5c4"

func specRow(method, path, scope, id string, h Handler) Route {
	return Route{
		Method: method, Path: path, Scope: scope, Stability: StabilityDraft, Handler: h,
		Doc: Doc{ID: id, Summary: "Do " + id, Text: "Does " + id + ".", Tag: Tag{Name: "Items", Group: "Items", Text: "Items."}},
	}
}

func specRoutesFixture() []Route {
	item := func(context.Context, specGetRequest) (specItem, error) { return specItem{ID: specItemID}, nil }
	return []Route{
		specRow(http.MethodGet, "/items", "item:_:read", "listItems",
			Handle(func(context.Context, specListRequest) (List[specItem], error) { return List[specItem]{}, nil }, "Unable to list")),
		specRow(http.MethodPost, "/items", "item:_:create", "createItem",
			Handle(func(context.Context, specCreateRequest) (specItem, error) { return specItem{ID: specItemID}, nil }, "Unable to create")),
		specRow(http.MethodGet, "/items/{id}", "item:_:read", "getItem", Handle(item, "Unable to get")),
		specRow(http.MethodGet, "/itemTypes/{type}", "item:_:read", "getItemType",
			Handle(func(context.Context, specGetByKeyRequest) (specItem, error) { return specItem{}, nil }, "Unable to get")),
		specRow(http.MethodPatch, "/items/{id}", "item:_:update", "updateItem",
			Handle(func(context.Context, specUpdateRequest) (specItem, error) { return specItem{}, nil }, "Unable to update")),
		specRow(http.MethodDelete, "/items/{id}", "item:_:delete", "deleteItem",
			Handle(func(context.Context, specGetRequest) (struct{}, error) { return struct{}{}, nil }, "Unable to delete")),
		specRow(http.MethodPut, "/items/{id}/start", "item:_:update", "startItem",
			Handle(func(_ context.Context, req specGetRequest) (Accepted, error) {
				return Accepted{Operation: Operation{ID: specItemID}, Location: "/items/" + req.ID}, nil
			}, "Unable to start")),
		specRow(http.MethodGet, "/items/{id}/export", "item:_:read", "exportItem",
			Handle(func(context.Context, specGetRequest) (Download, error) {
				return Download{Content: []byte("exported"), MimeType: "text/plain", Filename: "item.txt"}, nil
			}, "Unable to export")),
		specRow(http.MethodGet, "/items/{id}/bundle", "item:_:read", "getItemBundle",
			Handle(func(context.Context, specGetRequest) (FileResponse, error) {
				return FileResponse{ContentType: "application/zip", Filename: "item.zip", Body: io.NopCloser(strings.NewReader("zip"))}, nil
			}, "Unable to get bundle")),
		specRow(http.MethodGet, "/itemCallback", "item:_:read", "itemCallback",
			Handle(func(context.Context, struct{}) (HTMLPage, error) { return HTMLPage("<p>done</p>"), nil }, "Unable to show page")),
	}
}

func specOptions(routes []Route) openapigen.Options {
	return openapigen.Options{
		Title:           "Test API",
		Version:         "2.0.0",
		Docs:            docStub{},
		Mapper:          OpenAPITypeMapper,
		Unwrap:          OpenAPIUnwrap,
		TagDescriptions: tagDescriptionsOf(routes),
		ErrorType:       OpenAPIErrorType(),
		AuthErrorType:   OpenAPIAuthErrorType(),
	}
}

func buildSpec(t *testing.T, routes []Route) *openapi3.T {
	t.Helper()
	specs, err := openapigen.Build(operationsOf(routes), specOptions(routes))
	require.NoError(t, err)
	return specs.Full
}

func operationAt(t *testing.T, doc *openapi3.T, method, path string) *openapi3.Operation {
	t.Helper()
	item := doc.Paths.Find(platpkg.APIV2Prefix + path)
	require.NotNil(t, item, path)
	op := item.GetOperation(method)
	require.NotNil(t, op, method+" "+path)
	return op
}

func parameter(t *testing.T, op *openapi3.Operation, name string) *openapi3.Parameter {
	t.Helper()
	for _, p := range op.Parameters {
		if p.Value.Name == name {
			return p.Value
		}
	}
	require.Failf(t, "no such parameter", name)
	return nil
}

func bodySchema(t *testing.T, doc *openapi3.T, op *openapi3.Operation) *openapi3.Schema {
	t.Helper()
	ref := op.RequestBody.Value.Content["application/json"].Schema.Ref
	return doc.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")].Value
}

func TestSpec_ListIsAPaginatedResponseWithPagingAndSort(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	op := operationAt(t, doc, http.MethodGet, "/items")

	assert.Equal(t, "#/components/schemas/PaginatedResponseSpecItem", op.Responses.Status(http.StatusOK).Value.Content["application/json"].Schema.Ref)
	pageSize := parameter(t, op, "pageSize").Schema.Value
	assert.EqualValues(t, defaultPageSize, pageSize.Default)
	assert.EqualValues(t, maxPageSize, *pageSize.Max)
	assert.Equal(t, "^(name|createdTime)(:(asc|desc))?$", parameter(t, op, "sort").Schema.Value.Pattern)
}

func TestSpec_CreateIsDataResponseWithLocation(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	op := operationAt(t, doc, http.MethodPost, "/items")

	created := op.Responses.Status(http.StatusCreated).Value
	assert.Equal(t, "#/components/schemas/DataResponseSpecItem", created.Content["application/json"].Schema.Ref)
	assert.Contains(t, created.Headers, "Location")
	assert.Nil(t, operationAt(t, doc, http.MethodGet, "/items/{id}").Responses.Status(http.StatusOK).Value.Headers["Location"])
}

func TestSpec_EmptyResultIs204WithNoBody(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	op := operationAt(t, doc, http.MethodDelete, "/items/{id}")

	assert.Nil(t, op.Responses.Status(http.StatusOK))
	assert.Empty(t, op.Responses.Status(http.StatusNoContent).Value.Content)
}

func TestSpec_PathIDsAreUUIDsAndBuiltInKeysArePatterns(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())

	byID := parameter(t, operationAt(t, doc, http.MethodGet, "/items/{id}"), "id").Schema.Value
	assert.Equal(t, "uuid", byID.Format)
	byKey := parameter(t, operationAt(t, doc, http.MethodGet, "/itemTypes/{type}"), "type").Schema.Value
	assert.Equal(t, keyPatternForTest, byKey.Pattern)
	assert.Empty(t, byKey.Format)
}

const keyPatternForTest = "^[a-z0-9]+(_[a-z0-9]+)*$"

func TestOpenAPITypeMapper_UsageMoneyIsADecimalString(t *testing.T) {
	schema, ok := OpenAPITypeMapper(usageMoneyType)

	require.True(t, ok)
	assert.True(t, schema.Type.SimpleTypes != nil && *schema.Type.SimpleTypes == "string")
	assert.Equal(t, usageMoneyPattern, *schema.Pattern)
	assert.Equal(t, usageMoneyExample, schema.ExtraProperties["example"])
}

func TestSpec_InstantIsANullableDateTime(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())

	created := doc.Components.Schemas["specItem"].Value.Properties["createdTime"].Value
	assert.Equal(t, "date-time", created.Format)
	assert.True(t, created.Nullable)
}

func TestSpec_PatchFieldIsItsValueAndOptional(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	body := bodySchema(t, doc, operationAt(t, doc, http.MethodPatch, "/items/{id}"))

	assert.True(t, body.Properties["name"].Value.Type.Is("string"))
	assert.Equal(t, "date-time", body.Properties["expires"].Value.Format)
	assert.Empty(t, body.Required)
}

func TestSpec_PatchFieldIsNullableOnlyWhereNullClearsTheValue(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	body := bodySchema(t, doc, operationAt(t, doc, http.MethodPatch, "/items/{id}"))

	assert.False(t, body.Properties["name"].Value.Nullable, "the handler rejects null for name")
	assert.True(t, body.Properties["note"].Value.Nullable, "null clears note")
	assert.True(t, body.Properties["expires"].Value.Nullable, "null clears expires")
	assert.False(t, body.Properties["values"].Value.Nullable, "the handler rejects null for a map-typed values")
}

func TestSpec_PatchOfAnObjectDescribesItsFieldsAndRejectsNull(t *testing.T) {
	doc := buildSpec(t, []Route{specRow(http.MethodPatch, "/items", "item:_:update", "patchItems",
		Handle(func(context.Context, specPatchOfObjectRequest) (specItem, error) { return specItem{}, nil }, "Unable to update"))})
	body := bodySchema(t, doc, operationAt(t, doc, http.MethodPatch, "/items"))

	item := body.Properties["item"].Value
	assert.Contains(t, item.Properties, "name", "the object's own fields are described")
	assert.False(t, item.Nullable, "the handler rejects null for an item not tagged clearable")
}

func TestSpec_StatusAndLocationMatchWhatTheRoutesSend(t *testing.T) {
	bodies := map[string]string{http.MethodPost: `{"name":"first"}`, http.MethodPatch: `{"name":"renamed"}`}
	for _, rt := range specRoutesFixture() {
		t.Run(rt.Doc.ID, func(t *testing.T) {
			op := operationOf(rt)
			mux := http.NewServeMux()
			mux.HandleFunc(rt.Method+" "+rt.Path, rt.Handler.ServeHTTP)
			target := strings.NewReplacer("{id}", specItemID, "{type}", "item_type").Replace(rt.Path)
			req := httptest.NewRequest(rt.Method, target, strings.NewReader(bodies[rt.Method]))
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			assert.Equal(t, op.SuccessStatus, rec.Code)
			assert.Equal(t, op.LocationHeader, rec.Header().Get("Location") != "")
			assert.Equal(t, op.SuccessStatus != http.StatusNoContent, rec.Body.Len() > 0)
		})
	}
}

func TestSpec_AcceptedIs202WithTheOperationAndALocationToPoll(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())
	op := operationAt(t, doc, http.MethodPut, "/items/{id}/start")

	accepted := op.Responses.Status(http.StatusAccepted).Value
	assert.Equal(t, "#/components/schemas/DataResponseOperation", accepted.Content["application/json"].Schema.Ref)
	assert.Contains(t, accepted.Headers, "Location")
	assert.Nil(t, op.Responses.Status(http.StatusOK))
}

func TestSpec_DownloadAndPageAre200NotNoContent(t *testing.T) {
	doc := buildSpec(t, specRoutesFixture())

	for _, path := range []string{"/items/{id}/export", "/items/{id}/bundle", "/itemCallback"} {
		op := operationAt(t, doc, http.MethodGet, path)
		assert.NotNil(t, op.Responses.Status(http.StatusOK), path)
		assert.Nil(t, op.Responses.Status(http.StatusNoContent), path)
	}
}

func TestEveryRouteDocumentsItsWriterStatusAndLocation(t *testing.T) {
	doc := buildSpecs(t).Full
	for _, rt := range specRoutes() {
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rt.Handler.WriteSample(rec, httptest.NewRequest(rt.Method, platpkg.APIV2Prefix+rt.Path, nil))
			op := operationAt(t, doc, rt.Method, rt.Path)

			sent := op.Responses.Status(rec.Code)
			require.NotNil(t, sent, "the handler sends %d, which the spec does not document", rec.Code)
			upsert := rt.Handler.Response.Implements(reflect.TypeFor[apicontract.CreatingUpsert]())
			for code := range op.Responses.Map() {
				if upsert && code == strconv.Itoa(http.StatusCreated) {
					continue
				}
				if strings.HasPrefix(code, "2") {
					assert.Equal(t, strconv.Itoa(rec.Code), code, "the spec documents a success status the handler does not send")
				}
			}
			assert.Equal(t, rec.Header().Get("Location") != "", sent.Value.Headers["Location"] != nil, "Location header")
		})
	}
}

var (
	sharedSpecsOnce sync.Once
	sharedSpecs     *openapigen.Specs
	sharedSpecsErr  error
)

func buildSpecs(t *testing.T) *openapigen.Specs {
	t.Helper()
	sharedSpecsOnce.Do(func() { sharedSpecs, sharedSpecsErr = buildFreshSpecs() })
	require.NoError(t, sharedSpecsErr)
	return sharedSpecs
}

func buildFreshSpecs() (*openapigen.Specs, error) {
	docs, err := openapigen.NewASTDocsForPackages(OpenAPIDocPackages()...)
	if err != nil {
		return nil, err
	}
	return openapigen.Build(OpenAPIOperations(), realSpecOptions(docs))
}

func realSpecOptions(docs openapigen.DocSource) openapigen.Options {
	return openapigen.Options{
		Title:           "test",
		Version:         "2.0.0",
		Docs:            docs,
		Mapper:          OpenAPITypeMapper,
		Unwrap:          OpenAPIUnwrap,
		TagDescriptions: OpenAPITagDescriptions(),
		TagGroups:       OpenAPITagGroups(),
		ErrorType:       OpenAPIErrorType(),
		AuthErrorType:   OpenAPIAuthErrorType(),
	}
}

type goNameCheckingDocs struct {
	openapigen.DocSource
	offenders map[string]string
}

func (d *goNameCheckingDocs) Type(pkgPath, typeName string) string {
	text := d.DocSource.Type(pkgPath, typeName)
	d.check(typeName, typeName, text)
	return text
}

func (d *goNameCheckingDocs) Field(pkgPath, typeName, fieldName string) string {
	text := d.DocSource.Field(pkgPath, typeName, fieldName)
	d.check(typeName+"."+fieldName, fieldName, text)
	return text
}

func (d *goNameCheckingDocs) check(subject, goName, text string) {
	rest, found := strings.CutPrefix(text, goName)
	if !found {
		return
	}
	next, _ := utf8.DecodeRuneInString(rest)
	if rest == "" || !unicode.IsLetter(next) && !unicode.IsDigit(next) {
		d.offenders[subject] = text
	}
}

func TestNoDescriptionOpensWithItsGoName(t *testing.T) {
	docs, err := openapigen.NewASTDocsForPackages(OpenAPIDocPackages()...)
	require.NoError(t, err)
	checking := &goNameCheckingDocs{DocSource: docs, offenders: map[string]string{}}

	_, err = openapigen.Build(OpenAPIOperations(), realSpecOptions(checking))
	require.NoError(t, err)

	assert.Empty(t, checking.offenders, "reword these doc comments so the text after the Go name reads as a sentence")
}

func operationCount(doc *openapi3.T) int {
	n := 0
	for _, item := range doc.Paths.Map() {
		n += len(item.Operations())
	}
	return n
}

func TestOpenAPISpecDescribesEveryRoute(t *testing.T) {
	routes := specRoutes()
	require.NotEmpty(t, routes)

	assert.Equal(t, len(routes), operationCount(buildSpecs(t).Full), "a route is missing from the spec, or two share a method and path")
}

func TestOpenAPISpecGroupsEveryTagOnce(t *testing.T) {
	full := buildSpecs(t).Full
	groups, ok := full.Extensions["x-tagGroups"].([]any)
	require.True(t, ok, "the description has no x-tagGroups")
	var names []string
	seen := map[string]int{}
	for _, raw := range groups {
		group := raw.(map[string]any)
		names = append(names, group["name"].(string))
		for _, tag := range group["tags"].([]any) {
			seen[tag.(string)]++
		}
	}
	assert.True(t, slices.IsSorted(names), "groups are not sorted by name: %v", names)
	for _, tag := range full.Tags {
		assert.Equal(t, 1, seen[tag.Name], "tag %q is listed by %d groups", tag.Name, seen[tag.Name])
	}
}

func TestEveryRouteBuilderYieldsRoutes(t *testing.T) {
	deps, core := specDeps()
	for _, g := range routeGroups(deps, core, core.Logger()) {
		assert.NotEmpty(t, g.build(), "%s returned no routes under the describing dependencies, so its routes are missing from the spec", g.name)
	}
}

func TestOpenAPISpecPublishesOnlyPublishedRoutes(t *testing.T) {
	published := 0
	for _, rt := range specRoutes() {
		if rt.Published {
			published++
		}
	}

	assert.Equal(t, published, operationCount(buildSpecs(t).Public))
}

func TestPublishedRoutesAreStable(t *testing.T) {
	var drafts []string
	for _, rt := range specRoutes() {
		if rt.Published && rt.Stability != StabilityStable {
			drafts = append(drafts, rt.Method+" "+rt.Path)
		}
	}

	assert.Empty(t, drafts, "a published route is a customer promise, so the breaking-change check must hold it")
}

func TestRouteErrorsReachTheSpec(t *testing.T) {
	deleteEntrypoint := buildSpecs(t).Full.Paths.Value(platpkg.APIV2Prefix + "/entrypoints/{id}").Delete

	assert.NotNil(t, deleteEntrypoint.Responses.Value("502"))
}

func TestAnUpsertDocumentsBothItsSuccessStatuses(t *testing.T) {
	setTopUp := buildSpecs(t).Full.Paths.Value(platpkg.APIV2Prefix + "/budgetSubjects/{id}/topUp").Put

	assert.NotNil(t, setTopUp.Responses.Value("200"))
	assert.NotNil(t, setTopUp.Responses.Value("201"))
	assert.Nil(t, setTopUp.Responses.Value("201").Value.Headers["Location"])
}

func TestUpdateFieldsAreNullableOnlyWhereNullClearsTheStoredValue(t *testing.T) {
	update := buildSpecs(t).Full.Components.Schemas["UpdateEntrypointRequest"].Value

	assert.True(t, update.Properties["name"].Value.Type.Is("string"))
	assert.False(t, update.Properties["name"].Value.Nullable, "the handler rejects a null name")
	assert.True(t, update.Properties["publicUrl"].Value.Nullable, "a null publicUrl clears it")
}

func TestEveryDownloadIsA200WithAFileBodyAndNoWireSchema(t *testing.T) {
	doc := buildSpecs(t).Full
	downloads := 0
	for _, rt := range specRoutes() {
		if !rt.Handler.IsFile {
			continue
		}
		downloads++
		op := operationAt(t, doc, rt.Method, rt.Path)
		assert.Nil(t, op.Responses.Status(http.StatusNoContent), "%s %s", rt.Method, rt.Path)
		ok := op.Responses.Status(http.StatusOK)
		require.NotNil(t, ok, "%s %s", rt.Method, rt.Path)
		require.Len(t, ok.Value.Content, len(apicontract.FileTypesOf(rt)), "%s %s", rt.Method, rt.Path)
		for _, fileType := range apicontract.FileTypesOf(rt) {
			require.Contains(t, ok.Value.Content, fileType, "%s %s", rt.Method, rt.Path)
			assert.Equal(t, "binary", ok.Value.Content[fileType].Schema.Value.Format)
		}
		assert.Contains(t, ok.Value.Headers, "Content-Disposition")
	}
	assert.NotZero(t, downloads, "no route downloads a file, so this test checks nothing")
}

func TestEveryUploadIsMultipartWithABinaryFileAndNoQueryParameter(t *testing.T) {
	doc := buildSpecs(t).Full
	uploads := 0
	for _, rt := range specRoutes() {
		if len(rt.Handler.FormParts) == 0 {
			continue
		}
		uploads++
		op := operationAt(t, doc, rt.Method, rt.Path)
		require.NotNil(t, op.RequestBody, "%s %s", rt.Method, rt.Path)
		require.Len(t, op.RequestBody.Value.Content, 1)
		form := op.RequestBody.Value.Content["multipart/form-data"]
		require.NotNil(t, form, "%s %s", rt.Method, rt.Path)
		for _, part := range rt.Handler.FormParts {
			property := form.Schema.Value.Properties[part.Name]
			require.NotNil(t, property, "%s %s part %s", rt.Method, rt.Path, part.Name)
			assert.Equal(t, part.File, property.Value.Format == "binary")
			assert.Equal(t, part.File, slices.Contains(form.Schema.Value.Required, part.Name))
		}
		for _, param := range op.Parameters {
			assert.NotEqual(t, "query", param.Value.In, "%s %s: a form part must not also be a query parameter", rt.Method, rt.Path)
		}
	}
	assert.NotZero(t, uploads)
}

func TestARawBodyRouteDocumentsItsBytes(t *testing.T) {
	doc := buildSpecs(t).Full
	raw := 0
	for _, rt := range specRoutes() {
		if !rt.Handler.RawBody {
			continue
		}
		raw++
		body := operationAt(t, doc, rt.Method, rt.Path).RequestBody
		require.NotNil(t, body, "%s %s", rt.Method, rt.Path)
		assert.Equal(t, "binary", body.Value.Content[octetStream].Schema.Value.Format)
	}
	assert.NotZero(t, raw)
}

func TestBuiltInSkillIDsAreAcceptedByTheSpec(t *testing.T) {
	id := parameter(t, operationAt(t, buildSpecs(t).Full, http.MethodGet, "/skills/{id}"), "id").Schema.Value

	assert.Empty(t, id.Format, "a built-in skill's name is not a UUID")
	assert.Regexp(t, id.Pattern, "sam-docs")
	assert.Regexp(t, id.Pattern, specItemID)
	assert.NotRegexp(t, id.Pattern, "Not A Skill")
}

func TestPageNumberStatesTheLimitTheHandlerEnforces(t *testing.T) {
	page := parameter(t, operationAt(t, buildSpecs(t).Full, http.MethodGet, "/agents"), "pageNumber").Schema.Value

	require.NotNil(t, page.Max)
	assert.EqualValues(t, maxPageNumber, *page.Max)
}

func TestAnAPIKeyRequestNamesTheFieldsItsHandlerRequires(t *testing.T) {
	create := buildSpecs(t).Full.Components.Schemas["CreateAPIKeyRequest"].Value

	assert.ElementsMatch(t, []string{"userId", "label", "scopes"}, create.Required)
}

func TestAPointerSentAsNullIsListedAsRequired(t *testing.T) {
	meta := buildSpecs(t).Full.Components.Schemas["PaginationMeta"].Value

	assert.Contains(t, meta.Required, "nextPage")
	assert.True(t, meta.Properties["nextPage"].Value.Nullable)
}

type renderedSpecs struct {
	full, public string
	err          error
}

func renderFreshSpecs() renderedSpecs {
	specs, err := buildFreshSpecs()
	if err != nil {
		return renderedSpecs{err: err}
	}
	full, err := openapigen.Marshal(specs.Full)
	if err != nil {
		return renderedSpecs{err: err}
	}
	public, err := openapigen.Marshal(specs.Public)
	return renderedSpecs{full: string(full), public: string(public), err: err}
}

func TestRenderedSpecsAreByteIdenticalAcrossRuns(t *testing.T) {
	const renders = 6
	runs := make([]renderedSpecs, renders)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() { runs[i] = renderFreshSpecs() })
	}
	wg.Wait()
	for i, run := range runs {
		require.NoError(t, run.err, "render %d", i)
	}
	for _, run := range runs[1:] {
		assert.Equal(t, runs[0].full, run.full)
		assert.Equal(t, runs[0].public, run.public)
	}
}

func TestEveryOperationStatesItsTokenPermissions(t *testing.T) {
	doc := buildSpecs(t).Full
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			assert.Contains(t, op.Description, "\n\nToken Permissions: ", "%s %s", method, path)
		}
	}
}

func TestTokenPermissionsComeFromEachRoutesAuthorization(t *testing.T) {
	doc := buildSpecs(t).Full
	for _, c := range []struct {
		method, path, want string
	}{
		{http.MethodPatch, "/entrypoints/{id}", "Token Permissions: [ `entrypoint:*:update` ] API keys can call this operation."},
		{http.MethodGet, "/entrypoints/{id}/networkingDetails", "Token Permissions: [ `entrypoint:*:read` ]"},
		{http.MethodGet, "/agents/{id}/access", "Token Permissions: [ `rbac:_:read` **or** `agent_builder:*:read` ]"},
		{http.MethodGet, "/apiKeys", "Token Permissions: " + gateDocs[GateAPIKeyRead].Text + " API keys can call this operation."},
		{http.MethodPost, "/apiKeys", "Token Permissions: " + gateDocs[GateAPIKeyWrite].Text},
	} {
		description := operationAt(t, doc, c.method, c.path).Description
		assert.True(t, strings.HasSuffix(description, "\n\n"+c.want), "%s %s: %q", c.method, c.path, description)
	}
}

func TestAnOAuthQueryParameterTheHandlerRequiresIsRequired(t *testing.T) {
	doc := buildSpecs(t).Full
	for path, names := range map[string][]string{
		"/connectors/mcp/oauth/status":   {"sessionId"},
		"/connectors/mcp/oauth/callback": {"code", "state"},
	} {
		op := operationAt(t, doc, http.MethodGet, path)
		required := map[string]bool{}
		for _, p := range op.Parameters {
			required[p.Value.Name] = p.Value.Required
		}
		for _, name := range names {
			assert.True(t, required[name], "%s: the handler rejects a missing %s", path, name)
		}
	}
}

func TestAPatchOfAMapIsNullableOnlyWhereNullClearsIt(t *testing.T) {
	schemas := buildSpecs(t).Full.Components.Schemas
	for _, name := range []string{"UpdateConnectorRequest", "UpdateSkillConfigurationRequest", "UpdateToolsetConfigurationRequest", "UpdateEntrypointRequest"} {
		assert.False(t, schemas[name].Value.Properties["values"].Value.Nullable, "%s: the handler rejects a null values", name)
	}
	assert.True(t, schemas["UpdateModelRequest"].Value.Properties["pricing"].Value.Nullable, "a null pricing clears it")
}

func TestARequiredPointerIsNotNullable(t *testing.T) {
	schemas := buildSpecs(t).Full.Components.Schemas
	for _, name := range []string{"CreateRemoteAgentRequest", "UpdateRemoteAgentRequest"} {
		config := schemas[name].Value.Properties["agentAuthConfig"].Value
		assert.False(t, config.Nullable, "%s: validate:\"required\" rejects a null agentAuthConfig", name)
	}
}

func servedStatus() int {
	if openapispec.Available() {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

func TestOpenAPISpecIsServedWithoutCredentialsAtTheV2Root(t *testing.T) {
	refuseAll := entplatform.NewAuthMiddleware(nil, nil, nil, slog.Default())
	for _, c := range []struct {
		name string
		path string
		want int
	}{
		{"spec", "/api/v2/openapi.yaml", servedStatus()},
		{"another v2 route", platpkg.APIV2Prefix + "/agents", http.StatusUnauthorized},
		{"viewer", "/redoc", http.StatusOK},
		{"viewer script", "/redoc/redoc.standalone.js", http.StatusOK},
		{"viewer sidebar script", "/redoc/collapsible-groups.js", http.StatusOK},
		{"viewer stylesheet", "/redoc/stylesheet.css", http.StatusOK},
		{"viewer logo", "/redoc/solace-logo.png", http.StatusOK},
	} {
		t.Run(c.name, func(t *testing.T) {
			core, _ := newTestCoreWithAuth(t, true, nil, nil, refuseAll)
			rec := httptest.NewRecorder()
			core.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			assert.Equal(t, c.want, rec.Code, rec.Body.String())
			if c.want == http.StatusOK && c.path == "/api/v2/openapi.yaml" {
				assert.Equal(t, "application/yaml", rec.Header().Get("Content-Type"))
				assert.Contains(t, rec.Body.String(), "\nopenapi: 3.0.3\n", "the embedded file is not an OpenAPI description")
			}
		})
	}
}

const openapiSpecPublicReason = "the published API reference describes the contract and holds no tenant data, and a client reads it before it has a token"

const chatLoginConfigPublicReason = "the login configuration holds the sign-in route and branding a client needs before it has a token, and nothing about any caller"

func TestTheOnlyUnauthenticatedV2PathsOutsideThePlatformPrefixHaveARecordedReason(t *testing.T) {
	reasons := map[string]string{
		openapispec.Path:                openapiSpecPublicReason,
		chatv2.Prefix + "/config/login": chatLoginConfigPublicReason,
	}
	var outside []string
	for path := range auth.SkipPaths {
		if strings.HasPrefix(path, "/api/v2/") && !strings.HasPrefix(path, platpkg.APIV2Prefix+"/") {
			outside = append(outside, path)
		}
	}
	want := slices.Sorted(maps.Keys(reasons))
	slices.Sort(outside)

	assert.Equal(t, want, outside,
		"a path served without authentication outside routeTable has no GatePublic row or recorded reason; add its reason to reasons")
}

func TestEverySpecPathIsUnderTheV2Prefix(t *testing.T) {
	specs := buildSpecs(t)

	for name, doc := range map[string]*openapi3.T{"full": specs.Full, "public": specs.Public} {
		for path := range doc.Paths.Map() {
			assert.True(t, strings.HasPrefix(path, platpkg.APIV2Prefix+"/"), "the %s description lists %s, which is not under %s", name, path, platpkg.APIV2Prefix)
		}
	}
}

func TestDescriptionOf_NamesTheUnofferedScopeAndWhoHoldsIt(t *testing.T) {
	unoffered := descriptionOf(Route{Scope: rbac.ScopeBudgetCreate, Doc: Doc{Text: "Creates a budget."}})
	for _, want := range []string{"Creates a budget. ", "`" + rbac.ScopeBudgetCreate + "`", "`*:*:*`", "`sam_manager`"} {
		if !strings.Contains(unoffered, want) {
			t.Errorf("description = %q, want it to contain %q", unoffered, want)
		}
	}
	if offered := descriptionOf(Route{Scope: rbac.ScopeBudgetRead, Doc: Doc{Text: "Lists budgets."}}); offered != "Lists budgets." {
		t.Errorf("description = %q, want the text alone for a scope the role editor offers", offered)
	}
}
