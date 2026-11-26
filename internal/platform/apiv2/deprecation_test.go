// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/openapigen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const widgetID = "019f9b46-77bc-76ba-a494-017487b5f5c4"

type widget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type getWidgetRequest struct {
	ID string `path:"id" json:"-"`
}

const testNotice = "https://docs.example.com/deprecations/widgets"

func widgetRoute(d *httputil.Deprecation) Route {
	return Route{
		Method:      http.MethodGet,
		Path:        "/widgets/{id}",
		Scope:       rbac.ScopeAgentBuilderRead,
		Stability:   StabilityDraft,
		Deprecation: d,
		Handler: Handle(func(_ context.Context, req getWidgetRequest) (widget, error) {
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to get widget"),
	}
}

func getWidget(t *testing.T, route Route, id string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	register(mux, nil, false, []Route{route})
	req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/widgets/"+id, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "u", Authenticated: true, Scopes: []string{rbac.ScopeAgentBuilderRead},
	})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDeprecatedRouteAnnouncesItself(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	rec := getWidget(t, widgetRoute(&httputil.Deprecation{NoticeURL: testNotice, Since: since}), widgetID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, testNotice, rec.Header().Get("X-Solace-API-Deprecated"))
	assert.Equal(t, "@1790812800", rec.Header().Get("Deprecation"))
	assert.Equal(t, `<`+testNotice+`>; rel="deprecation"`, rec.Header().Get("Link"))
}

func TestDeprecationWithoutSinceOmitsTheDeprecationHeader(t *testing.T) {
	rec := getWidget(t, widgetRoute(&httputil.Deprecation{NoticeURL: testNotice}), widgetID)

	assert.Equal(t, testNotice, rec.Header().Get("X-Solace-API-Deprecated"))
	assert.Empty(t, rec.Header().Get("Deprecation"))
}

type countedMetric struct {
	name   string
	labels []string
}

type recordingMetrics struct {
	runtime.NoopMetrics
	counters []countedMetric
}

func (m *recordingMetrics) Counter(name string, _ int64, labels ...string) {
	m.counters = append(m.counters, countedMetric{name: name, labels: labels})
}

func TestDeprecatedRouteCountsEachRequestByRouteAndKind(t *testing.T) {
	for name, c := range map[string]struct {
		deprecation *httputil.Deprecation
		wantKind    string
	}{
		"the route is deprecated":             {&httputil.Deprecation{NoticeURL: testNotice}, "route"},
		"only a returned field is deprecated": {&httputil.Deprecation{NoticeURL: testNotice, FieldsOnly: true}, "fields"},
	} {
		t.Run(name, func(t *testing.T) {
			metrics := &recordingMetrics{}
			runtime.Init(metrics)
			t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

			rec := getWidget(t, widgetRoute(c.deprecation), widgetID)

			assert.Equal(t, []countedMetric{{
				name:   "api.deprecated.requests",
				labels: []string{"route.template", "GET " + platpkg.APIV2Prefix + "/widgets/{id}", "deprecation.kind", c.wantKind},
			}}, metrics.counters)
			assert.Equal(t, testNotice, rec.Header().Get("X-Solace-API-Deprecated"))
		})
	}
}

func TestRouteWithoutDeprecationCountsNothing(t *testing.T) {
	metrics := &recordingMetrics{}
	runtime.Init(metrics)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

	getWidget(t, widgetRoute(nil), widgetID)

	assert.Empty(t, metrics.counters)
}

func TestRouteWithoutDeprecationAddsNoHeaders(t *testing.T) {
	rec := getWidget(t, widgetRoute(nil), widgetID)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, h := range []string{"X-Solace-API-Deprecated", "Deprecation", "Link"} {
		assert.Empty(t, rec.Header().Get(h), h)
	}
}

func TestDeprecatedRouteAnnouncesItselfOnErrors(t *testing.T) {
	rec := getWidget(t, widgetRoute(&httputil.Deprecation{NoticeURL: testNotice, Since: time.Unix(1, 0)}), "not-a-uuid")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, testNotice, rec.Header().Get("X-Solace-API-Deprecated"))
}

func TestDeprecationHeadersMatchTheSpec(t *testing.T) {
	ops := map[string]*openapi3.Operation{}
	for path, item := range buildSpecs(t).Full.Paths.Map() {
		for method, op := range item.Operations() {
			ops[method+" "+path] = op
		}
	}

	for _, rt := range specRoutes() {
		op := ops[rt.Method+" "+platpkg.APIV2Prefix+strings.ReplaceAll(rt.Path, "...}", "}")]
		require.NotNil(t, op, "%s %s", rt.Method, rt.Path)
		assert.Equal(t, operationIsDeprecated(op), rt.Deprecation != nil,
			"%s %s: Route.Deprecation and the spec disagree about whether the route is deprecated", rt.Method, rt.Path)
		assert.Equal(t, op.Deprecated, rt.Deprecation != nil && !rt.Deprecation.FieldsOnly,
			"%s %s: Route.Deprecation.FieldsOnly must be set exactly when the spec marks only fields deprecated, not the operation", rt.Method, rt.Path)
	}
}

func operationIsDeprecated(op *openapi3.Operation) bool {
	if op.Deprecated {
		return true
	}
	seen := map[*openapi3.Schema]bool{}
	for _, p := range op.Parameters {
		if p.Value.Deprecated || schemaIsDeprecated(p.Value.Schema, seen) {
			return true
		}
	}
	if op.RequestBody != nil {
		for _, media := range op.RequestBody.Value.Content {
			if schemaIsDeprecated(media.Schema, seen) {
				return true
			}
		}
	}
	for _, resp := range op.Responses.Map() {
		for _, media := range resp.Value.Content {
			if schemaIsDeprecated(media.Schema, seen) {
				return true
			}
		}
	}
	return false
}

func schemaIsDeprecated(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return false
	}
	seen[ref.Value] = true
	s := ref.Value
	if s.Deprecated {
		return true
	}
	children := []*openapi3.SchemaRef{s.Items, s.AdditionalProperties.Schema}
	children = append(children, s.AllOf...)
	children = append(children, s.OneOf...)
	children = append(children, s.AnyOf...)
	for _, child := range s.Properties {
		children = append(children, child)
	}
	for _, child := range children {
		if schemaIsDeprecated(child, seen) {
			return true
		}
	}
	return false
}

func specOperation(t *testing.T, h Handler, text string) *openapi3.Operation {
	t.Helper()
	rt := specRow(http.MethodGet, "/items/{id}", "item:_:read", "getItem", h)
	rt.Doc.Text += text
	routes := []Route{rt}
	opts := specOptions(routes)
	opts.Docs = deprecatingDocs{}
	specs, err := openapigen.Build(operationsOf(routes), opts)
	require.NoError(t, err)
	return operationAt(t, specs.Full, http.MethodGet, "/items/{id}")
}

type deprecatingDocs struct{}

const deprecatedFieldComment = "Old is the pre-rename name.\n\nDeprecated: Reason: replaced by name. Removal Date: 2027-06-01."

func (deprecatingDocs) Type(_, _ string) string { return "A documented type." }

func (deprecatingDocs) Field(_, typeName, field string) string {
	if (typeName == "deprecatedParamRequest" && field == "Old") || (typeName == "deprecatedFieldItem" && field == "Legacy") {
		return deprecatedFieldComment
	}
	return "A documented field."
}

type deprecatedParamRequest struct {
	ID  string `path:"id" json:"-"`
	Old string `query:"old" json:"-" example:"x"`
}

type deprecatedFieldItem struct {
	ID     string `json:"id" example:"w"`
	Type   string `json:"type" example:"item"`
	Legacy string `json:"legacy" example:"old"`
}

const deprecatedParagraph = "\n\nDeprecated: Reason: replaced by the items list. Removal Date: 2027-06-01."

func plainItem(context.Context, specGetRequest) (specItem, error) { return specItem{}, nil }

func TestAnOperationIsDeprecatedWhenItsTextOrAnyOfItsSchemasSaySo(t *testing.T) {
	assert.False(t, operationIsDeprecated(specOperation(t, Handle(plainItem, "Unable to get"), "")),
		"the fixture itself deprecates nothing")

	byParameter := specOperation(t, Handle(func(context.Context, deprecatedParamRequest) (specItem, error) { return specItem{}, nil }, "Unable to get"), "")
	assert.True(t, operationIsDeprecated(byParameter), "a deprecated query parameter deprecates the operation")

	byResponseField := specOperation(t, Handle(func(context.Context, specGetRequest) (deprecatedFieldItem, error) {
		return deprecatedFieldItem{}, nil
	}, "Unable to get"), "")
	assert.True(t, operationIsDeprecated(byResponseField), "a deprecated response field deprecates the operation")

	byText := specOperation(t, Handle(plainItem, "Unable to get"), deprecatedParagraph)
	assert.True(t, operationIsDeprecated(byText), "a Deprecated paragraph in the text deprecates the operation")
}

func TestARouteDeprecatedOnlyInTheSpecReadsAsADisagreement(t *testing.T) {
	deprecatedInSpec := operationIsDeprecated(specOperation(t, Handle(plainItem, "Unable to get"), deprecatedParagraph))

	assert.NotEqual(t, deprecatedInSpec, widgetRoute(nil).Deprecation != nil,
		"deprecated in the spec but announcing nothing must differ")
	assert.Equal(t, deprecatedInSpec, widgetRoute(&httputil.Deprecation{NoticeURL: testNotice}).Deprecation != nil,
		"deprecated on both sides must agree")
}
