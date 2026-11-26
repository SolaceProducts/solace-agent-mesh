// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

func makeStringSchema() *openapi3.Schema {
	return &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}
}

func TestAssignPyNames_CamelCase(t *testing.T) {
	params := []paramSpec{
		{OriginalName: "userId", In: locQuery},
		{OriginalName: "OrderNumber", In: locQuery},
		{OriginalName: "SKU_Code", In: locQuery},
	}
	assignPyNames(params)
	wantPyNames := []string{"user_id", "order_number", "sku_code"}
	for i, w := range wantPyNames {
		if params[i].PyName != w {
			t.Errorf("params[%d].PyName = %q, want %q", i, params[i].PyName, w)
		}
	}
}

func TestDedupPyNames(t *testing.T) {
	params := []paramSpec{
		{OriginalName: "userId", PyName: "user_id"},
		{OriginalName: "user_id", PyName: "user_id"},
		{OriginalName: "User_Id", PyName: "user_id"},
	}
	dedupPyNames(params)
	want := []string{"user_id", "user_id_0", "user_id_1"}
	for i, w := range want {
		if params[i].PyName != w {
			t.Errorf("params[%d].PyName = %q, want %q", i, params[i].PyName, w)
		}
	}
}

func TestTruncateTo60(t *testing.T) {
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	got := truncateTo60(long)
	if len(got) != 60 {
		t.Errorf("truncateTo60(68a) len = %d, want 60", len(got))
	}
	if got := truncateTo60("short"); got != "short" {
		t.Errorf("unchanged short = %q, want %q", got, "short")
	}
}

func TestToSnakeCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"camelCase", "camel_case"},
		{"UpperCamelCase", "upper_camel_case"},
		{"space separated", "space_separated"},
		{"HTTPRequest", "http_request"},
		{"REST API", "rest_api"},
		{"user_id", "user_id"},
		{"", ""},
	}
	for _, c := range cases {
		if got := toSnakeCase(c.in); got != c.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenamePythonKeywords(t *testing.T) {
	if got := renamePythonKeywords("for"); got != "param_for" {
		t.Errorf("got %q, want param_for", got)
	}
	if got := renamePythonKeywords("hello"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestBuildOperationTemplate_JSONBody(t *testing.T) {
	op := operationInfo{
		OperationID: "create_user",
		Method:      "POST",
		Path:        "/users",
		RequestBody: &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Required: true,
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
						Type:     &openapi3.Types{openapi3.TypeObject},
						Required: []string{"userName"},
						Properties: openapi3.Schemas{
							"userName": &openapi3.SchemaRef{Value: makeStringSchema()},
							"age":      &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}}},
						},
					}},
				},
			},
		}},
	}
	tmpl := buildOperationTemplate(op)
	if tmpl.BodyMIME != "application/json" || tmpl.BodyShape != "object" {
		t.Errorf("unexpected body metadata: %+v", tmpl)
	}

	byPy := map[string]paramSpec{}
	for _, p := range tmpl.Params {
		byPy[p.PyName] = p
	}
	user, ok := byPy["user_name"]
	if !ok {
		t.Fatalf("missing user_name param; got %+v", tmpl.Params)
	}
	if user.OriginalName != "userName" {
		t.Errorf("original name = %q, want userName", user.OriginalName)
	}
	if !user.Required {
		t.Error("user_name should be required")
	}
	if _, ok := byPy["age"]; !ok {
		t.Error("missing age param")
	}
}

func TestBuildOperationTemplate_QueryParams(t *testing.T) {
	explode := true
	op := operationInfo{
		OperationID: "list_items",
		Method:      "GET",
		Path:        "/items",
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{Value: &openapi3.Parameter{
				Name:    "tagIds",
				In:      "query",
				Style:   "form",
				Explode: &explode,
				Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
					Type:  &openapi3.Types{openapi3.TypeArray},
					Items: &openapi3.SchemaRef{Value: makeStringSchema()},
				}},
			}},
		},
	}
	tmpl := buildOperationTemplate(op)
	if len(tmpl.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(tmpl.Params))
	}
	p := tmpl.Params[0]
	if p.OriginalName != "tagIds" {
		t.Errorf("OriginalName = %q", p.OriginalName)
	}
	if p.PyName != "tag_ids" {
		t.Errorf("PyName = %q, want tag_ids", p.PyName)
	}
	if p.Style != "form" || !p.Explode {
		t.Errorf("Style/Explode = %q/%v, want form/true", p.Style, p.Explode)
	}
}

func TestRenderSchema_EnumAndConstraints(t *testing.T) {
	min := 0.0
	max := 100.0
	s := &openapi3.Schema{
		Type:   &openapi3.Types{openapi3.TypeInteger},
		Format: "int32",
		Min:    &min,
		Max:    &max,
		Enum:   []any{1, 2, 3},
	}
	out := renderSchema(s, "")
	if out["type"] != "integer" {
		t.Errorf("type = %v", out["type"])
	}
	if out["format"] != "int32" {
		t.Errorf("format = %v", out["format"])
	}
	if out["minimum"] != 0.0 || out["maximum"] != 100.0 {
		t.Errorf("min/max = %v/%v", out["minimum"], out["maximum"])
	}
	if out["enum"] == nil {
		t.Error("enum missing")
	}
}

func TestRenderSchema_Combinators(t *testing.T) {
	s := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{
			&openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
			&openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeInteger}}},
		},
	}
	out := renderSchema(s, "")
	one, ok := out["oneOf"].([]any)
	if !ok || len(one) != 2 {
		t.Fatalf("oneOf = %v", out["oneOf"])
	}
}

func TestRenderSchema_NestedObject(t *testing.T) {
	inner := &openapi3.Schema{
		Type:     &openapi3.Types{openapi3.TypeObject},
		Required: []string{"id"},
		Properties: openapi3.Schemas{
			"id":   &openapi3.SchemaRef{Value: makeStringSchema()},
			"name": &openapi3.SchemaRef{Value: makeStringSchema()},
		},
	}
	s := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"user": &openapi3.SchemaRef{Value: inner},
		},
	}
	out := renderSchema(s, "")
	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing")
	}
	userSchema, ok := props["user"].(map[string]any)
	if !ok {
		t.Fatalf("user schema missing")
	}
	if userSchema["required"] == nil {
		t.Error("nested required missing")
	}
}

func TestRenderSchema_CyclicProperty(t *testing.T) {
	foo := &openapi3.Schema{
		Type:  &openapi3.Types{openapi3.TypeObject},
		Title: "Foo",
	}
	foo.Properties = openapi3.Schemas{
		"self": &openapi3.SchemaRef{Value: foo},
	}

	done := make(chan map[string]any, 1)
	go func() {
		done <- renderSchema(foo, "")
	}()

	select {
	case out := <-done:
		props, ok := out["properties"].(map[string]any)
		if !ok {
			t.Fatalf("missing properties on outer schema")
		}
		self, ok := props["self"].(map[string]any)
		if !ok {
			t.Fatalf("missing self property; got %T: %v", props["self"], props["self"])
		}
		if desc, _ := self["description"].(string); desc != "circular schema reference" {
			t.Errorf("self.description = %q, want \"circular schema reference\"", desc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("renderSchema hung on a cyclic schema — cycle detection is broken")
	}
}

func TestRenderSchema_CyclicViaRef_Foo_Bar(t *testing.T) {
	foo := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}, Title: "Foo"}
	bar := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}, Title: "Bar"}
	foo.Properties = openapi3.Schemas{"bar": &openapi3.SchemaRef{Value: bar}}
	bar.Properties = openapi3.Schemas{"foo": &openapi3.SchemaRef{Value: foo}}

	done := make(chan map[string]any, 1)
	go func() { done <- renderSchema(foo, "") }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("renderSchema hung on Foo↔Bar cycle")
	}
}

func TestHasBinaryField_Cyclic(t *testing.T) {
	s := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}}
	s.Properties = openapi3.Schemas{"self": &openapi3.SchemaRef{Value: s}}

	done := make(chan bool, 1)
	go func() { done <- hasBinaryField(s) }()
	select {
	case got := <-done:
		if got {
			t.Errorf("hasBinaryField = true on cyclic schema with no binary field; want false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hasBinaryField hung on a cyclic schema — cycle detection is broken")
	}
}

func TestRenderSchema_LongPropertyTruncated(t *testing.T) {
	longName := ""
	for i := 0; i < 80; i++ {
		longName += "x"
	}
	s := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			longName: &openapi3.SchemaRef{Value: makeStringSchema()},
		},
	}
	out := renderSchema(s, "")
	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties")
	}
	for k := range props {
		if len(k) > 60 {
			t.Errorf("property key %q longer than 60 chars", k)
		}
	}
}

func TestBuildAcceptHeader(t *testing.T) {
	resps := openapi3.NewResponses()
	resps.Set("200", &openapi3.ResponseRef{Value: &openapi3.Response{
		Content: openapi3.Content{
			"application/json": &openapi3.MediaType{},
			"application/xml":  &openapi3.MediaType{},
		},
	}})
	resps.Set("201", &openapi3.ResponseRef{Value: &openapi3.Response{
		Content: openapi3.Content{
			"text/plain": &openapi3.MediaType{},
		},
	}})
	resps.Set("404", &openapi3.ResponseRef{Value: &openapi3.Response{
		Content: openapi3.Content{
			"application/problem+json": &openapi3.MediaType{},
		},
	}})

	got := buildAcceptHeader(resps)
	want := "application/json, application/xml, text/plain"
	if got != want {
		t.Errorf("accept = %q, want %q", got, want)
	}
}

func TestHasBinaryField(t *testing.T) {
	s := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"name": &openapi3.SchemaRef{Value: makeStringSchema()},
			"file": &openapi3.SchemaRef{Value: &openapi3.Schema{
				Type:   &openapi3.Types{openapi3.TypeString},
				Format: "binary",
			}},
		},
	}
	if !hasBinaryField(s) {
		t.Error("expected hasBinaryField = true")
	}
	plain := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			"name": &openapi3.SchemaRef{Value: makeStringSchema()},
		},
	}
	if hasBinaryField(plain) {
		t.Error("expected hasBinaryField = false")
	}
}
