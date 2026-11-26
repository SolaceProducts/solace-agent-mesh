// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"fmt"
	"strings"
)

type slideElement interface {
	emitXML(b *strings.Builder, ids *idAllocator)
}

type relationship struct {
	id      string
	relType string
	target  string
}

type Slide struct {
	p        *Presentation
	bg       *BackgroundOptions
	elements []slideElement
	rels     []relationship
	relIDs   *relIDAllocator

	notes string

	notesRelID string
}

func newSlide(p *Presentation) *Slide {
	return &Slide{p: p, relIDs: newRelIDAllocator(2)}
}

func (s *Slide) SetBackground(opts BackgroundOptions) {
	col := parseColor(opts.Color)
	if col == "" {
		s.bg = nil
		return
	}
	s.bg = &BackgroundOptions{Color: col}
}

func (s *Slide) SetNotes(text string) {
	if text == "" {
		s.notes = ""
		s.notesRelID = ""
		return
	}
	s.notes = text
	if s.notesRelID == "" {
		s.notesRelID = s.relIDs.Next()
	}
}

func (s *Slide) AddText(text string, opts TextOptions) {
	s.elements = append(s.elements, &textElement{text: text, opts: opts})
}

func (s *Slide) AddShape(kind ShapeType, opts ShapeOptions) {
	s.elements = append(s.elements, &shapeElement{kind: kind, opts: opts})
}

func (s *Slide) AddImage(opts ImageOptions) {
	data, err := loadImageBytes(opts)
	if err != nil {
		s.p.recordLoadErr(err)
		return
	}
	ext, contentType, err := detectImageFormat(data)
	if err != nil {
		s.p.recordLoadErr(err)
		return
	}
	media := s.p.registerMedia(data, ext, contentType)
	rid := s.relIDs.Next()
	s.rels = append(s.rels, relationship{
		id:      rid,
		relType: imageRelType,
		target:  "../media/image" + fmtInt(media.index) + "." + media.ext,
	})
	s.elements = append(s.elements, &imageElement{
		xEMU:       inchToEMU(opts.X),
		yEMU:       inchToEMU(opts.Y),
		wEMU:       inchToEMU(opts.Width),
		hEMU:       inchToEMU(opts.Height),
		relID:      rid,
		mediaIndex: media.index,
		altText:    opts.AltText,
	})
}

func (s *Slide) AddTable(rows [][]TableCell, opts TableOptions) {
	s.elements = append(s.elements, &tableElement{rows: rows, opts: opts})
}

func (s *Slide) AddChart(kind ChartType, series []ChartSeries, opts ChartOptions) {
	chartIdx := s.p.nextChartIdx()
	rID := s.relIDs.Next()

	ch := &chartElement{
		kind:     kind,
		series:   series,
		opts:     opts,
		chartIdx: chartIdx,
		rID:      rID,
	}
	s.elements = append(s.elements, ch)
	s.rels = append(s.rels, relationship{
		id:      rID,
		relType: "http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart",
		target:  fmt.Sprintf("../charts/chart%d.xml", chartIdx),
	})
}
