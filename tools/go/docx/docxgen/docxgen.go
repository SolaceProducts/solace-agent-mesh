// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Alignment string

const (
	AlignLeft    Alignment = "left"
	AlignCenter  Alignment = "center"
	AlignRight   Alignment = "right"
	AlignJustify Alignment = "both"
)

const EMUPerInch = 914400

var zipEpoch = time.Date(1980, time.January, 1, 12, 0, 0, 0, time.UTC)

type Run struct {
	Text      string
	Bold      bool
	Italic    bool
	Underline bool
	ColorHex  string
}

var allowedImageExts = map[string]bool{"png": true, "jpeg": true, "jpg": true, "gif": true}

type imageAsset struct {
	relID     string
	mediaName string
	data      []byte
}

type Document struct {
	title, author, subject string
	bodyParts              [][]byte
	images                 []imageAsset
	numberedLists          int
}

func New() *Document {
	return &Document{}
}

func (d *Document) SetMetadata(title, author, subject string) {
	d.title = title
	d.author = author
	d.subject = subject
}

func NormalizeColorHex(c string) (string, error) {
	trimmed := strings.TrimSpace(c)
	if trimmed == "" {
		return "", nil
	}
	hex := strings.TrimPrefix(trimmed, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return "", fmt.Errorf("color %q must be 3 or 6 hex digits, optionally prefixed with '#' (e.g. C0392B or #C0392B)", c)
	}
	for _, r := range hex {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return "", fmt.Errorf("color %q contains non-hex character %q; use a hex value such as C0392B, not a colour name", c, string(r))
		}
	}
	return strings.ToUpper(hex), nil
}

func buildRun(r Run) ctRun {
	var rpr *ctRPr
	if r.Bold || r.Italic || r.Underline || r.ColorHex != "" {
		rpr = &ctRPr{}
		if r.Bold {
			rpr.B = &ctEmpty{}
		}
		if r.Italic {
			rpr.I = &ctEmpty{}
		}
		if r.Underline {
			rpr.U = &ctVal{Val: "single"}
		}
		if r.ColorHex != "" {
			rpr.Color = &ctVal{Val: r.ColorHex}
		}
	}
	return ctRun{RPr: rpr, Text: &ctText{Space: "preserve", Value: r.Text}}
}

func (d *Document) appendXML(v any) error {
	b, err := xml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal xml: %w", err)
	}
	d.bodyParts = append(d.bodyParts, b)
	return nil
}

func (d *Document) AddHeading(level int, text string) error {
	if level < 1 || level > 4 {
		return fmt.Errorf("heading level must be 1-4, got %d", level)
	}
	p := ctParagraph{
		PPr:  &ctPPr{PStyle: &ctVal{Val: fmt.Sprintf("Heading%d", level)}},
		Runs: []ctRun{buildRun(Run{Text: text})},
	}
	return d.appendXML(p)
}

func (d *Document) AddParagraph(runs []Run, align Alignment) error {
	var ppr *ctPPr
	if align != "" {
		ppr = &ctPPr{Jc: &ctVal{Val: string(align)}}
	}
	xruns := make([]ctRun, 0, len(runs))
	for i, r := range runs {
		normalized, err := NormalizeColorHex(r.ColorHex)
		if err != nil {
			return fmt.Errorf("run %d: %w", i+1, err)
		}
		r.ColorHex = normalized
		xruns = append(xruns, buildRun(r))
	}
	return d.appendXML(ctParagraph{PPr: ppr, Runs: xruns})
}

func (d *Document) addList(items []string, numID string) error {
	for _, item := range items {
		p := ctParagraph{
			PPr: &ctPPr{
				PStyle: &ctVal{Val: "ListParagraph"},
				NumPr:  &ctNumPr{Ilvl: ctVal{Val: "0"}, NumID: ctVal{Val: numID}},
			},
			Runs: []ctRun{buildRun(Run{Text: item})},
		}
		if err := d.appendXML(p); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) AddBulletList(items []string) error {
	return d.addList(items, numIDBullet)
}

func (d *Document) AddNumberedList(items []string) error {
	if len(items) == 0 {
		return nil
	}
	d.numberedLists++
	return d.addList(items, strconv.Itoa(firstDecimalNumID+d.numberedLists-1))
}

func buildNumberingXML(decimalLists int) []byte {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	b.WriteString(numberingAbstractXML)
	fmt.Fprintf(&b, `<w:num w:numId="%s"><w:abstractNumId w:val="0"/></w:num>`, numIDBullet)
	for i := range decimalLists {
		fmt.Fprintf(&b,
			`<w:num w:numId="%d"><w:abstractNumId w:val="1"/>`+
				`<w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`,
			firstDecimalNumID+i)
	}
	b.WriteString(`</w:numbering>`)
	return []byte(b.String())
}

const (
	MaxTableColumns = 63
	MaxTableCells   = 200_000
)

const minGridColTwips = 144

const maxImageBytes = 64 << 20

type TableOptions struct {
	HeaderBold bool
	ColWidths  []float64
}

func (d *Document) AddTable(headers []string, rows [][]string, opts TableOptions) error {
	ncols := len(headers)
	for _, r := range rows {
		if len(r) > ncols {
			ncols = len(r)
		}
	}
	if ncols == 0 {
		return fmt.Errorf("table requires at least one column (headers or a row)")
	}
	nrows := len(rows)
	if len(headers) > 0 {
		nrows++
	}
	if ncols > MaxTableColumns {
		return fmt.Errorf("table has %d columns, more than the %d a Word table supports; "+
			"this usually means one row was emitted as a flat list of every value instead of "+
			"being nested as its own row — check the widest row in the spec",
			ncols, MaxTableColumns)
	}
	if cells := ncols * nrows; cells > MaxTableCells {
		return fmt.Errorf("table is too large: %d columns x %d rows = %d cells exceeds the %d-cell limit; "+
			"split it across several table blocks, or use the spreadsheet tool for bulk tabular data",
			ncols, nrows, cells, MaxTableCells)
	}

	colTwips := resolveGridColTwips(opts.ColWidths, ncols)

	cellAt := func(row []string, i int, bold bool) ctTableCell {
		text := ""
		if i < len(row) {
			text = row[i]
		}
		return ctTableCell{
			TcPr:  ctTcPr{TcW: ctTblW{Type: "dxa", W: strconv.Itoa(colTwips[i])}},
			Paras: []ctParagraph{{Runs: []ctRun{buildRun(Run{Text: text, Bold: bold})}}},
		}
	}

	var trows []ctTableRow
	if len(headers) > 0 {
		cells := make([]ctTableCell, ncols)
		for i := range ncols {
			cells[i] = cellAt(headers, i, opts.HeaderBold)
		}
		trows = append(trows, ctTableRow{Cells: cells})
	}
	for _, row := range rows {
		cells := make([]ctTableCell, ncols)
		for i := range ncols {
			cells[i] = cellAt(row, i, false)
		}
		trows = append(trows, ctTableRow{Cells: cells})
	}

	gridCols := make([]ctGridCol, ncols)
	for i := range gridCols {
		gridCols[i] = ctGridCol{W: strconv.Itoa(colTwips[i])}
	}

	tblW := ctTblW{Type: "pct", W: "5000"}
	layout := "autofit"
	if len(opts.ColWidths) > 0 {
		tblW = ctTblW{Type: "dxa", W: strconv.Itoa(PrintableWidthTwips)}
		layout = "fixed"
	}

	t := ctTable{
		TblPr: ctTblPr{
			TblStyle:  ctVal{Val: "TableGrid"},
			TblW:      tblW,
			TblLayout: ctTblLayout{Type: layout},
		},
		Grid: ctTblGrid{GridCols: gridCols},
		Rows: trows,
	}
	if err := d.appendXML(t); err != nil {
		return err
	}
	return d.appendXML(ctParagraph{})
}

func resolveGridColTwips(shares []float64, ncols int) []int {
	out := make([]int, ncols)

	total := 0.0
	if len(shares) >= ncols {
		for _, s := range shares[:ncols] {
			if usableShare(s) {
				total += s
			}
		}
	}

	if total <= 0 {
		even := PrintableWidthTwips / ncols
		for i := range out {
			out[i] = even
		}
		out[ncols-1] += PrintableWidthTwips - even*ncols
		return out
	}

	spreadShares(out, shares, total, 0)
	if slices.Min(out) >= minGridColTwips {
		return out
	}
	spreadShares(out, shares, total, minGridColTwips)
	return out
}

func spreadShares(out []int, shares []float64, total float64, floor int) {
	pool := PrintableWidthTwips - floor*len(out)

	assigned, widest := 0, 0
	for i := range out {
		part := 0
		if usableShare(shares[i]) {
			part = int(float64(pool) * shares[i] / total)
		}
		out[i] = floor + part
		assigned += out[i]
		if out[i] > out[widest] {
			widest = i
		}
	}
	out[widest] += PrintableWidthTwips - assigned
}

func usableShare(s float64) bool {
	return s > 0 && !math.IsInf(s, 0)
}

func (d *Document) AddImage(data []byte, ext string, widthEMU, heightEMU int64, align Alignment) error {
	if len(data) == 0 {
		return fmt.Errorf("image data is empty")
	}
	if len(data) > maxImageBytes {
		return fmt.Errorf("image is %.1f MB, over the %d MB limit; downscale it before embedding",
			float64(len(data))/(1<<20), maxImageBytes>>20)
	}
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if !allowedImageExts[ext] {
		return fmt.Errorf("unsupported image extension %q (use png, jpeg, jpg, or gif)", ext)
	}
	if widthEMU <= 0 || heightEMU <= 0 {
		return fmt.Errorf("image width/height must be positive (got %dx%d EMU)", widthEMU, heightEMU)
	}

	idx := len(d.images) + 1
	relID := fmt.Sprintf("rId%d", 100+idx)
	d.images = append(d.images, imageAsset{
		relID:     relID,
		mediaName: fmt.Sprintf("image%d.%s", idx, ext),
		data:      data,
	})

	pic := ctPic{
		NvPicPr: ctNvPicPr{
			CNvPr:  ctIDName{ID: 0, Name: fmt.Sprintf("Picture %d", idx)},
			CNvPic: ctCNvPicPr{PicLocks: ctPicLocks{NoChangeAspect: "1"}},
		},
		BlipFill: ctBlipFill{Blip: ctBlip{Embed: relID}},
		SpPr: ctSpPr{
			Xfrm:     ctXfrm{Off: ctOff{X: 0, Y: 0}, Ext: ctExt{Cx: widthEMU, Cy: heightEMU}},
			PrstGeom: ctPrstGeom{Prst: "rect"},
		},
	}
	drawing := ctDrawing{Inline: ctInline{
		Extent:       ctExtent{Cx: widthEMU, Cy: heightEMU},
		EffectExtent: ctEffectExtent{L: "0", T: "0", R: "0", B: "0"},
		DocPr:        ctIDName{ID: idx, Name: fmt.Sprintf("Picture %d", idx)},
		Graphic: ctGraphic{Data: ctGraphicData{
			URI: "http://schemas.openxmlformats.org/drawingml/2006/picture",
			Pic: pic,
		}},
	}}

	var ppr *ctPPr
	if align != "" {
		ppr = &ctPPr{Jc: &ctVal{Val: string(align)}}
	}
	return d.appendXML(ctParagraph{PPr: ppr, Runs: []ctRun{{Drawing: &drawing}}})
}

func (d *Document) AddPageBreak() error {
	return d.appendXML(ctParagraph{Runs: []ctRun{{Br: &ctBr{Type: "page"}}}})
}

func (d *Document) AddTableOfContents(title, levels string) error {
	if title != "" {
		if err := d.AddHeading(1, title); err != nil {
			return err
		}
	}
	if levels == "" {
		levels = "1-3"
	}
	instr := fmt.Sprintf(`TOC \o %q \h \z \u`, levels)
	p := ctParagraph{Runs: []ctRun{
		{FldChar: &ctFldChar{Type: "begin", Dirty: "true"}},
		{InstrText: &ctText{Space: "preserve", Value: instr}},
		{FldChar: &ctFldChar{Type: "separate"}},
		{Text: &ctText{Value: `Right-click and choose "Update Field" to generate the table of contents.`}},
		{FldChar: &ctFldChar{Type: "end"}},
	}}
	return d.appendXML(p)
}

func buildDocumentRels(images []imageAsset) []byte {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
	b.WriteString(`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`)
	for _, img := range images {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, img.relID, img.mediaName)
	}
	b.WriteString(`</Relationships>`)
	return []byte(b.String())
}

const documentNamespaces = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
	`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
	`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"`

func (d *Document) ToBytes() ([]byte, error) {
	var body bytes.Buffer
	body.WriteString(`<w:document ` + documentNamespaces + `><w:body>`)
	for _, part := range d.bodyParts {
		body.Write(part)
	}
	body.WriteString(sectPrXML)
	body.WriteString(`</w:body></w:document>`)
	documentXML := append([]byte(xml.Header), body.Bytes()...)

	coreXML, err := buildCoreProps(d.title, d.author, d.subject)
	if err != nil {
		return nil, err
	}
	appXML, err := buildAppProps()
	if err != nil {
		return nil, err
	}

	files := []struct {
		name string
		data []byte
	}{
		{"[Content_Types].xml", []byte(contentTypesXML)},
		{"_rels/.rels", []byte(rootRelsXML)},
		{"docProps/core.xml", coreXML},
		{"docProps/app.xml", appXML},
		{"word/document.xml", documentXML},
		{"word/styles.xml", []byte(stylesXML)},
		{"word/numbering.xml", buildNumberingXML(d.numberedLists)},
		{"word/_rels/document.xml.rels", buildDocumentRels(d.images)},
	}
	for _, img := range d.images {
		files = append(files, struct {
			name string
			data []byte
		}{name: "word/media/" + img.mediaName, data: img.data})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     f.name,
			Method:   zip.Deflate,
			Modified: zipEpoch,
		})
		if err != nil {
			return nil, fmt.Errorf("zip create %q: %w", f.name, err)
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, fmt.Errorf("zip write %q: %w", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}
