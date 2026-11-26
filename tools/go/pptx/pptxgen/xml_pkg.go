// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"strings"
	"time"
)

const firstSldID = 256

type contentTypeOverride struct {
	partName    string
	contentType string
}

type contentTypeDefault struct {
	extension   string
	contentType string
}

func emitContentTypes(b *strings.Builder, slideCount int, extraDefaults []contentTypeDefault, extraOverrides []contentTypeOverride) {
	writeXMLDecl(b)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	for _, d := range extraDefaults {
		b.WriteString(`<Default Extension="`)
		b.WriteString(escapeXML(d.extension))
		b.WriteString(`" ContentType="`)
		b.WriteString(escapeXML(d.contentType))
		b.WriteString(`"/>`)
	}
	overrides := []contentTypeOverride{
		{"/ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"},
		{"/ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"},
		{"/ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
		{"/ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml"},
		{"/ppt/presProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"},
		{"/ppt/viewProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"},
		{"/ppt/tableStyles.xml", "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"},
		{"/docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml"},
		{"/docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml"},
	}
	for i := range slideCount {
		overrides = append(overrides, contentTypeOverride{
			partName:    "/ppt/slides/slide" + fmtInt(i+1) + ".xml",
			contentType: "application/vnd.openxmlformats-officedocument.presentationml.slide+xml",
		})
	}
	overrides = append(overrides, extraOverrides...)
	for _, o := range overrides {
		b.WriteString(`<Override PartName="`)
		b.WriteString(escapeXML(o.partName))
		b.WriteString(`" ContentType="`)
		b.WriteString(escapeXML(o.contentType))
		b.WriteString(`"/>`)
	}
	b.WriteString(`</Types>`)
}

func emitRootRels(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>`)
	b.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`)
	b.WriteString(`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>`)
	b.WriteString(`</Relationships>`)
}

func emitCoreProps(b *strings.Builder, title, author, description, keywords string, t time.Time) {
	writeXMLDecl(b)
	b.WriteString(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	b.WriteString(`<dc:title>`)
	b.WriteString(escapeXML(title))
	b.WriteString(`</dc:title>`)
	b.WriteString(`<dc:creator>`)
	b.WriteString(escapeXML(author))
	b.WriteString(`</dc:creator>`)
	if description != "" {
		b.WriteString(`<dc:description>`)
		b.WriteString(escapeXML(description))
		b.WriteString(`</dc:description>`)
	}
	if keywords != "" {
		b.WriteString(`<cp:keywords>`)
		b.WriteString(escapeXML(keywords))
		b.WriteString(`</cp:keywords>`)
	}
	b.WriteString(`<cp:lastModifiedBy>`)
	b.WriteString(escapeXML(author))
	b.WriteString(`</cp:lastModifiedBy>`)
	b.WriteString(`<cp:revision>1</cp:revision>`)
	stamp := t.UTC().Format("2006-01-02T15:04:05Z")
	b.WriteString(`<dcterms:created xsi:type="dcterms:W3CDTF">`)
	b.WriteString(stamp)
	b.WriteString(`</dcterms:created>`)
	b.WriteString(`<dcterms:modified xsi:type="dcterms:W3CDTF">`)
	b.WriteString(stamp)
	b.WriteString(`</dcterms:modified>`)
	b.WriteString(`</cp:coreProperties>`)
}

func emitAppProps(b *strings.Builder, slideCount, notesCount int) {
	writeXMLDecl(b)
	b.WriteString(`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`)
	b.WriteString(`<Application>pptxgen</Application>`)
	b.WriteString(`<PresentationFormat>On-screen Show</PresentationFormat>`)
	b.WriteString(`<Slides>`)
	b.WriteString(fmtInt(slideCount))
	b.WriteString(`</Slides>`)
	b.WriteString(`<Notes>`)
	b.WriteString(fmtInt(notesCount))
	b.WriteString(`</Notes>`)
	b.WriteString(`<HiddenSlides>0</HiddenSlides>`)
	b.WriteString(`<MMClips>0</MMClips>`)
	b.WriteString(`<ScaleCrop>false</ScaleCrop>`)
	b.WriteString(`<HeadingPairs>`)
	b.WriteString(`<vt:vector size="2" baseType="variant">`)
	b.WriteString(`<vt:variant><vt:lpstr>Slide Titles</vt:lpstr></vt:variant>`)
	b.WriteString(`<vt:variant><vt:i4>`)
	b.WriteString(fmtInt(slideCount))
	b.WriteString(`</vt:i4></vt:variant>`)
	b.WriteString(`</vt:vector>`)
	b.WriteString(`</HeadingPairs>`)
	b.WriteString(`<TitlesOfParts>`)
	b.WriteString(`<vt:vector size="`)
	b.WriteString(fmtInt(slideCount))
	b.WriteString(`" baseType="lpstr">`)
	for i := range slideCount {
		b.WriteString(`<vt:lpstr>Slide `)
		b.WriteString(fmtInt(i + 1))
		b.WriteString(`</vt:lpstr>`)
	}
	b.WriteString(`</vt:vector>`)
	b.WriteString(`</TitlesOfParts>`)
	b.WriteString(`<LinksUpToDate>false</LinksUpToDate>`)
	b.WriteString(`<SharedDoc>false</SharedDoc>`)
	b.WriteString(`<HyperlinksChanged>false</HyperlinksChanged>`)
	b.WriteString(`<AppVersion>1.0000</AppVersion>`)
	b.WriteString(`</Properties>`)
}

func emitPresentation(b *strings.Builder, widthEMU, heightEMU, slideCount int, hasNotes bool) {
	writeXMLDecl(b)
	b.WriteString(`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" saveSubsetFonts="1">`)
	b.WriteString(`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	if hasNotes {
		b.WriteString(`<p:notesMasterIdLst><p:notesMasterId r:id="rId2"/></p:notesMasterIdLst>`)
	}
	first := presFirstSlideRelID(hasNotes)
	b.WriteString(`<p:sldIdLst>`)
	for i := range slideCount {
		b.WriteString(`<p:sldId id="`)
		b.WriteString(fmtInt(firstSldID + i))
		b.WriteString(`" r:id="rId`)
		b.WriteString(fmtInt(first + i))
		b.WriteString(`"/>`)
	}
	b.WriteString(`</p:sldIdLst>`)
	b.WriteString(`<p:sldSz cx="`)
	b.WriteString(fmtInt(widthEMU))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(heightEMU))
	b.WriteString(`"/>`)
	b.WriteString(`<p:notesSz cx="`)
	b.WriteString(fmtInt(defaultNotesWidthEMU))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(defaultNotesHeightEMU))
	b.WriteString(`"/>`)
	b.WriteString(`<p:defaultTextStyle>`)
	b.WriteString(`<a:defPPr><a:defRPr lang="en-US"/></a:defPPr>`)
	b.WriteString(`<a:lvl1pPr algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">`)
	b.WriteString(`<a:defRPr sz="1800" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr>`)
	b.WriteString(`</a:lvl1pPr>`)
	b.WriteString(`</p:defaultTextStyle>`)
	b.WriteString(`</p:presentation>`)
}

func presFirstSlideRelID(hasNotes bool) int {
	if hasNotes {
		return 7
	}
	return 6
}

func emitPresentationRels(b *strings.Builder, slideCount int, hasNotes bool) {
	writeXMLDecl(b)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	rid := 2
	if hasNotes {
		b.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster" Target="notesMasters/notesMaster1.xml"/>`)
		rid = 3
	}
	b.WriteString(`<Relationship Id="rId`)
	b.WriteString(fmtInt(rid))
	b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>`)
	b.WriteString(`<Relationship Id="rId`)
	b.WriteString(fmtInt(rid + 1))
	b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps" Target="presProps.xml"/>`)
	b.WriteString(`<Relationship Id="rId`)
	b.WriteString(fmtInt(rid + 2))
	b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/viewProps" Target="viewProps.xml"/>`)
	b.WriteString(`<Relationship Id="rId`)
	b.WriteString(fmtInt(rid + 3))
	b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles" Target="tableStyles.xml"/>`)
	first := presFirstSlideRelID(hasNotes)
	for i := range slideCount {
		b.WriteString(`<Relationship Id="rId`)
		b.WriteString(fmtInt(first + i))
		b.WriteString(`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide`)
		b.WriteString(fmtInt(i + 1))
		b.WriteString(`.xml"/>`)
	}
	b.WriteString(`</Relationships>`)
}

func emitPresProps(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`)
}

func emitViewProps(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<p:viewPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`)
}

func emitTableStyles(b *strings.Builder) {
	writeXMLDecl(b)
	b.WriteString(`<a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)
}
