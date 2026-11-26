// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestCreatePresentationSchemaRequiresNoThemeField(t *testing.T) {
	props := sdk.BuildSchema[CreatePresentationParams]().Properties
	theme, ok := props["theme"].(map[string]any)
	if !ok {
		t.Fatalf("theme property = %#v, want an object schema", props["theme"])
	}
	if required, found := theme["required"]; found {
		t.Errorf("theme marks %v required; the model is told to send only the fields it changes", required)
	}
	themeProps, ok := theme["properties"].(map[string]any)
	if !ok {
		t.Fatalf("theme properties = %#v, want a map", theme["properties"])
	}
	colors, ok := themeProps["colors"].(map[string]any)
	if !ok {
		t.Fatalf("theme.colors = %#v, want an object schema", themeProps["colors"])
	}
	if required, found := colors["required"]; found {
		t.Errorf("theme.colors marks %v required; the model is told to send only the colours it changes", required)
	}
}

func TestPartialThemeOverrideKeepsTheBaseForEveryOmittedField(t *testing.T) {
	base := defaultTheme()
	override := &ThemeOverride{
		BodyFont: new("Arial"),
		Colors:   &ThemeColorsOverride{Accent1: new("#1F4E79")},
	}

	got := mergeTheme(base, *override.overlay())

	want := base
	want.BodyFont = "Arial"
	want.Colors.Accent1 = "1F4E79"
	if got != want {
		t.Errorf("merged theme = %+v, want %+v", got, want)
	}
}

func TestAnOmittedThemeOverridesNothing(t *testing.T) {
	var override *ThemeOverride
	if got := override.overlay(); got != nil {
		t.Errorf("overlay of an omitted theme = %+v, want nil", got)
	}
}
