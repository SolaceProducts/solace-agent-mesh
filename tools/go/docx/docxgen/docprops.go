// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import (
	"encoding/xml"
	"fmt"
)

type coreProperties struct {
	XMLName xml.Name `xml:"cp:coreProperties"`
	XmlnsCP string   `xml:"xmlns:cp,attr"`
	XmlnsDC string   `xml:"xmlns:dc,attr"`
	Title   string   `xml:"dc:title,omitempty"`
	Subject string   `xml:"dc:subject,omitempty"`
	Creator string   `xml:"dc:creator,omitempty"`
}

func buildCoreProps(title, author, subject string) ([]byte, error) {
	cp := coreProperties{
		XmlnsCP: "http://schemas.openxmlformats.org/package/2006/metadata/core-properties",
		XmlnsDC: "http://purl.org/dc/elements/1.1/",
		Title:   title,
		Subject: subject,
		Creator: author,
	}
	out, err := xml.Marshal(cp)
	if err != nil {
		return nil, fmt.Errorf("marshal docProps/core.xml: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}

type extendedProperties struct {
	XMLName     xml.Name `xml:"Properties"`
	Xmlns       string   `xml:"xmlns,attr"`
	XmlnsVT     string   `xml:"xmlns:vt,attr"`
	Application string   `xml:"Application"`
}

func buildAppProps() ([]byte, error) {
	ep := extendedProperties{
		Xmlns:       "http://schemas.openxmlformats.org/officeDocument/2006/extended-properties",
		XmlnsVT:     "http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes",
		Application: "Solace Agent Mesh",
	}
	out, err := xml.Marshal(ep)
	if err != nil {
		return nil, fmt.Errorf("marshal docProps/app.xml: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}
