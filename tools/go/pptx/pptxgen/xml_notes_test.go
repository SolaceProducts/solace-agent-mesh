// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestEmitNotesMaster(t *testing.T) {
	var b strings.Builder
	emitNotesMaster(&b)
	got := []byte(b.String())
	const golden = "testdata/notes_master.xml"

	if *updateGolden {
		if err := writeGolden(golden, got); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("emitNotesMaster differs from %s\n--- want ---\n%s\n--- got ---\n%s",
			golden, want, got)
	}
}

func TestEmitNotesSlide(t *testing.T) {
	cases := []struct {
		name       string
		notes      string
		slideIndex int
		golden     string
	}{
		{
			name:       "single_line",
			notes:      "Speak slowly here.",
			slideIndex: 1,
			golden:     "testdata/notes_single_line.xml",
		},
		{
			name:       "multi_line",
			notes:      "First line.\n\nThird line after blank.",
			slideIndex: 2,
			golden:     "testdata/notes_multi_line.xml",
		},
		{
			name:       "escaped_chars",
			notes:      "Q&A: <press> \"enter\"",
			slideIndex: 3,
			golden:     "testdata/notes_escaped_chars.xml",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			emitNotesSlide(&b, c.notes, c.slideIndex)
			got := []byte(b.String())

			if *updateGolden {
				if err := writeGolden(c.golden, got); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(c.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v", c.golden, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("emitNotesSlide(%q) differs from %s\n--- want ---\n%s\n--- got ---\n%s",
					c.notes, c.golden, want, got)
			}
		})
	}
}

func TestEmitNotesSlideRels(t *testing.T) {
	var b strings.Builder
	emitNotesSlideRels(&b, 4)
	got := b.String()
	for _, want := range []string{
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster" Target="../notesMasters/notesMaster1.xml"/>`,
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="../slides/slide4.xml"/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notes rels missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestSetNotesEmptyClears(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.SetNotes("hello")
	if s.notes == "" {
		t.Fatalf("SetNotes(\"hello\") did not record notes")
	}
	s.SetNotes("")
	if s.notes != "" {
		t.Errorf("SetNotes(\"\") did not clear; got %q", s.notes)
	}
	if s.notesRelID != "" {
		t.Errorf("notesRelID not cleared; got %q", s.notesRelID)
	}
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	for _, name := range []string{
		"ppt/notesMasters/notesMaster1.xml",
		"ppt/notesSlides/notesSlide1.xml",
	} {
		if entryExists(t, out, name) {
			t.Errorf("entry %s present after clearing notes", name)
		}
	}
}

func TestSetNotesEndToEnd(t *testing.T) {
	p := New()
	p.SetTitle("notes deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)
	s1 := p.AddSlide()
	s1.SetNotes("Notes for slide 1.")
	_ = p.AddSlide()
	s3 := p.AddSlide()
	s3.SetNotes("Slide three — emphasise the timeline.")

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	have := map[string][]byte{}
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %s: %v", f.Name, oerr)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		have[f.Name] = data
	}

	for _, name := range []string{
		"ppt/notesMasters/notesMaster1.xml",
		"ppt/notesMasters/_rels/notesMaster1.xml.rels",
		"ppt/notesSlides/notesSlide1.xml",
		"ppt/notesSlides/_rels/notesSlide1.xml.rels",
		"ppt/notesSlides/notesSlide3.xml",
		"ppt/notesSlides/_rels/notesSlide3.xml.rels",
	} {
		if _, ok := have[name]; !ok {
			t.Errorf("missing expected part: %s", name)
		}
	}

	for _, name := range []string{
		"ppt/notesSlides/notesSlide2.xml",
		"ppt/notesSlides/_rels/notesSlide2.xml.rels",
	} {
		if _, ok := have[name]; ok {
			t.Errorf("unexpected part present (slide 2 has no notes): %s", name)
		}
	}

	if !bytes.Contains(have["ppt/notesSlides/notesSlide1.xml"], []byte("Notes for slide 1.")) {
		t.Errorf("notesSlide1.xml does not contain the notes text:\n%s",
			have["ppt/notesSlides/notesSlide1.xml"])
	}

	pres := string(have["ppt/presentation.xml"])
	if !strings.Contains(pres, `<p:notesMasterIdLst><p:notesMasterId r:id="rId2"/></p:notesMasterIdLst>`) {
		t.Errorf("presentation.xml missing notesMasterIdLst:\n%s", pres)
	}
	if !strings.Contains(pres, `<p:sldId id="256" r:id="rId7"/>`) {
		t.Errorf("presentation.xml: slide 1 rId did not shift to rId7 with notes:\n%s", pres)
	}

	presRels := string(have["ppt/_rels/presentation.xml.rels"])
	if !strings.Contains(presRels, `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster" Target="notesMasters/notesMaster1.xml"/>`) {
		t.Errorf("presentation.xml.rels missing notesMaster rel:\n%s", presRels)
	}
	if !strings.Contains(presRels, `Id="rId7"`) {
		t.Errorf("presentation.xml.rels missing slide rId7:\n%s", presRels)
	}

	ct := string(have["[Content_Types].xml"])
	for _, want := range []string{
		`<Override PartName="/ppt/notesMasters/notesMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"/>`,
		`<Override PartName="/ppt/notesSlides/notesSlide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"/>`,
		`<Override PartName="/ppt/notesSlides/notesSlide3.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"/>`,
	} {
		if !strings.Contains(ct, want) {
			t.Errorf("[Content_Types].xml missing %q", want)
		}
	}
	if strings.Contains(ct, "notesSlide2") {
		t.Errorf("[Content_Types].xml unexpectedly references notesSlide2:\n%s", ct)
	}

	app := string(have["docProps/app.xml"])
	if !strings.Contains(app, `<Notes>2</Notes>`) {
		t.Errorf("docProps/app.xml: <Notes>2</Notes> not present:\n%s", app)
	}

	rels1 := string(have["ppt/slides/_rels/slide1.xml.rels"])
	if !strings.Contains(rels1, `Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide"`) {
		t.Errorf("slide 1 rels missing notesSlide rel:\n%s", rels1)
	}
	if !strings.Contains(rels1, `Target="../notesSlides/notesSlide1.xml"`) {
		t.Errorf("slide 1 rels: notesSlide target wrong:\n%s", rels1)
	}
	rels2 := string(have["ppt/slides/_rels/slide2.xml.rels"])
	if strings.Contains(rels2, "notesSlide") {
		t.Errorf("slide 2 rels unexpectedly references notesSlide:\n%s", rels2)
	}
}

func entryExists(t *testing.T, out []byte, name string) bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}
