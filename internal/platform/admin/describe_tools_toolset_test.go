// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (f *describeFixture) seedPackage(t *testing.T, name, status string, toolParameters map[string]string) {
	t.Helper()
	pkg := &store.ToolPackage{Name: name, DiscoveryStatus: status, CreatedBy: "fixture", UpdatedBy: "fixture"}
	if err := f.packages.Create(t.Context(), pkg); err != nil {
		t.Fatalf("seed package %q: %v", name, err)
	}
	for toolName, parameters := range toolParameters {
		description := toolName + " does the work."
		file := &store.ToolPackageFile{PackageID: pkg.ID, ToolName: toolName, ToolDescription: &description, TimeoutSeconds: 30, SandboxProfile: "standard"}
		if parameters != "" {
			file.Parameters = &parameters
		}
		if err := f.packages.UpsertToolFile(t.Context(), file); err != nil {
			t.Fatalf("seed tool %q of package %q: %v", toolName, name, err)
		}
	}
}

func TestToolsDescribe_AReadyPackageToolComesFromItsDiscoveredParameters(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme-tools", "ready", map[string]string{
		"lookup_order": `{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`,
		"no_schema":    "",
	})

	got := f.one(t, toolRef("acme-tools__lookup_order"))
	if got.Status != describeStatusOK || got.Kind != describeKindToolset || got.Name != "acme-tools__lookup_order" {
		t.Fatalf("got %+v, want an ok toolset tool", got)
	}
	if props := schemaProperties(t, got.Parameters); !slices.Equal(props, []string{"order_id"}) {
		t.Errorf("parameters = %v, want the discovered ones", props)
	}
	if got.Description != "lookup_order does the work." || !got.ToolNode.Usable {
		t.Errorf("description = %q, toolNode = %+v", got.Description, got.ToolNode)
	}
	if !isNullSchema(got.Result) || !slices.Equal(got.Notes, []string{reasonResultUndeclared}) {
		t.Errorf("result = %s, notes = %v, want a null result with its note", got.Result, got.Notes)
	}

	if bare := f.one(t, toolRef("acme-tools__no_schema")); bare.Status != describeStatusOK || !isNullSchema(bare.Parameters) {
		t.Errorf("a tool discovered without parameters: got %+v, want ok with null parameters", bare)
	}
}

func TestToolsDescribe_APackageThatIsNotReadyIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "fresh-pkg", "created", map[string]string{"run": ""})
	f.seedPackage(t, "pending-pkg", "pending", map[string]string{"run": ""})
	f.seedPackage(t, "broken-pkg", "failed", map[string]string{"run": ""})

	cases := map[string]string{
		"fresh-pkg__run":   reasonPackageDiscovering,
		"pending-pkg__run": reasonPackageDiscovering,
		"broken-pkg__run":  reasonPackageFailed,
	}
	for name, wantReason := range cases {
		got := f.one(t, toolRef(name))
		if got.Status != describeStatusUnavailable || got.Reason != wantReason || got.ToolNode.Usable || got.Kind != describeKindToolset {
			t.Errorf("%s: got %+v, want unavailable with %q", name, got, wantReason)
		}
		if !isNullSchema(got.Parameters) {
			t.Errorf("%s: parameters = %s, want none before discovery finishes", name, got.Parameters)
		}
	}
}

func TestToolsDescribe_AToolTheReadyPackageDoesNotHaveListsWhatItDoes(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme-tools", "ready", map[string]string{"lookup_order": "", "cancel_order": ""})

	got := f.one(t, toolRef("acme-tools__refund_order"))
	if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound {
		t.Fatalf("got %+v, want not_found", got)
	}
	if want := []string{"acme-tools__cancel_order", "acme-tools__lookup_order"}; !slices.Equal(got.Suggestions, want) {
		t.Errorf("suggestions = %v, want the package's tools %v", got.Suggestions, want)
	}
}

func TestToolsDescribe_ANameWhosePackageDoesNotExistFallsThrough(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedToolRow(t, "ghost__run", "A tool whose package is gone.", pathParamsSchema)

	if got := f.one(t, toolRef("ghost__run")); got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound {
		t.Errorf("got %+v, want not_found: a tools row for a tool no agent can bind does not make it one", got)
	}
	if got := f.one(t, toolRef("nobody__run")); got.Status != describeStatusNotFound {
		t.Errorf("got %+v, want not_found when neither a package nor a row exists", got)
	}
}

func TestToolsDescribe_APackageAnswersBeforeAToolsRowOfTheSameName(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme", "pending", nil)
	f.seedToolRow(t, "acme__run", "Announced while discovery was still running.", pathParamsSchema)

	got := f.one(t, toolRef("acme__run"))
	if got.Status != describeStatusUnavailable || got.Reason != reasonPackageDiscovering || got.Kind != describeKindToolset {
		t.Fatalf("got %+v, want unavailable with %q: a package's discovery state outranks the row OnToolInit wrote", got, reasonPackageDiscovering)
	}
}

func TestToolsDescribe_APackageInAnyOtherStatusIsStillDiscovering(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "odd-pkg", "reindexing", map[string]string{"run": ""})
	f.seedToolRow(t, "odd-pkg__run", "A row that must not answer.", pathParamsSchema)

	got := f.one(t, toolRef("odd-pkg__run"))
	if got.Status != describeStatusUnavailable || got.Reason != reasonPackageDiscovering || got.ToolNode.Usable {
		t.Fatalf("got %+v, want unavailable with %q for a status that is neither ready nor failed", got, reasonPackageDiscovering)
	}
}

func TestToolsDescribe_PackageNotFoundSuggestionsAreCapped(t *testing.T) {
	f := newDescribeFixture(t)
	tools := map[string]string{}
	for i := range describeMaxListedTools + 10 {
		tools[fmt.Sprintf("tool_%03d", i)] = ""
	}
	f.seedPackage(t, "big-pkg", "ready", tools)

	got := f.one(t, toolRef("big-pkg__no_such_tool"))
	if got.Status != describeStatusNotFound || len(got.Suggestions) != describeMaxListedTools {
		t.Errorf("got status %q with %d suggestions, want not_found with %d", got.Status, len(got.Suggestions), describeMaxListedTools)
	}
}

func TestToolsDescribe_PackageToolsNeedToolsetReadAndAnswerTheSameWhetherOrNotThePackageExists(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "ready-pkg", "ready", map[string]string{"lookup_order": `{"type":"object"}`})
	f.seedPackage(t, "pending-pkg", "pending", map[string]string{"run": ""})
	f.seedPackage(t, "broken-pkg", "failed", map[string]string{"run": ""})
	toolReadOnly := &auth.Identity{Scopes: []string{rbac.ScopeToolRead}}

	cases := []struct{ name, existing, missing string }{
		{"a ready package", "ready-pkg__lookup_order", "nobody-ready__lookup_order"},
		{"a package still discovering", "pending-pkg__run", "nobody-pending__run"},
		{"a failed package", "broken-pkg__run", "nobody-broken__run"},
		{"a wrong tool in a ready package", "ready-pkg__refund_order", "nobody-wrong__refund_order"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := f.callAs(t, toolReadOnly, toolRef(tc.existing), toolRef(tc.missing))
			if err != nil {
				t.Fatalf("tools/describe: %v", err)
			}
			existing, missing := resp.Tools[0], resp.Tools[1]
			if existing.Status != describeStatusNotFound || existing.Reason != reasonToolNotFound || len(existing.Suggestions) != 0 {
				t.Errorf("existing package without toolset read: got %+v, want not_found with no suggestions", existing)
			}
			existing.Ref, missing.Ref = DescribeToolRef{}, DescribeToolRef{}
			if !reflect.DeepEqual(existing, missing) {
				t.Errorf("existing package answered %+v, missing package answered %+v: the answers must not differ", existing, missing)
			}
		})
	}
}

func TestToolsDescribe_PackageToolsAreNotSuggestedWithoutToolsetRead(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme-tools", "ready", map[string]string{"lookup_order": ""})

	withoutToolset, err := f.callAs(t, &auth.Identity{Scopes: []string{rbac.ScopeToolRead}}, toolRef("acme-tools__lookup_ordr"))
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	if got := withoutToolset.Tools[0]; len(got.Suggestions) != 0 {
		t.Errorf("tool read only: suggestions = %v, want no package tool names", got.Suggestions)
	}

	withToolset, err := f.callAs(t, &auth.Identity{Scopes: []string{rbac.ScopeToolRead, rbac.ScopeToolsetRead}}, toolRef("acme-tools__lookup_ordr"))
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	if got := withToolset.Tools[0]; !slices.Equal(got.Suggestions, []string{"acme-tools__lookup_order"}) {
		t.Errorf("tool and toolset read: suggestions = %v, want the package tool", got.Suggestions)
	}
}

func TestToolsDescribe_ToolAndToolsetReadKeepThePackageAnswers(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme-tools", "ready", map[string]string{"lookup_order": `{"type":"object"}`})
	f.seedPackage(t, "broken-pkg", "failed", map[string]string{"run": ""})
	identity := &auth.Identity{Scopes: []string{rbac.ScopeToolRead, rbac.ScopeToolsetRead}}

	resp, err := f.callAs(t, identity, toolRef("acme-tools__lookup_order"), toolRef("broken-pkg__run"))
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	if got := resp.Tools[0]; got.Status != describeStatusOK || got.Kind != describeKindToolset {
		t.Errorf("ready package: got %+v, want an ok toolset tool", got)
	}
	if got := resp.Tools[1]; got.Status != describeStatusUnavailable || got.Reason != reasonPackageFailed {
		t.Errorf("failed package: got %+v, want unavailable with %q", got, reasonPackageFailed)
	}
}
