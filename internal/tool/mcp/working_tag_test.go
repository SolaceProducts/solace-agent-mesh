// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func companionTags(t *testing.T, toolCtx *tool.Context, filename string) []string {
	t.Helper()
	meta := artifact.LoadCompanionMetadata(context.Background(), toolCtx.ArtifactService,
		toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename, nil)
	if meta == nil {
		t.Fatalf("no companion metadata for %q (expected SaveArtifactWithMetadata to write one)", filename)
	}
	raw, ok := meta["tags"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestSaveContentItems_TagsTextWorking(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := []ContentItem{
		textItem("search_json_0_abc.json", strings.Repeat("x", 3000)),
	}
	if _, _, err := saveContentItems(ctx, items, "search", toolCtx, DefaultHandlerConfig()); err != nil {
		t.Fatalf("saveContentItems: %v", err)
	}

	tags := companionTags(t, toolCtx, "search_json_0_abc.json")
	if !slices.Contains(tags, a2a.ArtifactTagWorking) {
		t.Fatalf("text MCP artifact tags = %v, want to contain %q", tags, a2a.ArtifactTagWorking)
	}
}

func TestSaveContentItems_DoesNotTagNonTextWorking(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	items := []ContentItem{{
		Type:     ContentTypeImage,
		Data:     []byte("\x89PNG\r\n\x1a\nfake-image-bytes"),
		MIMEType: "image/png",
		Filename: "chart_image_0_def.png",
	}}
	if _, _, err := saveContentItems(ctx, items, "render", toolCtx, DefaultHandlerConfig()); err != nil {
		t.Fatalf("saveContentItems: %v", err)
	}

	tags := companionTags(t, toolCtx, "chart_image_0_def.png")
	if slices.Contains(tags, a2a.ArtifactTagWorking) {
		t.Fatalf("image MCP artifact tags = %v, should NOT contain %q", tags, a2a.ArtifactTagWorking)
	}
}

func TestHandleRawFallback_TagsWorking(t *testing.T) {
	ctx := context.Background()
	toolCtx := newMCPToolCtx(t)

	raw := &rawCallToolResult{
		Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"dump"}`)},
	}
	result, err := HandleRawFallback(ctx, raw, "search", DefaultHandlerConfig(), toolCtx)
	if err != nil {
		t.Fatalf("HandleRawFallback: %v", err)
	}
	details := result.Data["saved_mcp_response_artifact_details"].(*mcpSaveResult)
	if details.FallbackArtifact == nil {
		t.Fatal("HandleRawFallback should attach a FallbackArtifact")
	}

	tags := companionTags(t, toolCtx, details.FallbackArtifact.DataFilename)
	if !slices.Contains(tags, a2a.ArtifactTagWorking) {
		t.Fatalf("raw fallback artifact tags = %v, want to contain %q", tags, a2a.ArtifactTagWorking)
	}
}

func TestSaveContentItems_WorkingTagByContentKind(t *testing.T) {
	cases := []struct {
		name        string
		item        ContentItem
		wantWorking bool
	}{
		{
			name:        "plain text result",
			item:        textItem("search_text_0_abc.txt", "hello"),
			wantWorking: true,
		},
		{
			name: "markdown text resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Text:     "# Set Scaling Params\n\nbody",
				MIMEType: "text/markdown",
				URI:      "repo://owner/repo/contents/Set-Scaling-Params-HA.md",
				Filename: "get_file_contents_resource_1_Set-Scaling-Params-HA.md",
			},
			wantWorking: true,
		},
		{
			name: "json text resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Text:     `{"a":1}`,
				MIMEType: "application/json",
				Filename: "get_file_contents_resource_0_package.json",
			},
			wantWorking: true,
		},
		{
			name: "json blob resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Data:     []byte(`{"a":1}`),
				MIMEType: "application/json; charset=utf-8",
				Filename: "fetch_resource_0_data.json",
			},
			wantWorking: true,
		},
		{
			name: "untyped blob resource with utf-8 text",
			item: ContentItem{
				Type:     ContentTypeResource,
				Data:     []byte("key: value\n"),
				MIMEType: "application/octet-stream",
				Filename: "fetch_resource_0_config",
			},
			wantWorking: true,
		},
		{
			name: "png blob resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Data:     []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
				MIMEType: "image/png",
				Filename: "get_file_contents_resource_0_logo.png",
			},
			wantWorking: false,
		},
		{
			name: "pdf blob resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Data:     []byte("%PDF-1.7\n\x00\xe2\xe3\xcf\xd3"),
				MIMEType: "application/pdf",
				Filename: "get_file_contents_resource_0_report.pdf",
			},
			wantWorking: false,
		},
		{
			name: "untyped binary blob resource",
			item: ContentItem{
				Type:     ContentTypeResource,
				Data:     []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0xfe},
				MIMEType: "application/octet-stream",
				Filename: "get_file_contents_resource_0_bundle",
			},
			wantWorking: false,
		},
		{
			name: "image content",
			item: ContentItem{
				Type:     ContentTypeImage,
				Data:     []byte("\x89PNG\r\n\x1a\nfake-image-bytes"),
				MIMEType: "image/png",
				Filename: "chart_image_0_def.png",
			},
			wantWorking: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx := newMCPToolCtx(t)

			if _, _, err := saveContentItems(context.Background(), []ContentItem{tc.item}, "get_file_contents", toolCtx, DefaultHandlerConfig()); err != nil {
				t.Fatalf("saveContentItems: %v", err)
			}

			tags := companionTags(t, toolCtx, tc.item.Filename)
			if got := slices.Contains(tags, a2a.ArtifactTagWorking); got != tc.wantWorking {
				t.Fatalf("tags = %v, working = %v, want %v", tags, got, tc.wantWorking)
			}
		})
	}
}

func TestSaveContentItems_GitHubFileReadResourceIsWorking(t *testing.T) {
	toolCtx := newMCPToolCtx(t)

	raw := &rawCallToolResult{Content: []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"successfully downloaded text file"}`),
		json.RawMessage(`{"type":"resource","resource":{"uri":"repo://owner/repo/refs/heads/main/contents/docs/Set-Scaling-Params-HA.md","mimeType":"text/markdown; charset=utf-8","text":"# Set Scaling Params\n"}}`),
	}}
	items := convertCallToolResult(raw, "get_file_contents")
	if len(items) != 2 {
		t.Fatalf("convertCallToolResult returned %d items, want 2", len(items))
	}
	if _, _, err := saveContentItems(context.Background(), items, "get_file_contents", toolCtx, DefaultHandlerConfig()); err != nil {
		t.Fatalf("saveContentItems: %v", err)
	}

	for _, item := range items {
		tags := companionTags(t, toolCtx, item.Filename)
		if !slices.Contains(tags, a2a.ArtifactTagWorking) {
			t.Errorf("%s tags = %v, want to contain %q", item.Filename, tags, a2a.ArtifactTagWorking)
		}
	}
}
