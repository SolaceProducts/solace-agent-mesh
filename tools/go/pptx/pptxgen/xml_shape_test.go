// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const shapeGoldenDir = "testdata/shape"

var shapeCases = []struct {
	name string
	kind ShapeType
	opts ShapeOptions
}{
	{
		name: "rect_fill_no_line",
		kind: ShapeRect,
		opts: ShapeOptions{X: 1, Y: 1, Width: 4, Height: 0.1, Fill: "1F4E79"},
	},
	{
		name: "rect_fill_and_line",
		kind: ShapeRect,
		opts: ShapeOptions{
			X: 0.5, Y: 0.5, Width: 9, Height: 5,
			Fill:      "FFFFFF",
			LineColor: "9CA3AF", LineWidth: 1,
		},
	},
	{
		name: "round_rect_fill_and_line",
		kind: ShapeRoundRect,
		opts: ShapeOptions{
			X: 1, Y: 1, Width: 4, Height: 2,
			Fill:      "FFFFFF",
			LineColor: "D1D5DB", LineWidth: 0.75,
		},
	},
	{
		name: "ellipse_fill_no_line",
		kind: ShapeEllipse,
		opts: ShapeOptions{X: 0.5, Y: 0.5, Width: 0.12, Height: 0.12, Fill: "DC2626"},
	},
	{
		name: "rect_no_fill_no_line",
		kind: ShapeRect,
		opts: ShapeOptions{X: 1, Y: 1, Width: 2, Height: 1},
	},
	{
		name: "rect_width_no_color",
		kind: ShapeRect,
		opts: ShapeOptions{X: 1, Y: 1, Width: 2, Height: 1, Fill: "EEEEEE", LineWidth: 1},
	},
	{
		name: "rect_color_no_width",
		kind: ShapeRect,
		opts: ShapeOptions{X: 1, Y: 1, Width: 2, Height: 1, Fill: "EEEEEE", LineColor: "000000"},
	},
	{
		name: "rect_hash_prefixed_colors",
		kind: ShapeRect,
		opts: ShapeOptions{
			X: 0, Y: 0, Width: 1, Height: 1,
			Fill:      "#abcdef",
			LineColor: "#123456", LineWidth: 2,
		},
	},
}

func TestShapeElementGoldens(t *testing.T) {
	for _, c := range shapeCases {
		t.Run(c.name, func(t *testing.T) {
			el := &shapeElement{kind: c.kind, opts: c.opts}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			got := []byte(b.String())

			path := filepath.Join(shapeGoldenDir, c.name+".xml")
			if *updateGolden {
				if err := os.MkdirAll(shapeGoldenDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run with -update to seed)", path, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("shape fragment %q differs from golden\n--- want ---\n%s\n--- got ---\n%s",
					c.name, want, got)
			}
		})
	}
}

func TestShapeElementWellFormed(t *testing.T) {
	for _, c := range shapeCases {
		t.Run(c.name, func(t *testing.T) {
			el := &shapeElement{kind: c.kind, opts: c.opts}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			doc := wrapNS(b.String())
			if err := parseAll([]byte(doc)); err != nil {
				t.Fatalf("malformed XML for %s: %v\n%s", c.name, err, doc)
			}
		})
	}
}

func TestShapeEndToEnd(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddShape(ShapeRect, ShapeOptions{X: 0, Y: 0, Width: 2, Height: 1, Fill: "FF0000"})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	slide := findEntry(t, out, "ppt/slides/slide1.xml")
	if !bytes.Contains(slide, []byte(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Shape 2"/>`)) {
		t.Errorf("shape not present in slide1.xml:\n%s", slide)
	}
	if !bytes.Contains(slide, []byte(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)) {
		t.Errorf("rect geometry missing:\n%s", slide)
	}
	if !bytes.Contains(slide, []byte(`<a:srgbClr val="FF0000"/>`)) {
		t.Errorf("fill colour missing:\n%s", slide)
	}
}

func TestShapeTransparency(t *testing.T) {
	t.Run("fill_and_line_50pct", func(t *testing.T) {
		el := &shapeElement{
			kind: ShapeRect,
			opts: ShapeOptions{
				X: 0, Y: 0, Width: 1, Height: 1,
				Fill:         "000000",
				LineColor:    "000000",
				LineWidth:    1,
				Transparency: 50,
			},
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `<a:solidFill><a:srgbClr val="000000"><a:alpha val="50000"/></a:srgbClr></a:solidFill>`) {
			t.Errorf("expected fill alpha 50000 in fill, got:\n%s", got)
		}
		if !strings.Contains(got, `<a:ln w="12700"><a:solidFill><a:srgbClr val="000000"><a:alpha val="50000"/></a:srgbClr></a:solidFill></a:ln>`) {
			t.Errorf("expected fill alpha 50000 in line, got:\n%s", got)
		}
	})

	t.Run("fill_only_40pct_no_line", func(t *testing.T) {
		el := &shapeElement{
			kind: ShapeRect,
			opts: ShapeOptions{
				X: 0, Y: 0, Width: 1, Height: 1,
				Fill:         "000000",
				LineColor:    "000000",
				Transparency: 40,
			},
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `<a:alpha val="60000"/>`) {
			t.Errorf("expected alpha 60000 (60%% opaque), got:\n%s", got)
		}
		if strings.Contains(got, "<a:ln") {
			t.Errorf("expected no line element when LineWidth=0, got:\n%s", got)
		}
	})

	t.Run("zero_transparency_omits_alpha", func(t *testing.T) {
		el := &shapeElement{
			kind: ShapeRect,
			opts: ShapeOptions{
				X: 0, Y: 0, Width: 1, Height: 1,
				Fill: "FF0000",
			},
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if strings.Contains(got, "<a:alpha") {
			t.Errorf("zero transparency must not emit <a:alpha/>, got:\n%s", got)
		}
	})

	t.Run("clamps_above_100", func(t *testing.T) {
		el := &shapeElement{
			kind: ShapeRect,
			opts: ShapeOptions{
				X: 0, Y: 0, Width: 1, Height: 1,
				Fill:         "FF0000",
				Transparency: 250,
			},
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `<a:alpha val="0"/>`) {
			t.Errorf("expected alpha 0 from clamped Transparency=250, got:\n%s", got)
		}
	})
}

func TestShapeShadow(t *testing.T) {
	t.Run("full_params", func(t *testing.T) {
		el := &shapeElement{
			kind: ShapeRoundRect,
			opts: ShapeOptions{
				X: 0, Y: 0, Width: 4, Height: 2, Fill: "FFFFFF",
				Shadow: &Shadow{Color: "0A1622", BlurPt: 7, DistancePt: 3, DirectionDeg: 90, OpacityPct: 18},
			},
		}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		want := `<a:effectLst><a:outerShdw blurRad="88900" dist="38100" dir="5400000" algn="bl" rotWithShape="0"><a:srgbClr val="0A1622"><a:alpha val="18000"/></a:srgbClr></a:outerShdw></a:effectLst>`
		if !strings.Contains(got, want) {
			t.Errorf("shadow XML mismatch\nwant substring: %s\ngot:\n%s", want, got)
		}
		if strings.Index(got, "<a:solidFill") > strings.Index(got, "<a:effectLst") {
			t.Errorf("effectLst must follow fill in spPr, got:\n%s", got)
		}
	})

	t.Run("defaults_color_and_opacity", func(t *testing.T) {
		el := &shapeElement{kind: ShapeRect, opts: ShapeOptions{Width: 1, Height: 1, Shadow: &Shadow{BlurPt: 4}}}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		got := b.String()
		if !strings.Contains(got, `<a:srgbClr val="000000"><a:alpha val="40000"/>`) {
			t.Errorf("expected black 40%% default shadow, got:\n%s", got)
		}
	})

	t.Run("nil_shadow_emits_nothing", func(t *testing.T) {
		el := &shapeElement{kind: ShapeRect, opts: ShapeOptions{Width: 1, Height: 1, Fill: "FF0000"}}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		if strings.Contains(b.String(), "effectLst") {
			t.Errorf("nil shadow must not emit effectLst, got:\n%s", b.String())
		}
	})
}

func TestRoundRectCornerRadius(t *testing.T) {
	t.Run("absolute_radius_constant_across_sizes", func(t *testing.T) {
		el := &shapeElement{kind: ShapeRoundRect, opts: ShapeOptions{Width: 4, Height: 2, CornerRadiusIn: 0.15}}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		if !strings.Contains(b.String(), `fmla="val 7500"`) {
			t.Errorf("expected adj 7500 for 0.15in/2in, got:\n%s", b.String())
		}
	})

	t.Run("clamped_to_pill", func(t *testing.T) {
		el := &shapeElement{kind: ShapeRoundRect, opts: ShapeOptions{Width: 2, Height: 0.4, CornerRadiusIn: 5}}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		if !strings.Contains(b.String(), `fmla="val 50000"`) {
			t.Errorf("expected adj clamped to 50000, got:\n%s", b.String())
		}
	})

	t.Run("zero_keeps_default", func(t *testing.T) {
		el := &shapeElement{kind: ShapeRoundRect, opts: ShapeOptions{Width: 4, Height: 2}}
		var b strings.Builder
		el.emitXML(&b, newIDAllocator())
		if !strings.Contains(b.String(), `fmla="val 20000"`) {
			t.Errorf("expected default adj 20000 when CornerRadiusIn=0, got:\n%s", b.String())
		}
	})
}

func TestMixedTextAndShapeIDs(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddText("hi", TextOptions{X: 0, Y: 0, Width: 2, Height: 1})
	s.AddShape(ShapeRect, ShapeOptions{X: 2, Y: 0, Width: 2, Height: 1, Fill: "FF0000"})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	slide := findEntry(t, out, "ppt/slides/slide1.xml")
	if !bytes.Contains(slide, []byte(`id="2" name="TextBox 2"`)) {
		t.Errorf("expected TextBox at id=2:\n%s", slide)
	}
	if !bytes.Contains(slide, []byte(`id="3" name="Shape 3"`)) {
		t.Errorf("expected Shape at id=3:\n%s", slide)
	}
}
