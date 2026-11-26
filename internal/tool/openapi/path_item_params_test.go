// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

const petIDRef = `{$ref: "#/components/parameters/PetId"}`

func pathItemSpec(serverURL string, pathLevel, operationLevel []string) string {
	block := func(indent int, params []string) string {
		if len(params) == 0 {
			return ""
		}
		pad := strings.Repeat(" ", indent)
		var b strings.Builder
		b.WriteString(pad + "parameters:\n")
		for _, p := range params {
			b.WriteString(pad + "  - " + p + "\n")
		}
		return b.String()
	}
	return fmt.Sprintf(`openapi: 3.0.0
info: {title: Pets, version: 1.0.0}
servers:
  - url: %s
components:
  parameters:
    PetId: {name: petId, in: path, required: true, description: The pet, schema: {type: string}}
paths:
  /pets/{petId}:
%s    get:
      operationId: getPet
%s      responses:
        "200": {description: ok}
`, serverURL, block(4, pathLevel), block(6, operationLevel))
}

var petID = `{name: petId, in: path, required: true, schema: {type: string}}`

type pathItemCase struct {
	name           string
	pathLevel      []string
	operationLevel []string
	want           map[string]bool
}

var pathItemCases = []pathItemCase{
	{
		name:      "a parameter declared only on the path item",
		pathLevel: []string{petID},
		want:      map[string]bool{"pet_id": true},
	},
	{
		name:           "path-item and operation parameters together",
		pathLevel:      []string{petID},
		operationLevel: []string{`{name: limit, in: query, schema: {type: integer}}`},
		want:           map[string]bool{"pet_id": true, "limit": false},
	},
	{
		name:      "a path-item parameter given by $ref",
		pathLevel: []string{petIDRef},
		want:      map[string]bool{"pet_id": true},
	},
	{
		name:           "an operation parameter makes the path-item one of the same name required",
		pathLevel:      []string{petID, `{name: limit, in: query, schema: {type: integer}}`},
		operationLevel: []string{`{name: limit, in: query, required: true, schema: {type: integer}}`},
		want:           map[string]bool{"pet_id": true, "limit": true},
	},
	{
		name:           "an operation parameter relaxes the path-item one of the same name",
		pathLevel:      []string{petID, `{name: limit, in: query, required: true, schema: {type: integer}}`},
		operationLevel: []string{`{name: limit, in: query, schema: {type: integer}}`},
		want:           map[string]bool{"pet_id": true, "limit": false},
	},
	{
		name:           "the same name in another location is not an override",
		pathLevel:      []string{petID, `{name: filter, in: query, schema: {type: string}}`},
		operationLevel: []string{`{name: filter, in: header, required: true, schema: {type: string}}`},
		want:           map[string]bool{"pet_id": true, "filter": true, "filter_0": false},
	},
	{
		name:           "header names override whatever their case",
		pathLevel:      []string{petID, `{name: X-Request-Id, in: header, schema: {type: string}}`},
		operationLevel: []string{`{name: x-request-id, in: header, required: true, schema: {type: string}}`},
		want:           map[string]bool{"pet_id": true, "x_request_id": true},
	},
	{
		name:           "operation parameters alone are unchanged",
		operationLevel: []string{petID},
		want:           map[string]bool{"pet_id": true},
	},
}

func declaredBy(t *testing.T, spec string) map[string]bool {
	t.Helper()
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: spec})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("registered %d tools, want 1", len(tools))
	}
	schema := tools[0].Parameters()
	got := map[string]bool{}
	for name := range schema.Properties {
		got[name] = slices.Contains(schema.Required, name)
	}
	return got
}

func TestPathItemParameters_ToolSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range pathItemCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := declaredBy(t, pathItemSpec("https://api.example.com", tc.pathLevel, tc.operationLevel))
			if !maps.Equal(got, tc.want) {
				t.Fatalf("tool declares %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPathItemParameters_EnumerateOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range pathItemCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := pathItemSpec("https://api.example.com", tc.pathLevel, tc.operationLevel)
			sigs, err := EnumerateOperations(t.Context(), []byte(spec), nil, nil)
			if err != nil {
				t.Fatalf("EnumerateOperations: %v", err)
			}
			if len(sigs) != 1 {
				t.Fatalf("signatures = %v, want the operation graded, not left out", sigs)
			}
			got := map[string]bool{}
			for _, p := range sigs[0].Params {
				got[p.Name] = p.Required
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("enumerated %v, want %v", got, tc.want)
			}
			if runtime := declaredBy(t, spec); !maps.Equal(got, runtime) {
				t.Fatalf("enumerated %v but the runtime tool declares %v", got, runtime)
			}
		})
	}
}

type seenRequest struct {
	path   string
	query  string
	header http.Header
}

func TestPathItemParameters_RequestBuilder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		pathLevel      []string
		operationLevel []string
		args           map[string]any
		wantPath       string
		wantQuery      string
		wantHeader     map[string]string
		wantHeaderAll  map[string][]string
		wantErr        string
	}{
		{
			name:      "a path-item path parameter fills the placeholder",
			pathLevel: []string{petID},
			args:      map[string]any{"pet_id": "42"},
			wantPath:  "/pets/42",
		},
		{
			name:      "a path-item path parameter given by $ref fills the placeholder",
			pathLevel: []string{petIDRef},
			args:      map[string]any{"pet_id": "42"},
			wantPath:  "/pets/42",
		},
		{
			name:      "a path-item query parameter is sent",
			pathLevel: []string{petID, `{name: include, in: query, schema: {type: string}}`},
			args:      map[string]any{"pet_id": "42", "include": "owner"},
			wantPath:  "/pets/42",
			wantQuery: "include=owner",
		},
		{
			name:      "a path-item header parameter is sent",
			pathLevel: []string{petID, `{name: X-Tenant, in: header, schema: {type: string}}`},
			args:      map[string]any{"pet_id": "42", "x_tenant": "acme"},
			wantPath:  "/pets/42",
			wantHeader: map[string]string{
				"X-Tenant": "acme",
			},
		},
		{
			name:           "an operation header overrides a path-item one of another case and is sent once",
			pathLevel:      []string{petID, `{name: X-Tenant, in: header, schema: {type: string}}`},
			operationLevel: []string{`{name: x-tenant, in: header, required: true, schema: {type: string}}`},
			args:           map[string]any{"pet_id": "42", "x_tenant": "acme", "x_tenant_0": "other"},
			wantPath:       "/pets/42",
			wantHeaderAll:  map[string][]string{"X-Tenant": {"acme"}},
		},
		{
			name:      "a path-item cookie parameter is sent",
			pathLevel: []string{petID, `{name: session, in: cookie, schema: {type: string}}`},
			args:      map[string]any{"pet_id": "42", "session": "abc"},
			wantPath:  "/pets/42",
			wantHeader: map[string]string{
				"Cookie": "session=abc",
			},
		},
		{
			name:           "of two parameters that snake_case alike the operation's own keeps the plain name",
			pathLevel:      []string{petID, `{name: filter, in: query, schema: {type: string}}`},
			operationLevel: []string{`{name: filter, in: header, required: true, schema: {type: string}}`},
			args:           map[string]any{"pet_id": "42", "filter": "from-header", "filter_0": "from-query"},
			wantPath:       "/pets/42",
			wantQuery:      "filter=from-query",
			wantHeader:     map[string]string{"Filter": "from-header"},
		},
		{
			name:           "the operation's own parameter drives serialization of an overridden one",
			pathLevel:      []string{petID, `{name: ids, in: query, schema: {type: string}}`},
			operationLevel: []string{`{name: ids, in: query, style: form, explode: false, schema: {type: array, items: {type: integer}}}`},
			args:           map[string]any{"pet_id": "42", "ids": []any{1, 2, 3}},
			wantPath:       "/pets/42",
			wantQuery:      "ids=1,2,3",
		},
		{
			name:      "a required path-item path parameter left out is not sent as a literal placeholder",
			pathLevel: []string{petID},
			args:      map[string]any{},
			wantErr:   `missing required path parameter "petId"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu   sync.Mutex
				seen []seenRequest
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, seenRequest{path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()})
				mu.Unlock()
				_, _ = io.WriteString(w, `{}`)
			}))
			defer srv.Close()

			mgr := NewManager(slog.New(slog.DiscardHandler), nil)
			tools, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{SpecInline: pathItemSpec(srv.URL, tc.pathLevel, tc.operationLevel)})
			if err != nil || len(tools) != 1 {
				t.Fatalf("LoadAndDiscoverTools: %d tools, err %v", len(tools), err)
			}

			_, err = mgr.ExecuteTool(t.Context(), tools[0], tc.args)

			mu.Lock()
			defer mu.Unlock()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				if len(seen) != 0 {
					t.Fatalf("server received %+v, want no request", seen)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}
			if len(seen) != 1 {
				t.Fatalf("server received %d requests, want 1", len(seen))
			}
			if seen[0].path != tc.wantPath || seen[0].query != tc.wantQuery {
				t.Errorf("request = %s?%s, want %s?%s", seen[0].path, seen[0].query, tc.wantPath, tc.wantQuery)
			}
			for name, want := range tc.wantHeader {
				if got := seen[0].header.Get(name); got != want {
					t.Errorf("header %s = %q, want %q", name, got, want)
				}
			}
			for name, want := range tc.wantHeaderAll {
				if got := seen[0].header.Values(name); !slices.Equal(got, want) {
					t.Errorf("header %s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestPathItemParameters_SiblingOperationsKeepTheirOwnOverrides(t *testing.T) {
	t.Parallel()
	spec := `openapi: 3.0.0
info: {title: Pets, version: 1.0.0}
servers:
  - url: https://api.example.com
paths:
  /pets/{petId}:
    parameters:
      - {name: petId, in: path, required: true, schema: {type: string}}
      - {name: limit, in: query, schema: {type: integer}}
    get:
      operationId: getPet
      responses:
        "200": {description: ok}
    put:
      operationId: putPet
      parameters:
        - {name: limit, in: query, required: true, schema: {type: integer}}
      responses:
        "200": {description: ok}
`
	tools, err := NewManager(slog.New(slog.DiscardHandler), nil).LoadAndDiscoverTools(&ToolsetConfig{SpecInline: spec})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	got := map[string]map[string]bool{}
	for _, tl := range tools {
		got[tl.Name()] = map[string]bool{}
		for name := range tl.Parameters().Properties {
			got[tl.Name()][name] = slices.Contains(tl.Parameters().Required, name)
		}
	}
	want := map[string]map[string]bool{
		"get_pet": {"pet_id": true, "limit": false},
		"put_pet": {"pet_id": true, "limit": true},
	}
	for name, params := range want {
		if !maps.Equal(got[name], params) {
			t.Errorf("%s declares %v, want %v", name, got[name], params)
		}
	}
}
