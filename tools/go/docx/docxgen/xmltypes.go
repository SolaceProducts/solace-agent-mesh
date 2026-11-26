// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import "encoding/xml"

type ctVal struct {
	Val string `xml:"w:val,attr"`
}

type ctEmpty struct{}

type ctText struct {
	Space string `xml:"xml:space,attr,omitempty"`
	Value string `xml:",chardata"`
}

type ctRPr struct {
	XMLName xml.Name `xml:"w:rPr"`
	B       *ctEmpty `xml:"w:b,omitempty"`
	I       *ctEmpty `xml:"w:i,omitempty"`
	Color   *ctVal   `xml:"w:color,omitempty"`
	U       *ctVal   `xml:"w:u,omitempty"`
}

type ctFldChar struct {
	XMLName xml.Name `xml:"w:fldChar"`
	Type    string   `xml:"w:fldCharType,attr"`
	Dirty   string   `xml:"w:dirty,attr,omitempty"`
}

type ctRun struct {
	XMLName   xml.Name   `xml:"w:r"`
	RPr       *ctRPr     `xml:"w:rPr"`
	FldChar   *ctFldChar `xml:"w:fldChar"`
	InstrText *ctText    `xml:"w:instrText"`
	Drawing   *ctDrawing `xml:"w:drawing"`
	Br        *ctBr      `xml:"w:br"`
	Text      *ctText    `xml:"w:t"`
}

type ctBr struct {
	XMLName xml.Name `xml:"w:br"`
	Type    string   `xml:"w:type,attr"`
}

type ctNumPr struct {
	XMLName xml.Name `xml:"w:numPr"`
	Ilvl    ctVal    `xml:"w:ilvl"`
	NumID   ctVal    `xml:"w:numId"`
}

type ctPPr struct {
	XMLName xml.Name `xml:"w:pPr"`
	PStyle  *ctVal   `xml:"w:pStyle,omitempty"`
	NumPr   *ctNumPr `xml:"w:numPr"`
	Jc      *ctVal   `xml:"w:jc,omitempty"`
}

type ctParagraph struct {
	XMLName xml.Name `xml:"w:p"`
	PPr     *ctPPr   `xml:"w:pPr"`
	Runs    []ctRun  `xml:"w:r"`
}

type ctTcPr struct {
	XMLName xml.Name `xml:"w:tcPr"`
	TcW     ctTblW   `xml:"w:tcW"`
}

type ctTableCell struct {
	XMLName xml.Name      `xml:"w:tc"`
	TcPr    ctTcPr        `xml:"w:tcPr"`
	Paras   []ctParagraph `xml:"w:p"`
}

type ctTableRow struct {
	XMLName xml.Name      `xml:"w:tr"`
	Cells   []ctTableCell `xml:"w:tc"`
}

type ctTblW struct {
	Type string `xml:"w:type,attr"`
	W    string `xml:"w:w,attr"`
}

type ctTblLayout struct {
	XMLName xml.Name `xml:"w:tblLayout"`
	Type    string   `xml:"w:type,attr"`
}

type ctTblPr struct {
	XMLName   xml.Name    `xml:"w:tblPr"`
	TblStyle  ctVal       `xml:"w:tblStyle"`
	TblW      ctTblW      `xml:"w:tblW"`
	TblLayout ctTblLayout `xml:"w:tblLayout"`
}

type ctGridCol struct {
	XMLName xml.Name `xml:"w:gridCol"`
	W       string   `xml:"w:w,attr"`
}

type ctTblGrid struct {
	XMLName  xml.Name    `xml:"w:tblGrid"`
	GridCols []ctGridCol `xml:"w:gridCol"`
}

type ctTable struct {
	XMLName xml.Name     `xml:"w:tbl"`
	TblPr   ctTblPr      `xml:"w:tblPr"`
	Grid    ctTblGrid    `xml:"w:tblGrid"`
	Rows    []ctTableRow `xml:"w:tr"`
}

type ctExtent struct {
	XMLName xml.Name `xml:"wp:extent"`
	Cx      int64    `xml:"cx,attr"`
	Cy      int64    `xml:"cy,attr"`
}

type ctEffectExtent struct {
	XMLName xml.Name `xml:"wp:effectExtent"`
	L       string   `xml:"l,attr"`
	T       string   `xml:"t,attr"`
	R       string   `xml:"r,attr"`
	B       string   `xml:"b,attr"`
}

type ctIDName struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type ctPicLocks struct {
	XMLName        xml.Name `xml:"a:picLocks"`
	NoChangeAspect string   `xml:"noChangeAspect,attr"`
}

type ctCNvPicPr struct {
	XMLName  xml.Name   `xml:"pic:cNvPicPr"`
	PicLocks ctPicLocks `xml:"a:picLocks"`
}

type ctNvPicPr struct {
	XMLName xml.Name   `xml:"pic:nvPicPr"`
	CNvPr   ctIDName   `xml:"pic:cNvPr"`
	CNvPic  ctCNvPicPr `xml:"pic:cNvPicPr"`
}

type ctBlip struct {
	XMLName xml.Name `xml:"a:blip"`
	Embed   string   `xml:"r:embed,attr"`
}

type ctStretch struct {
	XMLName  xml.Name `xml:"a:stretch"`
	FillRect struct{} `xml:"a:fillRect"`
}

type ctBlipFill struct {
	XMLName xml.Name  `xml:"pic:blipFill"`
	Blip    ctBlip    `xml:"a:blip"`
	Stretch ctStretch `xml:"a:stretch"`
}

type ctOff struct {
	XMLName xml.Name `xml:"a:off"`
	X       int64    `xml:"x,attr"`
	Y       int64    `xml:"y,attr"`
}

type ctExt struct {
	XMLName xml.Name `xml:"a:ext"`
	Cx      int64    `xml:"cx,attr"`
	Cy      int64    `xml:"cy,attr"`
}

type ctXfrm struct {
	XMLName xml.Name `xml:"a:xfrm"`
	Off     ctOff    `xml:"a:off"`
	Ext     ctExt    `xml:"a:ext"`
}

type ctPrstGeom struct {
	XMLName xml.Name `xml:"a:prstGeom"`
	Prst    string   `xml:"prst,attr"`
	AvLst   struct{} `xml:"a:avLst"`
}

type ctSpPr struct {
	XMLName  xml.Name   `xml:"pic:spPr"`
	Xfrm     ctXfrm     `xml:"a:xfrm"`
	PrstGeom ctPrstGeom `xml:"a:prstGeom"`
}

type ctPic struct {
	XMLName  xml.Name   `xml:"pic:pic"`
	NvPicPr  ctNvPicPr  `xml:"pic:nvPicPr"`
	BlipFill ctBlipFill `xml:"pic:blipFill"`
	SpPr     ctSpPr     `xml:"pic:spPr"`
}

type ctGraphicData struct {
	XMLName xml.Name `xml:"a:graphicData"`
	URI     string   `xml:"uri,attr"`
	Pic     ctPic    `xml:"pic:pic"`
}

type ctGraphic struct {
	XMLName xml.Name      `xml:"a:graphic"`
	Data    ctGraphicData `xml:"a:graphicData"`
}

type ctInline struct {
	XMLName      xml.Name       `xml:"wp:inline"`
	Extent       ctExtent       `xml:"wp:extent"`
	EffectExtent ctEffectExtent `xml:"wp:effectExtent"`
	DocPr        ctIDName       `xml:"wp:docPr"`
	Graphic      ctGraphic      `xml:"a:graphic"`
}

type ctDrawing struct {
	XMLName xml.Name `xml:"w:drawing"`
	Inline  ctInline `xml:"wp:inline"`
}
