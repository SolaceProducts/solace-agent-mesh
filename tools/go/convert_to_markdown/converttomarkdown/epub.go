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
	"strings"
)

func convertEPUBToMarkdown(content []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open epub (zip): %w", err)
	}

	index := make(map[string]*zip.File, len(r.File))
	for _, f := range r.File {
		index[f.Name] = f
	}

	budget := newZipBudget()

	containerFile, ok := index["META-INF/container.xml"]
	if !ok {
		return "", fmt.Errorf("epub missing META-INF/container.xml")
	}
	containerXML, err := readCappedZipEntry(containerFile, budget)
	if err != nil {
		return "", err
	}
	opfPath, err := parseEPUBContainer(containerXML)
	if err != nil {
		return "", err
	}
	opfFile, ok := index[opfPath]
	if !ok {
		return "", fmt.Errorf("epub root file %q not in archive", opfPath)
	}

	opfXML, err := readCappedZipEntry(opfFile, budget)
	if err != nil {
		return "", err
	}
	manifest, spine, err := parseEPUBOPF(opfXML)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	opfDir := path.Dir(opfPath)

	for i, id := range spine {
		href, ok := manifest[id]
		if !ok {
			continue
		}
		full := href
		if opfDir != "." && opfDir != "" {
			full = path.Join(opfDir, href)
		}
		f, ok := index[full]
		if !ok {
			continue
		}
		chapterXML, err := readCappedZipEntry(f, budget)
		if err != nil {
			return "", err
		}
		chapter, err := convertHTMLToMarkdown(chapterXML)
		if err != nil {
			continue
		}
		chapter = strings.TrimSpace(chapter)
		if chapter == "" {
			continue
		}
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(chapter)
	}

	return strings.TrimRight(out.String(), "\n"), nil
}

func parseEPUBContainer(containerXML []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(containerXML))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse container.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "rootfile" {
			for _, a := range se.Attr {
				if a.Name.Local == "full-path" {
					return a.Value, nil
				}
			}
		}
	}
	return "", fmt.Errorf("epub container.xml: no rootfile/full-path")
}

func parseEPUBOPF(opfXML []byte) (map[string]string, []string, error) {
	dec := xml.NewDecoder(bytes.NewReader(opfXML))
	manifest := make(map[string]string)
	var spine []string

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("parse opf: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "item":
			var id, href string
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "id":
					id = a.Value
				case "href":
					href = a.Value
				}
			}
			if id != "" && href != "" {
				manifest[id] = href
			}
		case "itemref":
			for _, a := range se.Attr {
				if a.Name.Local == "idref" && a.Value != "" {
					spine = append(spine, a.Value)
				}
			}
		}
	}

	return manifest, spine, nil
}
