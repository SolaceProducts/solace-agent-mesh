// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import "strings"

func emitNotesMaster(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<p:notesMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`)
	b.WriteString(`<p:cSld>`)
	b.WriteString(`<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg>`)
	b.WriteString(`<p:spTree>`)
	b.WriteString(`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`)
	b.WriteString(`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr>`)
	b.WriteString(`<p:cNvPr id="2" name="Slide Image Placeholder 1"/>`)
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>`)
	b.WriteString(`<p:nvPr><p:ph type="sldImg"/></p:nvPr>`)
	b.WriteString(`</p:nvSpPr>`)
	b.WriteString(`<p:spPr>`)
	b.WriteString(`<a:xfrm><a:off x="685800" y="457200"/><a:ext cx="5486400" cy="4114800"/></a:xfrm>`)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	b.WriteString(`<a:noFill/>`)
	b.WriteString(`<a:ln w="9525"><a:solidFill><a:srgbClr val="000000"/></a:solidFill></a:ln>`)
	b.WriteString(`</p:spPr>`)
	b.WriteString(`<p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody>`)
	b.WriteString(`</p:sp>`)
	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr>`)
	b.WriteString(`<p:cNvPr id="3" name="Notes Placeholder 2"/>`)
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`)
	b.WriteString(`<p:nvPr><p:ph type="body" idx="1"/></p:nvPr>`)
	b.WriteString(`</p:nvSpPr>`)
	b.WriteString(`<p:spPr>`)
	b.WriteString(`<a:xfrm><a:off x="685800" y="4800600"/><a:ext cx="5486400" cy="3600450"/></a:xfrm>`)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	b.WriteString(`</p:spPr>`)
	b.WriteString(`<p:txBody>`)
	b.WriteString(`<a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0"><a:normAutofit/></a:bodyPr>`)
	b.WriteString(`<a:lstStyle/>`)
	b.WriteString(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
	b.WriteString(`</p:txBody>`)
	b.WriteString(`</p:sp>`)
	b.WriteString(`</p:spTree>`)
	b.WriteString(`</p:cSld>`)
	b.WriteString(`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`)
	b.WriteString(`<p:notesStyle>`)
	b.WriteString(`<a:lvl1pPr marL="0" indent="0" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">`)
	b.WriteString(`<a:defRPr sz="1200" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr>`)
	b.WriteString(`</a:lvl1pPr>`)
	b.WriteString(`</p:notesStyle>`)
	b.WriteString(`</p:notesMaster>`)
}

func emitNotesMasterRels(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>`)
	b.WriteString(`</Relationships>`)
}

func emitNotesSlide(b *strings.Builder, notes string, slideIndex int) {
	writeXMLDecl(b)
	b.WriteString(`<p:notes xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`)
	b.WriteString(`<p:cSld>`)
	b.WriteString(`<p:spTree>`)
	b.WriteString(`<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`)
	b.WriteString(`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr>`)
	b.WriteString(`<p:cNvPr id="2" name="Slide Image Placeholder 1"/>`)
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>`)
	b.WriteString(`<p:nvPr><p:ph type="sldImg"/></p:nvPr>`)
	b.WriteString(`</p:nvSpPr>`)
	b.WriteString(`<p:spPr/>`)
	b.WriteString(`</p:sp>`)
	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr>`)
	b.WriteString(`<p:cNvPr id="3" name="Notes Placeholder 2"/>`)
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`)
	b.WriteString(`<p:nvPr><p:ph type="body" idx="1"/></p:nvPr>`)
	b.WriteString(`</p:nvSpPr>`)
	b.WriteString(`<p:spPr/>`)
	b.WriteString(`<p:txBody>`)
	b.WriteString(`<a:bodyPr/>`)
	b.WriteString(`<a:lstStyle/>`)
	emitNotesParagraphs(b, notes)
	b.WriteString(`</p:txBody>`)
	b.WriteString(`</p:sp>`)
	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr>`)
	b.WriteString(`<p:cNvPr id="4" name="Slide Number Placeholder 3"/>`)
	b.WriteString(`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`)
	b.WriteString(`<p:nvPr><p:ph type="sldNum" sz="quarter" idx="10"/></p:nvPr>`)
	b.WriteString(`</p:nvSpPr>`)
	b.WriteString(`<p:spPr/>`)
	b.WriteString(`<p:txBody>`)
	b.WriteString(`<a:bodyPr/>`)
	b.WriteString(`<a:lstStyle/>`)
	b.WriteString(`<a:p><a:fld id="{00000000-0000-0000-0000-000000000000}" type="slidenum"><a:rPr lang="en-US"/><a:t>`)
	b.WriteString(fmtInt(slideIndex))
	b.WriteString(`</a:t></a:fld></a:p>`)
	b.WriteString(`</p:txBody>`)
	b.WriteString(`</p:sp>`)
	b.WriteString(`</p:spTree>`)
	b.WriteString(`</p:cSld>`)
	b.WriteString(`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`)
	b.WriteString(`</p:notes>`)
}

func emitNotesParagraphs(b *strings.Builder, notes string) {
	for line := range strings.SplitSeq(notes, "\n") {
		b.WriteString(`<a:p>`)
		if line == "" {
			b.WriteString(`<a:endParaRPr lang="en-US"/>`)
		} else {
			b.WriteString(`<a:r><a:rPr lang="en-US" dirty="0"/><a:t>`)
			b.WriteString(escapeXML(line))
			b.WriteString(`</a:t></a:r>`)
		}
		b.WriteString(`</a:p>`)
	}
}

func emitNotesSlideRels(b *strings.Builder, slideIndex int) {
	writeXMLDecl(b)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster" Target="../notesMasters/notesMaster1.xml"/>`)
	b.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="../slides/slide`)
	b.WriteString(fmtInt(slideIndex))
	b.WriteString(`.xml"/>`)
	b.WriteString(`</Relationships>`)
}

const notesSlideRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide"
