// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func newArrayQueryParam(name, style string, explode bool) paramSpec {
	p := paramSpec{
		OriginalName: name,
		PyName:       pyNameFor(name),
		In:           locQuery,
		Style:        style,
		Explode:      explode,
		Schema: &openapi3.Schema{
			Type:  &openapi3.Types{openapi3.TypeArray},
			Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
		},
	}
	p.SchemaJSON = renderSchema(p.Schema, "")
	return p
}

func execQueryStyle(t *testing.T, p paramSpec, value any) string {
	t.Helper()
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		name:         "q",
		method:       "GET",
		pathTemplate: "/q",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl:         operationTemplate{Params: []paramSpec{p}},
	}
	_, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		p.PyName: value,
	})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	return rawQuery
}

func TestQueryArrayStyles(t *testing.T) {
	values := []any{"a", "b", "c"}
	cases := []struct {
		name    string
		style   string
		explode bool
		want    string
	}{
		{"form explode", "form", true, "tags=a&tags=b&tags=c"},
		{"form no-explode", "form", false, "tags=a,b,c"},
		{"spaceDelimited explode", "spaceDelimited", true, "tags=a&tags=b&tags=c"},
		{"pipeDelimited explode", "pipeDelimited", true, "tags=a&tags=b&tags=c"},
		{"pipeDelimited no-explode", "pipeDelimited", false, "tags=a|b|c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newArrayQueryParam("tags", c.style, c.explode)
			got := execQueryStyle(t, p, values)
			if got != c.want {
				t.Errorf("style=%s explode=%v: got %q, want %q",
					c.style, c.explode, got, c.want)
			}
		})
	}
}

func TestQueryParamSentOnlyWhenArgumentCarriesAValue(t *testing.T) {
	cases := []struct {
		name       string
		schemaType string
		value      any
		want       string
	}{
		{"boolean false is sent", openapi3.TypeBoolean, false, "p=false"},
		{"boolean true is sent", openapi3.TypeBoolean, true, "p=true"},
		{"integer zero decoded from JSON is sent", openapi3.TypeInteger, float64(0), "p=0"},
		{"integer five decoded from JSON is sent", openapi3.TypeInteger, float64(5), "p=5"},
		{"number one and a half is sent", openapi3.TypeNumber, 1.5, "p=1.5"},
		{"Go int zero is sent", openapi3.TypeInteger, 0, "p=0"},
		{"int64 zero is sent", openapi3.TypeInteger, int64(0), "p=0"},
		{"null is left out", openapi3.TypeBoolean, nil, ""},
		{"empty string is left out", openapi3.TypeString, "", ""},
		{"empty array is left out", openapi3.TypeArray, []any{}, ""},
		{"empty object is left out", openapi3.TypeObject, map[string]any{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := paramSpec{
				OriginalName: "p",
				PyName:       "p",
				In:           locQuery,
				Style:        "form",
				Explode:      true,
				Schema:       &openapi3.Schema{Type: &openapi3.Types{c.schemaType}},
			}
			got := execQueryStyle(t, p, c.value)
			if got != c.want {
				t.Errorf("ExecuteTool(p=%#v) sent query %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestRenderQuery_SpaceDelimitedNoExplode(t *testing.T) {
	tmpl := operationTemplate{Params: []paramSpec{
		newArrayQueryParam("tags", "spaceDelimited", false),
	}}
	got, err := renderQuery(tmpl, map[string]any{"tags": []any{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "tags=a b c" {
		t.Errorf("got %q, want %q", got, "tags=a b c")
	}
}

func TestQueryDeepObject(t *testing.T) {
	p := paramSpec{
		OriginalName: "filter",
		PyName:       "filter",
		In:           locQuery,
		Style:        "deepObject",
		Explode:      true,
		Schema:       &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}},
	}
	got := execQueryStyle(t, p, map[string]any{"name": "foo", "age": 30})
	u := unescape(got)
	if !strings.Contains(u, "filter[name]=foo") {
		t.Errorf("expected filter[name]=foo, got %q", u)
	}
	if !strings.Contains(u, "filter[age]=30") {
		t.Errorf("expected filter[age]=30, got %q", u)
	}
}

func TestQueryDeepObjectNested(t *testing.T) {
	p := paramSpec{
		OriginalName: "filter",
		PyName:       "filter",
		In:           locQuery,
		Style:        "deepObject",
		Explode:      true,
		Schema:       &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}},
	}
	got := execQueryStyle(t, p, map[string]any{
		"tags":  []any{"a", "b"},
		"owner": map[string]any{"name": "foo"},
	})
	u := unescape(got)
	for _, want := range []string{"filter[tags][0]=a", "filter[tags][1]=b", "filter[owner][name]=foo"} {
		if !strings.Contains(u, want) {
			t.Errorf("expected %s, got %q", want, u)
		}
	}
}

func TestQueryPreservesOriginalName(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "GET",
		pathTemplate: "/x",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{{
				OriginalName: "userId",
				PyName:       "user_id",
				In:           locQuery,
				Style:        "form",
				Explode:      true,
				Schema:       makeStringSchema(),
			}},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"user_id": "42",
	}); err != nil {
		t.Fatal(err)
	}
	if raw != "userId=42" {
		t.Errorf("expected query 'userId=42', got %q", raw)
	}
}

func TestHeaderParams(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Request-Id")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "GET",
		pathTemplate: "/x",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{{
				OriginalName: "X-Request-Id",
				PyName:       "x_request_id",
				In:           locHeader,
				Style:        "simple",
				Schema:       makeStringSchema(),
			}},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"x_request_id": "abc-123",
	}); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "abc-123" {
		t.Errorf("expected X-Request-Id=abc-123, got %q", gotHeader)
	}
}

func TestCookieParams(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "GET",
		pathTemplate: "/x",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{
				{
					OriginalName: "session",
					PyName:       "session",
					In:           locCookie,
					Style:        "form",
					Explode:      true,
					Schema:       makeStringSchema(),
				},
				{
					OriginalName: "lang",
					PyName:       "lang",
					In:           locCookie,
					Style:        "form",
					Explode:      true,
					Schema:       makeStringSchema(),
				},
			},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"session": "sess123",
		"lang":    "en-us",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotCookie, "session=sess123") {
		t.Errorf("missing session cookie: %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "lang=en-us") {
		t.Errorf("missing lang cookie: %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "; ") {
		t.Errorf("expected '; ' separator: %q", gotCookie)
	}
}

func TestPathParamCamelCaseRoundTrip(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "GET",
		pathTemplate: "/users/{userId}",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{{
				OriginalName: "userId",
				PyName:       "user_id",
				In:           locPath,
				Style:        "simple",
				Required:     true,
				Schema:       makeStringSchema(),
			}},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"user_id": "42",
	}); err != nil {
		t.Fatal(err)
	}
	if gotURI != "/users/42" {
		t.Errorf("expected /users/42, got %q", gotURI)
	}
}

func TestFormURLEncodedBody(t *testing.T) {
	var bodyStr, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		bodyStr = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "POST",
		pathTemplate: "/form",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "application/x-www-form-urlencoded",
			BodyShape: "object",
			Params: []paramSpec{
				{
					OriginalName: "userName",
					PyName:       "user_name",
					In:           locBody,
					Schema:       makeStringSchema(),
				},
				{
					OriginalName: "age",
					PyName:       "age",
					In:           locBody,
					Schema:       &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}},
				},
			},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"user_name": "bob",
		"age":       30,
	}); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if !strings.Contains(bodyStr, "userName=bob") {
		t.Errorf("body = %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "age=30") {
		t.Errorf("body = %q", bodyStr)
	}
}

func TestOctetStreamBody(t *testing.T) {
	var received []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		received, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "POST",
		pathTemplate: "/raw",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "application/octet-stream",
			BodyShape: "primitive",
			Params: []paramSpec{{
				OriginalName: "",
				PyName:       "body",
				In:           locBody,
				Schema: &openapi3.Schema{
					Type:   &openapi3.Types{openapi3.TypeString},
					Format: "binary",
				},
			}},
		},
	}
	payload := []byte{0x00, 0x01, 0x02, 0xff}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"body": payload,
	}); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if string(received) != string(payload) {
		t.Errorf("body = %v, want %v", received, payload)
	}
}

func TestTextPlainBody(t *testing.T) {
	var received []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		received, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "POST",
		pathTemplate: "/txt",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "text/plain",
			BodyShape: "primitive",
			Params: []paramSpec{{
				OriginalName: "",
				PyName:       "body",
				In:           locBody,
				Schema:       makeStringSchema(),
			}},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"body": "hello world",
	}); err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if string(received) != "hello world" {
		t.Errorf("body = %q", received)
	}
}

func TestMultipartBody_WithFile(t *testing.T) {
	var bodyStr, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		bodyStr = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "POST",
		pathTemplate: "/upload",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "multipart/form-data",
			BodyShape: "object",
			Params: []paramSpec{
				{
					OriginalName: "caption",
					PyName:       "caption",
					In:           locBody,
					Schema:       makeStringSchema(),
				},
				{
					OriginalName: "file",
					PyName:       "file",
					In:           locBody,
					Schema: &openapi3.Schema{
						Type:   &openapi3.Types{openapi3.TypeString},
						Format: "binary",
					},
				},
			},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"caption": "cat",
		"file":    []byte("binary data"),
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q", contentType)
	}
	if !strings.Contains(bodyStr, "form-data; name=\"caption\"") {
		t.Errorf("missing caption part: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "form-data; name=\"file\"; filename=\"file\"") {
		t.Errorf("missing file part header: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "binary data") {
		t.Errorf("missing file content: %q", bodyStr)
	}
}

func TestMultipartBody_ArtifactURI(t *testing.T) {
	var bodyStr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodyStr = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "POST",
		pathTemplate: "/upload",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			BodyMIME:  "multipart/form-data",
			BodyShape: "object",
			Params: []paramSpec{{
				OriginalName: "file",
				PyName:       "file",
				In:           locBody,
				Schema: &openapi3.Schema{
					Type:   &openapi3.Types{openapi3.TypeString},
					Format: "binary",
				},
			}},
		},
	}

	resolver := func(ctx context.Context, uri string) ([]byte, string, string, error) {
		if uri != "artifact://logo.png" {
			t.Fatalf("unexpected uri: %q", uri)
		}
		return []byte("pngdata"), "logo.png", "image/png", nil
	}
	if _, err := mgr.ExecuteToolWithArtifacts(context.Background(), tool, map[string]any{
		"file": "artifact://logo.png",
	}, resolver); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodyStr, "filename=\"logo.png\"") {
		t.Errorf("expected filename=logo.png, got: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "Content-Type: image/png") {
		t.Errorf("expected Content-Type image/png, got: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "pngdata") {
		t.Errorf("expected pngdata in body, got: %q", bodyStr)
	}
}

func TestEmptyQueryValuesFiltered(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tool := &OpenAPITool{
		method:       "GET",
		pathTemplate: "/x",
		baseURL:      srv.URL,
		manager:      mgr,
		logger:       slog.Default(),
		tmpl: operationTemplate{
			Params: []paramSpec{
				{OriginalName: "a", PyName: "a", In: locQuery, Style: "form", Explode: true, Schema: makeStringSchema()},
				{OriginalName: "b", PyName: "b", In: locQuery, Style: "form", Explode: true, Schema: makeStringSchema()},
			},
		},
	}
	if _, err := mgr.ExecuteTool(context.Background(), tool, map[string]any{
		"a": "x",
		"b": "",
	}); err != nil {
		t.Fatal(err)
	}
	if raw != "a=x" {
		t.Errorf("expected 'a=x', got %q", raw)
	}
}

func TestBuildRequestBody_JSONPreservesOriginalName(t *testing.T) {
	tmpl := operationTemplate{
		BodyMIME:  "application/json",
		BodyShape: "object",
		Params: []paramSpec{{
			OriginalName: "userName",
			PyName:       "user_name",
			In:           locBody,
			Schema:       makeStringSchema(),
		}},
	}
	body, _, err := buildRequestBody(context.Background(), tmpl, map[string]any{"user_name": "alice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["userName"] != "alice" {
		t.Errorf("expected userName=alice, got %v", m)
	}
	if _, ok := m["user_name"]; ok {
		t.Errorf("body should not include snake_case key, got %v", m)
	}
}

func unescape(s string) string {
	b := strings.NewReplacer(
		"%20", " ",
		"%7C", "|",
		"%2C", ",",
		"%5B", "[",
		"%5D", "]",
	).Replace(s)
	return b
}
