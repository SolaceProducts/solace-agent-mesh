// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

var validLayouts = map[string]bool{
	"title": true, "section": true, "text": true, "bullet_list": true,
	"two_column": true, "three_column": true, "comparison": true,
	"image_left": true, "image_right": true, "image_center": true, "image_full": true,
	"table": true, "chart": true, "quote": true,
	"big_number": true, "stat": true,
	"timeline": true,
	"process":  true, "flow": true, "steps": true,
	"callout": true, "banner": true,
	"icon_grid": true, "features": true,
	"closing": true, "blank": true,
}

var validChartTypes = map[string]bool{
	"bar": true, "line": true, "pie": true, "doughnut": true, "area": true,
}

func validateDeck(slides []LayoutSlide, theme *Theme) []string {
	var problems []string
	problems = append(problems, validateThemeColors(theme)...)
	for i, sl := range slides {
		problems = append(problems, sorted(validateSlide(i+1, sl))...)
	}
	return problems
}

func validateSlide(number int, sl LayoutSlide) []string {
	var problems []string

	layout := strings.ToLower(strings.TrimSpace(sl.Layout))
	switch {
	case layout == "":
		problems = append(problems, fmt.Sprintf(
			"slide %d: missing 'layout'; expected one of %s", number, knownLayouts()))
	case !validLayouts[layout]:
		problems = append(problems, fmt.Sprintf(
			"slide %d: unknown layout %q; expected one of %s", number, sl.Layout, knownLayouts()))
	}

	if sl.Background != nil {
		if _, err := pptxgen.NormalizeColorHex(*sl.Background); err != nil {
			problems = append(problems, fmt.Sprintf("slide %d: background %v", number, err))
		}
	}

	if sl.ChartData != nil && sl.ChartData.ChartType != "" && !validChartTypes[sl.ChartData.ChartType] {
		problems = append(problems, fmt.Sprintf(
			"slide %d: unknown chart_type %q; expected one of %s",
			number, sl.ChartData.ChartType, knownChartTypes()))
	}
	return problems
}

func validateThemeColors(theme *Theme) []string {
	if theme == nil {
		return nil
	}
	slots := []struct {
		name  string
		value string
	}{
		{"background1", theme.Colors.Background1},
		{"background2", theme.Colors.Background2},
		{"text1", theme.Colors.Text1},
		{"text2", theme.Colors.Text2},
		{"accent1", theme.Colors.Accent1},
		{"accent2", theme.Colors.Accent2},
		{"accent3", theme.Colors.Accent3},
		{"accent4", theme.Colors.Accent4},
		{"accent5", theme.Colors.Accent5},
		{"accent6", theme.Colors.Accent6},
	}
	var problems []string
	for _, slot := range slots {
		if _, err := pptxgen.NormalizeColorHex(slot.value); err != nil {
			problems = append(problems, fmt.Sprintf("theme.colors.%s: %v", slot.name, err))
		}
	}
	return problems
}

func knownLayouts() string { return sortedNames(validLayouts) }

func LayoutNames() []string { return slices.Sorted(maps.Keys(validLayouts)) }

func ChartTypeNames() []string { return slices.Sorted(maps.Keys(validChartTypes)) }

func knownChartTypes() string { return sortedNames(validChartTypes) }

func sortedNames(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func sorted(s []string) []string {
	sort.Strings(s)
	return s
}
