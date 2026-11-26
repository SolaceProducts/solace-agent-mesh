// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

func TestParseSlidesArtifact_YAMLAndJSON(t *testing.T) {
	yamlSpec := []byte(`
- layout: title
  title: "Q3 Review"
  subtitle: "Engineering"
  bg_image_ref: "0"
- layout: big_number
  title: "Reliability"
  big_number: "99.9%"
  big_label: "uptime"
- layout: table
  title: "Numbers"
  table_data:
    headers: ["Quarter", "Revenue"]
    rows:
      - ["Q1", "1.2M"]
      - ["Q2", "1.4M"]
- layout: chart
  title: "Growth"
  chart_data:
    chart_type: "bar"
    series:
      - name: "Revenue"
        categories: ["Q1", "Q2"]
        values: [1.2, 1.4]
- layout: image_left
  title: "Photo"
  image_ref: "1"
  image_alt: "diagram"
`)

	jsonSpec := []byte(`[
  {"layout": "title", "title": "Q3 Review", "subtitle": "Engineering", "bg_image_ref": "0"},
  {"layout": "big_number", "title": "Reliability", "big_number": "99.9%", "big_label": "uptime"},
  {"layout": "table", "title": "Numbers", "table_data": {"headers": ["Quarter", "Revenue"], "rows": [["Q1", "1.2M"], ["Q2", "1.4M"]]}},
  {"layout": "chart", "title": "Growth", "chart_data": {"chart_type": "bar", "series": [{"name": "Revenue", "categories": ["Q1", "Q2"], "values": [1.2, 1.4]}]}},
  {"layout": "image_left", "title": "Photo", "image_ref": "1", "image_alt": "diagram"}
]`)

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"yaml", yamlSpec},
		{"json", jsonSpec},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slides, err := parseSlidesArtifact(sdk.Artifact{
				Filename: "spec." + tc.name,
				Content:  tc.content,
			})
			if err != nil {
				t.Fatalf("parseSlidesArtifact(%s): %v", tc.name, err)
			}
			if len(slides) != 5 {
				t.Fatalf("expected 5 slides, got %d", len(slides))
			}

			if slides[0].BgImageRef == nil || *slides[0].BgImageRef != "0" {
				t.Errorf("slide 0: bg_image_ref not parsed (got %v)", slides[0].BgImageRef)
			}
			if slides[1].BigNumber == nil || *slides[1].BigNumber != "99.9%" {
				t.Errorf("slide 1: big_number not parsed (got %v)", slides[1].BigNumber)
			}
			if slides[1].BigLabel == nil || *slides[1].BigLabel != "uptime" {
				t.Errorf("slide 1: big_label not parsed (got %v)", slides[1].BigLabel)
			}
			if slides[2].TableData == nil || len(slides[2].TableData.Rows) != 2 || slides[2].TableData.Rows[0][1] != "1.2M" {
				t.Errorf("slide 2: table_data not parsed (got %+v)", slides[2].TableData)
			}
			if slides[3].ChartData == nil || slides[3].ChartData.ChartType != "bar" || len(slides[3].ChartData.Series) != 1 {
				t.Errorf("slide 3: chart_data not parsed (got %+v)", slides[3].ChartData)
			}
			if slides[4].ImageRef == nil || *slides[4].ImageRef != "1" {
				t.Errorf("slide 4: image_ref not parsed (got %v)", slides[4].ImageRef)
			}
		})
	}
}

func TestParseSlidesArtifact_Errors(t *testing.T) {
	cases := []struct {
		name    string
		art     sdk.Artifact
		wantSub string
	}{
		{
			name:    "missing filename",
			art:     sdk.Artifact{Content: []byte("- layout: title")},
			wantSub: "slides artifact is missing",
		},
		{
			name:    "empty content",
			art:     sdk.Artifact{Filename: "deck.yaml"},
			wantSub: "is empty",
		},
		{
			name:    "empty array",
			art:     sdk.Artifact{Filename: "deck.yaml", Content: []byte("[]")},
			wantSub: "parsed to an empty array",
		},
		{
			name:    "malformed yaml",
			art:     sdk.Artifact{Filename: "deck.yaml", Content: []byte("- layout: title\n  bullets: [unclosed")},
			wantSub: "not valid YAML or JSON",
		},
		{
			name:    "wrong shape (object instead of array)",
			art:     sdk.Artifact{Filename: "deck.yaml", Content: []byte("layout: title\nbullets: [a, b]")},
			wantSub: "does not match the expected slide-array schema",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSlidesArtifact(tc.art)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestRenderSlideRoutesMarkdown(t *testing.T) {
	title := "The **Mesh**"
	sl := LayoutSlide{
		Layout:  "bullet_list",
		Title:   &title,
		Bullets: []string{"**Event-Driven AI:** uses `code` and a [link](https://x.test)"},
	}

	rc := NewRenderContext(nil, nil, defaultTheme())
	pres := pptxgen.New()
	pres.SetSlideSize(rc.SlideW, rc.SlideH)
	renderSlide(pres.AddSlide(), sl, rc, 1)

	out, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	xml := readZipEntry(t, out, "ppt/slides/slide1.xml")

	if !strings.Contains(xml, `b="1">`) {
		t.Errorf("expected a bold run from markdown, got none:\n%s", xml)
	}
	if !strings.Contains(xml, "<a:t>Event-Driven AI:</a:t>") || !strings.Contains(xml, "<a:t>Mesh</a:t>") {
		t.Errorf("bold run text missing (markdown not parsed):\n%s", xml)
	}
	if strings.Contains(xml, "**") {
		t.Errorf("literal markdown markers leaked into slide:\n%s", xml)
	}
	if !strings.Contains(xml, `typeface="Consolas"`) || !strings.Contains(xml, "<a:t>code</a:t>") {
		t.Errorf("inline code not rendered monospace:\n%s", xml)
	}
	if !strings.Contains(xml, "<a:t>link</a:t>") || strings.Contains(xml, "x.test") {
		t.Errorf("link should keep text and drop URL:\n%s", xml)
	}
}

func TestCreatePresentationTagsSpec(t *testing.T) {
	specName := "deck-spec.yaml"
	specContent := []byte("- layout: title\n  title: Hello")
	out := "mydeck"
	res, err := createPresentation(context.Background(), CreatePresentationParams{
		Title:      "Demo",
		OutputName: &out,
		Slides: sdk.Artifact{
			Filename: specName,
			Content:  specContent,
			MIMEType: "application/yaml",
		},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createPresentation: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success (%q)", res.Status, res.Message)
	}
	if len(res.DataObjects) != 2 {
		t.Fatalf("expected 2 data objects (pptx + spec), got %d", len(res.DataObjects))
	}

	deck := res.DataObjects[0]
	if deck.Name != "mydeck.pptx" || deck.MIMEType != pptxMIMEType {
		t.Errorf("first object = %s (%s), want mydeck.pptx (%s)", deck.Name, deck.MIMEType, pptxMIMEType)
	}
	if len(deck.Tags) != 0 {
		t.Errorf("deck should not be tagged, got %v", deck.Tags)
	}

	spec := res.DataObjects[1]
	if spec.Name != specName || !bytes.Equal(spec.Content, specContent) {
		t.Errorf("spec object = %s (%q), want %s (%q)", spec.Name, spec.Content, specName, specContent)
	}
	if !slices.Contains(spec.Tags, sdk.TagWorking) {
		t.Errorf("spec tags = %v, want to contain %q", spec.Tags, sdk.TagWorking)
	}
}

func TestCreatePresentationSkipsSpecReemitOnNameCollision(t *testing.T) {
	out := "deck"
	res, err := createPresentation(context.Background(), CreatePresentationParams{
		Title:      "Demo",
		OutputName: &out,
		Slides: sdk.Artifact{
			Filename: "deck.pptx",
			Content:  []byte("- layout: title"),
			MIMEType: "application/yaml",
		},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createPresentation: %v", err)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object (no spec re-emit on collision), got %d", len(res.DataObjects))
	}
	deck := res.DataObjects[0]
	if deck.Name != "deck.pptx" || deck.MIMEType != pptxMIMEType || len(deck.Tags) != 0 {
		t.Errorf("deck = %s (%s) tags=%v, want deck.pptx (%s) untagged", deck.Name, deck.MIMEType, deck.Tags, pptxMIMEType)
	}
}

func readZipEntry(t *testing.T, archive []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}
	t.Fatalf("entry %q not found in archive", name)
	return ""
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
