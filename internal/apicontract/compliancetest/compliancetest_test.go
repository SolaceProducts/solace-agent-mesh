// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package compliancetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

func TestDocViolations(t *testing.T) {
	doc := apicontract.Doc{ID: "getAgentVersion", Summary: "Get an agent version", Text: "Returns one version of an agent.", Tag: apicontract.Tag{Name: "Agents", Group: "Management"}}
	valid := apicontract.Route{Method: http.MethodGet, Path: "/agents/{agentId}/versions/{id}", Stability: apicontract.StabilityDraft, Doc: doc}
	keyed := apicontract.Route{Method: http.MethodPost, Path: "/agents", Stability: apicontract.StabilityStable, AllowAPIKey: true, Published: true, Doc: apicontract.Doc{ID: "createAgent", Summary: "Create an agent", Text: "Creates an agent.", Tag: doc.Tag}}
	if got := DocViolations([]apicontract.Route{valid, keyed}); len(got) > 0 {
		t.Fatalf("valid routes rejected: %v", got)
	}
	without := func(change func(*apicontract.Route)) apicontract.Route {
		rt := valid
		change(&rt)
		return rt
	}
	for name, bad := range map[string]apicontract.Route{
		"no stability":           without(func(rt *apicontract.Route) { rt.Stability = "" }),
		"unknown stability":      without(func(rt *apicontract.Route) { rt.Stability = "beta" }),
		"no doc ID":              without(func(rt *apicontract.Route) { rt.Doc.ID = "" }),
		"no summary":             without(func(rt *apicontract.Route) { rt.Doc.Summary = "" }),
		"no text":                without(func(rt *apicontract.Route) { rt.Doc.Text = "" }),
		"no tag name":            without(func(rt *apicontract.Route) { rt.Doc.Tag.Name = "" }),
		"summary verb":           without(func(rt *apicontract.Route) { rt.Doc.Summary = "Start an agent version" }),
		"no tag group":           without(func(rt *apicontract.Route) { rt.Doc.Tag.Group = "" }),
		"lowercase tag word":     without(func(rt *apicontract.Route) { rt.Doc.Tag.Name = "Access control" }),
		"API key route is draft": without(func(rt *apicontract.Route) { rt.AllowAPIKey = true }),
		"published draft":        without(func(rt *apicontract.Route) { rt.Published = true }),
	} {
		if got := DocViolations([]apicontract.Route{bad}); len(got) == 0 {
			t.Errorf("%s: passed", name)
		}
	}
	if got := DocViolations([]apicontract.Route{valid, without(func(rt *apicontract.Route) { rt.Method = http.MethodDelete })}); len(got) == 0 {
		t.Error("duplicate doc ID passed")
	}
}

func TestPathViolations(t *testing.T) {
	text := map[string]bool{"agentName": true, "filename": true, "file_name": true}
	for _, path := range []string{
		"/experiments/{experimentId}/agents/{agentName}",
		"/runResults/{runResultId}/artifacts/{filename...}",
	} {
		if got := PathViolations(Config{}, path, text); len(got) > 0 {
			t.Errorf("%s rejected: %v", path, got)
		}
	}
	for _, path := range []string{
		"agents",
		"/agents/",
		"/api/v2/platform/agents",
		"/agent_deployments",
		"/agents/{agent_id}",
		"/agents/{name}",
		"/agents/{id}/versions/{versionId}",
		"/files/{file_name}",
		"/files/{filename...}/versions",
		"/files/{path...}",
	} {
		if len(PathViolations(Config{}, path, text)) == 0 {
			t.Errorf("%s passed", path)
		}
	}
}

func TestSourceRules(t *testing.T) {
	const src = `package apiv2
type listed struct{ ID string ` + "`json:\"id\"`" + ` }
type unlisted struct{ ID string ` + "`json:\"id\"`" + ` }
type notWire struct{ n int }
func register(mux *http.ServeMux) { mux.HandleFunc("GET /a", nil) }
func sneaky(mux *http.ServeMux) { mux.Handle("GET /b", nil) }
func typed() { _ = apicontract.Handle(nil, "x") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	if got := UnreachedWireStructs(files, map[string]bool{"listed": true}); !slices.Equal(got, []string{"unlisted"}) {
		t.Errorf("unreached wire structs = %v, want [unlisted]", got)
	}
	if got := MuxCallsOutsideMount(fset, files, "register"); len(got) != 1 || !strings.Contains(got[0], "sneaky") {
		t.Errorf("mux calls outside register = %v, want one in sneaky (apicontract.Handle in typed must pass)", got)
	}
}

func TestRouteViolations_FlagsAnUnsupportedMethod(t *testing.T) {
	got := RouteViolations(Config{}, []apicontract.Route{{Method: "TRACE", Path: "/agents"}})
	if !slices.ContainsFunc(got, func(v string) bool { return strings.Contains(v, "method must be") }) {
		t.Errorf("violations = %v, want the method rule to fire", got)
	}
}

type untaggedHolder struct {
	Untagged inner
}

type inner struct {
	Bad string `json:"Bad_Name"`
}

func TestTypeViolations_ReportsAnUntaggedFieldAndTheRuleBreakInsideIt(t *testing.T) {
	got, _ := TypeViolations(Config{Results: map[reflect.Type]bool{reflect.TypeFor[untaggedHolder](): true}}, []reflect.Type{reflect.TypeFor[untaggedHolder]()}, nil)
	var untagged, nested bool
	for _, v := range got {
		untagged = untagged || strings.Contains(v, "needs a json tag")
		nested = nested || strings.Contains(v, "camelCase")
	}
	if !untagged || !nested {
		t.Errorf("violations = %v, want both the missing tag and the nested camelCase break", got)
	}
}
