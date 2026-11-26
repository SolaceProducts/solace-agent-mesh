// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"testing"
)

func TestExtractToolNameFromTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/sam_remote_tool/invoke/echo_tool", "echo_tool"},
		{"single", ""},
		{"trailing/", ""},
		{"a/b/c", "c"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractToolNameFromTopic(tt.topic)
		if got != tt.want {
			t.Errorf("extractToolNameFromTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

func TestGetProfile_UnknownFallsBackToStandard(t *testing.T) {
	p := GetProfile("nonexistent-profile")
	std := Profiles["standard"]
	if p.MaxCPUSeconds != std.MaxCPUSeconds {
		t.Errorf("unknown profile MaxCPUSeconds = %d, want standard %d", p.MaxCPUSeconds, std.MaxCPUSeconds)
	}
	if p.MaxOpenFiles != std.MaxOpenFiles {
		t.Errorf("unknown profile MaxOpenFiles = %d, want standard %d", p.MaxOpenFiles, std.MaxOpenFiles)
	}
}

func TestGetProfile_EmptyFallsBackToStandard(t *testing.T) {
	p := GetProfile("")
	std := Profiles["standard"]
	if p.MaxCPUSeconds != std.MaxCPUSeconds {
		t.Errorf("empty profile MaxCPUSeconds = %d, want standard %d", p.MaxCPUSeconds, std.MaxCPUSeconds)
	}
}

func TestNormalizeToolResult_Nil(t *testing.T) {
	runner := &SandboxRunner{logger: slog.Default()}
	result := runner.normalizeToolResult(nil)
	if result["text"] != nil {
		t.Errorf("nil input text = %v, want nil", result["text"])
	}
	if result["data_objects"] == nil {
		t.Error("nil input should have data_objects key")
	}
}

func TestNormalizeToolResult_Map(t *testing.T) {
	runner := &SandboxRunner{logger: slog.Default()}
	input := map[string]any{"text": "hello", "data_objects": []any{}}
	result := runner.normalizeToolResult(input)
	if result["text"] != "hello" {
		t.Errorf("map input text = %v, want hello", result["text"])
	}
}

func TestNormalizeToolResult_UnexpectedType(t *testing.T) {
	runner := &SandboxRunner{logger: slog.Default()}
	result := runner.normalizeToolResult(42)
	if result["text"] != "42" {
		t.Errorf("int input text = %v, want '42'", result["text"])
	}
}

func TestGuessMIMEType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"data.json", "application/json"},
		{"data.csv", "text/csv"},
		{"readme.md", "text/markdown"},
		{"page.html", "text/html"},
		{"page.htm", "text/html"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"image.gif", "image/gif"},
		{"doc.pdf", "application/pdf"},
		{"archive.zip", "application/zip"},
		{"script.py", "text/x-python"},
		{"config.yaml", "application/yaml"},
		{"config.yml", "application/yaml"},
		{"unknown.xyz", "application/octet-stream"},
		{"IMAGE.PNG", "image/png"},
	}
	for _, tt := range tests {
		got := guessMIMEType(tt.filename)
		if got != tt.want {
			t.Errorf("guessMIMEType(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
