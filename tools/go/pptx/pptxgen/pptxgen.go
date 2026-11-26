// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"errors"
	"time"
)

const (
	defaultSlideWidthEMU  = 9144000
	defaultSlideHeightEMU = 6858000
	defaultNotesWidthEMU  = 6858000
	defaultNotesHeightEMU = 9144000
)

type Presentation struct {
	title       string
	author      string
	description string
	keywords    string
	widthEMU    int
	heightEMU   int

	slides []*Slide

	media []*mediaEntry

	loadErr error

	chartIdxNext int

	fixedTime time.Time
}

type mediaEntry struct {
	index       int
	ext         string
	contentType string
	data        []byte
}

func (m *mediaEntry) partName() string {
	return "ppt/media/image" + fmtInt(m.index) + "." + m.ext
}

func (p *Presentation) registerMedia(data []byte, ext, contentType string) *mediaEntry {
	m := &mediaEntry{
		index:       len(p.media) + 1,
		ext:         ext,
		contentType: contentType,
		data:        data,
	}
	p.media = append(p.media, m)
	return m
}

func (p *Presentation) recordLoadErr(err error) {
	if p.loadErr == nil {
		p.loadErr = err
	}
}

func (p *Presentation) hasNotes() bool {
	for _, s := range p.slides {
		if s.notes != "" {
			return true
		}
	}
	return false
}

func New() *Presentation {
	return &Presentation{
		widthEMU:  defaultSlideWidthEMU,
		heightEMU: defaultSlideHeightEMU,
	}
}

func (p *Presentation) SetTitle(s string) { p.title = s }

func (p *Presentation) SetAuthor(s string) { p.author = s }

func (p *Presentation) SetDescription(s string) { p.description = s }

func (p *Presentation) SetKeywords(s string) { p.keywords = s }

func (p *Presentation) SetSlideSize(widthInches, heightInches float64) {
	p.widthEMU = inchToEMU(widthInches)
	p.heightEMU = inchToEMU(heightInches)
}

func (p *Presentation) AddSlide() *Slide {
	s := newSlide(p)
	p.slides = append(p.slides, s)
	return s
}

func (p *Presentation) nextChartIdx() int {
	p.chartIdxNext++
	return p.chartIdxNext
}

func (p *Presentation) ToBytes() ([]byte, error) {
	if len(p.slides) == 0 {
		return nil, errors.New("pptxgen: cannot serialise a presentation with zero slides")
	}
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	return packPresentation(p)
}

func (p *Presentation) timestamp() time.Time {
	if !p.fixedTime.IsZero() {
		return p.fixedTime
	}
	return time.Now().UTC()
}

func (p *Presentation) setFixedTime(t time.Time) { p.fixedTime = t.UTC() }
