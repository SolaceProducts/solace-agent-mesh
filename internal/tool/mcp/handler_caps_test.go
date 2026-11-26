// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newMCPToolCtx(t *testing.T) *tool.Context {
	t.Helper()
	svc := &artifact.ScopedService{
		Inner: memory.New(),
		Scope: "agent",
	}
	return &tool.Context{
		ArtifactService: svc,
		AgentName:       "test-agent",
		UserID:          "test-user",
		SessionID:       "test-session",
	}
}

func textItem(filename, body string) ContentItem {
	return ContentItem{
		Type:     ContentTypeText,
		Text:     body,
		MIMEType: "text/plain",
		Filename: filename,
		Metadata: map[string]string{"detected_format": "plain"},
	}
}

func mustCountSavedArtifacts(t *testing.T, r *tool.Result) int {
	t.Helper()
	if r == nil || r.Data == nil {
		t.Fatalf("result has no data: %+v", r)
	}
	details, ok := r.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if !ok {
		t.Fatalf("saved_mcp_response_artifact_details missing or wrong type: %T", r.Data["saved_mcp_response_artifact_details"])
	}
	return len(details.ArtifactsSaved)
}

func TestHandleMCPResponse_MaxContentItemsTruncates(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := make([]ContentItem, 0, 75)
	for i := range 75 {
		items = append(items, textItem(
			"flood_"+itoa(i)+".txt",
			strings.Repeat("x", 3000),
		))
	}

	cfg := DefaultHandlerConfig()

	result, err := HandleMCPResponse(ctx, items, "flood_tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	saved := mustCountSavedArtifacts(t, result)
	if saved != 50 {
		t.Fatalf("saved %d artifacts, want 50 (MaxContentItems cap)", saved)
	}
}

func TestClampCapInt(t *testing.T) {
	const ceiling = 10_000
	const dflt = 50
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"negative falls back", -1, dflt},
		{"zero falls back", 0, dflt},
		{"above ceiling falls back", ceiling + 1, dflt},
		{"in range passes", 123, 123},
		{"at ceiling passes", ceiling, ceiling},
		{"at 1 passes", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampCapInt(c.in, dflt, ceiling)
			if got != c.want {
				t.Errorf("clampCapInt(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestHandleMCPResponse_MaxContentItemsDisabled(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := make([]ContentItem, 0, 60)
	for i := range 60 {
		items = append(items, textItem(
			"uncapped_"+itoa(i)+".txt",
			strings.Repeat("x", 3000),
		))
	}

	cfg := DefaultHandlerConfig()
	cfg.MaxContentItems = 0

	result, err := HandleMCPResponse(ctx, items, "uncapped_tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if saved := mustCountSavedArtifacts(t, result); saved != 60 {
		t.Errorf("MaxContentItems=0 should not truncate; saved %d, want 60", saved)
	}
}

func TestHandleMCPResponse_MaxSingleItemSizeRejects(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	cfg := DefaultHandlerConfig()
	cfg.MaxSingleItemSizeMB = 1
	oversized := strings.Repeat("z", 2*1024*1024)
	small := strings.Repeat("k", DefaultSaveThresholdBytes+1000)

	items := []ContentItem{
		textItem("oversized.txt", oversized),
		textItem("small.txt", small),
	}

	result, err := HandleMCPResponse(ctx, items, "big_response_tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	saved := mustCountSavedArtifacts(t, result)
	if saved != 1 {
		t.Fatalf("saved %d artifacts, want 1 (oversized item dropped)", saved)
	}
	details := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if details.ArtifactsSaved[0].DataFilename != "small.txt" {
		t.Errorf("kept wrong artifact: %s, want small.txt", details.ArtifactsSaved[0].DataFilename)
	}
}

func TestHandleMCPResponse_AllItemsDropped(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	cfg := DefaultHandlerConfig()
	cfg.MaxSingleItemSizeMB = 1
	items := []ContentItem{
		textItem("too_big.txt", strings.Repeat("z", 2*1024*1024)),
	}
	result, err := HandleMCPResponse(ctx, items, "flood_tool", cfg, toolCtx, nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success; body=%q", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "dropped by content caps") {
		t.Errorf("expected caps-drop message, got %q", result.Message)
	}
}

func TestBuildMCPCustomMetadata_ContentParsingDisabled(t *testing.T) {
	item := ContentItem{
		Type:     ContentTypeText,
		Text:     `{"hello": "world"}`,
		MIMEType: "application/json",
		Filename: "payload.json",
		Metadata: map[string]string{"detected_format": string(TextFormatJSON)},
	}
	cfg := DefaultHandlerConfig()
	cfg.EnableContentParsing = false

	custom, _ := buildMCPCustomMetadata(item, "jsonish_tool", cfg)

	if _, ok := custom["parsed_data_summary"]; ok {
		t.Errorf("parsed_data_summary should be absent when EnableContentParsing=false")
	}
	if _, ok := custom["schema"]; ok {
		t.Errorf("schema should be absent when EnableContentParsing=false")
	}
	if df, _ := custom["detected_format"].(string); df != string(TextFormatJSON) {
		t.Errorf("detected_format = %v, want json (should still be recorded)", custom["detected_format"])
	}
}

func TestBuildMCPCustomMetadata_ContentParsingEnabled(t *testing.T) {
	item := ContentItem{
		Type:     ContentTypeText,
		Text:     `{"hello": "world"}`,
		MIMEType: "application/json",
		Filename: "payload.json",
		Metadata: map[string]string{"detected_format": string(TextFormatJSON)},
	}
	cfg := DefaultHandlerConfig()

	custom, _ := buildMCPCustomMetadata(item, "jsonish_tool", cfg)

	if _, ok := custom["parsed_data_summary"]; !ok {
		t.Errorf("parsed_data_summary should be present when EnableContentParsing=true")
	}
	if _, ok := custom["schema"]; !ok {
		t.Errorf("schema should be present for JSON when EnableContentParsing=true")
	}
}

func TestBuildHandlerConfig_KnobsTakeEffect(t *testing.T) {
	enabled := true
	tc := &tool.Context{
		ToolConfig: map[string]any{
			"mcp_intelligent_processing": map[string]any{
				"enable_intelligent_processing":  enabled,
				"enable_text_format_detection":   false,
				"enable_content_parsing":         false,
				"fallback_to_raw_on_error":       false,
				"save_raw_alongside_intelligent": true,
				"max_content_items":              7,
				"max_single_item_size_mb":        3,
			},
			"mcp_tool_response_save_threshold_bytes": 1234,
			"mcp_tool_llm_return_max_bytes":          5678,
			"schema_inference_depth":                 2,
		},
	}

	cfg := buildHandlerConfig(tc)

	if cfg.EnableIntelligentProcessing == nil || *cfg.EnableIntelligentProcessing != true {
		t.Errorf("EnableIntelligentProcessing not wired: %+v", cfg.EnableIntelligentProcessing)
	}
	if cfg.EnableTextFormatDetection != false {
		t.Errorf("EnableTextFormatDetection not wired")
	}
	if cfg.EnableContentParsing != false {
		t.Errorf("EnableContentParsing not wired")
	}
	if cfg.FallbackToRawOnError != false {
		t.Errorf("FallbackToRawOnError not wired")
	}
	if cfg.SaveRawAlongsideIntelligent != true {
		t.Errorf("SaveRawAlongsideIntelligent not wired")
	}
	if cfg.MaxContentItems != 7 {
		t.Errorf("MaxContentItems = %d, want 7", cfg.MaxContentItems)
	}
	if cfg.MaxSingleItemSizeMB != 3 {
		t.Errorf("MaxSingleItemSizeMB = %d, want 3", cfg.MaxSingleItemSizeMB)
	}
	if cfg.SaveThresholdBytes != 1234 {
		t.Errorf("SaveThresholdBytes = %d, want 1234", cfg.SaveThresholdBytes)
	}
	if cfg.LLMReturnMaxBytes != 5678 {
		t.Errorf("LLMReturnMaxBytes = %d, want 5678", cfg.LLMReturnMaxBytes)
	}
	if cfg.SchemaInferenceDepth != 2 {
		t.Errorf("SchemaInferenceDepth = %d, want 2", cfg.SchemaInferenceDepth)
	}
}

func TestBuildHandlerConfig_Defaults(t *testing.T) {
	cfg := buildHandlerConfig(&tool.Context{})
	def := DefaultHandlerConfig()
	if cfg.MaxContentItems != def.MaxContentItems {
		t.Errorf("MaxContentItems default = %d, want %d", cfg.MaxContentItems, def.MaxContentItems)
	}
	if cfg.MaxSingleItemSizeMB != def.MaxSingleItemSizeMB {
		t.Errorf("MaxSingleItemSizeMB default = %d, want %d", cfg.MaxSingleItemSizeMB, def.MaxSingleItemSizeMB)
	}
	if cfg.EnableTextFormatDetection != true {
		t.Errorf("EnableTextFormatDetection default = false, want true")
	}
	if cfg.EnableContentParsing != true {
		t.Errorf("EnableContentParsing default = false, want true")
	}
	if cfg.FallbackToRawOnError != true {
		t.Errorf("FallbackToRawOnError default = false, want true")
	}
	if cfg.SaveRawAlongsideIntelligent != false {
		t.Errorf("SaveRawAlongsideIntelligent default = true, want false")
	}
}

func TestBuildHandlerConfig_NilSafe(t *testing.T) {
	cfg := buildHandlerConfig(nil)
	if cfg.MaxContentItems != 50 {
		t.Errorf("nil ctx should yield defaults, got MaxContentItems=%d", cfg.MaxContentItems)
	}
}

func TestHandleMCPResponse_SaveRawAlongsideIntelligent(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := []ContentItem{
		textItem("one.txt", strings.Repeat("a", DefaultSaveThresholdBytes+1000)),
	}
	raw := &rawCallToolResult{
		Content: []json.RawMessage{
			json.RawMessage(`{"type":"text","text":"aaaa"}`),
		},
	}
	cfg := DefaultHandlerConfig()
	cfg.SaveRawAlongsideIntelligent = true

	result, err := HandleMCPResponse(ctx, items, "alongside_tool", cfg, toolCtx, raw)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	details, ok := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if !ok {
		t.Fatalf("missing details: %T", result.Data["saved_mcp_response_artifact_details"])
	}
	if details.FallbackArtifact == nil {
		t.Fatal("SaveRawAlongsideIntelligent=true should attach a FallbackArtifact")
	}
	if !strings.HasSuffix(details.FallbackArtifact.DataFilename, ".json") {
		t.Errorf("fallback filename = %q, want .json suffix", details.FallbackArtifact.DataFilename)
	}
}

func TestHandleMCPResponse_NoSaveRawAlongsideDefault(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := []ContentItem{
		textItem("one.txt", strings.Repeat("a", DefaultSaveThresholdBytes+1000)),
	}
	raw := &rawCallToolResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"aaaa"}`)},
	}
	cfg := DefaultHandlerConfig()

	result, err := HandleMCPResponse(ctx, items, "default_tool", cfg, toolCtx, raw)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	details := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if details.FallbackArtifact != nil {
		t.Errorf("default config should not attach FallbackArtifact; got %+v", details.FallbackArtifact)
	}
}

func TestIsYAML_RealParser(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"mapping", "name: test\nversion: 1.0\n", true},
		{"sequence", "- a\n- b\n- c\n", true},
		{"nested", "server:\n  host: localhost\n  port: 8080\n", true},
		{"bare scalar", "just a single plain line", false},
		{"garbage", "  :::\n\t- bad: : :\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isYAML(tc.input); got != tc.want {
				t.Errorf("isYAML(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
