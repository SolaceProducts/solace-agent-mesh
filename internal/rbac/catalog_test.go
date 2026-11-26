// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"slices"
	"strings"
	"testing"
)

func TestCatalogCompleteness(t *testing.T) {
	consts := parseScopeCatalog(t, scopeSourceFiles()...)
	if len(consts) == 0 {
		t.Fatal("parsed 0 scope constants — parser likely broke")
	}

	catalog := Catalog()
	inCatalog := make(map[string]bool, len(catalog))
	for _, e := range catalog {
		if inCatalog[e.Scope] {
			t.Errorf("scope %q appears more than once in Catalog()", e.Scope)
		}
		inCatalog[e.Scope] = true
	}

	declared := make(map[string]bool, len(consts))
	for _, c := range consts {
		declared[c.value] = true
		category, _, _ := strings.Cut(c.value, ":")
		if category == "tool" || unassignableScopes[c.value] {
			if inCatalog[c.value] {
				t.Errorf("scope %q (%s) must be excluded from the assignable catalog", c.value, c.name)
			}
			continue
		}
		if !inCatalog[c.value] {
			t.Errorf("scope %q (%s) is assignable but missing from Catalog() — add a scopeMeta entry in catalog.go", c.value, c.name)
		}
	}

	for _, e := range catalog {
		if !declared[e.Scope] {
			t.Errorf("Catalog() entry %q is not a declared Scope* constant — remove it or add the constant to scopes.go", e.Scope)
		}
	}

	listed := DeclaredScopes()
	if len(listed) != len(declared) {
		t.Errorf("DeclaredScopes() has %d entries, scopes.go declares %d distinct values", len(listed), len(declared))
	}
	for _, scope := range listed {
		if !declared[scope] {
			t.Errorf("DeclaredScopes() entry %q is not a declared Scope* constant", scope)
		}
	}
	for _, c := range consts {
		if !slices.Contains(listed, c.value) {
			t.Errorf("scope %q (%s) is missing from DeclaredScopes() — add `tool` constants to toolScopes in catalog.go", c.value, c.name)
		}
	}
}

func TestCatalogEntriesWellFormed(t *testing.T) {
	for _, e := range Catalog() {
		t.Run(e.Scope, func(t *testing.T) {
			segs := strings.Split(e.Scope, ":")
			if len(segs) != 3 || segs[0] == "" || segs[1] == "" || segs[2] == "" {
				t.Fatalf("scope %q is not a 3-segment scope", e.Scope)
			}
			if segs[0] == "tool" {
				t.Errorf("tool scope %q must not be in the catalog", e.Scope)
			}
			if strings.TrimSpace(e.Name) == "" {
				t.Errorf("scope %q has empty name", e.Scope)
			}
			if strings.TrimSpace(e.Description) == "" {
				t.Errorf("scope %q has empty description", e.Scope)
			}
		})
	}
}

func TestFeedbackScopeDescriptionsAreGA(t *testing.T) {
	descriptions := map[string]string{}
	for _, e := range Catalog() {
		descriptions[e.Scope] = e.Description
	}

	tests := []struct {
		scope    string
		mustKeep []string
	}{
		{ScopeAgentReadFeedbackAny, []string{
			"View thumbs feedback on tasks handled by any agent.",
		}},
		{ScopeWorkflowReadFeedbackAny, []string{
			"View thumbs feedback on tasks handled by any workflow.",
		}},
		{ScopeAgentReadTraceAny, []string{
			"Open the full contents of task traces, including prompts, tool inputs and outputs, for every agent.",
			"A trace opens only when every agent it ran is covered.",
			"Also lists every agent's top-level tasks, whoever ran them, including runs attributed to service accounts, and opens them.",
			"With evidence:*:read, the agent:*:read_trace grant also opens the evidence store's plain-language explanations of findings and the evaluation sets drafted for them, which a model writes from traces of any agent.",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.scope, func(t *testing.T) {
			got, ok := descriptions[tc.scope]
			if !ok {
				t.Fatalf("scope %q is missing from the catalog", tc.scope)
			}
			for _, caveat := range []string{"Experimental", "still in development", "off by default"} {
				if strings.Contains(got, caveat) {
					t.Errorf("description still carries the pre-GA caveat %q: %s", caveat, got)
				}
			}
			for _, sentence := range tc.mustKeep {
				if !strings.Contains(got, sentence) {
					t.Errorf("description lost its substantive guidance %q: %s", sentence, got)
				}
			}
		})
	}
}
