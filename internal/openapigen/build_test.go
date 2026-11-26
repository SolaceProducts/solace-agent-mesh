// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swaggest/jsonschema-go"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/openapifixtures"
)

const fixturePkg = "github.com/SolaceProducts/solace-agent-mesh/internal/testing/openapifixtures"

func testDocs(t *testing.T) DocSource {
	t.Helper()
	docs, err := NewASTDocsForPackages(fixturePkg)
	require.NoError(t, err)
	return docs
}

func stampMapper(t reflect.Type) (*jsonschema.Schema, bool) {
	if t != reflect.TypeOf(openapifixtures.Stamp(0)) {
		return nil, false
	}
	return new(jsonschema.Schema).WithType(jsonschema.String.Type()).WithFormat("date-time"), true
}

func testOptions(t *testing.T) Options {
	return Options{
		Title:           "Test API",
		Version:         "2.0.0",
		Docs:            testDocs(t),
		Mapper:          stampMapper,
		ErrorType:       reflect.TypeOf(openapifixtures.Failure{}),
		TagDescriptions: map[string]string{"Widgets": "Manage widgets.", "A": "A tag.", "D": "D tag."},
	}
}

var testAccess = Access{Scopes: []string{"widget:*:read"}}

func fixtureOps() []Operation {
	return []Operation{
		{Method: "POST", Path: "/api/v2/widgets", Published: true, Stability: StabilityStable, Tags: []string{"Widgets"},
			OperationID: "createWidget", Summary: "Create a widget", Description: "Creates a widget.", Access: Access{Scopes: []string{"widget:_:create"}},
			Request: reflect.TypeOf(openapifixtures.CreateWidget{}), Response: reflect.TypeOf(openapifixtures.Widget{}), SuccessStatus: 201},
		{Method: "GET", Path: "/api/v2/widgets", Published: true, Stability: StabilityStable, Tags: []string{"Widgets"},
			OperationID: "listWidgets", Summary: "Get all widgets", Description: "Returns every widget the caller may see.\n\nResults are ordered by name.", Access: Access{Scopes: []string{"widget:*:read"}},
			Response: reflect.TypeOf(openapifixtures.Page[openapifixtures.Widget]{})},
		{Method: "GET", Path: "/api/v2/widgets/{widgetId}", Stability: StabilityDraft, Tags: []string{"Widgets"},
			OperationID: "getWidget", Summary: "Get a widget", Description: "Returns one widget.", Access: Access{Scopes: []string{"widget:*:read"}},
			Response: reflect.TypeOf(openapifixtures.Widget{})},
	}
}

func TestBuildSplitsPublicFromFull(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)

	assert.NotNil(t, specs.Full.Paths.Value("/api/v2/widgets/{widgetId}"))
	assert.Nil(t, specs.Public.Paths.Value("/api/v2/widgets/{widgetId}"))
	assert.NotNil(t, specs.Public.Paths.Value("/api/v2/widgets"))

	list := specs.Public.Paths.Value("/api/v2/widgets").Get
	assert.Equal(t, "stable", list.Extensions["x-stability-level"])
	assert.Equal(t, "listWidgets", list.OperationID)
	assert.Equal(t, "Get all widgets", list.Summary)
	assert.Equal(t, []string{"Widgets"}, list.Tags)
	assert.Contains(t, list.Description, "Results are ordered by name.")
	assert.NotContains(t, list.Description, "sam:summary")

	require.NoError(t, Validate(specs.Full))
	require.NoError(t, Validate(specs.Public))
}

func TestSchemaShapes(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	widget := specs.Full.Components.Schemas["Widget"].Value

	assert.Equal(t, "date-time", widget.Properties["createdAt"].Value.Format)
	assert.True(t, widget.Properties["note"].Value.Nullable)
	assert.NotContains(t, widget.Properties, "Secret")
	assert.Equal(t, []string{"createdAt", "id", "name", "owner", "tags"}, widget.Required)
	assert.Equal(t, "date-time", widget.Properties["updated"].Value.Format)
	assert.Equal(t, "The display name.", widget.Properties["name"].Value.Description)

	owner := widget.Properties["owner"].Value
	require.Len(t, owner.AllOf, 1)
	assert.Equal(t, "#/components/schemas/Owner", owner.AllOf[0].Ref)
	assert.Contains(t, specs.Full.Components.Schemas, "PageWidget")
}

func TestPathParametersAndErrorResponses(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	get := specs.Full.Paths.Value("/api/v2/widgets/{widgetId}").Get

	require.Len(t, get.Parameters, 1)
	assert.Equal(t, "widgetId", get.Parameters[0].Value.Name)
	assert.True(t, get.Parameters[0].Value.Required)
	for _, code := range []string{"200", "400", "401", "403", "404", "422", "500"} {
		assert.NotNil(t, get.Responses.Value(code), code)
	}
	assert.Nil(t, get.Responses.Value("409"), "a read cannot conflict")

	post := specs.Full.Paths.Value("/api/v2/widgets").Post
	assert.NotNil(t, post.Responses.Value("409"), "a write can conflict")
}

func TestAnIntegerPathParameterIsNotAUUID(t *testing.T) {
	op := bodyOp("PUT", "/a/{id}", "putA", func(o *Operation) {
		o.Request = reflect.TypeOf(openapifixtures.Chunk{})
		o.RawBody = &RawBody{ContentType: "application/octet-stream", Description: "The chunk."}
	})
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	index := specs.Full.Paths.Value("/a/{id}").Put.Parameters.GetByInAndName("path", "id").Schema.Value
	assert.True(t, index.Type.Is("integer"))
	assert.NotEqual(t, "uuid", index.Format)
}

func TestAuthRefusalsHaveTheirOwnErrorBody(t *testing.T) {
	opts := testOptions(t)
	opts.AuthErrorType = reflect.TypeOf(openapifixtures.Refusal{})
	specs, err := Build(fixtureOps(), opts)
	require.NoError(t, err)

	get := specs.Full.Paths.Value("/api/v2/widgets/{widgetId}").Get
	for code, body := range map[string]string{"401": "Refusal", "403": "Refusal", "400": "Failure", "404": "Failure", "500": "Failure"} {
		assert.Equal(t, "#/components/schemas/"+body, get.Responses.Value(code).Value.Content.Get("application/json").Schema.Ref, code)
	}
	assert.NotContains(t, specs.Full.Components.Schemas["Refusal"].Value.Required, "reason")
}

func TestBuildReportsEveryProblem(t *testing.T) {
	documented := Operation{OperationID: "getX", Summary: "Get x", Access: testAccess, Description: "Returns x."}
	with := func(method, path string, stability Stability, mutate func(*Operation)) Operation {
		op := documented
		op.Method, op.Path, op.Stability, op.Tags = method, path, stability, []string{"A"}
		mutate(&op)
		return op
	}
	ops := []Operation{
		with("GET", "/a", StabilityStable, func(o *Operation) { o.Description = "" }),
		with("GET", "/b", "", func(*Operation) {}),
		with("GET", "/c", StabilityDraft, func(o *Operation) { o.OperationID = "" }),
		with("GET", "/f", StabilityDraft, func(o *Operation) { o.Published = true }),
		with("GET", "/d", StabilityDraft, func(o *Operation) { o.Summary = "" }),
		with("GET", "/e", StabilityDraft, func(o *Operation) { o.Tags = nil }),
	}
	_, err := Build(ops, testOptions(t))
	require.Error(t, err)
	msg := err.Error()

	assert.Contains(t, msg, `GET /a: no Description`)
	assert.Contains(t, msg, `GET /b: unknown stability ""`)
	assert.Contains(t, msg, `GET /c: no OperationID`)
	assert.Contains(t, msg, `GET /f: a published operation cannot be draft`)
	assert.Contains(t, msg, `GET /d: no Summary`)
	assert.Contains(t, msg, `GET /e: at least one tag is required`)
	assert.Equal(t, 1, strings.Count(msg, `GET /b: unknown stability`), "duplicates across full and public builds are collapsed")
}

func TestMissingFieldAndTypeDescriptionsFailGeneration(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Tags: []string{"A"},
		Summary: "Get a", Description: "Returns a.", Response: reflect.TypeOf(openapifixtures.Undocumented{})}}
	_, err := Build(ops, testOptions(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing description for type")
	assert.Contains(t, err.Error(), "missing description for field")
	assert.Contains(t, err.Error(), "missing example for field openapifixtures.Undocumented.Field")
}

func TestOutputIsByteIdenticalAcrossRuns(t *testing.T) {
	render := func() []byte {
		specs, err := Build(fixtureOps(), testOptions(t))
		require.NoError(t, err)
		data, err := Marshal(specs.Full)
		require.NoError(t, err)
		return data
	}
	first := render()
	for range 5 {
		assert.Equal(t, string(first), string(render()))
	}
	assert.Contains(t, string(first), "\n  /api/v2/widgets:\n", "pretty-printed with two-space indent")
	assert.Contains(t, string(first), `"200":`, "numeric status keys stay quoted strings")
}

func TestWriteSpecsWritesBothFilesAndLeavesNoTempFiles(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "out")

	require.NoError(t, WriteSpecs(dir, specs))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{FullSpecFile, PublicSpecFile}, names)

	loaded, err := openapi3.NewLoader().LoadFromFile(filepath.Join(dir, PublicSpecFile))
	require.NoError(t, err)
	assert.NotNil(t, loaded.Paths.Value("/api/v2/widgets"))
	assert.Nil(t, loaded.Paths.Value("/api/v2/widgets/{widgetId}"))
}

func TestDeprecationAndExamples(t *testing.T) {
	ops := append(fixtureOps(), Operation{Method: "GET", Path: "/api/v2/old", Stability: StabilityDraft,
		OperationID: "getOldWidget", Summary: "Get an old widget", Tags: []string{"Widgets"}, Access: testAccess,
		Description: "Is retired.\n\nDeprecated: Reason: superseded by getWidget. Removal Date: 2027-06-01."})
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	widget := specs.Full.Components.Schemas["Widget"].Value
	assert.True(t, widget.Properties["legacy"].Value.Deprecated)
	assert.Contains(t, widget.Properties["legacy"].Value.Description, "Removal Date")
	assert.False(t, widget.Properties["name"].Value.Deprecated)
	assert.Equal(t, "Blue widget", widget.Properties["name"].Value.Example)
	assert.EqualValues(t, 3, widget.Properties["count"].Value.Example)

	assert.True(t, specs.Full.Paths.Value("/api/v2/old").Get.Deprecated)
	assert.False(t, specs.Full.Paths.Value("/api/v2/widgets").Get.Deprecated)
}

func TestBadDeprecationAndExamplesFailGeneration(t *testing.T) {
	ops := []Operation{
		{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
			Access: testAccess, Description: "Is retired without a date.\n\nDeprecated: superseded.",
			Tags:     []string{"A"},
			Response: reflect.TypeOf(openapifixtures.BadAnnotations{})},
	}
	_, err := Build(ops, testOptions(t))
	require.Error(t, err)
	msg := err.Error()

	assert.Contains(t, msg, `GET /a is deprecated but its doc comment lacks a "Reason"`)
	assert.Contains(t, msg, "field openapifixtures.BadAnnotations.Old is deprecated")
	assert.Contains(t, msg, "Size: parsing example as JSON")
}

func TestQueryParameters(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/api/v2/widgets", Published: true, Stability: StabilityStable, OperationID: "listWidgets", Summary: "Get all widgets", Access: testAccess, Description: "Returns widgets.",
		Tags: []string{"Widgets"}, Query: reflect.TypeOf(openapifixtures.WidgetQuery{}), Response: reflect.TypeOf(openapifixtures.Widget{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	byName := map[string]*openapi3.Parameter{}
	for _, p := range specs.Public.Paths.Value("/api/v2/widgets").Get.Parameters {
		byName[p.Value.Name] = p.Value
	}
	require.Len(t, byName, 5)

	pageSize := byName["pageSize"]
	assert.Equal(t, "query", pageSize.In)
	assert.EqualValues(t, 20, pageSize.Schema.Value.Default)
	assert.EqualValues(t, 1, *pageSize.Schema.Value.Min)
	assert.EqualValues(t, 100, *pageSize.Schema.Value.Max)
	assert.Equal(t, "How many items each page holds.", pageSize.Description)
	assert.False(t, pageSize.Required)

	assert.True(t, byName["owner"].Required)
	assert.Equal(t, "alice", byName["owner"].Schema.Value.Example)

	expand := byName["expand"]
	assert.Equal(t, "form", expand.Style)
	assert.False(t, *expand.Explode)
	assert.True(t, expand.Schema.Value.Type.Is("array"))

	assert.True(t, byName["colour"].Deprecated)
}

func TestBadQueryParametersFailGeneration(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "listWidgets", Summary: "Get all widgets", Access: testAccess, Description: "Returns widgets.",
		Tags: []string{"A"}, Query: reflect.TypeOf(openapifixtures.BadQuery{})}}
	_, err := Build(ops, testOptions(t))
	require.Error(t, err)
	msg := err.Error()

	assert.NotContains(t, msg, "Untagged", "a field without a query tag is not a query parameter")
	assert.Contains(t, msg, "must be a scalar or a slice of scalars")
	assert.Contains(t, msg, "parsing default as JSON")
}

func TestAccessIsPublishedAsTokenPermissions(t *testing.T) {
	ops := fixtureOps()
	ops[0].Access = Access{Scopes: []string{"widget:_:create", "rbac:_:read"}, APIKeys: true}
	ops[2].Access = Access{Gate: "the universal grant `*:*:*`."}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	create := specs.Full.Paths.Value("/api/v2/widgets").Post
	assert.Equal(t, "Creates a widget.\n\nToken Permissions: [ `widget:_:create` **or** `rbac:_:read` ] API keys can call this operation.", create.Description)
	list := specs.Full.Paths.Value("/api/v2/widgets").Get
	assert.True(t, strings.HasSuffix(list.Description, "\n\nToken Permissions: [ `widget:*:read` ]"), list.Description)
	get := specs.Full.Paths.Value("/api/v2/widgets/{widgetId}").Get
	assert.Equal(t, "Returns one widget.\n\nToken Permissions: the universal grant `*:*:*`.", get.Description)
	for _, op := range []*openapi3.Operation{create, list, get} {
		assert.NotContains(t, op.Extensions, "x-sam-required-scope")
	}
}

func TestEveryOperationNeedsABearerTokenUnlessPublic(t *testing.T) {
	ops := append(fixtureOps(), Operation{Method: "GET", Path: "/api/v2/version", Published: true, Stability: StabilityStable,
		OperationID: "getVersion", Summary: "Get the version", Description: "Returns the version.", Tags: []string{"Widgets"},
		Access: Access{Gate: "none.", Public: true}, Response: reflect.TypeOf(openapifixtures.Owner{})})
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	for _, doc := range []*openapi3.T{specs.Full, specs.Public} {
		scheme := doc.Components.SecuritySchemes["bearerAuth"]
		require.NotNil(t, scheme)
		assert.Equal(t, "http", scheme.Value.Type)
		assert.Equal(t, "bearer", scheme.Value.Scheme)
		assert.Empty(t, scheme.Value.BearerFormat, "SAM API keys are not JWTs")
		assert.Equal(t, openapi3.SecurityRequirements{{"bearerAuth": []string{}}}, doc.Security)
		assert.Nil(t, doc.Paths.Value("/api/v2/widgets").Post.Security, "an operation with access rules keeps the document's requirement")
		version := doc.Paths.Value("/api/v2/version").Get
		require.NotNil(t, version.Security)
		assert.Empty(t, *version.Security, "a public operation needs no credential")
		assert.Nil(t, version.Responses.Value("401"), "a public operation never refuses a credential")
		assert.Nil(t, version.Responses.Value("403"), "a public operation never refuses a credential")
	}
}

func TestOperationWithoutAccessIsRejected(t *testing.T) {
	for name, access := range map[string]Access{
		"no access":       {},
		"scopes and gate": {Scopes: []string{"widget:*:read"}, Gate: "the universal grant."},
		"api keys alone":  {APIKeys: true},
	} {
		ops := fixtureOps()
		ops[1].Access = access
		_, err := Build(ops, testOptions(t))
		assert.ErrorContains(t, err, "GET /api/v2/widgets: ", name)
	}
}

func TestDocsForPackagesReportsUnknownPackage(t *testing.T) {
	_, err := NewASTDocsForPackages("github.com/SolaceProducts/solace-agent-mesh/internal/doesnotexist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Go files")

	_, err = NewASTDocsForPackages("example.com/elsewhere")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside module")
}

func TestTagsAreDeclaredWithDescriptions(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	require.Len(t, specs.Public.Tags, 1)
	assert.Equal(t, "Widgets", specs.Public.Tags[0].Name)
	assert.Equal(t, "Manage widgets.", specs.Public.Tags[0].Description)

	opts := testOptions(t)
	opts.TagDescriptions = nil
	_, err = Build(fixtureOps(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tag "Widgets" has no description`)
}

func TestPagedOperationsListPageParameters(t *testing.T) {
	ops := fixtureOps()
	ops[1].Page = &Page{DefaultSize: 20, MaxSize: 100}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	byName := map[string]*openapi3.Parameter{}
	for _, p := range specs.Full.Paths.Value(ops[1].Path).GetOperation(ops[1].Method).Parameters {
		byName[p.Value.Name] = p.Value
	}
	assert.EqualValues(t, 1, byName["pageNumber"].Schema.Value.Default)
	assert.EqualValues(t, 20, byName["pageSize"].Schema.Value.Default)
	assert.EqualValues(t, 100, *byName["pageSize"].Schema.Value.Max)
}

func TestNoContentTooAddsA204Response(t *testing.T) {
	ops := fixtureOps()
	ops[2].NoContentToo = true
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	responses := specs.Full.Paths.Value(ops[2].Path).GetOperation(ops[2].Method).Responses
	assert.NotNil(t, responses.Value("204"))
	assert.NotNil(t, responses.Value("200"))
}

func TestCreatedTooAddsA201WithTheSuccessBody(t *testing.T) {
	ops := fixtureOps()
	ops[2].CreatedToo = true
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	responses := specs.Full.Paths.Value(ops[2].Path).GetOperation(ops[2].Method).Responses
	require.NotNil(t, responses.Value("201"))
	require.NotNil(t, responses.Value("200"))
	created := responses.Value("201").Value.Content["application/json"].Schema.Ref
	assert.NotEmpty(t, created)
	assert.Equal(t, responses.Value("200").Value.Content["application/json"].Schema.Ref, created)
}

func TestExtraErrorsAreAddedToTheStandardOnes(t *testing.T) {
	ops := fixtureOps()
	ops[2].ExtraErrors = []int{502, 503}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	responses := specs.Full.Paths.Value(ops[2].Path).GetOperation(ops[2].Method).Responses
	assert.NotNil(t, responses.Value("502"))
	assert.NotNil(t, responses.Value("503"))
	assert.NotNil(t, responses.Value("404"))
}

func TestRequestFieldsAreRequiredByTheirValidateRules(t *testing.T) {
	ops := []Operation{{Method: "POST", Path: "/a", Stability: StabilityDraft, OperationID: "createA", Summary: "Create a",
		Access: testAccess, Description: "Creates a.", Tags: []string{"A"}, Request: reflect.TypeOf(openapifixtures.RuleRequest{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	assert.Equal(t, []string{"name"}, specs.Full.Components.Schemas["RuleRequest"].Value.Required)
}

func TestARequestPointerTheHandlerRequiresIsNotNullable(t *testing.T) {
	op := bodyOp("POST", "/a", "postA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.HandlerCheckedRequest{}) })
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	schema := specs.Full.Components.Schemas["HandlerCheckedRequest"].Value
	assert.Equal(t, []string{"model"}, schema.Required)
	assert.False(t, schema.Properties["model"].Value.Nullable, "a null the handler refuses must not be offered")
}

func TestARequiredRequestFieldTaggedClearableStaysNullable(t *testing.T) {
	op := bodyOp("POST", "/a", "postA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.RequiredButNullableRequest{}) })
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	schema := specs.Full.Components.Schemas["RequiredButNullableRequest"].Value
	assert.Equal(t, []string{"limit"}, schema.Required)
	assert.True(t, schema.Properties["limit"].Value.Nullable, "a required field the handler accepts as null must offer null")
}

func TestAWrappedRequestFieldTakesTheRequestRules(t *testing.T) {
	opts := testOptions(t)
	opts.Unwrap = func(t reflect.Type) (reflect.Type, bool) {
		if t == reflect.TypeOf(openapifixtures.Optional[openapifixtures.RuleRequest]{}) {
			return reflect.TypeOf(openapifixtures.RuleRequest{}), true
		}
		return nil, false
	}
	op := bodyOp("POST", "/a", "postA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.ChangeRule{}) })
	specs, err := Build([]Operation{op}, opts)
	require.NoError(t, err)

	rule := specs.Full.Components.Schemas["ChangeRule"].Value.Properties["rule"].Value
	assert.Equal(t, []string{"name"}, rule.Required, "a wrapped request field is required by its validate rules, not by its json tags")
}

func TestANullablePointerToAStructKeepsItsReference(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
		Access: testAccess, Description: "Returns a.", Tags: []string{"A"}, Response: reflect.TypeOf(openapifixtures.Holder{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	owner := specs.Full.Components.Schemas["Holder"].Value.Properties["owner"].Value
	assert.True(t, owner.Nullable)
	require.Len(t, owner.AllOf, 1)
	assert.Equal(t, "#/components/schemas/Owner", owner.AllOf[0].Ref)
}

func TestANullableTagLetsAListOrMapBeNull(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
		Access: testAccess, Description: "Returns a.", Tags: []string{"A"}, Response: reflect.TypeOf(openapifixtures.Listing{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	listing := specs.Full.Components.Schemas["Listing"].Value
	assert.True(t, listing.Properties["labels"].Value.Nullable)
	assert.True(t, listing.Properties["owners"].Value.Nullable)
	assert.False(t, listing.Properties["tags"].Value.Nullable, "a list without the tag is never null")
}

func TestANullableTagOnAnOpenValueFailsGeneration(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
		Access: testAccess, Description: "Returns a.", Tags: []string{"A"}, Response: reflect.TypeOf(openapifixtures.OpenValue{})}}
	_, err := Build(ops, testOptions(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an open value already admits null")
}

func TestAnEnvelopePayloadStaysABareReferenceWithoutADescription(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
		Access: testAccess, Description: "Returns a.", Tags: []string{"A"}, Response: reflect.TypeOf(openapifixtures.Envelope{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	payload := specs.Full.Components.Schemas["Envelope"].Value.Properties["data"]
	assert.Equal(t, "#/components/schemas/Owner", payload.Ref)
}

func TestAPointerEnvelopePayloadIsNotNullable(t *testing.T) {
	ops := []Operation{{Method: "GET", Path: "/a", Stability: StabilityDraft, OperationID: "getA", Summary: "Get a",
		Access: testAccess, Description: "Returns a.", Tags: []string{"A"}, Response: reflect.TypeOf(openapifixtures.PointerEnvelope{})}}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	payload := specs.Full.Components.Schemas["PointerEnvelope"].Value.Properties["data"]
	assert.Equal(t, "#/components/schemas/Owner", payload.Ref)
}

func bodyOp(method, path, id string, mutate func(*Operation)) Operation {
	op := Operation{Method: method, Path: path, Stability: StabilityDraft, OperationID: id, Summary: "Do it",
		Access: testAccess, Description: "Does it.", Tags: []string{"A"}}
	mutate(&op)
	return op
}

func TestAFileResponseIsDocumentedAsA200WithABinaryBody(t *testing.T) {
	op := bodyOp("GET", "/a", "getA", func(o *Operation) {
		o.File = &RawContent{ContentTypes: []string{"application/zip", "application/json"}, Format: "binary", Attachment: true}
	})
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	get := specs.Full.Paths.Value("/a").Get
	assert.Nil(t, get.Responses.Value("204"))
	ok := get.Responses.Value("200").Value
	for _, contentType := range []string{"application/zip", "application/json"} {
		assert.Equal(t, "binary", ok.Content[contentType].Schema.Value.Format, contentType)
	}
	assert.Contains(t, ok.Headers, "Content-Disposition")
}

func TestAFormBodyIsMultipartWithABinaryFilePartAndNoQueryParameter(t *testing.T) {
	op := bodyOp("PUT", "/a", "putA", func(o *Operation) {
		o.Request = reflect.TypeOf(openapifixtures.Upload{})
		o.Form = &Form{Type: reflect.TypeOf(openapifixtures.Upload{}), Parts: []FormPart{
			{Name: "file", Field: "File", File: true}, {Name: "note", Field: "Note"}}}
	})
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	put := specs.Full.Paths.Value("/a").Put
	require.Len(t, put.RequestBody.Value.Content, 1)
	form := put.RequestBody.Value.Content["multipart/form-data"].Schema.Value
	assert.Equal(t, "binary", form.Properties["file"].Value.Format)
	assert.Equal(t, []string{"file"}, form.Required)
	assert.Equal(t, "string", form.Properties["note"].Value.Type.Slice()[0])
	assert.Empty(t, put.Parameters, "a form part is not also a query parameter")
	assert.NotContains(t, specs.Full.Components.Schemas, "FormDataUpload")
}

func TestAFormPartWithoutAFormDescriptionFailsGeneration(t *testing.T) {
	op := bodyOp("PUT", "/a", "putA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.Upload{}) })
	_, err := Build([]Operation{op}, testOptions(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a form part; describe the body with Form")
}

func TestARawBodyIsDocumentedAndAnUndescribedOneFailsGeneration(t *testing.T) {
	described := bodyOp("PUT", "/a/{id}", "putA", func(o *Operation) {
		o.Request = reflect.TypeOf(openapifixtures.Chunk{})
		o.RawBody = &RawBody{ContentType: "application/octet-stream", Description: "The chunk."}
	})
	specs, err := Build([]Operation{described}, testOptions(t))
	require.NoError(t, err)
	body := specs.Full.Paths.Value("/a/{id}").Put.RequestBody.Value
	assert.Equal(t, "binary", body.Content["application/octet-stream"].Schema.Value.Format)
	assert.True(t, body.Required)

	undescribed := bodyOp("PUT", "/a/{id}", "putA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.Chunk{}) })
	_, err = Build([]Operation{undescribed}, testOptions(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a raw body; describe it with RawBody")
}

func TestAPointerSentAsNullIsRequiredAndOmittableOnesAreNot(t *testing.T) {
	op := bodyOp("GET", "/a", "getA", func(o *Operation) { o.Response = reflect.TypeOf(openapifixtures.Maybe{}) })
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	maybe := specs.Full.Components.Schemas["Maybe"].Value
	assert.Equal(t, []string{"next"}, maybe.Required)
	assert.True(t, maybe.Properties["next"].Value.Nullable)
}

func TestARequestFieldTaggedRequiredIsRequired(t *testing.T) {
	op := bodyOp("POST", "/a", "postA", func(o *Operation) { o.Request = reflect.TypeOf(openapifixtures.Checked{}) })
	specs, err := Build([]Operation{op}, testOptions(t))
	require.NoError(t, err)

	assert.Equal(t, []string{"label"}, specs.Full.Components.Schemas["Checked"].Value.Required)
}

func TestPageNumberCarriesItsMaximum(t *testing.T) {
	ops := fixtureOps()
	ops[1].Page = &Page{DefaultSize: 20, MaxSize: 100, MaxNumber: 1_000_000}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)

	for _, p := range specs.Full.Paths.Value("/api/v2/widgets").Get.Parameters {
		if p.Value.Name == "pageNumber" {
			require.NotNil(t, p.Value.Schema.Value.Max)
			assert.EqualValues(t, 1_000_000, *p.Value.Schema.Value.Max)
			return
		}
	}
	t.Fatal("no pageNumber parameter")
}

func TestWholeNumbersAreWrittenWithoutAnExponent(t *testing.T) {
	ops := fixtureOps()
	ops[1].Page = &Page{DefaultSize: 20, MaxSize: 100, MaxNumber: 1_000_000}
	specs, err := Build(ops, testOptions(t))
	require.NoError(t, err)
	data, err := Marshal(specs.Full)
	require.NoError(t, err)

	assert.Contains(t, string(data), "maximum: 1000000")
	assert.NotContains(t, string(data), "e+")
}

func TestOutputDoesNotDependOnTheOrderOperationsAreGiven(t *testing.T) {
	render := func(ops []Operation) string {
		specs, err := Build(ops, testOptions(t))
		require.NoError(t, err)
		data, err := Marshal(specs.Full)
		require.NoError(t, err)
		return string(data)
	}
	forward := fixtureOps()
	reversed := fixtureOps()
	slices.Reverse(reversed)
	assert.Equal(t, render(forward), render(reversed))
}

func TestARepeatedProblemOnAPublishedOperationIsReportedOnce(t *testing.T) {
	op := bodyOp("GET", "/a", "getA", func(o *Operation) { o.Published = true; o.Stability = "" })
	_, err := Build([]Operation{op}, testOptions(t))
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "GET /a: unknown stability"), "full and public builds both report it")
}

func TestWriteSpecsLeavesTheOldPairWhenTheSecondRenameFails(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, WriteSpecs(dir, specs))
	before := map[string][]byte{}
	for _, name := range []string{FullSpecFile, PublicSpecFile} {
		before[name], err = os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
	}

	changed := fixtureOps()
	changed[0].Summary = "Create a widget, changed"
	next, err := Build(changed, testOptions(t))
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, PublicSpecFile)))
	require.NoError(t, os.Mkdir(filepath.Join(dir, PublicSpecFile), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, PublicSpecFile, "block"), nil, 0o644))

	require.Error(t, WriteSpecs(dir, next))

	got, err := os.ReadFile(filepath.Join(dir, FullSpecFile))
	require.NoError(t, err)
	assert.Equal(t, string(before[FullSpecFile]), string(got), "the first file is put back when the second cannot be replaced")
}

func TestProblemsAreReportedInPathOrderWhateverOrderOperationsAreGiven(t *testing.T) {
	broken := func(path string) Operation {
		return bodyOp("GET", path, "get"+strings.ReplaceAll(path, "/", ""), func(o *Operation) { o.Summary = "" })
	}
	_, err := Build([]Operation{broken("/c"), broken("/a"), broken("/b")}, testOptions(t))
	require.Error(t, err)
	msg := err.Error()
	assert.Less(t, strings.Index(msg, "GET /a:"), strings.Index(msg, "GET /b:"))
	assert.Less(t, strings.Index(msg, "GET /b:"), strings.Index(msg, "GET /c:"))
}

func TestWriteSpecsRefusesToReplaceAFileItCannotRead(t *testing.T) {
	specs, err := Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	dir := t.TempDir()
	unreadable := filepath.Join(dir, FullSpecFile)
	require.NoError(t, os.WriteFile(unreadable, []byte("keep"), 0o000))

	err = WriteSpecs(dir, specs)

	require.Error(t, err)
	require.NoError(t, os.Chmod(unreadable, 0o644))
	kept, readErr := os.ReadFile(unreadable)
	require.NoError(t, readErr)
	assert.Equal(t, "keep", string(kept))
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1, "no staged temp file is left behind")
}

func TestTagGroupsListEachGroupWithItsTags(t *testing.T) {
	opts := testOptions(t)
	opts.TagGroups = map[string]string{"Widgets": "Hardware"}
	specs, err := Build(fixtureOps(), opts)
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"name": "Hardware", "tags": []any{"Widgets"}}}, specs.Full.Extensions["x-tagGroups"])

	specs, err = Build(fixtureOps(), testOptions(t))
	require.NoError(t, err)
	assert.NotContains(t, specs.Full.Extensions, "x-tagGroups")
}

func TestTagGroupsAreSortedByNameWithTheirTagsSorted(t *testing.T) {
	opts := testOptions(t)
	opts.TagDescriptions["C"] = "C tag."
	opts.TagGroups = map[string]string{"Widgets": "Hardware", "D": "Assembly", "A": "Hardware", "C": "Assembly"}
	tagged := func(id, tag string) Operation {
		return Operation{Method: "GET", Path: "/api/v2/" + id, Published: true, Stability: StabilityStable, Tags: []string{tag},
			OperationID: id, Summary: "Get " + id, Description: "Returns " + id + ".", Access: testAccess,
			Response: reflect.TypeOf(openapifixtures.Widget{})}
	}
	ops := append(fixtureOps(), tagged("getD", "D"), tagged("getA", "A"), tagged("getC", "C"))

	specs, err := Build(ops, opts)

	require.NoError(t, err)
	assert.Equal(t, []any{
		map[string]any{"name": "Assembly", "tags": []any{"C", "D"}},
		map[string]any{"name": "Hardware", "tags": []any{"A", "Widgets"}},
	}, specs.Full.Extensions["x-tagGroups"])
}

func TestFieldDescriptionsDoNotRepeatTheFieldName(t *testing.T) {
	docs := testDocs(t)
	pkg := reflect.TypeOf(openapifixtures.Widget{}).PkgPath()

	assert.Equal(t, "Identifies the widget.", docs.Field(pkg, "Widget", "ID"))
	assert.Equal(t, "The display name.", docs.Field(pkg, "Widget", "Name"))
	assert.Equal(t, "How many parts the widget has.", docs.Field(pkg, "Widget", "Count"))
}

func TestFieldDescriptionsKeepTheWireSpellingOfALeadingValue(t *testing.T) {
	cases := []struct {
		name, doc, field, example, want string
	}{
		{"plural copula", "Tags are labels for the widget.", "Tags", "", "Labels for the widget."},
		{"name shared with another field", "Reason and ErrorID are for v2.", "Reason", "", "Reason and ErrorID are for v2."},
		{"verb after the name", "Owner decides who may edit.", "Owner", "", "Decides who may edit."},
		{"snake_case value", "Status is in_sync when nothing changed.", "Status", "in_sync", "in_sync when nothing changed."},
		{"value listed with or", "Format is csv or json; leave it out for csv.", "Format", "csv", "csv or json; leave it out for csv."},
		{"value that is the example", "Mode is discovery to read the settings.", "Mode", "discovery", "discovery to read the settings."},
		{"another field's name", "Total is inputTokens plus outputTokens.", "Total", "", "inputTokens plus outputTokens."},
		{"query sort value", "Sort is createdTime:asc or createdTime:desc.", "Sort", "", "createdTime:asc or createdTime:desc."},
		{"JSON literal", "Signed is true when the card is signed.", "Signed", "true", "True when the card is signed."},
		{"condition after a comma", "TestMode, when true, writes a scope.", "TestMode", "", "When true, writes a scope."},
		{"list after a comma", "Ops, terms and rest are filled first.", "Ops", "", "Ops, terms and rest are filled first."},
		{"other text", "The widget's name.", "Name", "", "The widget's name."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, withoutGoName(tc.doc, tc.field, tc.example))
		})
	}
}
