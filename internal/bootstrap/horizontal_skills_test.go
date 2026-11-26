// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func TestHorizontalSkillRefs(t *testing.T) {
	names := func(reg *features.Registry) []string {
		var out []string
		for _, r := range horizontalSkillRefs(reg) {
			out = append(out, r.Name)
		}
		return out
	}

	regWith := func(templatesDefault bool) *features.Registry {
		r := features.NewRegistry()
		r.Register(features.Definition{Key: samTemplatesFeatureKey, Default: templatesDefault})
		return r
	}

	t.Run("flag off omits sam-templates", func(t *testing.T) {
		got := names(regWith(false))
		if slices.Contains(got, "sam-templates") {
			t.Errorf("flag off should not auto-inject sam-templates: %v", got)
		}
		for _, want := range []string{"sam-build-redirect", "sam-scheduling"} {
			if !slices.Contains(got, want) {
				t.Errorf("missing always-on horizontal skill %q: %v", want, got)
			}
		}
	})

	t.Run("flag on adds sam-templates", func(t *testing.T) {
		got := names(regWith(true))
		if !slices.Contains(got, "sam-templates") {
			t.Errorf("flag on should auto-inject sam-templates: %v", got)
		}
	})

	t.Run("nil registry omits sam-templates", func(t *testing.T) {
		got := names(nil)
		if slices.Contains(got, "sam-templates") {
			t.Errorf("nil registry should not auto-inject sam-templates: %v", got)
		}
		if len(got) != 2 {
			t.Errorf("nil registry should still add the always-on horizontal skills: %v", got)
		}
	})
}

func TestEveryAgentGetsRedirectAndSchedulingOnce(t *testing.T) {
	names := func(refs []agent.SkillRef) []string {
		var out []string
		for _, r := range refs {
			out = append(out, r.Name)
		}
		return out
	}

	specialist := withHorizontalSkills([]agent.SkillRef{{Name: "dates"}}, nil)
	if want := []string{"dates", "sam-build-redirect", "sam-scheduling"}; !slices.Equal(names(specialist), want) {
		t.Errorf("specialist skills = %v, want %v", names(specialist), want)
	}

	orchestrator := withHorizontalSkills([]agent.SkillRef{{Name: "sam-scheduling"}}, nil)
	if want := []string{"sam-scheduling", "sam-build-redirect"}; !slices.Equal(names(orchestrator), want) {
		t.Errorf("Orchestrator skills = %v, want %v", names(orchestrator), want)
	}
}
