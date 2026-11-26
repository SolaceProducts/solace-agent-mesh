// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"testing"
)

const pptxNotesNamespaces = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`

func buildPPTXParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip.Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func pptxSlidePart(body string) string {
	return `<p:sld ` + pptxNotesNamespaces + `><p:cSld><p:spTree>` + body + `</p:spTree></p:cSld></p:sld>`
}

func pptxNotesPart(slideNumber int, notesParagraphs ...string) string {
	var paras strings.Builder
	for _, p := range notesParagraphs {
		paras.WriteString(pptxPara(p))
	}
	return `<p:notes ` + pptxNotesNamespaces + `><p:cSld><p:spTree>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image"/><p:cNvSpPr/><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes"/><p:cNvSpPr/><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/>` + paras.String() + `</p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="4" name="Slide Number"/><p:cNvSpPr/><p:nvPr><p:ph type="sldNum" idx="5"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:p><a:fld id="{1}" type="slidenum"><a:t>` + strconv.Itoa(slideNumber) + `</a:t></a:fld></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld></p:notes>`
}

func pptxSlideRels(notesFile string) string {
	rels := `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`
	if notesFile != "" {
		rels += `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/` + notesFile + `"/>`
	}
	return `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels + `</Relationships>`
}

func TestConvertPPTX_SpeakerNotes(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":                 pptxSlidePart(pptxPara("Opening title")),
		"ppt/slides/slide2.xml":                 pptxSlidePart(pptxPara("Second slide body")),
		"ppt/slides/_rels/slide1.xml.rels":      pptxSlideRels("notesSlide2.xml"),
		"ppt/slides/_rels/slide2.xml.rels":      pptxSlideRels(""),
		"ppt/notesSlides/notesSlide2.xml":       pptxNotesPart(1, "Remember to greet the room.", "Mention the roadmap."),
		"ppt/notesSlides/_rels/notesSlide2.xml": "<x/>",
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}

	want := "## Slide 1\n\n- Opening title\n\n### Notes\n\n- Remember to greet the room.\n- Mention the roadmap.\n\n\n## Slide 2\n\n- Second slide body"
	if got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_NotesWithoutSlideText(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":           pptxSlidePart(""),
		"ppt/notesSlides/notesSlide1.xml": pptxNotesPart(1, "Only notes here."),
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}

	want := "## Slide 1\n\n_(no text)_\n\n### Notes\n\n- Only notes here."
	if got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_NotesFallBackToSlideNumberWithoutRels(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":           pptxSlidePart(pptxPara("Body one")),
		"ppt/slides/slide2.xml":           pptxSlidePart(pptxPara("Body two")),
		"ppt/notesSlides/notesSlide2.xml": pptxNotesPart(2, "Notes for two."),
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}

	want := "## Slide 1\n\n- Body one\n\n\n## Slide 2\n\n- Body two\n\n### Notes\n\n- Notes for two."
	if got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_AbsoluteNotesTarget(t *testing.T) {
	absoluteRels := strings.Replace(pptxSlideRels("notesSlide7.xml"), "../notesSlides/notesSlide7.xml", "/ppt/notesSlides/notesSlide7.xml", 1)
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":            pptxSlidePart(pptxPara("Body one")),
		"ppt/slides/_rels/slide1.xml.rels": absoluteRels,
		"ppt/notesSlides/notesSlide7.xml":  pptxNotesPart(1, "Absolute target note."),
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}
	if !strings.Contains(got, "### Notes\n\n- Absolute target note.") {
		t.Errorf("notes behind absolute rels target missing; got %q", got)
	}
}

func TestConvertPPTX_RelatedNotesPartMissing(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":            pptxSlidePart(pptxPara("Body one")),
		"ppt/slides/_rels/slide1.xml.rels": pptxSlideRels("notesSlide9.xml"),
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}
	if want := "## Slide 1\n\n- Body one"; got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_EmptyNotesAreOmitted(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":           pptxSlidePart(pptxPara("Body one")),
		"ppt/notesSlides/notesSlide1.xml": pptxNotesPart(1),
	})

	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}
	if want := "## Slide 1\n\n- Body one"; got != want {
		t.Errorf("empty notes slide must add nothing\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_MalformedNotes(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":           pptxSlidePart(pptxPara("Body one")),
		"ppt/notesSlides/notesSlide1.xml": "<p:notes><unclosed>",
	})
	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("malformed notes must not fail the conversion: %v", err)
	}
	if want := "## Slide 1\n\n- Body one"; got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_MalformedSlideRels(t *testing.T) {
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":            pptxSlidePart(pptxPara("Body one")),
		"ppt/slides/_rels/slide1.xml.rels": "<Relationships><Relationship",
	})
	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("malformed slide rels must not fail the conversion: %v", err)
	}
	if want := "## Slide 1\n\n- Body one"; got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}

func TestConvertPPTX_NotesOverZipBudgetAreDroppedKeepingSlideText(t *testing.T) {
	original := maxUncompressedBytesTotal
	maxUncompressedBytesTotal = 600
	t.Cleanup(func() { maxUncompressedBytesTotal = original })

	slide := pptxSlidePart(pptxPara("Body one"))
	data := buildPPTXParts(t, map[string]string{
		"ppt/slides/slide1.xml":           slide,
		"ppt/notesSlides/notesSlide1.xml": pptxNotesPart(1, strings.Repeat("long note ", 100)),
	})
	if int64(len(slide)) >= maxUncompressedBytesTotal {
		t.Fatalf("slide alone must fit the budget; slide is %d bytes", len(slide))
	}
	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("notes over the zip budget must not fail the conversion: %v", err)
	}
	if want := "## Slide 1\n\n- Body one"; got != want {
		t.Errorf("unexpected markdown\n got: %q\nwant: %q", got, want)
	}
}
