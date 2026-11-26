// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import "testing"

func TestCardStyleToggles(t *testing.T) {
	base := func() *RenderContext {
		return &RenderContext{Theme: defaultTheme(), BgColor: "FFFFFF", Profile: &Profile{}}
	}

	t.Run("defaults when unset", func(t *testing.T) {
		rc := base()
		if rc.cardShadow() == nil {
			t.Error("shadow should default on when unset")
		}
		if rc.cardRadius() != defaultCardRadiusIn {
			t.Errorf("radius default = %v, want %v", rc.cardRadius(), defaultCardRadiusIn)
		}
		if c, w := rc.cardBorder(); c == "" || w == 0 {
			t.Error("border should default on for a light slide")
		}
	})

	t.Run("shadow off", func(t *testing.T) {
		rc := base()
		rc.Profile.CardStyle.Shadow = new(false)
		if rc.cardShadow() != nil {
			t.Error("shadow should be nil when disabled")
		}
	})

	t.Run("square corners", func(t *testing.T) {
		rc := base()
		rc.Profile.CardStyle.CornerRadiusIn = new(0.0)
		if rc.cardRadius() != 0 {
			t.Errorf("radius = %v, want 0 (square)", rc.cardRadius())
		}
	})

	t.Run("border off", func(t *testing.T) {
		rc := base()
		rc.Profile.CardStyle.Border = new(false)
		if c, w := rc.cardBorder(); c != "" || w != 0 {
			t.Errorf("border should be off, got %q/%v", c, w)
		}
	})

	t.Run("dark slide has no border", func(t *testing.T) {
		rc := base()
		rc.BgColor = "0A1622"
		if c, _ := rc.cardBorder(); c != "" {
			t.Error("dark slide should not draw a border (fill contrast separates)")
		}
	})
}

func TestDarkenClamp(t *testing.T) {
	if got := darken("808080", 0.5); got != "404040" {
		t.Errorf("darken(50%%) = %s, want 404040", got)
	}
	if got := darken("808080", -0.5); got != "C0C0C0" {
		t.Errorf("lighten(50%%) = %s, want C0C0C0", got)
	}
	if got := darken("FFFFFF", 0); got != "FFFFFF" {
		t.Errorf("darken(0) = %s, want FFFFFF", got)
	}
	if got := darken("zzz", 0.5); got != "zzz" {
		t.Error("malformed input should pass through unchanged")
	}
}

func TestSolaceProfileFlatCards(t *testing.T) {
	p, err := loadBuiltinProfile("solace_2025")
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	if p.CardStyle.Shadow == nil || *p.CardStyle.Shadow {
		t.Error("Solace profile should disable card shadow")
	}
	if p.CardStyle.CornerRadiusIn == nil || *p.CardStyle.CornerRadiusIn != 0 {
		t.Error("Solace profile should set square (0) corners")
	}
}
