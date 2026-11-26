// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
)

func TestResolveAgents_IncludeTagRejectedNamingFile(t *testing.T) {
	cases := map[string]string{
		"inline form":   "kind: agent\nname: helper\nspec:\n  systemPrompt: !include prompts/x.md\n",
		"own-line form": "kind: agent\nname: helper\nspec:\n  systemPrompt:\n    !include prompts/x.md\n",
		"in a sequence": "kind: agent\nname: helper\nspec:\n  tags:\n    - !include prompts/x.md\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeRepo(t, map[string]string{
				"agents/helper.yaml": body,
				"prompts/x.md":       "You are a helpful agent.",
			})
			m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"agents": {{Name: "helper"}}}}
			got, err := ResolveAgents(root, m)
			if err == nil {
				t.Fatalf("resolved with systemPrompt %v; want an error rather than a deployed literal path", got[0].Spec)
			}
			for _, want := range []string{filepath.Join("agents", "helper.yaml"), "!include"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestResolveAgents_QuotedIncludeTextIsNotATag(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"agents/helper.yaml": "kind: agent\nname: helper\nspec:\n  systemPrompt: \"!include prompts/x.md\"\n  note: |\n    !include is documented elsewhere\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"agents": {{Name: "helper"}}}}
	got, err := ResolveAgents(root, m)
	if err != nil {
		t.Fatalf("ResolveAgents: %v", err)
	}
	if got[0].Spec["systemPrompt"] != "!include prompts/x.md" {
		t.Errorf("systemPrompt = %v", got[0].Spec["systemPrompt"])
	}
}

func TestResolveToolsets_IncludeTagRejectedNamingFile(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/ts.yaml":   "kind: toolset\nname: ts\nspec:\n  config:\n    token: !include secrets/token.txt\n",
		"toolsets/ts/ts.zip": "zip",
		"secrets/token.txt":  "abc",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"toolsets": {{Name: "ts"}}}}
	_, err := New(root, m, Options{}).Toolsets(context.Background())
	if err == nil {
		t.Fatal("expected error for !include in a toolset file")
	}
	for _, want := range []string{filepath.Join("toolsets", "ts.yaml"), "!include"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveSkills_IncludeTagInFrontmatterRejectedNamingFile(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/web/SKILL.md": "---\nname: web\ndescription: !include desc.txt\n---\n\nbody\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"skills": {{Name: "web"}}}}
	_, err := ResolveSkills(root, m)
	if err == nil {
		t.Fatal("expected error for !include in SKILL.md frontmatter")
	}
	for _, want := range []string{filepath.Join("skills", "web", "SKILL.md"), "!include"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
