// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

type imageElement struct {
	xEMU, yEMU, wEMU, hEMU int
	relID                  string
	mediaIndex             int
	altText                string
}

func (e *imageElement) emitXML(b *strings.Builder, ids *idAllocator) {
	id := ids.Next()
	name := fmt.Sprintf("Picture %d", e.mediaIndex)
	b.WriteString(`<p:pic>`)
	b.WriteString(`<p:nvPicPr>`)
	b.WriteString(`<p:cNvPr id="`)
	b.WriteString(fmtInt(id))
	b.WriteString(`" name="`)
	b.WriteString(escapeXML(name))
	if e.altText != "" {
		b.WriteString(`" descr="`)
		b.WriteString(escapeXML(e.altText))
	}
	b.WriteString(`"/>`)
	b.WriteString(`<p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr>`)
	b.WriteString(`<p:nvPr/>`)
	b.WriteString(`</p:nvPicPr>`)
	b.WriteString(`<p:blipFill>`)
	b.WriteString(`<a:blip r:embed="`)
	b.WriteString(escapeXML(e.relID))
	b.WriteString(`"/>`)
	b.WriteString(`<a:stretch><a:fillRect/></a:stretch>`)
	b.WriteString(`</p:blipFill>`)
	b.WriteString(`<p:spPr>`)
	b.WriteString(`<a:xfrm>`)
	b.WriteString(`<a:off x="`)
	b.WriteString(fmtInt(e.xEMU))
	b.WriteString(`" y="`)
	b.WriteString(fmtInt(e.yEMU))
	b.WriteString(`"/>`)
	b.WriteString(`<a:ext cx="`)
	b.WriteString(fmtInt(e.wEMU))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(e.hEMU))
	b.WriteString(`"/>`)
	b.WriteString(`</a:xfrm>`)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	b.WriteString(`</p:spPr>`)
	b.WriteString(`</p:pic>`)
}

var (
	pngMagic  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
)

func detectImageFormat(data []byte) (ext, contentType string, err error) {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		return "png", "image/png", nil
	case bytes.HasPrefix(data, jpegMagic):
		return "jpeg", "image/jpeg", nil
	default:
		return "", "", fmt.Errorf("pptxgen: AddImage: unrecognised image format (need PNG or JPEG)")
	}
}

func loadImageBytes(opts ImageOptions) ([]byte, error) {
	switch {
	case len(opts.Data) > 0 && opts.Path != "":
		return nil, fmt.Errorf("pptxgen: AddImage: set exactly one of Data or Path, not both")
	case len(opts.Data) > 0:
		return opts.Data, nil
	case opts.Path != "":
		data, err := os.ReadFile(opts.Path)
		if err != nil {
			return nil, fmt.Errorf("pptxgen: AddImage: read %s: %w", opts.Path, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("pptxgen: AddImage: ImageOptions has neither Data nor Path")
	}
}

const imageRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
