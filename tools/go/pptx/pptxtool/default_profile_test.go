// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestDefaultProfileCarriesNoBrand(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   map[string]any
		wantName string
		wantLogo bool
	}{
		{"unset", nil, "Neutral", false},
		{"empty string", map[string]any{"template_profile": ""}, "Neutral", false},
		{"solace opt-in", map[string]any{"template_profile": "solace_2025"}, "Solace 2025", true},
		{"none", map[string]any{"template_profile": "none"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tctx := &sdk.ToolContext{}
			tctx.SetConfigForTest(tc.config)
			fromTool, err := loadProfileFromConfig(tctx)
			if err != nil {
				t.Fatalf("tool context: %v", err)
			}
			for via, p := range map[string]*Profile{
				"tool context": fromTool,
				"config map":   loadProfileFromConfigMap(tc.config),
			} {
				if tc.wantName == "" {
					if p != nil {
						t.Errorf("%s: got profile %q, want none", via, p.Name)
					}
					continue
				}
				if p == nil || p.Name != tc.wantName {
					t.Fatalf("%s: got %v, want profile %q", via, p, tc.wantName)
				}
				decorated := false
				for _, c := range p.Categories {
					decorated = decorated || len(c.Decorations) > 0
				}
				if got := len(p.Assets) > 0 || decorated; got != tc.wantLogo {
					t.Errorf("%s: %s has assets or decorations = %v, want %v", via, p.Name, got, tc.wantLogo)
				}
			}
		})
	}
}
