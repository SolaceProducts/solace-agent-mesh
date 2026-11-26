// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func deck(t *testing.T, spec string) *sdk.Result {
	t.Helper()
	res, err := createPresentation(context.Background(), CreatePresentationParams{
		Title:  "T",
		Slides: sdk.Artifact{Filename: "deck.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createPresentation returned a transport error: %v", err)
	}
	return res
}

func TestCreatePresentation_RejectsUnknownLayout(t *testing.T) {
	res := deck(t, "- layout: twocolumn\n  title: Typo")
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error; message: %s", res.Status, res.Message)
	}
	for _, want := range []string{"unknown layout", "twocolumn", "two_column"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q should mention %q so the model can correct the spec", res.Message, want)
		}
	}
}

func TestCreatePresentation_RejectsMissingLayout(t *testing.T) {
	res := deck(t, "- title: No layout")
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "missing 'layout'") {
		t.Errorf("message %q should name the missing field", res.Message)
	}
}

func TestCreatePresentation_RejectsNonHexColours(t *testing.T) {
	res := deck(t, "- layout: text\n  title: T\n  background: red")
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "not a colour name") {
		t.Errorf("message %q should explain that a hex value is required", res.Message)
	}
}

func TestCreatePresentation_RejectsBadThemeColour(t *testing.T) {
	res, err := createPresentation(context.Background(), CreatePresentationParams{
		Title:  "T",
		Slides: sdk.Artifact{Filename: "deck.yaml", Content: []byte("- layout: text\n  title: T")},
		Theme:  &ThemeOverride{Colors: &ThemeColorsOverride{Accent1: new("#12345")}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createPresentation: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "theme.colors.accent1") {
		t.Errorf("message %q should name the offending slot", res.Message)
	}
}

func TestCreatePresentation_AcceptsEverythingThatWorkedBefore(t *testing.T) {
	for _, spec := range []string{
		"- layout: TEXT\n  title: Upper",
		"- layout: '  text  '\n  title: Padded",
		"- layout: text\n  title: T\n  background: '#C0392B'",
		"- layout: text\n  title: T\n  background: abc",
	} {
		if res := deck(t, spec); res.Status != sdk.StatusSuccess {
			t.Errorf("spec %q: status = %q, want success; message: %s", spec, res.Status, res.Message)
		}
	}

	for layout := range validLayouts {
		spec := "- layout: " + layout + "\n  title: T"
		if res := deck(t, spec); res.Status != sdk.StatusSuccess {
			t.Errorf("layout %q: status = %q, want success; message: %s", layout, res.Status, res.Message)
		}
	}
}

func TestCreatePresentation_RejectsUnknownChartType(t *testing.T) {
	for _, bad := range []string{"bars", "Bar", "donut"} {
		spec := "- layout: chart\n  title: T\n  chart_data:\n    chart_type: " + bad +
			"\n    series: [{name: s, values: [1]}]"
		res := deck(t, spec)
		if res.Status != sdk.StatusError {
			t.Errorf("chart_type %q: status = %q, want error; message: %s", bad, res.Status, res.Message)
			continue
		}
		if !strings.Contains(res.Message, "unknown chart_type") {
			t.Errorf("chart_type %q: message %q should name the field", bad, res.Message)
		}
	}

	for _, good := range []string{"bar", "line", "pie", "doughnut", "area"} {
		spec := "- layout: chart\n  title: T\n  chart_data:\n    chart_type: " + good +
			"\n    series: [{name: s, values: [1]}]"
		if res := deck(t, spec); res.Status != sdk.StatusSuccess {
			t.Errorf("chart_type %q: status = %q, want success; message: %s", good, res.Status, res.Message)
		}
	}
}

func TestCreatePresentation_ReportsThemeAndSlideProblemsTogether(t *testing.T) {
	res, err := createPresentation(context.Background(), CreatePresentationParams{
		Title:  "T",
		Slides: sdk.Artifact{Filename: "deck.yaml", Content: []byte("- layout: bogus\n  title: A")},
		Theme:  &ThemeOverride{Colors: &ThemeColorsOverride{Accent1: new("red")}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createPresentation: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	for _, want := range []string{"theme.colors.accent1", "slide 1", "bogus"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message should mention %q — both tiers belong in one report:\n%s", want, res.Message)
		}
	}
}

func TestCreatePresentation_ReportsEveryProblemAtOnce(t *testing.T) {
	res := deck(t, "- layout: bogus\n  title: A\n- layout: text\n  title: B\n  background: red")
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	for _, want := range []string{"slide 1", "slide 2", "bogus", "red"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message should mention %q:\n%s", want, res.Message)
		}
	}
}

func switchCaseStrings(t *testing.T, filename, funcName string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == funcName {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatalf("%s not found in %s — this guard needs updating, not the map", funcName, filename)
	}

	labels := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote case label %s: %v", lit.Value, err)
			}
			labels[value] = true
		}
		return true
	})
	if len(labels) == 0 {
		t.Fatalf("parsed no case labels from %s — the guard is broken, not the map", funcName)
	}
	return labels
}

func TestValidLayouts_MatchesRenderSlideSwitch(t *testing.T) {
	assertSetMatchesSwitch(t, validLayouts,
		switchCaseStrings(t, "presentation.go", "renderSlide"),
		"validLayouts", "renderSlide",
		"it would silently render as a text slide")
}

func TestValidChartTypes_MatchesRenderChartSwitch(t *testing.T) {
	inSwitch := switchCaseStrings(t, "presentation.go", "renderChart")
	inSwitch["bar"] = true
	assertSetMatchesSwitch(t, validChartTypes, inSwitch,
		"validChartTypes", "renderChart",
		"it would silently render as a bar chart")
}

func assertSetMatchesSwitch(t *testing.T, allowed, inSwitch map[string]bool, setName, funcName, consequence string) {
	t.Helper()
	for name := range inSwitch {
		if !allowed[name] {
			t.Errorf("%s handles %q but %s omits it — decks using it are now rejected", funcName, name, setName)
		}
	}
	for name := range allowed {
		if !inSwitch[name] {
			t.Errorf("%s allows %q but %s has no case — %s", setName, name, funcName, consequence)
		}
	}
}
