// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestConvertResponse_NilResponse(t *testing.T) {
	result := convertResponse(nil, "test", DefaultProcessingConfig())
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
}

func TestConvertResponse_EmptyContent(t *testing.T) {
	resp := &schemas.BifrostMCPResponse{
		ChatMessage: &schemas.ChatMessage{},
	}
	result := convertResponse(resp, "test", DefaultProcessingConfig())
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success", result.Status)
	}
}

func TestConvertResponse_SimpleText(t *testing.T) {
	content := "Hello, world!"
	resp := &schemas.BifrostMCPResponse{
		ChatMessage: &schemas.ChatMessage{
			Content: &schemas.ChatMessageContent{
				ContentStr: &content,
			},
		},
	}

	result := convertResponse(resp, "test", DefaultProcessingConfig())
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success", result.Status)
	}
	if result.Message != "Hello, world!" {
		t.Errorf("message = %q, want Hello, world!", result.Message)
	}
}

func TestConvertResponse_LargeJSON(t *testing.T) {
	content := `{"key1": "` + string(make([]byte, 3000)) + `"}`
	resp := &schemas.BifrostMCPResponse{
		ChatMessage: &schemas.ChatMessage{
			Content: &schemas.ChatMessageContent{
				ContentStr: &content,
			},
		},
	}

	result := convertResponse(resp, "test", DefaultProcessingConfig())
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want success", result.Status)
	}
	if len(result.DataObjects) == 0 {
		t.Error("expected DataObjects for large JSON content")
	}
}

func TestConvertResponse_IntelligentProcessingDisabled(t *testing.T) {
	content := `{"key": "value"}`
	resp := &schemas.BifrostMCPResponse{
		ChatMessage: &schemas.ChatMessage{
			Content: &schemas.ChatMessageContent{
				ContentStr: &content,
			},
		},
	}

	procCfg := DefaultProcessingConfig()
	procCfg.EnableIntelligentProcessing = false

	result := convertResponse(resp, "test", procCfg)
	if result.Message != content {
		t.Errorf("message = %q, want raw content", result.Message)
	}
	if len(result.DataObjects) > 0 {
		t.Error("should not have DataObjects when intelligent processing is disabled")
	}
}

func TestContentItemToDataObject_Text(t *testing.T) {
	item := ContentItem{
		Type:     ContentTypeText,
		Text:     "hello",
		MIMEType: "text/plain",
		Filename: "result.txt",
		Metadata: map[string]string{"detected_format": "plain"},
	}

	do := contentItemToDataObject(item, DefaultProcessingConfig())
	if do == nil {
		t.Fatal("DataObject is nil")
	}
	if do.Disposition != tool.DispositionAuto {
		t.Errorf("disposition = %q, want auto", do.Disposition)
	}
	if string(do.Content) != "hello" {
		t.Errorf("content = %q", do.Content)
	}
}

func TestContentItemToDataObject_Image(t *testing.T) {
	item := ContentItem{
		Type:     ContentTypeImage,
		Data:     []byte("fake-image"),
		MIMEType: "image/png",
		Filename: "image.png",
	}

	do := contentItemToDataObject(item, DefaultProcessingConfig())
	if do == nil {
		t.Fatal("DataObject is nil")
	}
	if do.Disposition != tool.DispositionArtifact {
		t.Errorf("disposition = %q, want artifact", do.Disposition)
	}
}

func TestExtractResponseContent_StringContent(t *testing.T) {
	content := "test content"
	resp := &schemas.BifrostMCPResponse{
		ChatMessage: &schemas.ChatMessage{
			Content: &schemas.ChatMessageContent{
				ContentStr: &content,
			},
		},
	}

	got := extractResponseContent(resp)
	if got != "test content" {
		t.Errorf("got %q, want 'test content'", got)
	}
}

func TestExtractResponseContent_NilMessage(t *testing.T) {
	resp := &schemas.BifrostMCPResponse{}
	got := extractResponseContent(resp)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMarshalArgs(t *testing.T) {
	args := map[string]any{"key": "value"}
	got, err := marshalArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `{"key":"value"}` {
		t.Errorf("got %q", got)
	}

	got, err = marshalArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "{}" {
		t.Errorf("got %q, want {}", got)
	}
}
