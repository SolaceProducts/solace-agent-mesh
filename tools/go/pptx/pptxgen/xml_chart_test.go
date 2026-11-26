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

type chartGoldenCase struct {
	name  string
	chart *chartElement
}

func chartGoldenCases() []chartGoldenCase {
	labels := []string{"Q1", "Q2", "Q3", "Q4"}
	apples := ChartSeries{Name: "Apples", Labels: labels, Values: []float64{12, 18, 15, 22}}
	oranges := ChartSeries{Name: "Oranges", Labels: labels, Values: []float64{9, 14, 16, 11}}
	pears := ChartSeries{Name: "Pears", Labels: labels, Values: []float64{4.5, 7, 6.25, 8}}

	sliceLabels := []string{"Apples", "Oranges", "Pears", "Plums", "Berries"}
	pie := ChartSeries{Name: "Mix", Labels: sliceLabels, Values: []float64{30, 22, 18, 15, 15}}

	mk := func(kind ChartType, idx int, opts ChartOptions, series ...ChartSeries) *chartElement {
		return &chartElement{
			kind:     kind,
			series:   series,
			opts:     opts,
			chartIdx: idx,
			rID:      "rId2",
		}
	}

	return []chartGoldenCase{
		{
			name: "chart_bar_basic",
			chart: mk(ChartBar, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Sales", ShowTitle: true, ShowLegend: true},
				apples, oranges,
			),
		},
		{
			name: "chart_bar_show_values",
			chart: mk(ChartBar, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Sales", ShowTitle: true, ShowLegend: true, ShowValues: true},
				apples, oranges,
			),
		},
		{
			name: "chart_bar_no_title_no_legend",
			chart: mk(ChartBar, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5},
				apples, oranges,
			),
		},
		{
			name: "chart_bar_custom_colors",
			chart: mk(ChartBar, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Custom", ShowTitle: true, ShowLegend: true, Colors: []string{"#3366CC", "33AA55"}},
				apples, oranges,
			),
		},
		{
			name: "chart_line_basic",
			chart: mk(ChartLine, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Trend", ShowTitle: true, ShowLegend: true},
				apples, oranges, pears,
			),
		},
		{
			name: "chart_line_show_values",
			chart: mk(ChartLine, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Trend", ShowTitle: true, ShowLegend: true, ShowValues: true},
				apples,
			),
		},
		{
			name: "chart_area_basic",
			chart: mk(ChartArea, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Volume", ShowTitle: true, ShowLegend: true},
				apples, oranges,
			),
		},
		{
			name: "chart_area_single_series_no_legend",
			chart: mk(ChartArea, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5},
				apples,
			),
		},
		{
			name: "chart_pie_basic",
			chart: mk(ChartPie, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Pie", ShowTitle: true, ShowLegend: true},
				pie,
			),
		},
		{
			name: "chart_pie_no_title_no_legend",
			chart: mk(ChartPie, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5},
				pie,
			),
		},
		{
			name: "chart_pie_show_values",
			chart: mk(ChartPie, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Pie + values", ShowTitle: true, ShowLegend: true, ShowValues: true},
				pie,
			),
		},
		{
			name: "chart_pie_custom_colors",
			chart: mk(ChartPie, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Pie custom", ShowTitle: true, ShowLegend: true, Colors: []string{"#3366CC", "33AA55", "#CC3333", "F39C12"}},
				pie,
			),
		},
		{
			name: "chart_pie_multi_series_flattened",
			chart: mk(ChartPie, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Pie", ShowTitle: true, ShowLegend: true},
				pie, apples, oranges, pears,
			),
		},
		{
			name: "chart_doughnut_basic",
			chart: mk(ChartDoughnut, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Doughnut", ShowTitle: true, ShowLegend: true},
				pie,
			),
		},
		{
			name: "chart_doughnut_show_values",
			chart: mk(ChartDoughnut, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Doughnut + values", ShowTitle: true, ShowLegend: true, ShowValues: true},
				pie,
			),
		},
		{
			name: "chart_doughnut_custom_colors",
			chart: mk(ChartDoughnut, 1,
				ChartOptions{X: 1, Y: 1, Width: 8, Height: 5, Title: "Doughnut custom", ShowTitle: true, ShowLegend: true, Colors: []string{"#3366CC", "33AA55", "#CC3333", "F39C12"}},
				pie,
			),
		},
	}
}

func TestChartGoldens(t *testing.T) {
	for _, tc := range chartGoldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			emitChartPart(&b, tc.chart)
			got := []byte(b.String())
			path := filepath.Join("testdata", tc.name+".xml")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("wrote %s (%d bytes)", path, len(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run with -update to create)", path, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("chart %s differs from golden\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
					tc.name, len(want), preview(want), len(got), preview(got))
			}
		})
	}
}

func TestChartBugRegressions(t *testing.T) {
	cases := []struct {
		name string
		kind ChartType
	}{
		{"bar", ChartBar},
		{"line", ChartLine},
		{"area", ChartArea},
		{"pie", ChartPie},
		{"doughnut", ChartDoughnut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &chartElement{
				kind:     tc.kind,
				chartIdx: 1,
				rID:      "rId2",
				series: []ChartSeries{
					{Name: "S1", Labels: []string{"A", "B", "C"}, Values: []float64{1, 2, 3}},
					{Name: "S2", Labels: []string{"A", "B", "C"}, Values: []float64{4, 5, 6}},
				},
				opts: ChartOptions{X: 1, Y: 1, Width: 6, Height: 4, ShowValues: true, ShowLegend: true},
			}
			var b strings.Builder
			emitChartPart(&b, c)
			xmlText := b.String()

			banned := []string{
				"<c:strRef>", "</c:strRef>",
				"<c:numRef>", "</c:numRef>",
				"<c:strCache>", "</c:strCache>",
				"<c:numCache>", "</c:numCache>",
			}
			for _, p := range banned {
				if strings.Contains(xmlText, p) {
					t.Errorf("output contains banned pattern %q (bug-fix #1)", p)
				}
			}

			assertNoFormatCodeInLit(t, xmlText)

			assertSingleIdxInDPt(t, xmlText)
		})
	}
}

func TestChartBugRegressionsInRealDeck(t *testing.T) {
	p := New()
	p.SetTitle("chart bug regressions")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	labels := []string{"A", "B", "C"}
	series := []ChartSeries{
		{Name: "S1", Labels: labels, Values: []float64{1, 2, 3}},
		{Name: "S2", Labels: labels, Values: []float64{4, 5, 6}},
	}
	for _, kind := range []ChartType{ChartBar, ChartLine, ChartArea, ChartPie, ChartDoughnut} {
		s := p.AddSlide()
		s.AddChart(kind, series, ChartOptions{X: 1, Y: 1, Width: 6, Height: 4, Title: "Test", ShowTitle: true, ShowLegend: true, ShowValues: true})
	}

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	chartParts := 0
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "ppt/charts/chart") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		chartParts++
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %s: %v", f.Name, oerr)
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			t.Fatalf("read %s: %v", f.Name, rerr)
		}
		s := string(data)
		for _, banned := range []string{"<c:strRef>", "<c:numRef>", "<c:strCache>", "<c:numCache>"} {
			if strings.Contains(s, banned) {
				t.Errorf("%s contains banned pattern %q", f.Name, banned)
			}
		}
		assertNoFormatCodeInLit(t, s)
		assertSingleIdxInDPt(t, s)
	}
	if chartParts != 5 {
		t.Errorf("expected 5 chart parts in deck, got %d", chartParts)
	}
}

func TestChartContentTypesAndRels(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	p.SetTitle("chart content types")
	p.SetAuthor("pptxgen tests")

	labels := []string{"A", "B"}
	values := []float64{1, 2}

	s1 := p.AddSlide()
	s1.AddChart(ChartBar, []ChartSeries{{Name: "S1", Labels: labels, Values: values}},
		ChartOptions{X: 1, Y: 1, Width: 5, Height: 4, Title: "C1", ShowTitle: true, ShowLegend: true})

	s2 := p.AddSlide()
	s2.AddChart(ChartLine, []ChartSeries{{Name: "S1", Labels: labels, Values: values}},
		ChartOptions{X: 1, Y: 1, Width: 5, Height: 4, Title: "C2", ShowTitle: true, ShowLegend: true})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	ct := string(findEntry(t, out, "[Content_Types].xml"))
	for _, want := range []string{
		`<Override PartName="/ppt/charts/chart1.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.chart+xml"/>`,
		`<Override PartName="/ppt/charts/chart2.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.chart+xml"/>`,
	} {
		if !strings.Contains(ct, want) {
			t.Errorf("[Content_Types].xml missing override %q\nfull:\n%s", want, ct)
		}
	}

	wantSlide1 := []string{
		`Id="rId2"`,
		`Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart"`,
		`Target="../charts/chart1.xml"`,
	}
	rels1 := string(findEntry(t, out, "ppt/slides/_rels/slide1.xml.rels"))
	for _, w := range wantSlide1 {
		if !strings.Contains(rels1, w) {
			t.Errorf("slide1 rels missing %q\nfull:\n%s", w, rels1)
		}
	}
	rels2 := string(findEntry(t, out, "ppt/slides/_rels/slide2.xml.rels"))
	if !strings.Contains(rels2, `Target="../charts/chart2.xml"`) {
		t.Errorf("slide2 rels missing chart2 target\nfull:\n%s", rels2)
	}
}

func TestChartGraphicFrameInSpTree(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddChart(ChartBar, []ChartSeries{{Name: "S", Labels: []string{"x"}, Values: []float64{1}}},
		ChartOptions{X: 1, Y: 2, Width: 5, Height: 3, ShowLegend: true})
	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	slide := string(findEntry(t, out, "ppt/slides/slide1.xml"))
	for _, want := range []string{
		`<p:graphicFrame>`,
		`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">`,
		`r:id="rId2"`,
	} {
		if !strings.Contains(slide, want) {
			t.Errorf("slide xml missing %q\nfull:\n%s", want, slide)
		}
	}
}

func TestPieFlattensExtraSeries(t *testing.T) {
	primary := ChartSeries{Name: "Mix", Labels: []string{"A", "B", "C"}, Values: []float64{1, 2, 3}}
	extra := ChartSeries{Name: "Other", Labels: []string{"A", "B", "C"}, Values: []float64{9, 9, 9}}

	for _, kind := range []ChartType{ChartPie, ChartDoughnut} {
		single := &chartElement{kind: kind, chartIdx: 1, rID: "rId2", series: []ChartSeries{primary},
			opts: ChartOptions{X: 1, Y: 1, Width: 6, Height: 4, ShowLegend: true}}
		multi := &chartElement{kind: kind, chartIdx: 1, rID: "rId2", series: []ChartSeries{primary, extra, extra},
			opts: ChartOptions{X: 1, Y: 1, Width: 6, Height: 4, ShowLegend: true}}

		var bSingle, bMulti strings.Builder
		emitChartPart(&bSingle, single)
		emitChartPart(&bMulti, multi)
		if bSingle.String() != bMulti.String() {
			t.Errorf("kind %d: extra series leaked into output\nsingle:\n%s\nmulti:\n%s", kind, bSingle.String(), bMulti.String())
		}
		if got := strings.Count(bMulti.String(), "<c:ser>"); got != 1 {
			t.Errorf("kind %d: expected 1 <c:ser>, got %d", kind, got)
		}
	}
}

func TestDoughnutHoleSize(t *testing.T) {
	mk := func(kind ChartType) string {
		c := &chartElement{kind: kind, chartIdx: 1, rID: "rId2",
			series: []ChartSeries{{Labels: []string{"A", "B"}, Values: []float64{1, 2}}},
			opts:   ChartOptions{X: 1, Y: 1, Width: 6, Height: 4}}
		var b strings.Builder
		emitChartPart(&b, c)
		return b.String()
	}

	pie := mk(ChartPie)
	if strings.Contains(pie, "<c:holeSize") {
		t.Errorf("pie chart must not emit <c:holeSize>: %s", pie)
	}
	if !strings.Contains(pie, "<c:pieChart>") {
		t.Errorf("pie chart missing <c:pieChart> wrapper: %s", pie)
	}

	doughnut := mk(ChartDoughnut)
	if !strings.Contains(doughnut, `<c:holeSize val="50"/>`) {
		t.Errorf(`doughnut chart missing <c:holeSize val="50"/>: %s`, doughnut)
	}
	if !strings.Contains(doughnut, "<c:doughnutChart>") {
		t.Errorf("doughnut chart missing <c:doughnutChart> wrapper: %s", doughnut)
	}
	openIdx := strings.Index(doughnut, "<c:doughnutChart>")
	closeIdx := strings.Index(doughnut, "</c:doughnutChart>")
	if openIdx < 0 || closeIdx < 0 || !strings.Contains(doughnut[openIdx:closeIdx], "<c:holeSize") {
		t.Errorf("holeSize must be inside <c:doughnutChart>: %s", doughnut)
	}
}

func TestPieEmptySeries(t *testing.T) {
	for _, kind := range []ChartType{ChartPie, ChartDoughnut} {
		c := &chartElement{kind: kind, chartIdx: 1, rID: "rId2",
			opts: ChartOptions{X: 1, Y: 1, Width: 6, Height: 4}}
		var b strings.Builder
		emitChartPart(&b, c)
		got := b.String()

		if strings.Contains(got, "<c:ser>") {
			t.Errorf("kind %d: empty series produced <c:ser>: %s", kind, got)
		}
		if !strings.Contains(got, `<c:firstSliceAng val="0"/>`) {
			t.Errorf("kind %d: missing firstSliceAng: %s", kind, got)
		}
		if kind == ChartDoughnut && !strings.Contains(got, `<c:holeSize val="50"/>`) {
			t.Errorf("doughnut: missing holeSize: %s", got)
		}
		assertNoFormatCodeInLit(t, got)
		assertSingleIdxInDPt(t, got)
	}
}

func TestPieSingleSlice(t *testing.T) {
	for _, kind := range []ChartType{ChartPie, ChartDoughnut} {
		c := &chartElement{kind: kind, chartIdx: 1, rID: "rId2",
			series: []ChartSeries{{Name: "Total", Labels: []string{"Done"}, Values: []float64{100}}},
			opts:   ChartOptions{X: 1, Y: 1, Width: 6, Height: 4, ShowLegend: true}}
		var b strings.Builder
		emitChartPart(&b, c)
		got := b.String()

		if strings.Count(got, "<c:ser>") != 1 {
			t.Errorf("kind %d: expected 1 <c:ser>, got %d: %s", kind, strings.Count(got, "<c:ser>"), got)
		}
		if strings.Count(got, "<c:dPt>") != 1 {
			t.Errorf("kind %d: expected 1 <c:dPt> for the single slice, got %d", kind, strings.Count(got, "<c:dPt>"))
		}
		if !strings.Contains(got, `<c:pt idx="0"><c:v>100</c:v></c:pt>`) {
			t.Errorf("kind %d: missing 100 value: %s", kind, got)
		}
		assertNoFormatCodeInLit(t, got)
		assertSingleIdxInDPt(t, got)
	}
}

func TestPieDPtCountMatchesValues(t *testing.T) {
	c := &chartElement{kind: ChartPie, chartIdx: 1, rID: "rId2",
		series: []ChartSeries{{
			Labels: []string{"A", "B", "C", "D", "E"},
			Values: []float64{1, 2, 3},
		}},
		opts: ChartOptions{X: 1, Y: 1, Width: 6, Height: 4}}
	var b strings.Builder
	emitChartPart(&b, c)
	got := b.String()

	if g := strings.Count(got, "<c:dPt>"); g != 3 {
		t.Errorf("expected 3 dPt entries (one per value), got %d: %s", g, got)
	}
}

func TestPieDoughnutNoAxes(t *testing.T) {
	for _, kind := range []ChartType{ChartPie, ChartDoughnut} {
		c := &chartElement{kind: kind, chartIdx: 1, rID: "rId2",
			series: []ChartSeries{{Labels: []string{"A", "B"}, Values: []float64{1, 2}}},
			opts:   ChartOptions{X: 1, Y: 1, Width: 6, Height: 4}}
		var b strings.Builder
		emitChartPart(&b, c)
		got := b.String()
		for _, banned := range []string{"<c:catAx>", "<c:valAx>"} {
			if strings.Contains(got, banned) {
				t.Errorf("kind %d output contains %q (pie/doughnut have no axes)", kind, banned)
			}
		}
	}
}

func TestEmitDPtSingleIdxAndSpPr(t *testing.T) {
	var b strings.Builder
	emitDPt(&b, 0, "FF0000")
	emitDPt(&b, 1, "00FF00")
	emitDPt(&b, 2, "")
	xmlText := b.String()
	if strings.Count(xmlText, "<c:dPt>") != 3 {
		t.Fatalf("expected 3 dPt elements, got: %s", xmlText)
	}
	assertSingleIdxInDPt(t, xmlText)
	if !strings.Contains(xmlText, `<c:idx val="0"/>`) ||
		!strings.Contains(xmlText, `<c:idx val="1"/>`) ||
		!strings.Contains(xmlText, `<c:idx val="2"/>`) {
		t.Errorf("missing expected idx values: %s", xmlText)
	}
	if got := strings.Count(xmlText, "<c:spPr>"); got != 2 {
		t.Errorf("expected 2 spPr blocks (one per coloured dPt), got %d in: %s", got, xmlText)
	}
}

func assertNoFormatCodeInLit(t *testing.T, xmlText string) {
	t.Helper()
	for _, tag := range []string{"numLit", "strLit"} {
		open := "<c:" + tag + ">"
		closeTag := "</c:" + tag + ">"
		rest := xmlText
		for {
			i := strings.Index(rest, open)
			if i < 0 {
				break
			}
			rest = rest[i+len(open):]
			j := strings.Index(rest, closeTag)
			if j < 0 {
				t.Errorf("unclosed <c:%s>", tag)
				break
			}
			block := rest[:j]
			if strings.Contains(block, "<c:formatCode>") {
				t.Errorf("<c:formatCode> inside <c:%s>: %q", tag, block)
			}
			rest = rest[j+len(closeTag):]
		}
	}
}

func assertSingleIdxInDPt(t *testing.T, xmlText string) {
	t.Helper()
	open := "<c:dPt>"
	closeTag := "</c:dPt>"
	rest := xmlText
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			return
		}
		rest = rest[i+len(open):]
		j := strings.Index(rest, closeTag)
		if j < 0 {
			t.Errorf("unclosed <c:dPt>")
			return
		}
		block := rest[:j]
		if got := strings.Count(block, "<c:idx "); got != 1 {
			t.Errorf("c:dPt contains %d <c:idx> elements (want 1): %q", got, block)
		}
		if got := strings.Count(block, "<c:spPr>"); got > 1 {
			t.Errorf("c:dPt contains %d <c:spPr> elements (want at most 1): %q", got, block)
		}
		rest = rest[j+len(closeTag):]
	}
}
