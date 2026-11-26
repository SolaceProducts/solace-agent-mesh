// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	pptxSlidesDir           = "ppt/slides"
	pptxNotesRelationSuffix = "/notesSlide"
	pptxNotesBodyType       = "body"
)

var (
	pptxSlideRegexp     = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	pptxSlideRelsRegexp = regexp.MustCompile(`^ppt/slides/_rels/slide(\d+)\.xml\.rels$`)
	pptxNotesRegexp     = regexp.MustCompile(`^ppt/notesSlides/notesSlide\d+\.xml$`)
)

type pptxRelationships struct {
	Relationships []struct {
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type pptxShape struct {
	Placeholder *struct {
		Type string `xml:"type,attr"`
	} `xml:"nvSpPr>nvPr>ph"`
	Inner string `xml:",innerxml"`
}

func pptxNotesPartName(num int, rels []byte, hasRels bool) (string, error) {
	if !hasRels {
		return fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", num), nil
	}
	var parsed pptxRelationships
	if err := xml.Unmarshal(rels, &parsed); err != nil {
		return "", fmt.Errorf("parse pptx slide relationships: %w", err)
	}
	for _, rel := range parsed.Relationships {
		if !strings.HasSuffix(rel.Type, pptxNotesRelationSuffix) {
			continue
		}
		if strings.HasPrefix(rel.Target, "/") {
			return strings.TrimPrefix(rel.Target, "/"), nil
		}
		return path.Join(pptxSlidesDir, rel.Target), nil
	}
	return "", nil
}

func extractPPTXNotesParagraphs(body []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var paragraphs []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return paragraphs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse pptx notes: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "sp" {
			continue
		}
		var shape pptxShape
		if derr := dec.DecodeElement(&shape, &start); derr != nil {
			return nil, fmt.Errorf("parse pptx notes: %w", derr)
		}
		if shape.Placeholder == nil || shape.Placeholder.Type != pptxNotesBodyType {
			continue
		}
		shapeParagraphs, err := extractPPTXSlideParagraphs([]byte(shape.Inner))
		if err != nil {
			return nil, err
		}
		paragraphs = append(paragraphs, shapeParagraphs...)
	}
}

func convertPPTXToMarkdown(content []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open pptx (zip): %w", err)
	}

	type slideEntry struct {
		num  int
		body []byte
	}
	var slides []slideEntry
	slideRels := map[int][]byte{}
	notesParts := map[string][]byte{}

	budget := newZipBudget()
	for _, f := range r.File {
		m := pptxSlideRegexp.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		n, perr := strconv.Atoi(m[1])
		if perr != nil {
			continue
		}
		body, rerr := readCappedZipEntry(f, budget)
		if rerr != nil {
			return "", rerr
		}
		slides = append(slides, slideEntry{num: n, body: body})
	}

	if len(slides) == 0 {
		return "", fmt.Errorf("pptx contains no slides")
	}

	collectPPTXNotesParts(r.File, budget, slideRels, notesParts)

	sort.Slice(slides, func(i, j int) bool { return slides[i].num < slides[j].num })

	var out strings.Builder
	for i, s := range slides {
		if i > 0 {
			out.WriteString("\n\n")
		}

		paragraphs, perr := extractPPTXSlideParagraphs(s.body)
		if perr != nil {
			return "", perr
		}

		notes, nerr := pptxSlideNotes(s.num, slideRels, notesParts)
		if nerr != nil {
			notes = nil
		}

		fmt.Fprintf(&out, "## Slide %d\n\n", s.num)

		if len(paragraphs) == 0 {
			out.WriteString("_(no text)_")
			if len(notes) > 0 {
				out.WriteString("\n")
			}
		}

		writePPTXBullets(&out, paragraphs)

		if len(notes) > 0 {
			out.WriteString("\n### Notes\n\n")
			writePPTXBullets(&out, notes)
		}
	}

	return strings.TrimRight(out.String(), "\n"), nil
}

func collectPPTXNotesParts(files []*zip.File, budget *zipBudget, slideRels map[int][]byte, notesParts map[string][]byte) {
	for _, f := range files {
		if pptxNotesRegexp.MatchString(f.Name) {
			if body, err := readCappedZipEntry(f, budget); err == nil {
				notesParts[f.Name] = body
			}
			continue
		}
		rm := pptxSlideRelsRegexp.FindStringSubmatch(f.Name)
		if rm == nil {
			continue
		}
		n, err := strconv.Atoi(rm[1])
		if err != nil {
			continue
		}
		if body, rerr := readCappedZipEntry(f, budget); rerr == nil {
			slideRels[n] = body
		}
	}
}

func writePPTXBullets(out *strings.Builder, paragraphs []string) {
	for _, p := range paragraphs {
		out.WriteString("- ")
		out.WriteString(p)
		out.WriteString("\n")
	}
}

func pptxSlideNotes(num int, slideRels map[int][]byte, notesParts map[string][]byte) ([]string, error) {
	rels, hasRels := slideRels[num]
	name, err := pptxNotesPartName(num, rels, hasRels)
	if err != nil {
		return nil, err
	}
	body, ok := notesParts[name]
	if !ok {
		return nil, nil
	}
	return extractPPTXNotesParagraphs(body)
}

func extractPPTXSlideParagraphs(body []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))

	var paragraphs []string
	var current strings.Builder
	inParagraph := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse pptx slide: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				inParagraph = true
				current.Reset()
			case "t":
				txt, err := readPPTXText(dec)
				if err != nil {
					return nil, err
				}
				current.WriteString(txt)
			case "br":
				if inParagraph {
					current.WriteString(" ")
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" && inParagraph {
				if s := strings.TrimSpace(current.String()); s != "" {
					paragraphs = append(paragraphs, s)
				}
				current.Reset()
				inParagraph = false
			}
		}
	}

	return paragraphs, nil
}

func readPPTXText(dec *xml.Decoder) (string, error) {
	var s strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			s.Write(t)
		case xml.EndElement:
			return s.String(), nil
		case xml.StartElement:
			if err := dec.Skip(); err != nil {
				return "", err
			}
		}
	}
}
