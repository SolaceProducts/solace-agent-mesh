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
	"strings"
)

const docxDocumentPart = "word/document.xml"

func convertDOCXToMarkdown(content []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open docx (zip): %w", err)
	}

	var documentXML []byte
	for _, f := range r.File {
		if f.Name == docxDocumentPart {
			documentXML, err = readCappedZipEntry(f, nil)
			if err != nil {
				return "", err
			}
			break
		}
	}
	if documentXML == nil {
		return "", fmt.Errorf("docx missing %s", docxDocumentPart)
	}

	return renderDOCXBody(documentXML)
}

func renderDOCXBody(documentXML []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(documentXML))
	var out strings.Builder

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse docx xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "p":
			para, perr := readDOCXParagraph(dec, se)
			if perr != nil {
				return "", perr
			}
			if para != "" {
				out.WriteString(para)
				out.WriteString("\n\n")
			}
		case "tbl":
			tbl, terr := readDOCXTable(dec, se)
			if terr != nil {
				return "", terr
			}
			if tbl != "" {
				out.WriteString(tbl)
				out.WriteString("\n\n")
			}
		}
	}

	return strings.TrimRight(out.String(), "\n"), nil
}

type docxParagraphInfo struct {
	HeadingLevel int
	IsListItem   bool
}

func readDOCXParagraph(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var info docxParagraphInfo
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("read paragraph: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				info = readDOCXParagraphProps(dec)
			case "r":
				runText, rerr := readDOCXRun(dec)
				if rerr != nil {
					return "", rerr
				}
				text.WriteString(runText)
			case "hyperlink":
				inner, herr := readDOCXHyperlink(dec)
				if herr != nil {
					return "", herr
				}
				text.WriteString(inner)
			default:
				if err := dec.Skip(); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return formatDOCXParagraph(info, text.String()), nil
			}
		}
	}
}

func readDOCXParagraphProps(dec *xml.Decoder) docxParagraphInfo {
	var info docxParagraphInfo
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return info
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "pStyle":
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						if lvl := parseHeadingLevel(a.Value); lvl > 0 {
							info.HeadingLevel = lvl
						}
					}
				}
			case "numPr":
				info.IsListItem = true
			}
		case xml.EndElement:
			depth--
		}
	}
	return info
}

func parseHeadingLevel(style string) int {
	s := strings.ToLower(style)
	if !strings.HasPrefix(s, "heading") {
		return 0
	}
	digits := strings.TrimPrefix(s, "heading")
	if digits == "" {
		return 0
	}
	if len(digits) > 1 {
		digits = digits[:1]
	}
	n := int(digits[0] - '0')
	if n >= 1 && n <= 9 {
		return n
	}
	return 0
}

func readDOCXRun(dec *xml.Decoder) (string, error) {
	var rpr struct {
		Bold   bool
		Italic bool
	}
	var text strings.Builder
	depth := 1

	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "rPr":
				rprDepth := 1
				for rprDepth > 0 {
					rprTok, rerr := dec.Token()
					if rerr != nil {
						return "", rerr
					}
					switch rt := rprTok.(type) {
					case xml.StartElement:
						rprDepth++
						switch rt.Name.Local {
						case "b":
							rpr.Bold = !docxPropertyIsFalse(rt)
						case "i":
							rpr.Italic = !docxPropertyIsFalse(rt)
						}
					case xml.EndElement:
						rprDepth--
					}
				}
				depth--
			case "t":
				txt, err := readDOCXCharData(dec)
				if err != nil {
					return "", err
				}
				text.WriteString(txt)
				depth--
			case "tab":
				text.WriteString("\t")
			case "br":
				text.WriteString("\n")
			default:
				if err := dec.Skip(); err != nil {
					return "", err
				}
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}

	s := text.String()
	if s == "" {
		return "", nil
	}
	if rpr.Bold && rpr.Italic {
		return "***" + s + "***", nil
	}
	if rpr.Bold {
		return "**" + s + "**", nil
	}
	if rpr.Italic {
		return "*" + s + "*", nil
	}
	return s, nil
}

func docxPropertyIsFalse(e xml.StartElement) bool {
	for _, a := range e.Attr {
		if a.Name.Local == "val" && (a.Value == "0" || a.Value == "false") {
			return true
		}
	}
	return false
}

func readDOCXCharData(dec *xml.Decoder) (string, error) {
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

func readDOCXHyperlink(dec *xml.Decoder) (string, error) {
	var text strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "r" {
				runText, rerr := readDOCXRun(dec)
				if rerr != nil {
					return "", rerr
				}
				text.WriteString(runText)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return text.String(), nil
}

func readDOCXTable(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var rows [][]string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tr" {
				row, rerr := readDOCXTableRow(dec)
				if rerr != nil {
					return "", rerr
				}
				rows = append(rows, row)
			} else {
				if err := dec.Skip(); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return renderMarkdownTable(rows), nil
			}
		}
	}
}

func readDOCXTableRow(dec *xml.Decoder) ([]string, error) {
	var cells []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tc" {
				cell, cerr := readDOCXTableCell(dec)
				if cerr != nil {
					return nil, cerr
				}
				cells = append(cells, cell)
			} else {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tr" {
				return cells, nil
			}
		}
	}
}

func readDOCXTableCell(dec *xml.Decoder) (string, error) {
	var parts []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				para, perr := readDOCXParagraph(dec, t)
				if perr != nil {
					return "", perr
				}
				if strings.TrimSpace(para) != "" {
					parts = append(parts, para)
				}
			} else {
				if err := dec.Skip(); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tc" {
				return strings.Join(parts, " "), nil
			}
		}
	}
}

func renderMarkdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	pad := func(r []string) []string {
		if len(r) == width {
			return r
		}
		out := make([]string, width)
		copy(out, r)
		return out
	}

	var buf strings.Builder

	buf.WriteString("| " + strings.Join(pad(rows[0]), " | ") + " |\n")
	seps := make([]string, width)
	for i := range seps {
		seps[i] = "---"
	}
	buf.WriteString("| " + strings.Join(seps, " | ") + " |\n")

	for _, r := range rows[1:] {
		buf.WriteString("| " + strings.Join(pad(r), " | ") + " |\n")
	}

	return strings.TrimRight(buf.String(), "\n")
}

func formatDOCXParagraph(info docxParagraphInfo, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	switch {
	case info.HeadingLevel > 0:
		return strings.Repeat("#", info.HeadingLevel) + " " + trimmed
	case info.IsListItem:
		return "- " + trimmed
	default:
		return trimmed
	}
}
