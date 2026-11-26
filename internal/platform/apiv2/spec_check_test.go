// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type specCheck struct {
	router  routers.Router
	options *openapi3filter.Options
}

var (
	sharedSpecCheckOnce sync.Once
	sharedSpecCheck     *specCheck
	sharedSpecCheckErr  error

	checkedOperations sync.Map
)

const muxNotFound = "404 page not found\n"

func specCheckFor(t *testing.T) *specCheck {
	t.Helper()
	full := buildSpecs(t).Full
	sharedSpecCheckOnce.Do(func() {
		sharedSpecCheck, sharedSpecCheckErr = newSpecCheck(full)
	})
	require.NoError(t, sharedSpecCheckErr)
	return sharedSpecCheck
}

func newSpecCheck(full *openapi3.T) (*specCheck, error) {
	raw, err := full.MarshalJSON()
	if err != nil {
		return nil, err
	}
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		return nil, err
	}
	openValuesAdmitNull(doc)
	routeWildcards(doc)
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &specCheck{router: router, options: &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		MultiError:            true,
		SkipSettingDefaults:   true,
	}}, nil
}

func openValuesAdmitNull(doc *openapi3.T) {
	seen := map[*openapi3.Schema]bool{}
	var visit func(*openapi3.SchemaRef)
	visit = func(ref *openapi3.SchemaRef) {
		if ref == nil || ref.Value == nil || seen[ref.Value] {
			return
		}
		schema := ref.Value
		seen[schema] = true
		if schema.IsEmpty() {
			schema.Nullable = true
			return
		}
		for _, property := range schema.Properties {
			visit(property)
		}
		visit(schema.Items)
		visit(schema.AdditionalProperties.Schema)
		for _, group := range []openapi3.SchemaRefs{schema.AllOf, schema.AnyOf, schema.OneOf} {
			for _, member := range group {
				visit(member)
			}
		}
	}
	for _, schema := range doc.Components.Schemas {
		visit(schema)
	}
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			if op.RequestBody != nil {
				for _, media := range op.RequestBody.Value.Content {
					visit(media.Schema)
				}
			}
			for _, response := range op.Responses.Map() {
				for _, media := range response.Value.Content {
					visit(media.Schema)
				}
			}
		}
	}
}

var wildcardPathParam = regexp.MustCompile(`\{([^}/.]+)\.\.\.\}`)

func routeWildcards(doc *openapi3.T) {
	for _, op := range OpenAPIOperations() {
		documented := wildcardPathParam.ReplaceAllString(op.Path, "{$1}")
		item := doc.Paths.Value(documented)
		if documented == op.Path || item == nil {
			continue
		}
		doc.Paths.Delete(documented)
		doc.Paths.Set(wildcardPathParam.ReplaceAllString(op.Path, "{$1*}"), item)
	}
}

func checkedAgainstSpec(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	check := specCheckFor(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, platpkg.APIV2Prefix+"/") {
			next.ServeHTTP(w, r)
			return
		}
		route, params, described := check.find(r)
		if !described {
			sent := &sentResponse{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sent, r)
			if sent.status != http.StatusNotFound && sent.status != http.StatusMethodNotAllowed {
				t.Errorf("%s %s answered %d, but the spec describes no such operation", r.Method, r.URL.Path, sent.status)
			}
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the body of %s %s: %v", r.Method, r.URL.Path, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sent := &sentResponse{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sent, r)
		if sent.status == http.StatusNotFound && sent.body.String() == muxNotFound {
			return
		}
		checkedOperations.Store(route.Operation.OperationID, true)
		if err := check.exchange(route, params, r, body, sent.status, w.Header(), sent.body.Bytes()); err != nil {
			t.Error(err)
		}
	})
}

func (c *specCheck) find(r *http.Request) (*routers.Route, map[string]string, bool) {
	route, params, err := c.router.FindRoute(r)
	return route, params, err == nil
}

func (c *specCheck) exchange(route *routers.Route, params map[string]string, r *http.Request, reqBody []byte, status int, header http.Header, respBody []byte) error {
	ctx := context.Background()
	sent := r.Clone(ctx)
	sent.Body = io.NopCloser(bytes.NewReader(reqBody))
	in := &openapi3filter.RequestValidationInput{Request: sent, PathParams: params, Route: route, Options: c.options}
	var problems []string
	if status >= 200 && status < 300 {
		if err := openapi3filter.ValidateRequest(ctx, in); err != nil {
			for _, m := range mismatches(err) {
				problems = append(problems, "request "+m)
			}
		}
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: status, Header: header, Options: c.options}
	out.SetBodyBytes(respBody)
	if err := openapi3filter.ValidateResponse(ctx, out); err != nil {
		for _, m := range mismatches(err) {
			problems = append(problems, "response "+m)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s %s (%s) answered %d, which the spec does not describe:\n\t%s",
		r.Method, r.URL.Path, route.Operation.OperationID, status, strings.Join(problems, "\n\t"))
}

func mismatches(err error) []string {
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		var all []string
		for _, e := range multi {
			all = append(all, mismatches(e)...)
		}
		return all
	}
	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		return []string{"/" + strings.Join(schemaErr.JSONPointer(), "/") + ": " + schemaErr.Reason}
	}
	return []string{err.Error()}
}

type sentResponse struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (s *sentResponse) WriteHeader(status int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = status, true
	}
	s.ResponseWriter.WriteHeader(status)
}

func (s *sentResponse) Write(p []byte) (int, error) {
	s.wroteHeader = true
	s.body.Write(p)
	return s.ResponseWriter.Write(p)
}

func (s *sentResponse) Flush() {
	_ = http.NewResponseController(s.ResponseWriter).Flush()
}

func (s *sentResponse) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func TestSpecCheckCatchesBrokenResponses(t *testing.T) {
	check := specCheckFor(t)
	send := func(status int, body string) error {
		req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/version", nil)
		route, params, described := check.find(req)
		require.True(t, described, "the spec no longer describes GET /version; pick another operation")
		header := http.Header{"Content-Type": {"application/json"}}
		return check.exchange(route, params, req, nil, status, header, []byte(body))
	}
	version := func(products string) string {
		return `{"data":{"apiVersion":"2026-09-29","minClientVersion":null,"products":` + products + `}}`
	}
	product := `[{"key":"sam-gateway-go","name":"Solace Agent Mesh","version":"v2.455.1"}]`
	require.NoError(t, send(http.StatusOK, version(product)), "the check rejects a response the spec describes")

	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"a field of the wrong type", http.StatusOK, version(`[{"key":"sam-gateway-go","name":7,"version":"v2.455.1"}]`), "/data/products/0/name"},
		{"an undocumented status", http.StatusTeapot, version(product), "status is not supported"},
		{"null in a field that is not nullable", http.StatusOK, version("null"), "/data/products"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := send(tc.status, tc.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "getVersion")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSpecCheckCatchesBrokenRequests(t *testing.T) {
	check := specCheckFor(t)
	requestProblems := func(method, path, body string, status int) string {
		req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		route, params, described := check.find(req)
		require.True(t, described, "the spec no longer describes %s %s; pick another operation", method, path)
		err := check.exchange(route, params, req, []byte(body), status, http.Header{"Content-Type": {"application/json"}}, []byte(`{}`))
		if err == nil {
			return ""
		}
		var problems []string
		for line := range strings.SplitSeq(err.Error(), "\n\t") {
			if strings.HasPrefix(line, "request ") {
				problems = append(problems, line)
			}
		}
		return strings.Join(problems, "\n")
	}
	badRole := `{"name":7}`
	assert.Contains(t, requestProblems(http.MethodPost, "/rbac/roles", badRole, http.StatusCreated), "request /name",
		"the check accepts a request the spec rejects when the server answers 2xx")
	assert.Empty(t, requestProblems(http.MethodPost, "/rbac/roles", badRole, http.StatusUnprocessableEntity),
		"the check rejects a request the server refused")
}

func TestSpecDescribesSignInRefusals(t *testing.T) {
	check := specCheckFor(t)
	req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/agents", nil)
	route, params, described := check.find(req)
	require.True(t, described, "the spec no longer describes GET /agents; pick another operation")
	errorType := route.Operation.Responses.Status(http.StatusUnauthorized).Value.Content.Get("application/json").Schema.Value.Properties["errorType"].Value

	for _, refusal := range []struct {
		status  int
		errType auth.ErrorType
	}{
		{http.StatusUnauthorized, auth.ErrorAuthenticationRequired},
		{http.StatusUnauthorized, auth.ErrorInvalidToken},
		{http.StatusUnauthorized, auth.ErrorSessionExpired},
		{http.StatusForbidden, auth.ErrorAuthorizationFailure},
	} {
		t.Run(string(refusal.errType), func(t *testing.T) {
			sent := httptest.NewRecorder()
			auth.WriteError(sent, refusal.status, refusal.errType, "refused")
			assert.NoError(t, check.exchange(route, params, req, nil, sent.Code, sent.Header(), sent.Body.Bytes()))
			decoder := json.NewDecoder(bytes.NewReader(sent.Body.Bytes()))
			decoder.DisallowUnknownFields()
			var body entplatform.AuthError
			assert.NoError(t, decoder.Decode(&body), "the sign-in check sends a field the spec does not describe")
			assert.Contains(t, errorType.Description, string(refusal.errType), "the spec does not list this errorType")
		})
	}
}

func TestSpecCheckCoverageRunsAfterSequentialTests(t *testing.T) {
	t.Parallel()
	var unchecked []string
	total := 0
	for path, item := range buildSpecs(t).Full.Paths.Map() {
		for method, op := range item.Operations() {
			total++
			if _, checked := checkedOperations.Load(op.OperationID); !checked {
				unchecked = append(unchecked, op.OperationID+" ("+method+" "+path+")")
			}
		}
	}
	slices.Sort(unchecked)
	t.Logf("%d of %d operations got no checked response:\n%s", len(unchecked), total, strings.Join(unchecked, "\n"))
}
