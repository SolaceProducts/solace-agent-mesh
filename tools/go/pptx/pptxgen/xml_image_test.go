// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitImageElement(t *testing.T) {
	cases := []struct {
		name   string
		elem   *imageElement
		golden string
	}{
		{
			name: "png_basic",
			elem: &imageElement{
				xEMU:       914400,
				yEMU:       457200,
				wEMU:       2743200,
				hEMU:       1828800,
				relID:      "rId2",
				mediaIndex: 1,
			},
			golden: "testdata/image_png_basic.xml",
		},
		{
			name: "jpeg_offset",
			elem: &imageElement{
				xEMU:       4572000,
				yEMU:       2286000,
				wEMU:       1828800,
				hEMU:       1371600,
				relID:      "rId4",
				mediaIndex: 2,
			},
			golden: "testdata/image_jpeg_offset.xml",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			c.elem.emitXML(&b, newIDAllocator())
			got := []byte(b.String())

			if *updateGolden {
				if err := writeGolden(c.golden, got); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(c.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v", c.golden, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("emitXML differs from %s\n--- want ---\n%s\n--- got ---\n%s",
					c.golden, want, got)
			}
		})
	}
}

func TestImageAltText(t *testing.T) {
	t.Run("plain_alt_text", func(t *testing.T) {
		el := &imageElement{
			xEMU: 0, yEMU: 0, wEMU: 1, hEMU: 1,
			relID: "rId2", mediaIndex: 1,
			altText: "Otter mascot",
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `descr="Otter mascot"`) {
			t.Errorf("expected descr attribute, got:\n%s", got)
		}
	})

	t.Run("empty_alt_text_omits_descr", func(t *testing.T) {
		el := &imageElement{
			xEMU: 0, yEMU: 0, wEMU: 1, hEMU: 1,
			relID: "rId2", mediaIndex: 1,
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if strings.Contains(got, "descr=") {
			t.Errorf("empty alt text must not emit descr attribute, got:\n%s", got)
		}
	})

	t.Run("alt_text_xml_escaped", func(t *testing.T) {
		el := &imageElement{
			xEMU: 0, yEMU: 0, wEMU: 1, hEMU: 1,
			relID: "rId2", mediaIndex: 1,
			altText: `Caption with <tag> & "quotes"`,
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `descr="Caption with &lt;tag&gt; &amp; &quot;quotes&quot;"`) {
			t.Errorf("alt text not properly XML-escaped, got:\n%s", got)
		}
	})
}

func TestDetectImageFormat(t *testing.T) {
	cases := []struct {
		name            string
		data            []byte
		wantExt, wantCT string
		wantErr         bool
	}{
		{"png", append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0xDE, 0xAD), "png", "image/png", false},
		{"jpeg", append([]byte{0xFF, 0xD8, 0xFF}, 0xE0, 0x00), "jpeg", "image/jpeg", false},
		{"gif_rejected", []byte{0x47, 0x49, 0x46, 0x38}, "", "", true},
		{"empty", []byte{}, "", "", true},
		{"too_short_png", []byte{0x89, 0x50, 0x4E}, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext, ct, err := detectImageFormat(c.data)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if ext != c.wantExt {
				t.Errorf("ext = %q, want %q", ext, c.wantExt)
			}
			if ct != c.wantCT {
				t.Errorf("contentType = %q, want %q", ct, c.wantCT)
			}
		})
	}
}

func TestAddImageRejectsUnknownFormat(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddImage(ImageOptions{Data: []byte{0xDE, 0xAD, 0xBE, 0xEF}})
	if _, err := p.ToBytes(); err == nil {
		t.Fatalf("ToBytes: want error, got nil")
	}
	if len(s.elements) != 0 {
		t.Errorf("slide.elements = %d, want 0 (image should be dropped)", len(s.elements))
	}
	if len(p.media) != 0 {
		t.Errorf("p.media = %d, want 0", len(p.media))
	}
}

func TestAddImageRejectsBothSources(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddImage(ImageOptions{
		Data: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
		Path: "/nonexistent",
	})
	if _, err := p.ToBytes(); err == nil {
		t.Fatalf("ToBytes: want error, got nil")
	}
}

func TestAddImageEndToEnd(t *testing.T) {
	pngBytes := minimalPNG(t)
	jpegBytes := minimalJPEG(t)

	p := New()
	p.SetTitle("image deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)
	s1 := p.AddSlide()
	s1.AddImage(ImageOptions{X: 1, Y: 1, Width: 3, Height: 2, Data: pngBytes})
	s2 := p.AddSlide()
	s2.AddImage(ImageOptions{X: 2, Y: 1, Width: 4, Height: 3, Data: jpegBytes})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	have := map[string][]byte{}
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %s: %v", f.Name, oerr)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		have[f.Name] = data
	}

	for _, name := range []string{
		"ppt/media/image1.png",
		"ppt/media/image2.jpeg",
	} {
		if _, ok := have[name]; !ok {
			t.Errorf("missing media part: %s", name)
		}
	}
	if !bytes.Equal(have["ppt/media/image1.png"], pngBytes) {
		t.Errorf("ppt/media/image1.png bytes do not match input")
	}
	if !bytes.Equal(have["ppt/media/image2.jpeg"], jpegBytes) {
		t.Errorf("ppt/media/image2.jpeg bytes do not match input")
	}

	ct := string(have["[Content_Types].xml"])
	for _, want := range []string{
		`<Default Extension="png" ContentType="image/png"/>`,
		`<Default Extension="jpeg" ContentType="image/jpeg"/>`,
	} {
		if !strings.Contains(ct, want) {
			t.Errorf("[Content_Types].xml missing %q", want)
		}
	}

	rels1 := string(have["ppt/slides/_rels/slide1.xml.rels"])
	if !strings.Contains(rels1, `Target="../media/image1.png"`) {
		t.Errorf("slide1 rels missing image1.png target:\n%s", rels1)
	}
	rels2 := string(have["ppt/slides/_rels/slide2.xml.rels"])
	if !strings.Contains(rels2, `Target="../media/image2.jpeg"`) {
		t.Errorf("slide2 rels missing image2.jpeg target:\n%s", rels2)
	}

	slide1 := string(have["ppt/slides/slide1.xml"])
	if !strings.Contains(slide1, `<p:pic>`) {
		t.Errorf("slide1 xml missing <p:pic>:\n%s", slide1)
	}
	if !strings.Contains(slide1, `r:embed="rId2"`) {
		t.Errorf("slide1 xml missing r:embed=\"rId2\":\n%s", slide1)
	}
}

func TestAddImageFromPath(t *testing.T) {
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "img.png")
	if err := os.WriteFile(pngPath, minimalPNG(t), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddImage(ImageOptions{X: 0, Y: 0, Width: 1, Height: 1, Path: pngPath})
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if !bytes.Contains(findEntry(t, out, "ppt/slides/slide1.xml"), []byte("<p:pic>")) {
		t.Errorf("slide1 xml missing <p:pic>")
	}
}

func minimalPNG(t *testing.T) []byte {
	t.Helper()
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00, 0x05,
		0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44,
		0xAE, 0x42, 0x60, 0x82,
	}
}

func minimalJPEG(t *testing.T) []byte {
	t.Helper()
	return []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01,
		0x00, 0x01, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08,
		0x07, 0x07, 0x07, 0x09, 0x09, 0x08, 0x0A, 0x0C, 0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12,
		0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A, 0x1C, 0x1C, 0x20, 0x24, 0x2E, 0x27, 0x20,
		0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29, 0x2C, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27,
		0x39, 0x3D, 0x38, 0x32, 0x3C, 0x2E, 0x33, 0x34, 0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01,
		0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4, 0x00, 0x1F, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01,
		0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0xFF, 0xC4, 0x00, 0xB5, 0x10, 0x00, 0x02, 0x01, 0x03,
		0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7D, 0x01, 0x02, 0x03, 0x00,
		0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22, 0x71, 0x14, 0x32,
		0x81, 0x91, 0xA1, 0x08, 0x23, 0x42, 0xB1, 0xC1, 0x15, 0x52, 0xD1, 0xF0, 0x24, 0x33, 0x62, 0x72,
		0x82, 0x09, 0x0A, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x34, 0x35,
		0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4A, 0x53, 0x54, 0x55,
		0x56, 0x57, 0x58, 0x59, 0x5A, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6A, 0x73, 0x74, 0x75,
		0x76, 0x77, 0x78, 0x79, 0x7A, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8A, 0x92, 0x93, 0x94,
		0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xB2,
		0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9,
		0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, 0xD9, 0xDA, 0xE1, 0xE2, 0xE3, 0xE4, 0xE5, 0xE6,
		0xE7, 0xE8, 0xE9, 0xEA, 0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0xFA, 0xFF, 0xDA,
		0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, 0xFB, 0xD7, 0xFF, 0xD9,
	}
}

func writeGolden(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
