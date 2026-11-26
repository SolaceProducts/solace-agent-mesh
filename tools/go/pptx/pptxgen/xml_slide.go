// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import "strings"

func emitSlide(b *strings.Builder, s *Slide) {
	writeXMLDecl(b)
	b.WriteString(`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`)
	b.WriteString(`<p:cSld>`)
	if s.bg != nil {
		b.WriteString(`<p:bg><p:bgPr><a:solidFill><a:srgbClr val="`)
		b.WriteString(s.bg.Color)
		b.WriteString(`"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>`)
	}
	b.WriteString(`<p:spTree>`)
	b.WriteString(`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`)
	b.WriteString(`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	ids := newIDAllocator()
	for _, el := range s.elements {
		el.emitXML(b, ids)
	}
	b.WriteString(`</p:spTree>`)
	b.WriteString(`</p:cSld>`)
	b.WriteString(`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`)
	b.WriteString(`</p:sld>`)
}

func emitSlideRels(b *strings.Builder, s *Slide) {
	writeXMLDecl(b)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`)
	for _, r := range s.rels {
		b.WriteString(`<Relationship Id="`)
		b.WriteString(escapeXML(r.id))
		b.WriteString(`" Type="`)
		b.WriteString(escapeXML(r.relType))
		b.WriteString(`" Target="`)
		b.WriteString(escapeXML(r.target))
		b.WriteString(`"/>`)
	}
	if s.notes != "" && s.notesRelID != "" {
		b.WriteString(`<Relationship Id="`)
		b.WriteString(escapeXML(s.notesRelID))
		b.WriteString(`" Type="`)
		b.WriteString(notesSlideRelType)
		b.WriteString(`" Target="../notesSlides/notesSlide`)
		b.WriteString(fmtInt(slideIndexOf(s)))
		b.WriteString(`.xml"/>`)
	}
	b.WriteString(`</Relationships>`)
}

func slideIndexOf(s *Slide) int {
	if s.p == nil {
		return 0
	}
	for i, candidate := range s.p.slides {
		if candidate == s {
			return i + 1
		}
	}
	return 0
}
