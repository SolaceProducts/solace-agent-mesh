// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

type part struct {
	name string
	data []byte
}

func packPresentation(p *Presentation) ([]byte, error) {
	parts, err := buildParts(p)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mt := p.timestamp().UTC()
	for _, pt := range parts {
		fh := &zip.FileHeader{
			Name:     pt.name,
			Method:   zip.Deflate,
			Modified: mt,
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return nil, fmt.Errorf("pptxgen: create zip entry %q: %w", pt.name, err)
		}
		if _, err := w.Write(pt.data); err != nil {
			return nil, fmt.Errorf("pptxgen: write zip entry %q: %w", pt.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("pptxgen: close zip writer: %w", err)
	}
	return buf.Bytes(), nil
}

func buildParts(p *Presentation) ([]part, error) {
	if len(p.slides) == 0 {
		return nil, fmt.Errorf("pptxgen: presentation has no slides")
	}
	slideCount := len(p.slides)
	hasNotes := p.hasNotes()

	notesSlideIdxs := make([]int, 0, slideCount)
	for i, s := range p.slides {
		if s.notes != "" {
			notesSlideIdxs = append(notesSlideIdxs, i+1)
		}
	}

	var charts []*chartElement
	for _, s := range p.slides {
		for _, el := range s.elements {
			if c, ok := el.(*chartElement); ok {
				charts = append(charts, c)
			}
		}
	}

	extraDefaults, extraOverrides := buildDynamicContentTypes(p, hasNotes, notesSlideIdxs)
	for _, c := range charts {
		extraOverrides = append(extraOverrides, contentTypeOverride{
			partName:    fmt.Sprintf("/ppt/charts/chart%d.xml", c.chartIdx),
			contentType: "application/vnd.openxmlformats-officedocument.drawingml.chart+xml",
		})
	}

	parts := make([]part, 0, 14+slideCount*2+len(notesSlideIdxs)*2+len(p.media)+len(charts))

	parts = append(parts,
		part{name: "[Content_Types].xml", data: render(func(b *strings.Builder) {
			emitContentTypes(b, slideCount, extraDefaults, extraOverrides)
		})},
		part{name: "_rels/.rels", data: render(emitRootRels)},
		part{name: "docProps/app.xml", data: render(func(b *strings.Builder) {
			emitAppProps(b, slideCount, len(notesSlideIdxs))
		})},
		part{name: "docProps/core.xml", data: render(func(b *strings.Builder) {
			emitCoreProps(b, p.title, p.author, p.description, p.keywords, p.timestamp())
		})},
	)

	parts = append(parts,
		part{name: "ppt/_rels/presentation.xml.rels", data: render(func(b *strings.Builder) {
			emitPresentationRels(b, slideCount, hasNotes)
		})},
		part{name: "ppt/presentation.xml", data: render(func(b *strings.Builder) {
			emitPresentation(b, p.widthEMU, p.heightEMU, slideCount, hasNotes)
		})},
		part{name: "ppt/presProps.xml", data: render(emitPresProps)},
		part{name: "ppt/viewProps.xml", data: render(emitViewProps)},
		part{name: "ppt/tableStyles.xml", data: render(emitTableStyles)},
	)

	parts = append(parts, part{name: "ppt/theme/theme1.xml", data: render(emitTheme)})

	parts = append(parts,
		part{name: "ppt/slideMasters/_rels/slideMaster1.xml.rels", data: render(emitSlideMasterRels)},
		part{name: "ppt/slideMasters/slideMaster1.xml", data: render(emitSlideMaster)},
		part{name: "ppt/slideLayouts/_rels/slideLayout1.xml.rels", data: render(emitSlideLayoutRels)},
		part{name: "ppt/slideLayouts/slideLayout1.xml", data: render(emitSlideLayout)},
	)

	if hasNotes {
		parts = append(parts,
			part{name: "ppt/notesMasters/_rels/notesMaster1.xml.rels", data: render(emitNotesMasterRels)},
			part{name: "ppt/notesMasters/notesMaster1.xml", data: render(emitNotesMaster)},
		)
	}

	for i, s := range p.slides {
		idx := i + 1
		slide := s
		parts = append(parts, part{
			name: fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", idx),
			data: render(func(b *strings.Builder) { emitSlideRels(b, slide) }),
		})
		parts = append(parts, part{
			name: fmt.Sprintf("ppt/slides/slide%d.xml", idx),
			data: render(func(b *strings.Builder) { emitSlide(b, slide) }),
		})
		if s.notes != "" {
			notes := s.notes
			parts = append(parts, part{
				name: fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", idx),
				data: render(func(b *strings.Builder) { emitNotesSlideRels(b, idx) }),
			})
			parts = append(parts, part{
				name: fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", idx),
				data: render(func(b *strings.Builder) { emitNotesSlide(b, notes, idx) }),
			})
		}
	}

	for _, m := range p.media {
		parts = append(parts, part{name: m.partName(), data: m.data})
	}

	for _, c := range charts {
		chart := c
		parts = append(parts, part{
			name: fmt.Sprintf("ppt/charts/chart%d.xml", chart.chartIdx),
			data: render(func(b *strings.Builder) { emitChartPart(b, chart) }),
		})
	}

	return parts, nil
}

func buildDynamicContentTypes(p *Presentation, hasNotes bool, notesSlideIdxs []int) ([]contentTypeDefault, []contentTypeOverride) {
	var defaults []contentTypeDefault
	seenExt := map[string]bool{}
	for _, m := range p.media {
		if seenExt[m.ext] {
			continue
		}
		seenExt[m.ext] = true
		defaults = append(defaults, contentTypeDefault{
			extension:   m.ext,
			contentType: m.contentType,
		})
	}

	var overrides []contentTypeOverride
	if hasNotes {
		overrides = append(overrides, contentTypeOverride{
			partName:    "/ppt/notesMasters/notesMaster1.xml",
			contentType: "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml",
		})
		for _, idx := range notesSlideIdxs {
			overrides = append(overrides, contentTypeOverride{
				partName:    "/ppt/notesSlides/notesSlide" + fmtInt(idx) + ".xml",
				contentType: "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml",
			})
		}
	}
	return defaults, overrides
}

func render(fn func(*strings.Builder)) []byte {
	var b strings.Builder
	fn(&b)
	return []byte(b.String())
}
