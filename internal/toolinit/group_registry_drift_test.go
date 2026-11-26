// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestGroupRuntimeToolsAreAllDescribed(t *testing.T) {
	knownGaps := map[string]bool{
		"builder_tools/request_build_activate":          true,
		"builtin_document_tools/html_to_pdf":            true,
		"data_analysis/reinterpret_artifact":            true,
		"data_analysis/create_chart_from_plotly_config": true,
	}

	for _, group := range slices.Sorted(maps.Keys(schema.ValidBuiltinGroups)) {
		entry := schema.ValidBuiltinGroups[group]
		if strings.Contains(entry.Tools, "alias for") {
			continue
		}
		for _, name := range LookupBuiltinGroup(group, Options{}) {
			if knownGaps[group+"/"+name] {
				continue
			}
			bare := name
			if _, after, found := strings.Cut(name, "__"); found {
				bare = after
			}
			if !strings.Contains(entry.Tools, name) && !strings.Contains(entry.Tools, bare) {
				t.Errorf("group %q gives agents %q, but its registry prose never names it — "+
					"the builder will advertise this toolset as something it is not:\n  %s",
					group, name, entry.Tools)
			}
		}
	}
}

func TestGroupMembersAreRealTools(t *testing.T) {
	factories := map[string]bool{}
	for name := range builtinToolFactories {
		factories[name] = true
	}
	strTools := strToolNames(t)

	for _, group := range slices.Sorted(maps.Keys(schema.ValidBuiltinGroups)) {
		for _, name := range LookupBuiltinGroup(group, Options{}) {
			t.Run(group+"/"+name, func(t *testing.T) {
				if skill, bare, ok := strings.Cut(name, "__"); ok {
					assertSkillToolDeclared(t, skill, bare)
					return
				}
				if factories[name] || strTools[name] {
					return
				}
				t.Errorf("group %q gives agents %q, which is neither an in-process factory nor a name "+
					"any STR tool package registers — a call to it can never find a worker, and it "+
					"stalls on init_request retries rather than failing", group, name)
			})
		}
	}
}

func TestPDFGroupsCarryTheGoHostedRoute(t *testing.T) {
	for _, group := range append(slices.Sorted(maps.Keys(schema.ValidBuiltinGroups)), switchOnlyGroups...) {
		members := LookupBuiltinGroup(group, Options{})
		if !slices.Contains(members, "convert_pdf_to_markdown") {
			continue
		}
		if !slices.Contains(members, "pdf_extract_text") {
			t.Errorf("group %q offers convert_pdf_to_markdown with no Go-hosted PDF route beside it: %v — "+
				"in embedded and desktop that group advertises a PDF tool no worker provides, and nothing "+
				"else in it can read one", group, members)
		}
	}
}

var switchOnlyGroups = []string{"notification_tools"}

func TestSwitchOnlyGroupsAreListed(t *testing.T) {
	for _, group := range switchOnlyGroups {
		if LookupBuiltinGroup(group, Options{}) == nil {
			t.Errorf("switchOnlyGroups names %q, which LookupBuiltinGroup no longer resolves", group)
		}
		if _, inCatalog := schema.ValidBuiltinGroups[group]; inCatalog {
			t.Errorf("%q has a catalog entry now, so listing it here walks it twice", group)
		}
	}
}
