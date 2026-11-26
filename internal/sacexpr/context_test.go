// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"encoding/json"
	"testing"
)

func TestContext_InputPayload(t *testing.T) {
	payload := map[string]any{
		"name": "alice",
		"data": map[string]any{
			"items": []any{
				map[string]any{"id": 1},
				map[string]any{"id": 2},
			},
		},
	}
	ctx := NewContext(payload, "orders/create", nil)

	tests := []struct {
		expr string
		want any
	}{
		{"input.payload:", payload},
		{"input.payload:name", "alice"},
		{"input.payload:data.items[0].id", 1},
		{"input.payload:data.items[1].id", 2},
		{"input.payload:data.items[-1].id", 2},
		{"input.payload:missing", nil},
		{"input.payload:data.missing.deep", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			assertDeepEqual(t, got, tt.want)
		})
	}
}

func TestContext_InputTopic(t *testing.T) {
	ctx := NewContext(nil, "orders/create/v2", nil)

	tests := []struct {
		expr string
		want any
	}{
		{"input.topic:", "orders/create/v2"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("GetData(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestContext_InputTopicLevels(t *testing.T) {
	ctx := NewContext(nil, "orders/create/v2", nil)

	tests := []struct {
		expr string
		want any
	}{
		{"input.topic_levels:0", "orders"},
		{"input.topic_levels:1", "create"},
		{"input.topic_levels:2", "v2"},
		{"input.topic_levels:-1", "v2"},
		{"input.topic_levels:99", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("GetData(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestContext_UserProperties(t *testing.T) {
	props := map[string]string{
		"correlation_id": "abc-123",
		"user_id":        "user42",
	}
	ctx := NewContext(nil, "", props)

	tests := []struct {
		expr string
		want any
	}{
		{"input.user_properties:correlation_id", "abc-123"},
		{"input.user_properties:user_id", "user42"},
		{"input.user_properties:missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("GetData(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestContext_Static(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	tests := []struct {
		expr string
		want any
	}{
		{"static:hello", "hello"},
		{"static:image/jpeg", "image/jpeg"},
		{"static:", ""},
		{"static:123", "123"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Errorf("GetData(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestContext_UserData(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	err := ctx.SetData("user_data.forward_context:correlation_id", "abc-123")
	if err != nil {
		t.Fatalf("SetData: %v", err)
	}

	err = ctx.SetData("user_data.created_artifacts:uris", []any{"uri1", "uri2"})
	if err != nil {
		t.Fatalf("SetData: %v", err)
	}

	tests := []struct {
		expr string
		want any
	}{
		{"user_data.forward_context:correlation_id", "abc-123"},
		{"user_data.created_artifacts:uris", []any{"uri1", "uri2"}},
		{"user_data.nonexistent:", nil},
		{"user_data.forward_context:missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			assertDeepEqual(t, got, tt.want)
		})
	}
}

func TestContext_ListItem(t *testing.T) {
	ctx := NewContext(nil, "", nil)
	ctx.SetIterationItem(map[string]any{
		"docName":    "report.pdf",
		"docContent": "base64data",
	})

	tests := []struct {
		expr string
		want any
	}{
		{"list_item:docName", "report.pdf"},
		{"list_item:docContent", "base64data"},
		{"list_item:", map[string]any{"docName": "report.pdf", "docContent": "base64data"}},
		{"list_item:missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := ctx.GetData(tt.expr)
			if err != nil {
				t.Fatalf("GetData(%q): %v", tt.expr, err)
			}
			assertDeepEqual(t, got, tt.want)
		})
	}
}

func TestContext_ListItem_Nil(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	got, err := ctx.GetData("list_item:path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestContext_SetData_Payload(t *testing.T) {
	ctx := NewContext(map[string]any{"a": 1}, "", nil)

	err := ctx.SetData("input.payload:b", 2)
	if err != nil {
		t.Fatalf("SetData: %v", err)
	}

	got, _ := ctx.GetData("input.payload:b")
	if got != 2 {
		t.Errorf("expected 2, got %v", got)
	}

	got, _ = ctx.GetData("input.payload:a")
	if got != 1 {
		t.Errorf("expected 1, got %v", got)
	}
}

func TestContext_SetData_ReplacePayload(t *testing.T) {
	ctx := NewContext(map[string]any{"old": true}, "", nil)

	err := ctx.SetData("input.payload:", map[string]any{"new": true})
	if err != nil {
		t.Fatalf("SetData: %v", err)
	}

	got, _ := ctx.GetData("input.payload:new")
	if got != true {
		t.Errorf("expected true, got %v", got)
	}

	got, _ = ctx.GetData("input.payload:old")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestContext_SetData_UserDataWholeStore(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	store := map[string]any{"key1": "val1", "key2": "val2"}
	err := ctx.SetData("user_data.mystore:", store)
	if err != nil {
		t.Fatalf("SetData: %v", err)
	}

	got, _ := ctx.GetData("user_data.mystore:key1")
	if got != "val1" {
		t.Errorf("expected val1, got %v", got)
	}
}

func TestContext_Clone(t *testing.T) {
	ctx := NewContext(map[string]any{"key": "val"}, "topic/a", map[string]string{"p": "v"})
	_ = ctx.SetData("user_data.store:x", 42)

	cloned := ctx.Clone()

	got, _ := cloned.GetData("input.payload:key")
	if got != "val" {
		t.Errorf("expected val, got %v", got)
	}
	got, _ = cloned.GetData("user_data.store:x")
	if got != 42 {
		t.Errorf("expected 42, got %v", got)
	}

	_ = cloned.SetData("user_data.store:y", 99)
	got, _ = ctx.GetData("user_data.store:y")
	if got != nil {
		t.Errorf("expected nil (original should be unmodified), got %v", got)
	}
}

func TestContext_KeywordArgs(t *testing.T) {
	ctx := NewContext(nil, "", nil)

	ctx.SetKeywordArgs(map[string]any{
		"index":         0,
		"current_value": "hello",
	})

	v, ok := ctx.GetKeywordArg("index")
	if !ok || v != 0 {
		t.Errorf("expected 0, got %v (ok=%v)", v, ok)
	}

	v, ok = ctx.GetKeywordArg("current_value")
	if !ok || v != "hello" {
		t.Errorf("expected hello, got %v (ok=%v)", v, ok)
	}

	_, ok = ctx.GetKeywordArg("missing")
	if ok {
		t.Error("expected missing to not be found")
	}

	ctx.ClearKeywordArgs()
	_, ok = ctx.GetKeywordArg("index")
	if ok {
		t.Error("expected args cleared")
	}
}

func assertDeepEqual(t *testing.T, got, want any) {
	t.Helper()
	if got == nil && want == nil {
		return
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("got %s, want %s", gotJSON, wantJSON)
	}
}
