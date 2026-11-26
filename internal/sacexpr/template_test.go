// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"strings"
	"testing"
)

func TestTemplate_SimpleText(t *testing.T) {
	ctx := NewContext(map[string]any{
		"name": "alice",
		"data": map[string]any{"count": 42},
	}, "orders/create", nil)

	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			"text encoding",
			"template:Hello {{text://input.payload:name}}!",
			"Hello alice!",
		},
		{
			"default encoding (no prefix)",
			"template:Hello {{input.payload:name}}!",
			"Hello alice!",
		},
		{
			"json encoding",
			"template:Data: {{json://input.payload:data}}",
			`Data: {"count":42}`,
		},
		{
			"multiple expressions",
			"template:{{text://input.payload:name}} has {{text://input.payload:data.count}} items",
			"alice has 42 items",
		},
		{
			"topic in template",
			"template:Topic: {{text://input.topic:}}",
			"Topic: orders/create",
		},
		{
			"topic level in template",
			"template:Action: {{text://input.topic_levels:1}}",
			"Action: create",
		},
		{
			"static in template",
			"template:Type: {{text://static:image/jpeg}}",
			"Type: image/jpeg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			gotStr, ok := got.(string)
			if !ok {
				t.Fatalf("expected string, got %T: %v", got, got)
			}
			if gotStr != tt.want {
				t.Errorf("got %q, want %q", gotStr, tt.want)
			}
		})
	}
}

func TestTemplate_SingleExpressionRawValue(t *testing.T) {
	ctx := NewContext(map[string]any{
		"count": 42,
		"items": []any{1, 2, 3},
		"data":  map[string]any{"key": "val"},
	}, "", nil)

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"integer", "template:{{text://input.payload:count}}", 42},
		{"slice", "template:{{text://input.payload:items}}", []any{1, 2, 3}},
		{"map", "template:{{text://input.payload:data}}", map[string]any{"key": "val"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			assertDeepEqual(t, got, tt.want)
		})
	}
}

func TestTemplate_Base64Encoding(t *testing.T) {
	ctx := NewContext(map[string]any{"text": "hello"}, "", nil)

	got, err := ctx.GetData("template:{{base64://input.payload:text}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got != "aGVsbG8=" {
		t.Errorf("got %v, want aGVsbG8=", got)
	}
}

func TestTemplate_DataURIEncoding(t *testing.T) {
	ctx := NewContext(map[string]any{"img": "data"}, "", nil)

	got, err := ctx.GetData("template:{{datauri:image/png://input.payload:img}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	gotStr := got.(string)
	if !strings.HasPrefix(gotStr, "data:image/png;base64,") {
		t.Errorf("got %q, want data:image/png;base64,... prefix", gotStr)
	}
}

func TestTemplate_YAMLEncoding(t *testing.T) {
	ctx := NewContext(map[string]any{
		"data": map[string]any{"key": "val"},
	}, "", nil)

	got, err := ctx.GetData("template:{{yaml://input.payload:data}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	gotStr := got.(string)
	if !strings.Contains(gotStr, "key: val") {
		t.Errorf("got %q, want to contain 'key: val'", gotStr)
	}
}

func TestTemplate_NoExpressions(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	got, err := ctx.GetData("template:just plain text")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got != "just plain text" {
		t.Errorf("got %v, want 'just plain text'", got)
	}
}

func TestTemplate_MissingValue(t *testing.T) {
	ctx := NewContext(map[string]any{}, "", nil)

	got, err := ctx.GetData("template:value is {{text://input.payload:missing}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got != "value is <nil>" {
		t.Errorf("got %q", got)
	}
}

func TestTemplate_WithUserData(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	_ = ctx.SetData("user_data.forward_context:correlation_id", "corr-123")

	got, err := ctx.GetData("template:responses/{{text://user_data.forward_context:correlation_id}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if got != "responses/corr-123" {
		t.Errorf("got %v, want responses/corr-123", got)
	}
}

func TestTemplate_JsonEncodePayload(t *testing.T) {
	ctx := NewContext(map[string]any{
		"order_id": "ORD-001",
		"items":    []any{"widget", "gadget"},
	}, "", nil)

	got, err := ctx.GetData("template:Summarize this: {{json://input.payload}}")
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	gotStr := got.(string)
	if !strings.Contains(gotStr, "Summarize this:") {
		t.Errorf("missing prefix in %q", gotStr)
	}
	if !strings.Contains(gotStr, "ORD-001") {
		t.Errorf("missing order_id in %q", gotStr)
	}
}

func TestParseEncodedExpression(t *testing.T) {
	tests := []struct {
		input    string
		encoding string
		expr     string
	}{
		{"text://input.payload:name", "text", "input.payload:name"},
		{"json://input.payload:data", "json", "input.payload:data"},
		{"base64://input.payload:text", "base64", "input.payload:text"},
		{"yaml://input.payload:config", "yaml", "input.payload:config"},
		{"datauri:image/png://input.payload:img", "datauri:image/png", "input.payload:img"},
		{"input.payload:name", "text", "input.payload:name"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			enc, expr := parseEncodedExpression(tt.input)
			if enc != tt.encoding {
				t.Errorf("encoding = %q, want %q", enc, tt.encoding)
			}
			if expr != tt.expr {
				t.Errorf("expression = %q, want %q", expr, tt.expr)
			}
		})
	}
}
