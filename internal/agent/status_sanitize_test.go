// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestSanitizeBinaryInPlace_FlatBytes(t *testing.T) {
	m := map[string]any{
		"mime_type": "image/png",
		"data":      []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A},
	}
	sanitizeBinaryInPlace(m)
	if m["data"] != "<binary data: 6 bytes>" {
		t.Errorf("data = %v", m["data"])
	}
	if m["mime_type"] != "image/png" {
		t.Errorf("mime_type preserved: %v", m["mime_type"])
	}
}

func TestSanitizeBinaryInPlace_NestedMap(t *testing.T) {
	m := map[string]any{
		"contents": map[string]any{
			"parts": map[string]any{
				"inline_data": map[string]any{
					"mime_type": "image/jpeg",
					"data":      []byte("somebinary"),
				},
			},
		},
	}
	sanitizeBinaryInPlace(m)
	inner := m["contents"].(map[string]any)["parts"].(map[string]any)["inline_data"].(map[string]any)
	if inner["data"] != "<binary data: 10 bytes>" {
		t.Errorf("nested data = %v", inner["data"])
	}
}

func TestSanitizeBinaryInPlace_ListOfBytes(t *testing.T) {
	m := map[string]any{
		"items": []any{
			[]byte("abc"),
			"keep me",
			[]byte("defgh"),
		},
	}
	sanitizeBinaryInPlace(m)
	items := m["items"].([]any)
	if items[0] != "<binary data: 3 bytes>" {
		t.Errorf("items[0] = %v", items[0])
	}
	if items[1] != "keep me" {
		t.Errorf("items[1] = %v", items[1])
	}
	if items[2] != "<binary data: 5 bytes>" {
		t.Errorf("items[2] = %v", items[2])
	}
}

func TestSanitizeBinaryInPlace_MapInsideList(t *testing.T) {
	m := map[string]any{
		"parts": []any{
			map[string]any{
				"inline_data": map[string]any{
					"mime_type": "image/png",
					"data":      []byte("xxx"),
				},
			},
			map[string]any{"text": "hi"},
		},
	}
	sanitizeBinaryInPlace(m)
	first := m["parts"].([]any)[0].(map[string]any)["inline_data"].(map[string]any)
	if first["data"] != "<binary data: 3 bytes>" {
		t.Errorf("nested-in-list bytes not sanitised: %v", first["data"])
	}
}

func TestSanitizeBinaryInPlace_NoOpWhenNoBytes(t *testing.T) {
	m := map[string]any{
		"model": "gpt-4",
		"contents": []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
		},
	}
	before := deepCopy(m)
	sanitizeBinaryInPlace(m)
	if !reflect.DeepEqual(m, before) {
		t.Errorf("sanitizer mutated a byte-free map:\nbefore=%#v\nafter =%#v", before, m)
	}
}

func TestSanitizeBinaryInPlace_RawMessage(t *testing.T) {
	m := map[string]any{"args": json.RawMessage(`{"x":1}`)}
	sanitizeBinaryInPlace(m)
	got, ok := m["args"].(string)
	if !ok {
		t.Fatalf("expected sanitised string, got %T", m["args"])
	}
	if got != "<binary data: 7 bytes>" {
		t.Errorf("raw message sanitised to %q", got)
	}
}

func TestSanitizeBinaryInPlace_ImageDataURIString(t *testing.T) {
	m := map[string]any{
		"image_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg==",
	}
	sanitizeBinaryInPlace(m)
	got, ok := m["image_url"].(string)
	if !ok {
		t.Fatalf("expected string replacement, got %T", m["image_url"])
	}
	if got == "" || got[:14] != "<binary data: " {
		t.Errorf("expected sanitised placeholder, got %q", got)
	}
}

func TestSanitizeBinaryInPlace_PlainTextStringPreserved(t *testing.T) {
	m := map[string]any{"text": "hello world"}
	sanitizeBinaryInPlace(m)
	if m["text"] != "hello world" {
		t.Errorf("sanitizer clobbered plain string: %v", m["text"])
	}
}

func TestFormatLLMRequest_ImageContentBlockSanitised(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{
				Role:    "user",
				Content: "describe this image",
				ContentBlocks: []llm.ContentBlock{
					{Type: "text", Text: "describe this image"},
					{Type: "image_url", ImageURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="},
				},
			},
		},
	}
	m := formatLLMRequest(req, "claude-sonnet-4")
	sanitizeBinaryInPlace(m)

	contents, _ := m["contents"].([]map[string]any)
	if len(contents) == 0 {
		t.Fatalf("expected contents, got %#v", m)
	}
	parts, _ := contents[0]["parts"].([]map[string]any)
	var sawInline bool
	for _, p := range parts {
		if inline, ok := p["inline_data"].(map[string]any); ok {
			sawInline = true
			if mime, _ := inline["mime_type"].(string); mime != "image/png" {
				t.Errorf("mime_type = %v", inline["mime_type"])
			}
			replacement, ok := inline["data"].(string)
			if !ok {
				t.Fatalf("expected sanitised data string, got %T", inline["data"])
			}
			if len(replacement) < len("<binary data:") || replacement[:14] != "<binary data: " {
				t.Errorf("expected sanitised placeholder, got %q", replacement)
			}
		}
	}
	if !sawInline {
		t.Errorf("expected inline_data part in formatted request, got %#v", parts)
	}
}

func TestFormatLLMRequest_FileContentBlockSurfacedAndSanitised(t *testing.T) {
	rawB64 := "JVBERi0xLjQKJVBERg=="
	req := &llm.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{
				Role:    "user",
				Content: "read this",
				ContentBlocks: []llm.ContentBlock{
					{Type: "text", Text: "read this"},
					{Type: "file", FileData: "data:application/pdf;base64," + rawB64, FileName: "scan.pdf", FileType: "application/pdf"},
				},
			},
		},
	}
	m := formatLLMRequest(req, "claude-sonnet-4")
	sanitizeBinaryInPlace(m)

	contents, _ := m["contents"].([]map[string]any)
	if len(contents) == 0 {
		t.Fatalf("expected contents, got %#v", m)
	}
	parts, _ := contents[0]["parts"].([]map[string]any)
	var sawInline bool
	for _, p := range parts {
		inline, ok := p["inline_data"].(map[string]any)
		if !ok {
			continue
		}
		sawInline = true
		if mime, _ := inline["mime_type"].(string); mime != "application/pdf" {
			t.Errorf("mime_type = %v, want application/pdf", inline["mime_type"])
		}
		data, _ := inline["data"].(string)
		if len(data) < len("<binary data: ") || data[:14] != "<binary data: " {
			t.Errorf("expected placeholder, got %q", data)
		}
		if strings.Contains(data, rawB64) {
			t.Errorf("raw base64 leaked into status dump: %q", data)
		}
	}
	if !sawInline {
		t.Errorf("expected inline_data part for the PDF file block, got %#v", parts)
	}
}

func deepCopy(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}
