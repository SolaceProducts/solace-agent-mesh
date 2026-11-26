// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"mime"
	"testing"
)

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"data.csv", "text/csv"},
		{"config.json", "application/json"},
		{"task.stim", "application/x-yaml"},
		{"deploy.yaml", "application/x-yaml"},
		{"doc.md", "text/markdown"},
		{"page.html", "text/html"},
		{"feed.xml", "application/xml"},
		{"notes.txt", "text/plain"},
		{"script.py", "text/x-python"},
		{"main.go", "text/x-go"},
		{"app.js", "text/javascript"},
		{"app.ts", "text/typescript"},
		{"image.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"anim.gif", "image/gif"},
		{"logo.svg", "image/svg+xml"},
		{"doc.pdf", "application/pdf"},
		{"unknown", "text/plain; charset=utf-8"},
	}
	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			if got := DetectMIMEType(tc.filename, []byte("some text content")); got != tc.want {
				t.Errorf("DetectMIMEType(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

func TestDetectMIMEType_EmptyContent(t *testing.T) {
	if got := DetectMIMEType("unknownext", nil); got != "application/octet-stream" {
		t.Errorf("empty content with unknown ext = %q, want application/octet-stream", got)
	}
}

func TestDetectMIMEType_OSRegistryFallback(t *testing.T) {
	reg := mime.TypeByExtension(".xlsx")
	if reg == "" {
		t.Skip("OS mime registry has no .xlsx mapping on this platform")
	}
	if got := DetectMIMEType("sheet.xlsx", []byte("PK\x03\x04")); got != reg {
		t.Errorf("DetectMIMEType(sheet.xlsx) = %q, want OS-registry %q", got, reg)
	}
}

func TestIsGenericMIME(t *testing.T) {
	generic := []string{"", "application/octet-stream", "text/plain", "text/plain; charset=utf-8"}
	for _, m := range generic {
		if !IsGenericMIME(m) {
			t.Errorf("IsGenericMIME(%q) = false, want true", m)
		}
	}
	specific := []string{"application/x-yaml", "application/json", "text/csv", "image/png"}
	for _, m := range specific {
		if IsGenericMIME(m) {
			t.Errorf("IsGenericMIME(%q) = true, want false", m)
		}
	}
}

func TestResolveDataMIME(t *testing.T) {
	yaml := []byte("a: 1\nb: 2\n")
	if got := ResolveDataMIME("task.stim", "text/plain; charset=utf-8", yaml); got != "application/x-yaml" {
		t.Errorf("stim: got %q, want application/x-yaml", got)
	}
	if got := ResolveDataMIME("data.bin", "image/png", []byte{0x89}); got != "image/png" {
		t.Errorf("preserve: got %q, want image/png", got)
	}
	if got := ResolveDataMIME("notes.txt", "text/plain", []byte("hello")); !IsGenericMIME(got) {
		t.Errorf("plain text: got %q, want a generic type", got)
	}
}

func TestIsStructuredMIME(t *testing.T) {
	for _, m := range []string{"text/csv", "application/json", "application/x-yaml", "text/yaml"} {
		if !IsStructuredMIME(m) {
			t.Errorf("IsStructuredMIME(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"text/plain", "image/png", "application/pdf", ""} {
		if IsStructuredMIME(m) {
			t.Errorf("IsStructuredMIME(%q) = true, want false", m)
		}
	}
}

func TestIsSAMConfigMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"application/vnd.sam-workflow-config+yaml", true},
		{"application/vnd.sam-agent-config+yaml", true},
		{"application/vnd.sam-connector-config+json", true},
		{"application/vnd.sam-build-manifest+yaml", true},
		{"Application/vnd.SAM-agent-config+yaml", true},
		{"application/vnd.sam-agent-config+yaml; charset=utf-8", true},
		{"text/html", false},
		{"application/x-yaml", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsSAMConfigMIME(tc.mime); got != tc.want {
			t.Errorf("IsSAMConfigMIME(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestInlineContentType(t *testing.T) {
	tests := []struct {
		mime       string
		wantType   string
		wantInline bool
	}{
		{"image/png", "image/png", true},
		{"IMAGE/JPEG; charset=binary", "image/jpeg; charset=binary", true},
		{"image/gif", "image/gif", true},
		{"image/webp", "image/webp", true},
		{"application/pdf", "application/pdf", true},
		{"text/plain; charset=utf-8", "text/plain; charset=utf-8", true},
		{"text/csv", "text/csv", true},
		{"text/markdown", "text/markdown", true},
		{"application/json", "application/json", true},
		{"audio/mpeg", "audio/mpeg", true},
		{"video/mp4", "video/mp4", true},
		{`text/plain; a="x,text/html"`, `text/plain; a="x,text/html"`, true},
		{"text/html", "", false},
		{"Text/HTML; charset=utf-8", "", false},
		{"application/xhtml+xml", "", false},
		{"image/svg+xml", "", false},
		{"application/xml", "", false},
		{"text/xml", "", false},
		{"application/rss+xml", "", false},
		{"application/octet-stream", "", false},
		{"text/html,image/png", "", false},
		{"text/plain; x=1,text/html", "", false},
		{"audio/mpeg,text/html", "", false},
		{"image/png;,text/html", "", false},
		{"text/plain;charset=x;text/html", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		gotType, gotInline := InlineContentType(tc.mime)
		if gotType != tc.wantType || gotInline != tc.wantInline {
			t.Errorf("InlineContentType(%q) = (%q, %v), want (%q, %v)", tc.mime, gotType, gotInline, tc.wantType, tc.wantInline)
		}
	}
}

func TestIsYAMLMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"application/yaml", true},
		{"text/yaml", true},
		{"application/x-yaml", true},
		{"text/x-yaml", true},
		{"application/vnd.sam-build-manifest+yaml", true},
		{"application/vnd.sam-workflow-config+yaml", true},
		{"Application/VND.SAM-Agent-Config+YAML", true},
		{"application/vnd.sam-build-manifest+yaml; charset=utf-8", true},
		{"  application/yaml  ", true},
		{"application/json", false},
		{"application/vnd.sam-connector-config+json", false},
		{"application/vnd.sam-template+zip", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsYAMLMIME(tc.mime); got != tc.want {
			t.Errorf("IsYAMLMIME(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestIsCSVMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"text/csv", true},
		{"application/csv", true},
		{"text/csv; charset=utf-8", true},
		{"Text/CSV; charset=UTF-8", true},
		{"  text/csv  ", true},
		{"application/json", false},
		{"application/yaml", false},
		{"text/plain", false},
		{"application/vnd.sam-build-manifest+yaml", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsCSVMIME(tc.mime); got != tc.want {
			t.Errorf("IsCSVMIME(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestIsJSONMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"application/json", true},
		{"text/json", true},
		{"application/vnd.sam-connector-config+json", true},
		{"Application/JSON; charset=utf-8", true},
		{"application/yaml", false},
		{"application/vnd.sam-build-manifest+yaml", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsJSONMIME(tc.mime); got != tc.want {
			t.Errorf("IsJSONMIME(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestIsStructuredMIME_VendorSuffix(t *testing.T) {
	for _, m := range []string{"application/vnd.sam-build-manifest+yaml", "application/vnd.sam-connector-config+json"} {
		if !IsStructuredMIME(m) {
			t.Errorf("IsStructuredMIME(%q) = false, want true", m)
		}
	}
}
