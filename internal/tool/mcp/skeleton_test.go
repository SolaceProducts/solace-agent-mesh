// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func jsonItem(filename, body string) ContentItem {
	return ContentItem{
		Type:     ContentTypeText,
		Text:     body,
		MIMEType: "application/json",
		Filename: filename,
		Metadata: map[string]string{"detected_format": string(TextFormatJSON)},
	}
}

func TestBuildSkeleton_KeepsScalarsElidesContainers(t *testing.T) {
	var v any
	body := `{"isLast": false, "nextPageToken": "CAEaBg", "total": 250, "issues": [1,2,3], "page": 2.5}`
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sk, ok := buildSkeleton(v, 2, 256, 50).(map[string]any)
	if !ok {
		t.Fatalf("skeleton not an object: %T", buildSkeleton(v, 2, 256, 50))
	}
	if sk["isLast"] != false {
		t.Errorf("isLast = %v, want false", sk["isLast"])
	}
	if sk["nextPageToken"] != "CAEaBg" {
		t.Errorf("nextPageToken = %v, want CAEaBg", sk["nextPageToken"])
	}
	if sk["issues"] != "[3 items]" {
		t.Errorf("issues = %v, want \"[3 items]\"", sk["issues"])
	}
	if sk["total"] != float64(250) {
		t.Errorf("total = %v, want 250", sk["total"])
	}
}

func TestBuildSkeleton_DepthCollapsesNestedObjects(t *testing.T) {
	var v any
	body := `{"meta": {"a": 1, "b": 2}, "ok": true}`
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sk := buildSkeleton(v, 1, 256, 50).(map[string]any)
	if sk["meta"] != "{2 keys}" {
		t.Errorf("meta = %v, want \"{2 keys}\" at depth 1", sk["meta"])
	}

	sk2 := buildSkeleton(v, 2, 256, 50).(map[string]any)
	meta, ok := sk2["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta not expanded at depth 2: %T", sk2["meta"])
	}
	if meta["a"] != float64(1) {
		t.Errorf("meta.a = %v, want 1", meta["a"])
	}
}

func TestBuildSkeleton_TruncatesLongScalars(t *testing.T) {
	var v any
	long := strings.Repeat("x", 500)
	body := fmt.Sprintf(`{"description": %q}`, long)
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sk := buildSkeleton(v, 2, 256, 50).(map[string]any)
	got := sk["description"].(string)
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("long scalar not truncated: %q", got)
	}
	if len(got) > 256+len("…(truncated)") {
		t.Errorf("truncated scalar too long: %d bytes", len(got))
	}
}

func TestBuildSkeleton_KeepsLoopControlValuesWhole(t *testing.T) {
	token := strings.Repeat("Ab3_", 120)
	body := map[string]any{
		"nextPageToken": token, "cursor": token, "continuation_token": token, "next": token,
		"description": token, "meta": map[string]any{"next_cursor": token},
	}
	sk := buildSkeleton(body, 2, 256, 50).(map[string]any)
	for _, key := range []string{"nextPageToken", "cursor", "continuation_token", "next"} {
		if sk[key] != token {
			t.Errorf("%s = %.40q…, want the %d-byte token whole", key, sk[key], len(token))
		}
	}
	if meta, _ := sk["meta"].(map[string]any); meta["next_cursor"] != token {
		t.Errorf("a nested cursor was cut: %.40q…", meta["next_cursor"])
	}
	if got, _ := sk["description"].(string); !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("an ordinary long value was kept whole: %d bytes", len(got))
	}

	huge := strings.Repeat("x", extractFieldMaxBytes+1)
	sk = buildSkeleton(map[string]any{"nextPageToken": huge}, 2, 256, 50).(map[string]any)
	if got, _ := sk["nextPageToken"].(string); !strings.HasPrefix(got, "(value too large") {
		t.Errorf("an oversized token = %.40q…, want a marker, never a prefix that looks usable", got)
	}
}

func TestLoopControlKey(t *testing.T) {
	for key, want := range map[string]bool{
		"nextPageToken": true, "PageToken": true, "page_token": true, "cursor": true, "next_cursor": true,
		"ContinuationToken": true, "NextToken": true, "@odata.nextLink": true, "next": true, "nextLink": true,
		"pagingToken": true, "nextPage": true, "next_page": true, "next_url": true, "_scroll_id": true, "scrollId": true,
		"@odata.deltaLink": true, "$skiptoken": true,
		"access_token": false, "refresh_token": false, "id_token": false, "api_token": false, "csrf_token": false,
		"auth_token": false, "session_token": false, "tokenizer": false, "tokens_used": false, "nextSteps": false,
		"next_action": false, "description": false,
	} {
		if got := loopControlKey(key); got != want {
			t.Errorf("loopControlKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func verbatimOfMarshaledLen(n int) string { return strings.Repeat("a", n-2) }

func TestBuildSkeleton_KeepsATokenWholeWithinItsOwnCap(t *testing.T) {
	skeleton := func(body map[string]any) map[string]any {
		t.Helper()
		sk, ok := buildSkeleton(body, 2, 256, 50).(map[string]any)
		if !ok {
			t.Fatalf("skeleton dropped for %d keys", len(body))
		}
		if sk["isLast"] != false {
			t.Errorf("isLast = %v, want it kept beside the token", sk["isLast"])
		}
		return sk
	}
	atCap := verbatimOfMarshaledLen(maxVerbatimValueBytes)
	if got := skeleton(map[string]any{"isLast": false, "nextPageToken": atCap})["nextPageToken"]; got != atCap {
		t.Errorf("a token at the cap = %.40q…, want it whole", got)
	}
	overCap := verbatimOfMarshaledLen(maxVerbatimValueBytes + 1)
	if got, _ := skeleton(map[string]any{"isLast": false, "nextPageToken": overCap})["nextPageToken"].(string); !strings.HasPrefix(got, "(value too large") {
		t.Errorf("a token one byte over the cap = %.40q…, want a marker", got)
	}
	escaped := strings.Repeat("&", maxVerbatimValueBytes/2)
	if got, _ := skeleton(map[string]any{"isLast": false, "next": escaped})["next"].(string); !strings.HasPrefix(got, "(value too large") {
		t.Errorf("a URL that marshals past the cap = %.40q…, want a marker", got)
	}
	two := strings.Repeat("b", 900)
	sk := skeleton(map[string]any{"isLast": false, "nextPageToken": two, "prevPageToken": two})
	if sk["nextPageToken"] != two || sk["prevPageToken"] != two {
		t.Error("two mid-size page tokens were not both kept whole")
	}
	jwt := strings.Repeat("e", 1500)
	sk = skeleton(map[string]any{"isLast": false, "access_token": jwt, "refresh_token": jwt, "id_token": jwt})
	if got, _ := sk["access_token"].(string); !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("a credential was kept whole: %d bytes", len(got))
	}
}

func TestExtractConfiguredFields_BoundsAStringAtTheFieldCap(t *testing.T) {
	atCap := verbatimOfMarshaledLen(extractFieldMaxBytes)
	overCap := verbatimOfMarshaledLen(extractFieldMaxBytes + 1)
	out := extractConfiguredFields(map[string]any{"a": atCap, "b": overCap}, map[string]string{"a": "a", "b": "b"})
	if out["a"] != atCap {
		t.Errorf("a field at the cap was not kept whole: %.40q…", out["a"])
	}
	if got, _ := out["b"].(string); !strings.HasPrefix(got, "(value too large") {
		t.Errorf("a field one byte over the cap = %.40q…, want a marker", got)
	}
}

func TestExtractConfiguredFields_KeepsStringsWhole(t *testing.T) {
	token := strings.Repeat("t", 600)
	out := extractConfiguredFields(map[string]any{"page": map[string]any{"token": token}}, map[string]string{"next": "page.token"})
	if out["next"] != token {
		t.Errorf("a configured field was cut to %d bytes, want %d", len(fmt.Sprint(out["next"])), len(token))
	}
}

func TestBuildSkeleton_CapsObjectKeys(t *testing.T) {
	m := make(map[string]any, 500)
	for i := range 500 {
		m[fmt.Sprintf("k%03d", i)] = i
	}
	sk, ok := buildSkeleton(m, 2, 256, 50).(map[string]any)
	if !ok {
		t.Fatalf("skeleton dropped for many-key object: %T", buildSkeleton(m, 2, 256, 50))
	}
	tk, ok := sk["_truncated_keys"].(int)
	if !ok {
		t.Fatalf("_truncated_keys missing: %v", sk["_truncated_keys"])
	}
	if len(sk) != 51 {
		t.Errorf("kept %d entries, want 51 (50 keys + marker)", len(sk))
	}
	if tk != 450 {
		t.Errorf("_truncated_keys = %d, want 450", tk)
	}
	if _, ok := sk["k000"]; !ok {
		t.Error("expected k000 to be kept (sorted-first-N)")
	}
	if _, ok := sk["k499"]; ok {
		t.Error("expected k499 to be dropped past the key cap")
	}
}

func TestExtractConfiguredFields_BoundsLargeResult(t *testing.T) {
	rows := make([]any, 0, 1000)
	for i := range 1000 {
		rows = append(rows, map[string]any{"key": fmt.Sprintf("DATAGO-%d", i)})
	}
	parsed := map[string]any{"issues": rows}
	out := extractConfiguredFields(parsed, map[string]string{"everything": "issues"})
	got, ok := out["everything"].(string)
	if !ok {
		t.Fatalf("oversized extraction not collapsed to a marker: %T", out["everything"])
	}
	if !strings.Contains(got, "too large") {
		t.Errorf("marker = %q, want a 'too large' notice", got)
	}
}

func TestBuildSkeleton_TruncatesMultibyteScalarsSafely(t *testing.T) {
	var v any
	long := strings.Repeat("世界", 300)
	body := fmt.Sprintf(`{"description": %q}`, long)
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sk := buildSkeleton(v, 2, 256, 50).(map[string]any)
	got := sk["description"].(string)
	if !utf8.ValidString(got) {
		t.Errorf("truncated scalar is not valid UTF-8: %q", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), `�`) {
		t.Errorf("truncation produced replacement chars: %s", b)
	}
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestBuildSkeleton_TopLevelArray(t *testing.T) {
	sk := buildSkeleton([]any{1, 2, 3}, 2, 256, 50)
	if sk != "[3 items]" {
		t.Errorf("top-level array skeleton = %v, want \"[3 items]\"", sk)
	}
}

func TestBuildSkeleton_RetriesAtDepth1WhenOversized(t *testing.T) {
	meta := make(map[string]any, 50)
	for i := range 50 {
		meta[fmt.Sprintf("k%02d", i)] = strings.Repeat("x", 200)
	}
	v := map[string]any{"isLast": false, "meta": meta}
	sk, ok := buildSkeleton(v, 2, 256, 50).(map[string]any)
	if !ok {
		t.Fatalf("skeleton dropped instead of retrying at depth 1: %T", buildSkeleton(v, 2, 256, 50))
	}
	if sk["isLast"] != false {
		t.Errorf("isLast lost in depth-1 retry: %v", sk["isLast"])
	}
	if s, _ := sk["meta"].(string); s != "{50 keys}" {
		t.Errorf("meta = %v, want collapsed \"{50 keys}\" at depth 1", sk["meta"])
	}
}

func TestFirstStructuredValue(t *testing.T) {
	yamlItem := ContentItem{
		Type:     ContentTypeText,
		Text:     "isLast: true\ntotal: 5\n",
		Metadata: map[string]string{"detected_format": string(TextFormatYAML)},
	}
	v, ok := firstStructuredValue([]ContentItem{yamlItem})
	if !ok {
		t.Fatal("expected YAML to parse")
	}
	m := v.(map[string]any)
	if m["isLast"] != true {
		t.Errorf("yaml isLast = %v, want true", m["isLast"])
	}

	bin := ContentItem{Type: ContentTypeImage, Data: []byte{0x1, 0x2}}
	v, ok = firstStructuredValue([]ContentItem{bin, jsonItem("x.json", `{"k": 1}`)})
	if !ok {
		t.Fatal("expected JSON to parse past the binary item")
	}
	if v.(map[string]any)["k"] != float64(1) {
		t.Errorf("json k = %v, want 1", v.(map[string]any)["k"])
	}

	if _, ok := firstStructuredValue([]ContentItem{textItem("a.txt", "hello world")}); ok {
		t.Error("plain text should not yield a structured value")
	}
}

func TestExtractConfiguredFields(t *testing.T) {
	var v any
	body := `{"isLast": false, "meta": {"next_cursor": "abc123"}, "issues": [1,2]}`
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fields := map[string]string{
		"isLast":  "isLast",
		"cursor":  "meta.next_cursor",
		"missing": "does.not.exist",
		"count":   "length(issues)",
	}
	out := extractConfiguredFields(v, fields)
	if out["isLast"] != false {
		t.Errorf("isLast = %v, want false", out["isLast"])
	}
	if out["cursor"] != "abc123" {
		t.Errorf("cursor = %v, want abc123", out["cursor"])
	}
	if _, ok := out["missing"]; ok {
		t.Errorf("missing field should be omitted, got %v", out["missing"])
	}
	if out["count"] != float64(2) {
		t.Errorf("count = %v, want 2", out["count"])
	}
}

func TestBuildResponseDigest_DisabledReturnsNil(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ResponseSkeleton = false
	items := []ContentItem{jsonItem("x.json", `{"isLast": true}`)}
	if d := buildResponseDigest(items, cfg, "t"); d != nil {
		t.Errorf("digest should be nil when skeleton off and no extract fields, got %v", d)
	}
}

func TestBuildResponseDigest_SkeletonAndExtract(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.ExtractFields = map[string]string{"cursor": "meta.next_cursor"}
	items := []ContentItem{jsonItem("x.json", `{"isLast": false, "meta": {"next_cursor": "z9"}, "rows": [1,2,3]}`)}
	d := buildResponseDigest(items, cfg, "t")
	if d == nil {
		t.Fatal("expected a digest")
	}
	sk := d["response_skeleton"].(map[string]any)
	if sk["isLast"] != false {
		t.Errorf("skeleton isLast = %v, want false", sk["isLast"])
	}
	if sk["rows"] != "[3 items]" {
		t.Errorf("skeleton rows = %v, want \"[3 items]\"", sk["rows"])
	}
	ex := d["extracted_fields"].(map[string]any)
	if ex["cursor"] != "z9" {
		t.Errorf("extracted cursor = %v, want z9", ex["cursor"])
	}
}

func TestHandleMCPResponse_ArtifactOnlyIncludesSkeleton(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	cfg := DefaultHandlerConfig()
	cfg.LLMReturnMaxBytes = 4096
	issues := make([]map[string]any, 0, 100)
	for i := range 100 {
		issues = append(issues, map[string]any{
			"key":     fmt.Sprintf("DATAGO-%d", i),
			"summary": strings.Repeat("x", 80),
		})
	}
	token := strings.Repeat("CAEaBgiAgID", 40)
	payload := map[string]any{
		"isLast":        false,
		"nextPageToken": token,
		"total":         250,
		"issues":        issues,
	}
	body, _ := json.Marshal(payload)
	if len(body) <= cfg.LLMReturnMaxBytes {
		t.Fatalf("test payload too small (%d bytes) to exercise artifact-only path", len(body))
	}

	result, err := HandleMCPResponse(ctx, []ContentItem{jsonItem("issues.json", string(body))}, "searchJiraIssuesUsingJql", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if result.Status != "processed_saved_artifact_only" {
		t.Fatalf("status = %q, want processed_saved_artifact_only", result.Status)
	}
	sk, ok := result.Data["response_skeleton"].(map[string]any)
	if !ok {
		t.Fatalf("response_skeleton missing from artifact-only result: %v", result.Data)
	}
	if sk["isLast"] != false {
		t.Errorf("skeleton isLast = %v, want false", sk["isLast"])
	}
	if sk["nextPageToken"] != token {
		t.Errorf("skeleton nextPageToken = %.40q…, want the %d-byte token whole", sk["nextPageToken"], len(token))
	}
	if sk["issues"] != "[100 items]" {
		t.Errorf("skeleton issues = %v, want \"[100 items]\"", sk["issues"])
	}
	if msg, _ := result.Data["message_to_llm"].(string); !strings.Contains(msg, "response_skeleton") {
		t.Errorf("message should point the LLM at response_skeleton: %q", msg)
	}
}

func TestHandleMCPResponse_YAMLSkeleton(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	var sb strings.Builder
	sb.WriteString("isLast: false\nnextPageToken: CAEaBg\nitems:\n")
	for i := range 200 {
		fmt.Fprintf(&sb, "  - id: %d\n    note: %s\n", i, strings.Repeat("y", 40))
	}
	yamlItem := ContentItem{
		Type:     ContentTypeText,
		Text:     sb.String(),
		MIMEType: "application/yaml",
		Filename: "page.yaml",
		Metadata: map[string]string{"detected_format": string(TextFormatYAML)},
	}
	cfg := DefaultHandlerConfig()
	cfg.LLMReturnMaxBytes = 4096
	if len(yamlItem.Text) <= cfg.LLMReturnMaxBytes {
		t.Fatalf("YAML payload too small (%d bytes)", len(yamlItem.Text))
	}

	result, err := HandleMCPResponse(ctx, []ContentItem{yamlItem}, "yaml_tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	sk, ok := result.Data["response_skeleton"].(map[string]any)
	if !ok {
		t.Fatalf("response_skeleton missing for YAML response: %v", result.Data)
	}
	if sk["isLast"] != false {
		t.Errorf("yaml skeleton isLast = %v, want false", sk["isLast"])
	}
	if sk["items"] != "[200 items]" {
		t.Errorf("yaml skeleton items = %v, want \"[200 items]\"", sk["items"])
	}
}

func TestHandleMCPResponse_NoStructuredContentNoDigest(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	item := textItem("notes.txt", strings.Repeat("plain prose. ", 500))
	result, err := HandleMCPResponse(ctx, []ContentItem{item}, "prose_tool", DefaultHandlerConfig(), toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if _, ok := result.Data["response_skeleton"]; ok {
		t.Error("response_skeleton should be absent for non-structured content")
	}
}

func TestHandleMCPResponse_SkeletonKillSwitch(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	body := `{"isLast": false, "blob": "` + strings.Repeat("y", 5000) + `"}`
	cfg := DefaultHandlerConfig()
	cfg.ResponseSkeleton = false

	result, err := HandleMCPResponse(ctx, []ContentItem{jsonItem("x.json", body)}, "tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if _, ok := result.Data["response_skeleton"]; ok {
		t.Error("response_skeleton should be absent when kill-switch is set")
	}
}

func TestBuildHandlerConfig_DigestKnobs(t *testing.T) {
	tc := &tool.Context{
		ToolConfig: map[string]any{
			"mcp_intelligent_processing": map[string]any{
				"response_skeleton":         false,
				"skeleton_depth":            3,
				"skeleton_max_scalar_bytes": 64,
				"skeleton_max_object_keys":  25,
				"extract_fields": map[string]any{
					"cursor": "meta.next_cursor",
					"empty":  "",
				},
			},
		},
	}
	cfg := buildHandlerConfig(tc)
	if cfg.ResponseSkeleton != false {
		t.Errorf("ResponseSkeleton not wired")
	}
	if cfg.SkeletonDepth != 3 {
		t.Errorf("SkeletonDepth = %d, want 3", cfg.SkeletonDepth)
	}
	if cfg.SkeletonMaxScalarBytes != 64 {
		t.Errorf("SkeletonMaxScalarBytes = %d, want 64", cfg.SkeletonMaxScalarBytes)
	}
	if cfg.SkeletonMaxObjectKeys != 25 {
		t.Errorf("SkeletonMaxObjectKeys = %d, want 25", cfg.SkeletonMaxObjectKeys)
	}
	if cfg.ExtractFields["cursor"] != "meta.next_cursor" {
		t.Errorf("ExtractFields[cursor] = %q, want meta.next_cursor", cfg.ExtractFields["cursor"])
	}
	if _, ok := cfg.ExtractFields["empty"]; ok {
		t.Errorf("empty expression should be dropped")
	}
}

func TestBuildHandlerConfig_DigestDefaults(t *testing.T) {
	cfg := buildHandlerConfig(&tool.Context{})
	if !cfg.ResponseSkeleton {
		t.Error("ResponseSkeleton should default on")
	}
	if cfg.SkeletonDepth != defaultSkeletonDepth {
		t.Errorf("SkeletonDepth default = %d, want %d", cfg.SkeletonDepth, defaultSkeletonDepth)
	}
}
