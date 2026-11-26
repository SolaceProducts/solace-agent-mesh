// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

func ConvertDOCX(content []byte) (*Result, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open DOCX zip: %w", err)
	}

	var docXML []byte
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open document.xml: %w", err)
			}
			docXML, err = io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read document.xml: %w", err)
			}
			break
		}
	}
	if docXML == nil {
		return nil, fmt.Errorf("word/document.xml not found in DOCX")
	}

	paragraphs := extractDOCXParagraphs(docXML)

	var parts []string
	var citations []CitationRange
	charPos := 0
	paraNum := 0

	for _, text := range paragraphs {
		paraNum++
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}

		citations = append(citations, CitationRange{
			Location:  fmt.Sprintf("physical_paragraph_%d", paraNum),
			CharStart: charPos,
			CharEnd:   charPos + len(text),
		})
		parts = append(parts, text)
		charPos += len(text) + 1
	}

	extracted := strings.Join(parts, "\n")

	if len(citations) > 0 {
		citations[len(citations)-1].CharEnd = len(extracted)
	}

	return &Result{
		Text:         extracted,
		CitationType: "paragraph",
		Citations:    citations,
	}, nil
}

func extractDOCXParagraphs(docXML []byte) []string {
	var paragraphs []string
	decoder := xml.NewDecoder(bytes.NewReader(docXML))

	inParagraph := false
	inRun := false
	var currentPara strings.Builder

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "p" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main":
				inParagraph = true
				currentPara.Reset()
			case t.Name.Local == "r" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main":
				if inParagraph {
					inRun = true
				}
			case t.Name.Local == "t" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main":
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "p" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main":
				paragraphs = append(paragraphs, currentPara.String())
				inParagraph = false
			case t.Name.Local == "r" && t.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main":
				inRun = false
			}
		case xml.CharData:
			if inParagraph && inRun {
				currentPara.Write(t)
			}
		}
	}

	return paragraphs
}
