// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func mustDataObject(t *testing.T, res *sdk.Result, name string) sdk.DataObject {
	t.Helper()
	for _, obj := range res.DataObjects {
		if obj.Name == name {
			return obj
		}
	}
	t.Fatalf("result has no data object named %q (got %v)", name, dataObjectNames(res))
	return sdk.DataObject{}
}

func dataObjectNames(res *sdk.Result) []string {
	var names []string
	for _, obj := range res.DataObjects {
		names = append(names, obj.Name)
	}
	return names
}

func documentXML(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open docx as zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open word/document.xml: %v", err)
		}
		var buf bytes.Buffer
		_, readErr := buf.ReadFrom(rc)
		_ = rc.Close()
		if readErr != nil {
			t.Fatalf("read word/document.xml: %v", readErr)
		}
		return buf.String()
	}
	t.Fatal("docx has no word/document.xml")
	return ""
}

func TestCreateDocument_AllBlockTypes(t *testing.T) {
	content := sdk.Artifact{Filename: "spec.yaml", Content: []byte(`
- type: heading
  level: 1
  text: "Report Title"
- type: paragraph
  text: "Plain paragraph."
- type: paragraph
  runs:
    - text: "Mixed "
    - text: "bold"
      bold: true
- type: bullet_list
  items: ["one", "two"]
- type: numbered_list
  items: ["step a", "step b"]
- type: table
  headers: ["Col1", "Col2"]
  rows:
    - ["r1c1", "r1c2"]
- type: page_break
- type: table_of_contents
  toc_levels: "1-2"
- type: image
  image_ref: "0"
  width_in: 2
`)}

	png := makeTestPNG(t)
	p := CreateDocumentParams{
		Title:   "Report Title",
		Content: content,
		Images:  []sdk.Artifact{{Filename: "logo.png", MIMEType: "image/png", Content: png}},
	}

	res, err := createDocument(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
	}

	obj := mustDataObject(t, res, "document.docx")
	doc := documentXML(t, obj.Content)

	for _, want := range []string{
		"Report Title", "Plain paragraph.", "Mixed ", "bold",
		"one", "two", "step a", "step b", "r1c1", "r1c2",
		`w:type="page"`, `w:fldCharType="begin"`, `r:embed="rId101"`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing %q", want)
		}
	}
}

func TestCreateDocument_OutputNameDefaultAndExtension(t *testing.T) {
	content := sdk.Artifact{Filename: "spec.yaml", Content: []byte(`
- type: heading
  level: 1
  text: "T"
`)}
	res, err := createDocument(context.Background(), CreateDocumentParams{Title: "T", Content: content}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	mustDataObject(t, res, "document.docx")

	res2, err := createDocument(context.Background(), CreateDocumentParams{Title: "T", Content: content, OutputName: new("my-report")}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	mustDataObject(t, res2, "my-report.docx")
}

func TestCreateDocument_ReemitsSpecArtifactAsWorking(t *testing.T) {
	content := sdk.Artifact{Filename: "spec.yaml", Content: []byte(`
- type: heading
  level: 1
  text: "T"
`)}
	res, err := createDocument(context.Background(), CreateDocumentParams{Title: "T", Content: content}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	spec := mustDataObject(t, res, "spec.yaml")
	if len(spec.Tags) != 1 || spec.Tags[0] != sdk.TagWorking {
		t.Errorf("spec artifact tags = %v, want [%s]", spec.Tags, sdk.TagWorking)
	}
}

func TestCreateDocument_EmptyContentArtifact_Errors(t *testing.T) {
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: nil},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument returned Go error (want a Result error): %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("status = %q, want error", res.Status)
	}
}

func TestCreateDocument_UnknownBlockType_Errors(t *testing.T) {
	content := sdk.Artifact{Filename: "spec.yaml", Content: []byte(`
- type: not_a_real_block
`)}
	res, err := createDocument(context.Background(), CreateDocumentParams{Title: "T", Content: content}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "not_a_real_block") {
		t.Errorf("error message %q does not mention the bad block type", res.Message)
	}
}

func TestCreateDocument_ImageRefOutOfRange_DegradesRatherThanDiscarding(t *testing.T) {
	content := sdk.Artifact{Filename: "spec.yaml", Content: []byte(`
- type: heading
  level: 1
  text: "Kept"
- type: image
  image_ref: "5"
- type: paragraph
  text: "Also kept"
`)}
	res, err := createDocument(context.Background(), CreateDocumentParams{Title: "T", Content: content}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusPartial {
		t.Fatalf("status = %q, want partial", res.Status)
	}
	if !strings.Contains(res.Message, "block 2") {
		t.Errorf("message %q should identify which block degraded", res.Message)
	}
	if len(res.DataObjects) == 0 {
		t.Fatal("a partial result must still return the document")
	}

	doc := documentXML(t, res.DataObjects[0].Content)
	for _, want := range []string{"Kept", "Also kept", "image unavailable"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing %q", want)
		}
	}
}

func TestCreateDocument_TableSpansTextColumn(t *testing.T) {
	spec := `
- type: table
  headers: ["Principle", "Description"]
  rows:
    - ["Event-Driven", "All inter-agent communication flows through the Solace event broker, enabling decoupling, resilience, and scalability."]
    - ["Composability", "Agents and workflows can be assembled like building blocks to address complex use cases."]
`
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
	}
	doc := documentXML(t, res.DataObjects[0].Content)

	if strings.Contains(doc, `w:w="0"`) {
		t.Errorf("table declares a zero width, the defect being fixed:\n%s", doc)
	}
	if !strings.Contains(doc, `<w:tblW w:type="pct" w:w="5000">`) {
		t.Errorf("table does not claim the full text column:\n%s", doc)
	}
	if got := strings.Count(doc, `<w:gridCol w:w="4680">`); got != 2 {
		t.Errorf("expected 2 gridCols at half the 9360-twip text column, got %d:\n%s", got, doc)
	}
}

func TestCreateDocument_ColWidthsReachTheDocument(t *testing.T) {
	spec := `
- type: table
  headers: ["Principle", "Description"]
  col_widths: [1, 3]
  rows:
    - ["Event-Driven", "Flows through the Solace event broker."]
`
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
	}
	doc := documentXML(t, res.DataObjects[0].Content)

	if !strings.Contains(doc, `<w:tblLayout w:type="fixed">`) {
		t.Errorf("explicit shares should pin the layout as fixed:\n%s", doc)
	}
	for _, want := range []string{`<w:gridCol w:w="2340">`, `<w:gridCol w:w="7020">`} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing %s (shares [1 3] over 9360 twips):\n%s", want, doc)
		}
	}
}

func TestValidateColWidths_RejectsNonFiniteShares(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shares []float64
	}{
		{name: "NaN", shares: []float64{1, math.NaN()}},
		{name: "positive infinity", shares: []float64{1, math.Inf(1)}},
		{name: "negative infinity", shares: []float64{1, math.Inf(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateColWidths(tc.shares, len(tc.shares))
			if err == nil {
				t.Fatalf("validateColWidths(%v) = nil, want an error", tc.shares)
			}
			if !strings.Contains(err.Error(), "positive number") {
				t.Errorf("error %q should tell the caller what a valid share is", err)
			}
		})
	}
}

func TestParseContentArtifact_JSONWorks(t *testing.T) {
	art := sdk.Artifact{Filename: "spec.json", Content: []byte(`[{"type":"heading","level":1,"text":"Hi"}]`)}
	blocks, err := parseContentArtifact(art)
	if err != nil {
		t.Fatalf("parseContentArtifact: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != "heading" || blocks[0].Text != "Hi" {
		t.Errorf("unexpected blocks: %+v", blocks)
	}
}

func TestParseContentArtifact_MissingFilename_Errors(t *testing.T) {
	if _, err := parseContentArtifact(sdk.Artifact{}); err == nil {
		t.Error("expected error for artifact with no filename")
	}
}

func TestDecodeImage_DerivesFormatFromBytesNotLabels(t *testing.T) {
	png4x4 := makeTestPNG(t)

	format, ratio, err := decodeImage(png4x4)
	if err != nil {
		t.Fatalf("decodeImage(valid png): %v", err)
	}
	if format != "png" {
		t.Errorf("format = %q, want png", format)
	}
	if ratio != 1.0 {
		t.Errorf("ratio = %v, want 1.0 for a 4x4 image", ratio)
	}

	if _, _, err := decodeImage([]byte("this is definitely not an image")); err == nil {
		t.Error("expected an error for undecodable bytes, got nil")
	}
}

func makeTestPNG(t *testing.T) []byte {
	t.Helper()
	return makePNG(t, 4, 4)
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}
