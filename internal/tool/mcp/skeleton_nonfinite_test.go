// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuildResponseDigest_NonFiniteBecomesNull(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ExtractFields = map[string]string{
		"nested_empty": "{a: avg(xs)}",
		"top_empty":    "avg(xs)",
		"finite":       "avg(ys)",
	}
	d := buildResponseDigest([]ContentItem{jsonItem("x.json", `{"xs": [], "ys": [2, 4]}`)}, cfg, "t")
	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("digest is not serializable: %v", err)
	}
	ex := d["extracted_fields"].(map[string]any)
	if got, want := ex["nested_empty"], map[string]any{"a": nil}; !reflect.DeepEqual(got, want) {
		t.Errorf("nested_empty = %v, want %v", got, want)
	}
	if v, ok := ex["top_empty"]; ok {
		t.Errorf("top_empty should be omitted like any null result, got %v", v)
	}
	if ex["finite"] != float64(3) {
		t.Errorf("finite = %v, want 3", ex["finite"])
	}
}

func yamlItem(body string) ContentItem {
	return ContentItem{
		Type:     ContentTypeText,
		Text:     body,
		Metadata: map[string]string{"detected_format": string(TextFormatYAML)},
	}
}

func TestBuildResponseDigest_NonFiniteExtractionIsDeterministic(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ExtractFields = map[string]string{
		"all": "items",
		"avg": "avg(items[].v)",
		"c":   "length(items[?v == null])",
	}
	body := "items:\n  - v: .nan\n  - v: 2\n"
	for range 50 {
		d := buildResponseDigest([]ContentItem{yamlItem(body)}, cfg, "t")
		ex := d["extracted_fields"].(map[string]any)
		if ex["avg"] != float64(2) || ex["c"] != float64(1) {
			t.Fatalf("extracted avg=%v c=%v, want avg=2 c=1 on every run", ex["avg"], ex["c"])
		}
	}
}

func TestBuildResponseDigest_YAMLEdgeShapes(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ExtractFields = map[string]string{"cursor": "nextCursor", "m": "m"}
	d := buildResponseDigest([]ContentItem{yamlItem("nextCursor: 1234567890123456789\nm:\n  1: .nan\n  2: b\n")}, cfg, "t")
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("digest is not serializable: %v", err)
	}
	if !strings.Contains(string(b), `"nextCursor":1234567890123456789`) || !strings.Contains(string(b), `"cursor":1234567890123456789`) {
		t.Errorf("digest %s rounded the cursor above 2^53", b)
	}
	ex := d["extracted_fields"].(map[string]any)
	if got, want := ex["m"], map[string]any{"1": nil, "2": "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("m = %v, want %v", got, want)
	}
}

func TestBuildResponseDigest_YAMLNonFiniteAndIntegers(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ExtractFields = map[string]string{"avg": "avg(xs)"}
	item := ContentItem{
		Type:     ContentTypeText,
		Text:     "total: .inf\nxs:\n  - 2\n  - 4\n",
		Metadata: map[string]string{"detected_format": string(TextFormatYAML)},
	}
	d := buildResponseDigest([]ContentItem{item}, cfg, "t")
	if _, err := json.Marshal(d); err != nil {
		t.Fatalf("digest is not serializable: %v", err)
	}
	sk, ok := d["response_skeleton"].(map[string]any)
	if !ok {
		t.Fatalf("response_skeleton missing from digest %v", d)
	}
	if v, ok := sk["total"]; !ok || v != nil {
		t.Errorf("skeleton total = %v (present %v), want null", v, ok)
	}
	ex, _ := d["extracted_fields"].(map[string]any)
	if ex["avg"] != float64(3) {
		t.Errorf("extracted avg = %v, want 3", ex["avg"])
	}
}
