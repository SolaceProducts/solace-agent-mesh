// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildPayload_NestedPaths(t *testing.T) {
	params := []Parameter{
		{Name: "city", Type: "string", PayloadPath: "location.city"},
		{Name: "country", Type: "string", PayloadPath: "location.country"},
		{Name: "unit", Type: "string", PayloadPath: "unit"},
	}
	payload, resolved := buildPayloadAndResolveParams(params, map[string]any{
		"city":    "Ottawa",
		"country": "CA",
		"unit":    "celsius",
	}, nil)

	want := map[string]any{
		"location": map[string]any{"city": "Ottawa", "country": "CA"},
		"unit":     "celsius",
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload mismatch.\n  got:  %#v\n  want: %#v", payload, want)
	}
	if resolved["city"] != "Ottawa" || resolved["unit"] != "celsius" {
		t.Fatalf("resolved missing fields: %#v", resolved)
	}
}

func TestBuildPayload_DefaultsApplied(t *testing.T) {
	params := []Parameter{
		{Name: "city", Type: "string", PayloadPath: "city"},
		{Name: "unit", Type: "string", Default: "celsius", defaultPresent: true, PayloadPath: "unit"},
	}
	payload, resolved := buildPayloadAndResolveParams(params, map[string]any{
		"city": "Ottawa",
	}, nil)

	if payload["unit"] != "celsius" {
		t.Fatalf("default not applied: %#v", payload)
	}
	if resolved["unit"] != "celsius" {
		t.Fatalf("default missing from resolved: %#v", resolved)
	}
}

func TestBuildPayload_OverrideBeatsDefault(t *testing.T) {
	params := []Parameter{
		{Name: "unit", Type: "string", Default: "celsius", defaultPresent: true, PayloadPath: "unit"},
	}
	payload, _ := buildPayloadAndResolveParams(params, map[string]any{
		"unit": "fahrenheit",
	}, nil)
	if payload["unit"] != "fahrenheit" {
		t.Fatalf("override didn't beat default: %#v", payload)
	}
}

func TestBuildPayload_NoValueNoDefaultIsSkipped(t *testing.T) {
	params := []Parameter{
		{Name: "ghost", Type: "string", PayloadPath: "ghost"},
	}
	payload, resolved := buildPayloadAndResolveParams(params, map[string]any{}, nil)
	if _, has := payload["ghost"]; has {
		t.Fatalf("ghost should not be in payload: %#v", payload)
	}
	if _, has := resolved["ghost"]; has {
		t.Fatalf("ghost should not be in resolved: %#v", resolved)
	}
}

func TestBuildPayload_TopicOnlyParam(t *testing.T) {
	params := []Parameter{
		{Name: "request_id", Type: "string"},
	}
	payload, resolved := buildPayloadAndResolveParams(params, map[string]any{
		"request_id": "abc123",
	}, nil)
	if len(payload) != 0 {
		t.Fatalf("topic-only param should not appear in payload: %#v", payload)
	}
	if resolved["request_id"] != "abc123" {
		t.Fatalf("topic-only param missing from resolved: %#v", resolved)
	}
}

func TestBuildPayload_ContextExpression(t *testing.T) {
	view := &contextView{userID: "user-42"}
	params := []Parameter{
		{Name: "user_id", ContextExpression: "user_id", PayloadPath: "meta.user_id"},
	}
	payload, _ := buildPayloadAndResolveParams(params, map[string]any{}, view)
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta missing: %#v", payload)
	}
	if meta["user_id"] != "user-42" {
		t.Fatalf("user_id not from context: %#v", meta)
	}
}

func TestBuildPayload_ContextExpressionMissingFallsBackToDefault(t *testing.T) {
	view := &contextView{}
	params := []Parameter{
		{Name: "user_id", ContextExpression: "user_id", Default: "anonymous", defaultPresent: true, PayloadPath: "meta.user_id"},
	}
	payload, _ := buildPayloadAndResolveParams(params, map[string]any{}, view)
	meta := payload["meta"].(map[string]any)
	if meta["user_id"] != "anonymous" {
		t.Fatalf("default not applied for missing context: %#v", meta)
	}
}

func TestEncodePayload_JSON(t *testing.T) {
	body, err := encodePayload(map[string]any{"a": 1, "b": "two"}, "json", "utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body not valid json: %v", err)
	}
	if got["b"] != "two" {
		t.Fatalf("unexpected json shape: %#v", got)
	}
}

func TestEncodePayload_YAML(t *testing.T) {
	body, err := encodePayload(map[string]any{"a": 1}, "yaml", "utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty yaml output")
	}
}

func TestEncodePayload_TextSingleValue(t *testing.T) {
	body, err := encodePayload(map[string]any{"msg": "hello"}, "text", "utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("unexpected text body: %q", body)
	}
}

func TestEncodePayload_TextRejectsMultipleValues(t *testing.T) {
	_, err := encodePayload(map[string]any{"a": "1", "b": "2"}, "text", "utf-8")
	if err == nil {
		t.Fatal("expected error for multi-value text payload")
	}
}

func TestEncodePayload_Base64(t *testing.T) {
	body, err := encodePayload(map[string]any{"a": 1}, "json", "base64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		t.Fatalf("body not valid base64: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(decoded, &v); err != nil {
		t.Fatalf("decoded body not json: %v", err)
	}
}

func TestDecodePayload_JSON(t *testing.T) {
	got := decodePayload([]byte(`{"x": 1}`), "json", "utf-8")
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["x"].(float64) != 1 {
		t.Fatalf("unexpected decoded value: %#v", m)
	}
}

func TestDecodePayload_FallthroughIsString(t *testing.T) {
	got := decodePayload([]byte("not json"), "json", "utf-8")
	if got.(string) != "not json" {
		t.Fatalf("expected fallthrough string, got %#v", got)
	}
}

func TestDecodePayload_Empty(t *testing.T) {
	if got := decodePayload(nil, "json", "utf-8"); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestDecodePayload_Base64WrappedJSON(t *testing.T) {
	inner := []byte(`{"hello":"world"}`)
	wrapped := base64.StdEncoding.EncodeToString(inner)
	got := decodePayload([]byte(wrapped), "json", "base64")
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T (%v)", got, got)
	}
	if m["hello"] != "world" {
		t.Fatalf("base64-wrapped JSON not decoded: %#v", m)
	}
}

func TestBuildPayload_ExplicitNullDefaultIsApplied(t *testing.T) {
	raw := []byte(`[{"name":"flag","payload_path":"flag","default":null}]`)
	var params []Parameter
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !params[0].HasDefault() {
		t.Fatal("expected HasDefault() to be true for explicit null")
	}
	payload, _ := buildPayloadAndResolveParams(params, map[string]any{}, nil)
	v, has := payload["flag"]
	if !has {
		t.Fatalf("flag missing from payload: %#v", payload)
	}
	if v != nil {
		t.Fatalf("flag should be nil, got %#v", v)
	}
}

func TestBuildPayload_AbsentDefaultStillSkips(t *testing.T) {
	raw := []byte(`[{"name":"flag","payload_path":"flag"}]`)
	var params []Parameter
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if params[0].HasDefault() {
		t.Fatal("expected HasDefault() to be false when default key absent")
	}
	payload, _ := buildPayloadAndResolveParams(params, map[string]any{}, nil)
	if _, has := payload["flag"]; has {
		t.Fatalf("flag should be omitted: %#v", payload)
	}
}
