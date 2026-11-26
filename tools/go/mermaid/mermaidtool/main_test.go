// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mermaidtool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type fakeRenderer struct {
	png   []byte
	svg   string
	title string
	desc  string
	err   error
}

func (f *fakeRenderer) RenderAll(_ context.Context, _ string, _ int, svgOnly bool) (*RenderResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	r := &RenderResult{SVG: f.svg, Title: f.title, Desc: f.desc}
	if !svgOnly {
		r.PNG = f.png
	}
	return r, nil
}

func withFakeRenderer(t *testing.T, fr svgRenderer) {
	t.Helper()
	prev := defaultRenderer
	defaultRenderer = fr
	t.Cleanup(func() { defaultRenderer = prev })
}

func TestHandle_ChromiumUnavailableReturnsCleanMessage(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{err: &errChromiumNotFound{inner: fmt.Errorf("no Chromium binary found on PATH")}})

	res, err := handle(context.Background(), Params{MermaidSyntax: "graph TD; A-->B"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("handler returned a Go error; want a clean result error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "Chromium/Chrome browser") || !strings.Contains(res.Message, "SAM_CHROMIUM_PATH") {
		t.Errorf("message = %q, want the actionable Chromium-unavailable guidance", res.Message)
	}
}

func TestEnsureExtension(t *testing.T) {
	cases := []struct {
		in, format, want string
	}{
		{"diagram", "png", "diagram.png"},
		{"diagram.png", "png", "diagram.png"},
		{"DIAGRAM.PNG", "png", "DIAGRAM.PNG"},
		{"diagram.Png", "png", "diagram.Png"},
		{"file.jpg", "png", "file.jpg.png"},
		{"diagram", "svg", "diagram.svg"},
		{"diagram.png", "svg", "diagram.svg"},
		{"diagram.svg", "svg", "diagram.svg"},
	}
	for _, c := range cases {
		if got := ensureExtension(c.in, c.format); got != c.want {
			t.Errorf("ensureExtension(%q, %q) = %q, want %q", c.in, c.format, got, c.want)
		}
	}
}

func TestFinalFilename_UUIDFallback(t *testing.T) {
	got := finalFilename(nil, "png")
	re := regexp.MustCompile(`^mermaid_diagram_[0-9a-f-]{36}\.png$`)
	if !re.MatchString(got) {
		t.Errorf("finalFilename(nil, png) = %q, expected uuid-style name", got)
	}
}

func TestHandle_EmptySyntax(t *testing.T) {
	res, err := handle(context.Background(), Params{MermaidSyntax: "   "}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("handle returned unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status, got %v", res.Status)
	}
}

func TestHandle_HappyPath(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{
		png:   []byte{0x89, 0x50, 0x4E, 0x47},
		title: "My Title",
		desc:  "My Description",
	})

	fn := "my_diagram"
	res, err := handle(context.Background(), Params{
		MermaidSyntax:  "graph TD; A-->B",
		OutputFilename: &fn,
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %v: %s", res.Status, res.Message)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(res.DataObjects))
	}
	obj := res.DataObjects[0]
	if obj.Name != "my_diagram.png" {
		t.Errorf("filename = %q, want my_diagram.png", obj.Name)
	}
	if obj.MIMEType != "image/png" {
		t.Errorf("mime = %q, want image/png", obj.MIMEType)
	}
	if obj.Disposition != sdk.DispositionArtifact {
		t.Errorf("disposition = %q, want artifact", obj.Disposition)
	}
	if obj.Metadata["source_format"] != "mermaid_syntax" {
		t.Errorf("source_format missing: %v", obj.Metadata)
	}
	if obj.Metadata["mermaid_title"] != "My Title" {
		t.Errorf("title = %v", obj.Metadata["mermaid_title"])
	}
	if obj.Metadata["mermaid_description"] != "My Description" {
		t.Errorf("desc = %v", obj.Metadata["mermaid_description"])
	}
	if gt, _ := obj.Metadata["generation_tool"].(string); !strings.Contains(gt, "chromedp+mermaid.min.js") {
		t.Errorf("generation_tool = %v", obj.Metadata["generation_tool"])
	}
	if data := res.Data; data["mermaid_title"] != "My Title" || data["mermaid_description"] != "My Description" {
		t.Errorf("inline data mismatch: %v", data)
	}
	if !strings.Contains(obj.Description, "My Title") {
		t.Errorf("artifact description should include title, got %q", obj.Description)
	}
}

func TestHandle_EmptyPNG(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{png: nil})
	res, _ := handle(context.Background(), Params{MermaidSyntax: "graph TD; A-->B"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status, got %v", res.Status)
	}
}

func TestHandle_NoTitle_ReturnsNA(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{png: []byte("png")})
	res, _ := handle(context.Background(), Params{MermaidSyntax: "graph TD; A-->B"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %v: %s", res.Status, res.Message)
	}
	if res.Data["mermaid_title"] != "N/A" {
		t.Errorf("expected N/A title, got %v", res.Data["mermaid_title"])
	}
	if res.Data["mermaid_description"] != "N/A" {
		t.Errorf("expected N/A desc, got %v", res.Data["mermaid_description"])
	}
}

func TestHandle_FilenameFallback(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{png: []byte("png")})
	res, _ := handle(context.Background(), Params{MermaidSyntax: "graph TD; A-->B"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success: %s", res.Message)
	}
	name := res.DataObjects[0].Name
	re := regexp.MustCompile(`^mermaid_diagram_[0-9a-f-]{36}\.png$`)
	if !re.MatchString(name) {
		t.Errorf("filename = %q, expected uuid form", name)
	}
}

func TestExtractMetadata_TitleLine(t *testing.T) {
	title, desc := extractMetadata("title My Flow\naccDescr: Accessible description here\ngraph TD; A-->B")
	if title != "My Flow" {
		t.Errorf("title = %q", title)
	}
	if desc != "Accessible description here" {
		t.Errorf("desc = %q", desc)
	}
}

func TestExtractMetadata_FrontMatterTitle(t *testing.T) {
	src := "---\ntitle: Front Matter Title\n---\ngraph TD; A-->B"
	title, _ := extractMetadata(src)
	if title != "Front Matter Title" {
		t.Errorf("title = %q", title)
	}
}

func TestExtractMetadata_AccDescrBlock(t *testing.T) {
	src := "graph TD; A-->B\naccDescr {\nMulti-line\nblock description\n}"
	_, desc := extractMetadata(src)
	if !strings.Contains(desc, "Multi-line") || !strings.Contains(desc, "block description") {
		t.Errorf("desc = %q", desc)
	}
}

func TestExtractMetadata_Absent(t *testing.T) {
	title, desc := extractMetadata("graph TD; A-->B")
	if title != "" || desc != "" {
		t.Errorf("expected empty, got %q / %q", title, desc)
	}
}
