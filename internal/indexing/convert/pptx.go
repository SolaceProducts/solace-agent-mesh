// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func ConvertPPTX(content []byte) (*Result, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open PPTX zip: %w", err)
	}

	type slideFile struct {
		num  int
		file *zip.File
	}
	var slides []slideFile

	for _, f := range zr.File {
		dir := filepath.Dir(f.Name)
		base := filepath.Base(f.Name)
		if dir != "ppt/slides" || !strings.HasPrefix(base, "slide") || !strings.HasSuffix(base, ".xml") {
			continue
		}
		numStr := strings.TrimPrefix(base, "slide")
		numStr = strings.TrimSuffix(numStr, ".xml")
		num, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		slides = append(slides, slideFile{num: num, file: f})
	}

	slices.SortFunc(slides, func(a, b slideFile) int {
		return cmp.Compare(a.num, b.num)
	})

	var slideParts []string
	var citations []CitationRange
	charPos := 0

	for _, sf := range slides {
		rc, err := sf.file.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			continue
		}

		text := extractPPTXSlideText(data)
		if text == "" {
			continue
		}

		citations = append(citations, CitationRange{
			Location:  fmt.Sprintf("physical_slide_%d", sf.num),
			CharStart: charPos,
			CharEnd:   charPos + len(text),
		})
		slideParts = append(slideParts, text)
		charPos += len(text) + 2
	}

	extracted := strings.Join(slideParts, "\n\n")

	if len(citations) > 0 {
		citations[len(citations)-1].CharEnd = len(extracted)
	}

	return &Result{
		Text:         extracted,
		CitationType: "slide",
		Citations:    citations,
	}, nil
}

func extractPPTXSlideText(slideXML []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(slideXML))

	var shapes []string
	var currentShape strings.Builder
	inShape := false
	depth := 0

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "sp" && strings.HasSuffix(t.Name.Space, "/presentationml/2006/main") {
				inShape = true
				depth = 1
				currentShape.Reset()
			} else if inShape {
				depth++
			}
		case xml.EndElement:
			if inShape {
				depth--
				if depth == 0 {
					if text := currentShape.String(); text != "" {
						shapes = append(shapes, text)
					}
					inShape = false
				}
			}
		case xml.CharData:
			if inShape {
				currentShape.Write(t)
			}
		}
	}

	return strings.Join(shapes, "\n")
}
